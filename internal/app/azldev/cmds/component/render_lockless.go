// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
)

const noChangeRebuildMarker = ".no-change-rebuild"

var errNoRenderChanges = errors.New("no change since the last render; use '--no-change-rebuild' to rebuild anyway")

var errLocalManualRebuild = errors.New(
	"local non-autorelease components must manually update their release and changelog before rendering again",
)

var errRenderNEVRValidation = errors.New("lockless render NEVR validation failed")

type locklessRenderUpdate struct {
	repoRoot        string
	path            string
	subject         string
	changes         string
	noGitCommit     bool
	subjectProvided bool
	local           bool
}

type lastRender struct {
	spec           string
	tree           *object.Tree
	previous       string
	current        string
	previousBranch string
	currentBranch  string
}

type renderAutorelease interface {
	GenerateChangelog(ctx context.Context, dir, filename string) (string, error)
	CalculateBaseRelease(ctx context.Context, dir, filename string) (string, error)
	CalculateRelease(ctx context.Context, dir, filename string) (string, error)
	ExpandRelease(ctx context.Context, dir, filename string) (string, error)
}

//nolint:gochecknoglobals // Replaced by isolated render tests to open an in-memory project repository.
var openRenderRepo = openChangedRepo

func validateRenderComponents(
	env *azldev.Env, comps []components.Component, allowUncommitted bool,
) error {
	if len(comps) == 0 {
		return errors.New("no components were selected; " +
			"please use command-line options to indicate which components to render")
	}

	if allowUncommitted {
		return nil
	}

	for _, comp := range comps {
		if env.ComponentWithoutLockfile(comp.GetName()) {
			return checkRenderWorktree(env, comps)
		}
	}

	return nil
}

func checkRenderWorktree(env *azldev.Env, comps []components.Component) error {
	specDir := env.Config().Project.RenderedSpecsDir
	if specDir == "" {
		specDir = projectconfig.DefaultRenderedSpecsDir
	}

	if filepath.IsAbs(specDir) {
		relative, err := filepath.Rel(env.ProjectDir(), specDir)
		if err != nil {
			return fmt.Errorf("resolving rendered-specs directory for repository check:\n%w", err)
		}

		specDir = relative
	}

	specDir = filepath.Clean(specDir)
	componentPaths := make([]string, 0, len(comps))

	for _, comp := range comps {
		name := comp.GetName()
		path := filepath.Join(specDir, strings.ToLower(name[:1]), name)
		componentPaths = append(componentPaths, ":(literal)"+filepath.ToSlash(path))
	}

	scopes := [][]string{
		{":/", ":(exclude,literal)" + filepath.ToSlash(specDir)},
		componentPaths,
	}
	for _, scope := range scopes {
		args := append([]string{
			"status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none", "--",
		}, scope...)

		status, err := git.RunInDir(env, env, env.ProjectDir(), args...)
		if err != nil {
			return fmt.Errorf("checking project repository for uncommitted files:\n%w", err)
		}

		if status != "" {
			return errors.New("rendering will not be done due to uncommitted or untracked files " +
				"outside 'project.rendered-specs-dir' or in selected component dist-git directories; " +
				"use '--allow-uncommitted' to proceed")
		}
	}

	return nil
}

func prepareLocklessRender(
	env *azldev.Env, comp components.Component,
	dir, outputDir string, editor spec.EditorMode,
) (*locklessRenderUpdate, error) {
	repo, update, err := prepareLocklessRenderOutput(env, outputDir)
	if err != nil {
		return nil, err
	}

	history, err := readLastRender(env, repo, update.repoRoot, update.path, comp.GetName(), false)
	if err != nil {
		return nil, fmt.Errorf("reading committed render state:\n%w", err)
	}

	_, opened, err := openLocklessSpec(env, dir, comp.GetName(), editor)
	if err != nil {
		return nil, err
	}

	version, err := opened.GetLastTag("", "Version")
	if err != nil {
		return nil, fmt.Errorf("reading upstream Version:\n%w", err)
	}

	if err := collectRenderUpstreamChanges(env, dir, comp.GetName(), version, history, update); err != nil {
		return nil, err
	}

	if err := prepareLocklessSpec(env, hostRenderProcessor{env: env}, dir, comp.GetName(), editor, history); err != nil {
		return nil, fmt.Errorf("preparing changelog and release for %#q:\n%w", comp.GetName(), err)
	}

	return update, nil
}

func prepareFetchedLocklessRender(
	env *azldev.Env, comp components.Component, dir, outputDir string, editor spec.EditorMode,
) (*locklessRenderUpdate, error) {
	if comp.GetConfig().Spec.SourceType != projectconfig.SpecSourceTypeLocal {
		return prepareLocklessRender(env, comp, dir, outputDir, editor)
	}

	_, update, err := prepareLocklessRenderOutput(env, outputDir)
	if update != nil {
		update.local = true
	}

	return update, err
}

