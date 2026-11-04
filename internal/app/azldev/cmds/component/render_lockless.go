// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
)

type locklessRenderUpdate struct {
	repoRoot string
	path     string
	subject  string
	changes  string
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
	ExpandRelease(ctx context.Context, dir, filename string) (string, error)
}

//nolint:gochecknoglobals // Replaced by isolated render tests to open an in-memory project repository.
var openRenderRepo = openChangedRepo

func prepareLocklessRender(
	env *azldev.Env, comp components.Component,
	dir, outputDir string, editor spec.EditorMode,
) (history lastRender, update *locklessRenderUpdate, err error) {
	repo, root, err := openRenderRepo(env)
	if err != nil {
		return history, nil, err
	}

	path, err := repoRelPath(root, outputDir)
	if err != nil {
		return history, nil, fmt.Errorf("lockless render output must be inside the project repository:\n%w", err)
	}

	history, err = readLastRender(env, repo, root, path, comp.GetName())
	if err != nil {
		return history, nil, fmt.Errorf("reading committed render state:\n%w", err)
	}

	update = &locklessRenderUpdate{
		repoRoot: root,
		path:     path,
		subject:  "updated " + comp.GetName(),
	}

	_, opened, err := openLocklessSpec(env, dir, comp.GetName(), editor)
	if err != nil {
		return history, nil, err
	}

	version, err := opened.GetLastTag("", "Version")
	if err != nil {
		return history, nil, fmt.Errorf("reading upstream Version:\n%w", err)
	}

	if err := collectRenderUpstreamChanges(env, dir, comp.GetName(), version, history, update); err != nil {
		return history, nil, err
	}

	return history, update, nil
}

func readLastRender(
	env *azldev.Env, repo *gogit.Repository, root, path, name string,
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

	log, err := repo.Log(&gogit.LogOptions{
		From: head.Hash(),
		PathFilter: func(file string) bool {
			return strings.HasPrefix(file, path+"/")
		},
	})
	if err != nil {
		return history, fmt.Errorf("finding last render commit:\n%w", err)
	}
	defer log.Close()

	last, err := log.Next()
	if errors.Is(err, io.EOF) {
		return history, nil
	}

	if err != nil {
		return history, fmt.Errorf("reading last render commit:\n%w", err)
	}

	err = readRenderedUpstreamCommits(env, root, name, tree, last, &history)

	return history, err
}

func readRenderedUpstreamCommits(
	env *azldev.Env, root, name string, tree *object.Tree, last *object.Commit, history *lastRender,
) error {
	projectPath, err := repoRelPath(root, env.ProjectDir())
	if err != nil {
		return err
	}

	history.current, history.currentBranch, err = renderUpstreamCommit(env, tree, projectPath, name, false)
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

	comp, err := components.NewResolver(historicalEnv).GetComponentByName(name)
	if err != nil {
		return "", "", fmt.Errorf("resolving historical component %#q:\n%w", name, err)
	}

	commit = comp.GetConfig().Spec.UpstreamCommit
	if commit == "" && allowLockFallback {
		commit, err = lastRenderLockedUpstreamCommit(historicalEnv, name)
		if err != nil {
			return "", "", err
		}
	}

	if commit == "" {
		return "", "", fmt.Errorf("component %#q has no configured 'spec.upstream-commit'", name)
	}

	distro, err := sourceproviders.ResolveDistro(historicalEnv, comp)
	if err != nil {
		return "", "", fmt.Errorf("resolving historical upstream branch:\n%w", err)
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
	history lastRender, update *locklessRenderUpdate,
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

	release, err := locklessRenderRelease(env, autospec, dir, filepath.Base(path), original, history)
	if err != nil {
		return err
	}

	release, err = restoreLocklessRelease(original, release, version, history, update.changes != "")
	if err != nil {
		return err
	}

	if err := opened.SetTag("", "Release", release); err != nil {
		return fmt.Errorf("updating upstream Release:\n%w", err)
	}

	if err := carryStaticRenderRelease(env, opened, original, version, history, update.changes != "", editor); err != nil {
		return err
	}

	resultBytes, err := locklessSpecBytes(opened, history.spec, original, editor)
	if err != nil {
		return err
	}

	if err := fileutils.WriteFile(env.FS(), path, resultBytes, fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing prepared upstream spec:\n%w", err)
	}

	return nil
}

func locklessSpecBytes(opened *spec.Spec, previous, release string, editor spec.EditorMode) ([]byte, error) {
	var result bytes.Buffer
	if err := opened.Serialize(&result); err != nil {
		return nil, fmt.Errorf("serializing upstream spec:\n%w", err)
	}

	if previous != "" && !sources.ReleaseUsesAutorelease(release) {
		return restoreRenderChangelog(result.Bytes(), []byte(previous), editor)
	}

	return result.Bytes(), nil
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
			return fmt.Errorf("fetching last render upstream branch %#q:\n%w", history.previousBranch, err)
		}
	}

	changes, err := git.RunInDir(env, env, dir,
		"log", "--oneline", "--end-of-options", history.previous+".."+history.current, "--")
	if err != nil {
		return fmt.Errorf("collecting upstream changes:\n%w", err)
	}

	update.changes = changes

	return nil
}