func prepareLocklessRenderOutput(
	env *azldev.Env, outputDir string,
) (repo *gogit.Repository, update *locklessRenderUpdate, err error) {
	repo, root, err := openRenderRepo(env)
	if err != nil {
		return nil, nil, err
	}

	path, err := git.RepoRelPath(root, outputDir)
	if err != nil {
		return nil, nil, fmt.Errorf("lockless render output must be inside the project repository:\n%w", err)
	}

	return repo, &locklessRenderUpdate{repoRoot: root, path: path}, nil
}

func readLastRender(
	env *azldev.Env, repo *gogit.Repository, root, path, name string, useWorkingInputs bool,
) (lastRender, error) {
	var history lastRender

	head, err := repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return history, nil
	}

	if err != nil {
		return history, fmt.Errorf("reading project HEAD:\n%w", err)
	}

	tree, err := resolveTree(repo, head.Hash().String())
	if err != nil {
		return history, err
	}

	history.tree, err = tree.Tree(path)
	if errors.Is(err, object.ErrDirectoryNotFound) {
		return history, nil
	}

	if err != nil {
		return history, fmt.Errorf("reading committed dist-git directory:\n%w", err)
	}

	file, err := tree.File(filepath.ToSlash(filepath.Join(path, name+".spec")))
	if err != nil {
		return history, fmt.Errorf("reading committed spec:\n%w", err)
	}

	history.spec, err = file.Contents()
	if err != nil {
		return history, fmt.Errorf("reading committed spec contents:\n%w", err)
	}

	lastHash, err := git.RunInDir(env, env, root,
		"--literal-pathspecs", "log", "-1", "--format=%H", "--end-of-options", head.Hash().String(), "--", path+"/")
	if err != nil {
		return history, fmt.Errorf("finding last render commit:\n%w", err)
	}

	if lastHash == "" {
		return history, nil
	}

	last, err := repo.CommitObject(plumbing.NewHash(lastHash))
	if err != nil {
		return history, fmt.Errorf("reading last render commit:\n%w", err)
	}

	err = readRenderedUpstreamCommits(env, root, name, tree, last, &history, useWorkingInputs)

	return history, err
}

func readRenderedUpstreamCommits(
	env *azldev.Env, root, name string, tree *object.Tree, last *object.Commit, history *lastRender,
	useWorkingInputs bool,
) error {
	projectPath, err := git.RepoRelPath(root, env.ProjectDir())
	if err != nil {
		return err
	}

	if useWorkingInputs {
		history.current, history.currentBranch, err = configuredRenderUpstreamCommit(env, name, false)
	} else {
		history.current, history.currentBranch, err = renderUpstreamCommit(env, tree, projectPath, name, false)
	}

	if err != nil {
		return fmt.Errorf("reading current render upstream commit:\n%w", err)
	}

	lastTree, err := last.Tree()
	if err == nil {
		history.previous, history.previousBranch, err = renderUpstreamCommit(env, lastTree, projectPath, name, true)
	}

	if err != nil {
		slog.Warn("Cannot read last render upstream commit; treating upstream as changed",
			"component", name, "error", err)

		history.previous = ""
		history.previousBranch = ""
	}

	return nil
}

func renderUpstreamCommit(
	env *azldev.Env, tree *object.Tree, projectPath, name string, allowLockFallback bool,
) (commit string, branch string, err error) {
	historicalEnv, err := loadHistoricalEnv(env, tree, projectPath)
	if err != nil {
		return "", "", err
	}

	return configuredRenderUpstreamCommit(historicalEnv, name, allowLockFallback)
}

func configuredRenderUpstreamCommit(
	env *azldev.Env, name string, allowLockFallback bool,
) (commit string, branch string, err error) {
	comp, err := components.NewResolver(env).GetComponentByName(name)
	if err != nil {
		return "", "", fmt.Errorf("resolving component %#q:\n%w", name, err)
	}

	commit = comp.GetConfig().Spec.UpstreamCommit
	if commit == "" && allowLockFallback {
		commit, err = lastRenderLockedUpstreamCommit(env, name)
		if err != nil {
			return "", "", err
		}
	}

	if commit == "" {
		return "", "", fmt.Errorf("component %#q has no configured 'spec.upstream-commit'", name)
	}

	distro, err := sourceproviders.ResolveDistro(env, comp)
	if err != nil {
		return "", "", fmt.Errorf("resolving upstream branch:\n%w", err)
	}

	return commit, distro.Version.DistGitBranch, nil
}

func lastRenderLockedUpstreamCommit(env *azldev.Env, name string) (string, error) {
	selected, err := projectconfig.ReadLocklessComponents(env.FS(), env.ProjectDir())
	if err != nil {
		return "", fmt.Errorf("reading historical lockless component selection:\n%w", err)
	}

	if selected[name] {
		return "", fmt.Errorf("historical component %#q is lockless but has no 'spec.upstream-commit'", name)
	}

	lockDir := env.Config().Project.LockDir
	if lockDir == "" {
		return "", errors.New("no historical 'lock-dir' configured for lock fallback")
	}

	lock, err := lockfile.NewStore(env.FS(), lockDir).Get(name)
	if err != nil {
		return "", fmt.Errorf("reading historical lock for %#q:\n%w", name, err)
	}

	if lock.UpstreamCommit == "" {
		return "", fmt.Errorf("historical lock for %#q has no 'upstream-commit'", name)
	}

	return lock.UpstreamCommit, nil
}

func openLocklessSpec(
	env *azldev.Env, dir, name string, editor spec.EditorMode,
) (path string, opened *spec.Spec, err error) {
	path, err = findSpecFile(env.FS(), dir, name)
	if err != nil {
		return "", nil, err
	}

	content, err := fileutils.ReadFile(env.FS(), path)
	if err != nil {
		return "", nil, fmt.Errorf("reading upstream spec:\n%w", err)
	}

	opened, err = spec.OpenSpec(bytes.NewReader(content), spec.WithEditor(editor))
	if err != nil {
		return "", nil, fmt.Errorf("parsing upstream spec:\n%w", err)
	}

	return path, opened, nil
}

func prepareLocklessSpec(
	env *azldev.Env, autospec renderAutorelease, dir, name string, editor spec.EditorMode,
	history lastRender,
) error {
	path, opened, err := openLocklessSpec(env, dir, name, editor)
	if err != nil {
		return err
	}

	original, err := opened.GetLastTag("", "Release")
	if err != nil {
		return fmt.Errorf("reading upstream Release:\n%w", err)
	}

	version, err := opened.GetLastTag("", "Version")
	if err != nil {
		return fmt.Errorf("reading upstream Version:\n%w", err)
	}

	versionChanged, err := locklessVersionChanged(version, history)
	if err != nil {
		return err
	}

	history, err = staticRenderHistory(env, autospec, dir, name, original, history, editor)
	if err != nil {
		return err
	}

	if err := prepareLocklessChangelog(env, autospec, dir, filepath.Base(path), original, history); err != nil {
		return err
	}

	opened, err = carryLocklessInlineChangelog(opened, history.spec, original, editor)
	if err != nil {
		return err
	}

	if !versionChanged || sources.ReleaseUsesAutorelease(original) {
		release, err := locklessRenderRelease(env, autospec, dir, filepath.Base(path), original, history, versionChanged)
		if err != nil {
			return err
		}

		if err := opened.SetTag("", "Release", release); err != nil {
			return fmt.Errorf("updating upstream Release:\n%w", err)
		}

		if err := carryStaticRenderRelease(env, opened, original, history, editor); err != nil {
			return err
		}
	}

	var result bytes.Buffer
	if err := opened.Serialize(&result); err != nil {
		return fmt.Errorf("serializing prepared upstream spec:\n%w", err)
	}

	if err := fileutils.WriteFile(env.FS(), path, result.Bytes(), fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing prepared upstream spec:\n%w", err)
	}

	return nil
}

func carryLocklessInlineChangelog(
	opened *spec.Spec, previous, release string, editor spec.EditorMode,
) (*spec.Spec, error) {
	if previous == "" || sources.ReleaseUsesAutorelease(release) {
		return opened, nil
	}

	var result bytes.Buffer
	if err := opened.Serialize(&result); err != nil {
		return nil, fmt.Errorf("serializing upstream spec:\n%w", err)
	}

	content, err := restoreRenderChangelog(result.Bytes(), []byte(previous), editor)
	if err != nil {
		return nil, err
	}

	carried, err := spec.OpenSpec(bytes.NewReader(content), spec.WithEditor(editor))
	if err != nil {
		return nil, fmt.Errorf("parsing spec with committed changelog:\n%w", err)
	}

	return carried, nil
}

func collectRenderUpstreamChanges(
	env *azldev.Env, dir, name, version string, history lastRender, update *locklessRenderUpdate,
) error {
	if history.previous == "" {
		update.changes = fmt.Sprintf("Update %s to version %s", name, version)

		return nil
	}

	if history.previous == history.current {
		return nil
	}

	if history.previousBranch != "" && history.previousBranch != history.currentBranch {
		refspec := "+refs/heads/" + history.previousBranch + ":refs/remotes/origin/" + history.previousBranch
		if _, err := git.RunInDir(env, env, dir, "fetch", "--no-tags", "--", "origin", refspec); err != nil {
			slog.Warn("Cannot fetch last render upstream branch",
				"component", name, "branch", history.previousBranch, "error", err)
		}
	}

	if err := resolveRenderUpstreamCommit(env, dir, history.previous); err != nil {
		if env.Err() != nil {
			return fmt.Errorf("resolving last render upstream commit:\n%w", env.Err())
		}

		slog.Warn("Last render upstream commit is unavailable; using current upstream pin",
			"component", name, "commit", history.previous, "error", err)
		update.changes = fmt.Sprintf("update to upstream %s commit %s", name, history.current)

		return nil
	}

	changes, err := git.RunInDir(env, env, dir,
		"log", "--oneline", "--end-of-options", history.previous+".."+history.current, "--")
	if err != nil {
		return fmt.Errorf("collecting upstream changes:\n%w", err)
	}

	update.changes = changes

	return nil
}