func locklessRenderRelease(
	env *azldev.Env, autospec renderAutorelease, dir, filename, release string, history lastRender,
) (string, error) {
	if !sources.ReleaseUsesAutorelease(release) || env.DryRun() {
		return release, nil
	}

	if history.spec != "" {
		return carryRenderedAutorelease(env, autospec, dir, filename, release, history)
	}

	changelog, err := autospec.GenerateChangelog(env, dir, filename)
	if err != nil {
		return "", fmt.Errorf("generating initial upstream changelog:\n%w", err)
	}

	if err := fileutils.WriteFile(env.FS(), filepath.Join(dir, "changelog"), []byte(changelog),
		fileperms.PublicFile); err != nil {
		return "", fmt.Errorf("writing initial upstream changelog:\n%w", err)
	}

	calculated, err := autospec.CalculateBaseRelease(env, dir, filename)
	if err != nil {
		return "", fmt.Errorf("calculating upstream release base:\n%w", err)
	}

	return withAutoreleaseBase(release, calculated)
}

var (
	renderedAutoreleaseMarker = regexp.MustCompile(`(?m)^## RPMAUTOSPEC:[^\r\n]*autorelease`)
	renderedReleaseNumber     = regexp.MustCompile(`^[0-9]+`)
)

func carryRenderedAutorelease(
	env *azldev.Env, autospec renderAutorelease, dir, filename, release string, history lastRender,
) (string, error) {
	if !renderedAutoreleaseMarker.MatchString(history.spec) {
		return release, nil
	}

	expanded, err := expandCommittedRelease(env, autospec, dir, filename, history)
	if err != nil {
		return "", fmt.Errorf("expanding committed Release:\n%w", err)
	}

	release, err = withAutoreleaseBase(release, renderedReleaseNumber.FindString(strings.TrimSpace(expanded)))
	if err != nil {
		return "", fmt.Errorf("using committed Release %#q as autorelease base:\n%w", expanded, err)
	}

	previous := []byte(history.spec)

	start, end, err := renderChangelogBounds(previous, spec.EditorLegacy)
	if err != nil {
		return "", err
	}

	_, changelog, _ := strings.Cut(string(previous[start:end]), "\n")

	changelog = stripChangelogBoundaryComments(changelog)
	if err := fileutils.WriteFile(env.FS(), filepath.Join(dir, "changelog"), []byte(changelog),
		fileperms.PublicFile); err != nil {
		return "", fmt.Errorf("writing committed 'changelog' content:\n%w", err)
	}

	return release, nil
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

func restoreLocklessRelease(
	original, release, version string, history lastRender, upstreamChanged bool,
) (string, error) {
	if history.spec == "" && upstreamChanged {
		return original, nil
	}

	if history.spec == "" {
		return release, nil
	}

	previous, err := spec.OpenSpec(strings.NewReader(history.spec))
	if err != nil {
		return "", fmt.Errorf("parsing committed dist-git spec:\n%w", err)
	}

	if !upstreamChanged {
		return release, nil
	}

	oldVersion, err := previous.GetLastTag("", "Version")
	if err != nil {
		return "", fmt.Errorf("reading committed Version:\n%w", err)
	}

	if oldVersion != version {
		return original, nil
	}

	return release, nil
}

var autoreleaseBaseArg = regexp.MustCompile(`(^|\s)-b(?:\s+|=)?[0-9]+`)

func withAutoreleaseBase(release, output string) (string, error) {
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return "", errors.New("empty autorelease base")
	}

	number := fields[len(fields)-1]

	value, err := strconv.Atoi(number)
	if err != nil || value < 1 {
		return "", fmt.Errorf("invalid autorelease base %#q", output)
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

func bumpLocklessSpec(env *azldev.Env, path string, update *locklessRenderUpdate) (bool, error) {
	if err := prereqs.RequireExecutable(env, sources.RPMDevBumpspecBinary, nil); err != nil {
		return false, fmt.Errorf("lockless upstream rendering requires 'rpmdev-bumpspec'; install 'rpmdevtools':\n%w", err)
	}

	before, err := fileutils.ReadFile(env.FS(), path)
	if err != nil {
		return false, fmt.Errorf("reading spec before release bump:\n%w", err)
	}

	entry := update.subject
	if update.changes != "" {
		entry += "\n" + update.changes
	}

	raw := exec.CommandContext(env, sources.RPMDevBumpspecBinary, "-c", entry, path)
	raw.Dir = filepath.Dir(path)

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

func commitLocklessRenders(
	env *azldev.Env, prepared []*preparedComponent, results []*RenderResult, checkOnly bool,
) {
	if checkOnly || env.DryRun() {
		return
	}

	// All project index writes are serialized; source preparation and filtering
	// remain parallel. A failed commit leaves rendered output intact for recovery.
	for _, prep := range prepared {
		if prep.update == nil || results[prep.index].Status != renderStatusOK {
			continue
		}

		if err := commitLocklessRender(env, prep.update); err != nil {
			results[prep.index].Status = renderStatusError
			results[prep.index].Error = err.Error()
		}
	}
}

func commitLocklessRender(env *azldev.Env, update *locklessRenderUpdate) error {
	if _, err := git.RunInDir(env, env, update.repoRoot, "add", "--all", "--", update.path); err != nil {
		return fmt.Errorf("staging rendered component:\n%w", err)
	}

	diff, err := git.RunInDir(env, env, update.repoRoot, "diff", "--cached", "--name-only", "--", update.path)
	if err != nil {
		return fmt.Errorf("checking staged render:\n%w", err)
	}

	if diff == "" {
		return nil
	}

	args := []string{"commit", "--only", "-m", update.subject}
	if update.changes != "" {
		args = append(args, "-m", update.changes)
	}

	args = append(args, "--", update.path)
	if _, err := git.RunInDir(env, env, update.repoRoot, args...); err != nil {
		return fmt.Errorf("committing rendered component:\n%w", err)
	}

	return nil
}