func resolveRenderUpstreamCommit(env *azldev.Env, dir, commit string) error {
	if _, err := git.RunInDir(env, env, dir,
		"rev-parse", "--verify", "--end-of-options", commit+"^{commit}"); err == nil {
		return nil
	}

	if env.Err() != nil {
		return fmt.Errorf("resolving upstream commit %#q:\n%w", commit, env.Err())
	}

	if _, err := git.RunInDir(env, env, dir, "fetch", "--no-tags", "--", "origin", commit); err != nil {
		slog.Warn("Cannot fetch last render upstream commit", "commit", commit, "error", err)
	}

	_, err := git.RunInDir(env, env, dir,
		"rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolving upstream commit %#q after fetching:\n%w", commit, err)
	}

	return nil
}

func locklessRenderRelease(
	env *azldev.Env, autospec renderAutorelease, dir, filename, release string, history lastRender,
	versionChanged bool,
) (string, error) {
	if !sources.ReleaseUsesAutorelease(release) || env.DryRun() {
		return release, nil
	}

	if history.spec != "" && (!versionChanged || renderedAutoreleaseMarker.MatchString(history.spec)) {
		return carryRenderedAutorelease(env, autospec, dir, filename, release, history)
	}

	calculated, err := autospec.CalculateBaseRelease(env, dir, filename)
	if err != nil {
		return "", fmt.Errorf("calculating upstream release base:\n%w", err)
	}

	return withAutoreleaseBase(release, calculated, true)
}

var (
	renderedAutoreleaseMarker = regexp.MustCompile(`(?m)^## RPMAUTOSPEC:[^\r\n]*autorelease`)
	renderedAutoreleaseBase   = regexp.MustCompile(`(?:^|\s)-b(?:\s+|=)?([^\s}]*)`)
	renderedReleaseNumber     = regexp.MustCompile(`^[0-9]+`)
)

func carryRenderedAutorelease(
	env *azldev.Env, autospec renderAutorelease, dir, filename, release string, history lastRender,
) (string, error) {
	if !renderedAutoreleaseMarker.MatchString(history.spec) {
		return carryUnprocessedAutorelease(release, history)
	}

	expanded, err := expandCommittedRelease(env, autospec, dir, filename, history)
	if err != nil {
		return "", fmt.Errorf("expanding committed Release:\n%w", err)
	}

	release, err = withAutoreleaseBase(release, renderedReleaseNumber.FindString(strings.TrimSpace(expanded)), true)
	if err != nil {
		return "", fmt.Errorf("using committed Release %#q as autorelease base:\n%w", expanded, err)
	}

	return release, nil
}

func carryUnprocessedAutorelease(release string, history lastRender) (string, error) {
	previous, err := spec.OpenSpec(strings.NewReader(history.spec))
	if err != nil {
		return "", fmt.Errorf("parsing committed autorelease spec:\n%w", err)
	}

	previousRelease, err := previous.GetLastTag("", "Release")
	if err != nil {
		return "", fmt.Errorf("reading committed Release:\n%w", err)
	}

	if !sources.ReleaseUsesAutorelease(previousRelease) {
		return release, nil
	}

	if matches := renderedAutoreleaseBase.FindStringSubmatch(previousRelease); matches != nil {
		release, err = withAutoreleaseBase(release, matches[1], false)
		if err != nil {
			return "", fmt.Errorf("using committed autorelease base:\n%w", err)
		}
	} else {
		release = autoreleaseBaseArg.ReplaceAllString(release, "")
	}

	return release, nil
}

func prepareLocklessChangelog(
	env *azldev.Env, autospec renderAutorelease, dir, filename, release string, history lastRender,
) error {
	if !sources.ReleaseUsesAutorelease(release) || env.DryRun() {
		return nil
	}

	if history.spec != "" && !renderedAutoreleaseMarker.MatchString(history.spec) {
		return carryUnprocessedChangelog(env, dir, history)
	}

	var changelog string

	if history.spec == "" {
		var err error

		changelog, err = autospec.GenerateChangelog(env, dir, filename)
		if err != nil {
			return fmt.Errorf("generating initial upstream changelog:\n%w", err)
		}
	} else {
		previous := []byte(history.spec)

		start, end, err := renderChangelogBounds(previous, spec.EditorLegacy)
		if err != nil {
			return err
		}

		_, changelog, _ = strings.Cut(string(previous[start:end]), "\n")
		changelog = stripChangelogBoundaryComments(changelog)
	}

	if err := fileutils.WriteFile(env.FS(), filepath.Join(dir, "changelog"), []byte(changelog),
		fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing prepared 'changelog':\n%w", err)
	}

	return nil
}

func carryUnprocessedChangelog(env *azldev.Env, dir string, history lastRender) error {
	previous, err := spec.OpenSpec(strings.NewReader(history.spec))
	if err != nil {
		return fmt.Errorf("parsing committed autorelease spec:\n%w", err)
	}

	release, err := previous.GetLastTag("", "Release")
	if err != nil {
		return fmt.Errorf("reading committed Release:\n%w", err)
	}

	if !sources.ReleaseUsesAutorelease(release) {
		return nil
	}

	if history.tree == nil {
		return errors.New("missing committed dist-git tree for 'changelog'")
	}

	changelog, err := history.tree.File("changelog")
	if err != nil {
		return fmt.Errorf("reading committed 'changelog':\n%w", err)
	}

	content, err := changelog.Contents()
	if err != nil {
		return fmt.Errorf("reading committed 'changelog' contents:\n%w", err)
	}

	if err := fileutils.WriteFile(env.FS(), filepath.Join(dir, "changelog"), []byte(content),
		fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing committed 'changelog':\n%w", err)
	}

	return nil
}

func stripChangelogBoundaryComments(changelog string) string {
	lines := strings.SplitAfter(changelog, "\n")
	for idx, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		if !strings.HasPrefix(line, "##") {
			break
		}

		lines[idx] = ""
	}

	for idx := len(lines) - 1; idx >= 0; idx-- {
		if strings.TrimSpace(lines[idx]) == "" {
			continue
		}

		if !strings.HasPrefix(lines[idx], "##") {
			break
		}

		lines[idx] = ""
	}

	return strings.Join(lines, "")
}

func expandCommittedRelease(
	env *azldev.Env, autospec renderAutorelease, dir, filename string, history lastRender,
) (release string, err error) {
	snapshot, err := committedRenderSnapshot(env, dir, filename, history)
	if err != nil {
		return "", err
	}
	defer fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), snapshot, &err)

	release, err = autospec.ExpandRelease(env, snapshot, filename)
	if err != nil {
		return "", fmt.Errorf("querying committed spec:\n%w", err)
	}

	return release, nil
}

func committedRenderSnapshot(
	env *azldev.Env, dir, filename string, history lastRender,
) (snapshot string, err error) {
	snapshot, err = fileutils.MkdirTemp(env.FS(), filepath.Dir(dir), "azldev-render-previous-")
	if err != nil {
		return "", fmt.Errorf("creating committed dist-git snapshot:\n%w", err)
	}
	defer func(path string) {
		if err != nil {
			fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), path, &err)
		}
	}(snapshot)

	if history.tree != nil {
		if err := copyTreeToFS(history.tree, env.FS(), snapshot); err != nil {
			return "", fmt.Errorf("copying committed dist-git snapshot:\n%w", err)
		}
	}

	if err := fileutils.WriteFile(env.FS(), filepath.Join(snapshot, filename), []byte(history.spec),
		fileperms.PublicFile); err != nil {
		return "", fmt.Errorf("writing committed spec snapshot:\n%w", err)
	}

	return snapshot, nil
}

func locklessVersionChanged(version string, history lastRender) (bool, error) {
	if history.spec == "" {
		return true, nil
	}

	previous, err := spec.OpenSpec(strings.NewReader(history.spec))
	if err != nil {
		return false, fmt.Errorf("parsing committed dist-git spec:\n%w", err)
	}

	oldVersion, err := previous.GetLastTag("", "Version")
	if err != nil {
		return false, fmt.Errorf("reading committed Version:\n%w", err)
	}

	return oldVersion != version, nil
}

var autoreleaseBaseArg = regexp.MustCompile(`(^|\s)-b(?:\s+|=)?[0-9]+`)

func withAutoreleaseBase(release, output string, omitDefault bool) (string, error) {
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return "", errors.New("empty autorelease base")
	}

	number := fields[len(fields)-1]

	value, err := strconv.Atoi(number)
	if err != nil || value < 1 {
		return "", fmt.Errorf("invalid autorelease base %#q", output)
	}

	if omitDefault && value == 1 {
		return autoreleaseBaseArg.ReplaceAllString(release, ""), nil
	}

	if autoreleaseBaseArg.MatchString(release) {
		return autoreleaseBaseArg.ReplaceAllString(release, "${1}-b "+number), nil
	}

	if strings.HasPrefix(release, "%{autorelease") {
		depth := 0

		for index, char := range release {
			switch char {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return release[:index] + " -b " + number + release[index:], nil
				}
			}
		}
	}

	return release + " -b " + number, nil
}

func restoreRenderChangelog(upstream, previous []byte, editor spec.EditorMode) ([]byte, error) {
	oldStart, oldEnd, err := renderChangelogBounds(previous, editor)
	if err != nil {
		return nil, err
	}

	start, end, err := renderChangelogBounds(upstream, editor)
	if err != nil {
		return nil, err
	}

	result := append([]byte(nil), upstream[:start]...)
	if start == len(upstream) && oldStart != oldEnd && !bytes.HasSuffix(result, []byte("\n")) {
		result = append(result, '\n')
	}

	result = append(result, previous[oldStart:oldEnd]...)
	result = append(result, upstream[end:]...)

	return result, nil
}

func renderChangelogBounds(content []byte, editor spec.EditorMode) (start int, end int, err error) {
	opened, err := spec.OpenSpec(bytes.NewReader(content), spec.WithEditor(editor))
	if err != nil {
		return 0, 0, fmt.Errorf("parsing spec changelog:\n%w", err)
	}

	lines := bytes.SplitAfter(content, []byte("\n"))
	offset := func(line int) int {
		size := 0
		for _, text := range lines[:min(line, len(lines))] {
			size += len(text)
		}

		return size
	}
	start, end = len(content), len(content)

	err = opened.Visit(func(ctx *spec.Context) error {
		if ctx.Target.Section == nil || ctx.Target.Section.SectName != "%changelog" {
			return nil
		}

		switch ctx.Target.TargetType {
		case spec.SectionStartTarget:
			start = offset(ctx.CurrentLineNum)
		case spec.SectionEndTarget:
			end = offset(ctx.CurrentLineNum)
		case spec.SpecStartTarget, spec.SectionLineTarget, spec.SpecEndTarget:
		}

		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("finding spec changelog:\n%w", err)
	}

	return start, end, nil
}

func finishLocklessSpec(
	env *azldev.Env, path string, update *locklessRenderUpdate, changed, noChangeRebuild bool,
) (bool, error) {
	if !changed {
		if err := prepareLocklessRebuild(env, path, update.local, noChangeRebuild); err != nil {
			return false, err
		}
	}

	if err := prepareRenderMessage(env, path, update); err != nil {
		return false, err
	}

	if update.local {
		return !changed, nil
	}

	bumped, err := bumpLocklessSpec(env, path, update)

	return !changed || bumped, err
}

func prepareLocklessRebuild(env *azldev.Env, path string, local, noChangeRebuild bool) error {
	autorelease, err := locklessSpecUsesAutorelease(env, path)
	if err != nil {
		return err
	}

	if local && !autorelease {
		return errLocalManualRebuild
	}

	if !noChangeRebuild {
		return errNoRenderChanges
	}

	if !autorelease {
		return nil
	}

	return writeNoChangeRebuild(env, filepath.Dir(path))
}

func locklessSpecUsesAutorelease(env *azldev.Env, path string) (bool, error) {
	content, err := fileutils.ReadFile(env.FS(), path)
	if err != nil {
		return false, fmt.Errorf("reading spec for lockless render:\n%w", err)
	}

	opened, err := spec.OpenSpec(bytes.NewReader(content))
	if err != nil {
		return false, fmt.Errorf("parsing spec for lockless render:\n%w", err)
	}

	release, err := opened.GetLastTag("", "Release")
	if err != nil {
		return false, fmt.Errorf("reading Release for lockless render:\n%w", err)
	}

	return sources.ReleaseUsesAutorelease(release), nil
}

func validateRenderNEVR(env *azldev.Env, preparedDir string, prep *preparedComponent) error {
	existing, err := queryRenderSpecNEVR(env, filepath.Join(prep.compOutputDir, prep.specFilename))
	if err != nil {
		return fmt.Errorf("%w: querying existing NEVR:\n%w", errRenderNEVRValidation, err)
	}

	newPath := filepath.Join(preparedDir, prep.specFilename)

	rendered, err := queryRenderSpecNEVR(env, newPath)
	if err != nil {
		return fmt.Errorf("%w: querying new NEVR:\n%w", errRenderNEVRValidation, err)
	}

	autorelease, err := locklessSpecUsesAutorelease(env, newPath)
	if err != nil {
		return fmt.Errorf("%w: checking new spec autorelease:\n%w", errRenderNEVRValidation, err)
	}

	requirement := "higher than"
	if autorelease {
		requirement = "equal to or higher than"
	}

	comparison := rendered.Version.Compare(&existing.Version)
	if existing.Name != rendered.Name || comparison < 0 || (comparison == 0 && !autorelease) {
		return fmt.Errorf("%w: new NEVR must be %s existing NEVR for the same package; "+
			"existing %#q; new %#q", errRenderNEVRValidation, requirement,
			existing.Name+"-"+existing.Version.String(), rendered.Name+"-"+rendered.Version.String())
	}

	return nil
}

func queryRenderSpecNEVR(env *azldev.Env, path string) (*rpm.SpecInfo, error) {
	if err := prereqs.RequireExecutable(env, "rpmspec", nil); err != nil {
		return nil, fmt.Errorf("lockless render NEVR validation requires 'rpmspec':\n%w", err)
	}

	dir := filepath.Dir(path)
	raw := exec.CommandContext(env, "rpmspec", "-q", "--srpm",
		"--define", "_sourcedir "+dir, "--define", "_specdir "+dir,
		"--queryformat", rpm.SrpmQueryFormat, path)
	raw.Dir = dir

	var stderr bytes.Buffer

	raw.Stderr = &stderr

	command, err := env.Command(raw)
	if err != nil {
		return nil, fmt.Errorf("creating NEVR query for %#q:\n%w", path, err)
	}

	output, err := command.RunAndGetOutput(env)
	if err != nil {
		return nil, fmt.Errorf("querying NEVR for %#q (stderr: %#q):\n%w", path, stderr.String(), err)
	}

	info, err := rpm.ParseSrpmQueryOutput(path, output)
	if err != nil {
		return nil, fmt.Errorf("parsing NEVR query for %#q:\n%w", path, err)
	}

	return info, nil
}

func writeNoChangeRebuild(env *azldev.Env, dir string) error {
	if env.DryRun() {
		return nil
	}

	if err := prereqs.RequireExecutable(env, "date", nil); err != nil {
		return fmt.Errorf("no-change rebuild requires 'date':\n%w", err)
	}

	raw := exec.CommandContext(env, "date", "-Is")
	raw.Dir = dir

	var stderr bytes.Buffer

	raw.Stderr = &stderr

	command, err := env.Command(raw)
	if err != nil {
		return fmt.Errorf("creating rebuild timestamp command:\n%w", err)
	}

	output, err := command.RunAndGetOutput(env)
	if err != nil {
		return fmt.Errorf("getting rebuild timestamp (%s):\n%w", stderr.String(), err)
	}

	if err := fileutils.WriteFile(env.FS(), filepath.Join(dir, noChangeRebuildMarker),
		[]byte(output), fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing rebuild timestamp:\n%w", err)
	}

	return nil
}

func bumpLocklessSpec(env *azldev.Env, path string, update *locklessRenderUpdate) (changed bool, err error) {
	before, err := fileutils.ReadFile(env.FS(), path)
	if err != nil {
		return false, fmt.Errorf("reading spec before release bump:\n%w", err)
	}

	opened, err := spec.OpenSpec(bytes.NewReader(before))
	if err != nil {
		return false, fmt.Errorf("reading spec before release bump:\n%w", err)
	}

	release, err := opened.GetLastTag("", "Release")
	if err != nil {
		return false, fmt.Errorf("reading Release before release bump:\n%w", err)
	}

	if sources.ReleaseUsesAutorelease(release) {
		return false, nil
	}

	if err := prereqs.RequireExecutable(env, sources.RPMDevBumpspecBinary, nil); err != nil {
		return false, fmt.Errorf("lockless upstream rendering requires 'rpmdev-bumpspec'; install 'rpmdevtools':\n%w", err)
	}

	raw := exec.CommandContext(env, sources.RPMDevBumpspecBinary, "-c", update.subject, path)
	raw.Dir = filepath.Dir(path)

	wrapperDir, err := prepareRenderBumpspecEnvironment(env, raw, path)
	if wrapperDir != "" {
		defer fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), wrapperDir, &err)
	}

	if err != nil {
		return false, err
	}

	var stderr bytes.Buffer

	raw.Stderr = &stderr

	command, err := env.Command(raw)
	if err != nil {
		return false, fmt.Errorf("creating render release bump command:\n%w", err)
	}

	if err := command.Run(env); err != nil {
		return false, fmt.Errorf("bumping render release (%s):\n%w", stderr.String(), err)
	}

	after, err := fileutils.ReadFile(env.FS(), path)
	if err != nil {
		return false, fmt.Errorf("reading spec after release bump:\n%w", err)
	}

	return !bytes.Equal(before, after), nil
}

func prepareRenderBumpspecEnvironment(env *azldev.Env, raw *exec.Cmd, specPath string) (string, error) {
	dir, err := fileutils.MkdirTempInTempDir(env.FS(), "azldev-render-rpm-")
	if err != nil {
		return "", fmt.Errorf("creating temporary render RPM wrapper directory:\n%w", err)
	}

	if strings.ContainsRune(dir, os.PathListSeparator) {
		return dir, fmt.Errorf("temporary RPM wrapper directory %#q contains the PATH separator", dir)
	}

	wrapper := sources.GenerateRPMDevBumpspecWrapper(sources.RPMDevBumpspecRequest{SpecPath: specPath})
	if err := fileutils.WriteFile(env.FS(), filepath.Join(dir, "rpm"),
		[]byte(wrapper), fileperms.PublicExecutable); err != nil {
		return dir, fmt.Errorf("writing temporary render RPM wrapper:\n%w", err)
	}

	originalPath := env.OSEnv().Getenv("PATH")
	raw.Env = append(raw.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+originalPath,
		"RPMDEV_BUMPSPEC_ORIGINAL_PATH="+originalPath)

	return dir, nil
}

func commitLocklessRenders(
	env *azldev.Env, prepared []*preparedComponent, results []*RenderResult, checkOnly bool,
) {
	if checkOnly || env.DryRun() {
		return
	}

	// Serialize writes to the project index.
	includeInputs := true

	for _, prep := range prepared {
		result := results[prep.index]

		if prep.update != nil && !prep.update.noGitCommit && result.Status == renderStatusOK {
			committed, err := commitLocklessRender(env, prep.update, includeInputs)
			if err != nil {
				result.Status = renderStatusError
				result.Error = err.Error()
			} else if committed {
				includeInputs = false
			}
		}

		if !prep.lockless {
			continue
		}

		if result.Status != renderStatusOK {
			if prep.outputTouched {
				if err := restoreLocklessOutput(env, prep); err != nil {
					result.Error += "\n" + err.Error()
				}
			}
		} else {
			writeRenderedAlias(env, prep)
		}
	}
}

func restoreLocklessOutput(env *azldev.Env, prep *preparedComponent) error {
	if err := env.FS().RemoveAll(prep.compOutputDir); err != nil {
		return fmt.Errorf("clearing failed rendered output %#q:\n%w", prep.compOutputDir, err)
	}

	root := env.ProjectDir()
	if prep.update != nil {
		root = prep.update.repoRoot
	}

	path, err := filepath.Rel(root, prep.compOutputDir)
	if err != nil {
		return fmt.Errorf("locating rendered output in the project repository:\n%w", err)
	}

	path = filepath.ToSlash(path)
	ctx := context.WithoutCancel(env)

	// Reset this path first so checkout cannot reuse files staged by the failed render.
	if _, err := git.RunInDir(ctx, env, root, "reset", "--quiet", "--", path); err != nil {
		return fmt.Errorf("resetting failed rendered output in the index:\n%w", err)
	}

	if prep.outputExisted {
		if _, err := git.RunInDir(ctx, env, root, "checkout", "HEAD", "--", path); err != nil {
			return fmt.Errorf("restoring committed rendered output %#q:\n%w", prep.compOutputDir, err)
		}
	}

	return nil
}

func commitLocklessRender(env *azldev.Env, update *locklessRenderUpdate, includeInputs bool) (bool, error) {
	paths := []string{update.path}
	if includeInputs {
		inputs, err := stageLocklessRenderInputs(env, update)
		if err != nil {
			return false, err
		}

		paths = append(paths, inputs...)
	}

	if _, err := git.RunInDir(env, env, update.repoRoot, "add", "--all", "--", update.path); err != nil {
		return false, fmt.Errorf("staging rendered component:\n%w", err)
	}

	diffArgs := append([]string{"diff", "--cached", "--name-only", "--"}, paths...)

	diff, err := git.RunInDir(env, env, update.repoRoot, diffArgs...)
	if err != nil {
		return false, fmt.Errorf("checking staged render:\n%w", err)
	}

	if diff == "" {
		return false, nil
	}

	if err := runLocklessRenderCommit(env, update, paths); err != nil {
		return false, fmt.Errorf("committing rendered component:\n%w", err)
	}

	return true, nil
}

func stageLocklessRenderInputs(env *azldev.Env, update *locklessRenderUpdate) ([]string, error) {
	renderedPath := filepath.ToSlash(filepath.Dir(filepath.Dir(update.path)))
	exclusion := ":(top,exclude,literal)" + renderedPath

	if _, err := git.RunInDir(env, env, update.repoRoot, "add", "--all", "--", ".", exclusion); err != nil {
		return nil, fmt.Errorf("staging uncommitted files outside rendered specs:\n%w", err)
	}

	status, err := git.RunInDir(env, env, update.repoRoot,
		"diff", "--cached", "--name-status", "--no-renames", "-z", "--", ".", exclusion)
	if err != nil {
		return nil, fmt.Errorf("checking staged files outside rendered specs:\n%w", err)
	}

	// With '--no-renames', NUL-delimited name/status output contains status/path pairs.
	fields := strings.Split(status, "\x00")

	var paths []string

	for index := 1; index < len(fields); index += 2 {
		paths = append(paths, ":(top,literal)"+fields[index])
	}

	return paths, nil
}
