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
	repoRoot string
	path     string
	changes  string
	local    bool

	// name and lastCommit identify the component and its last render commit
	// for [renderMessageDetector].
	name       string
	lastCommit string
	// message is the explicit '--commit-message'; when empty, detector (if set)
	// detects the changelog text, else the placeholder is used.
	message  string
	detector *renderMessageDetector
	// unchanged is set for an unchanged render allowed by '--no-change-rebuild'.
	unchanged bool
}

type lastRender struct {
	// commit is the last project commit that changed the rendered output.
	commit         string
	spec           string
	tree           *git.Tree
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
	ExpandVersion(ctx context.Context, dir, filename string) (string, error)
}

//nolint:gochecknoglobals // Replaced by isolated render tests to open a test project repository.
var openRenderRepo = openLocklessRepo

// openLocklessRepo opens the project's git repository with the system's git
// program and returns it together with its worktree root.
func openLocklessRepo(env *azldev.Env) (*git.Repo, string, error) {
	repo, err := git.OpenRepo(env, env, env.ProjectDir())
	if err != nil {
		return nil, "", fmt.Errorf("opening project repository:\n%w", err)
	}

	return repo, repo.Root(), nil
}

func validateRenderComponents(comps []components.Component) error {
	if len(comps) == 0 {
		return errors.New("no components were selected; " +
			"please use command-line options to indicate which components to render")
	}

	return nil
}

// prepareLocklessRender reads the committed render state and upstream changes
// before overlays. [prepareOverlaidLocklessSpec] prepares the changelog and
// Release after overlays.
func prepareLocklessRender(
	env *azldev.Env, comp components.Component,
	dir, outputDir string, editor spec.EditorMode,
) (*locklessRenderUpdate, lastRender, error) {
	repo, update, err := prepareLocklessRenderOutput(env, outputDir)
	if err != nil {
		return nil, lastRender{}, err
	}

	history, err := readLastRender(env, repo, update.repoRoot, update.path, comp.GetName())
	if err != nil {
		return nil, history, fmt.Errorf("reading committed render state:\n%w", err)
	}

	_, opened, err := openLocklessSpec(env, dir, comp.GetName(), editor)
	if err != nil {
		return nil, history, err
	}

	version, err := opened.GetLastTag("", "Version")
	if err != nil {
		return nil, history, fmt.Errorf("reading upstream Version:\n%w", err)
	}

	if err := collectRenderUpstreamChanges(env, dir, comp.GetName(), version, history, update); err != nil {
		return nil, history, err
	}

	update.name = comp.GetName()
	update.lastCommit = history.commit

	return update, history, nil
}

func prepareFetchedLocklessRender(
	env *azldev.Env, comp components.Component, dir, outputDir string, editor spec.EditorMode,
) (*locklessRenderUpdate, lastRender, error) {
	if comp.GetConfig().Spec.SourceType != projectconfig.SpecSourceTypeLocal {
		return prepareLocklessRender(env, comp, dir, outputDir, editor)
	}

	_, update, err := prepareLocklessRenderOutput(env, outputDir)
	if update != nil {
		update.local = true
	}

	return update, lastRender{}, err
}

// prepareOverlaidLocklessSpec prepares the post-overlay spec's changelog and
// Release. The upstream checkout's HEAD is left at the upstream commit, with
// overlay edits as uncommitted working-tree changes.
func prepareOverlaidLocklessSpec(
	env *azldev.Env, comp components.Component, dir string, editor spec.EditorMode, history lastRender,
) error {
	if err := prepareLocklessSpec(env, hostRenderProcessor{env: env}, dir, comp.GetName(), editor, history); err != nil {
		return fmt.Errorf("preparing changelog and release for %#q:\n%w", comp.GetName(), err)
	}

	return nil
}

// withAmendedUpstream runs query while overlay edits to tracked files are
// amended into the upstream checkout's HEAD commit (see [amendOverlaidUpstream]),
// so host rpmautospec queries against dir do not count them. Afterwards, even
// if query fails, a mixed reset restores HEAD and the index to the original
// upstream commit, leaving the overlay edits as uncommitted working-tree changes.
func withAmendedUpstream(env *azldev.Env, dir string, query func() error) (err error) {
	if env.DryRun() {
		return query()
	}

	head, err := git.RunInDir(env, env, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("reading upstream HEAD:\n%w", err)
	}

	amended, err := amendOverlaidUpstream(env, dir)
	if err != nil {
		return err
	}

	if amended {
		defer func() {
			if _, resetErr := git.RunInDir(env, env, dir, "reset", "-q", head); resetErr != nil {
				err = errors.Join(err, fmt.Errorf("restoring upstream HEAD:\n%w", resetErr))
			}
		}()
	}

	return query()
}

// amendOverlaidUpstream amends the upstream HEAD commit with overlay edits to
// tracked files, so host rpmautospec does not count them as uncommitted changes.
// The original committer identity and date are kept, so calculated releases and
// changelog dates are unchanged. Untracked files do not affect rpmautospec.
// It reports whether HEAD was amended; a clean checkout or dry run is a no-op.
// Callers must restore the original HEAD afterwards; see [withAmendedUpstream].
func amendOverlaidUpstream(env *azldev.Env, dir string) (bool, error) {
	if env.DryRun() {
		return false, nil
	}

	status, err := git.RunInDir(env, env, dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, fmt.Errorf("checking upstream checkout for overlay changes:\n%w", err)
	}

	if status == "" {
		return false, nil
	}

	committer, err := git.RunInDir(env, env, dir, "log", "-1", "--format=%cn%n%ce%n%cI")
	if err != nil {
		return false, fmt.Errorf("reading upstream committer:\n%w", err)
	}

	fields := strings.SplitN(committer, "\n", 3) //nolint:mnd // Name, email, and date.
	if len(fields) != 3 {                        //nolint:mnd // Name, email, and date.
		return false, fmt.Errorf("reading upstream committer: unexpected output %#q", committer)
	}

	raw := exec.CommandContext(env, "git", "-C", dir, "-c", "core.hooksPath=/dev/null",
		"commit", "--all", "--amend", "--no-edit", "--allow-empty", "--no-verify", "--no-gpg-sign")

	raw.Env = append(os.Environ(),
		"GIT_COMMITTER_NAME="+fields[0], "GIT_COMMITTER_EMAIL="+fields[1], "GIT_COMMITTER_DATE="+fields[2])

	var stderr bytes.Buffer

	raw.Stderr = &stderr

	cmd, err := env.Command(raw)
	if err != nil {
		return false, fmt.Errorf("creating upstream amend command:\n%w", err)
	}

	if _, err := cmd.RunAndGetOutput(env); err != nil {
		return false, fmt.Errorf("amending upstream commit with overlay changes:\n%s\n%w", stderr.String(), err)
	}

	return true, nil
}

func prepareLocklessRenderOutput(
	env *azldev.Env, outputDir string,
) (repo *git.Repo, update *locklessRenderUpdate, err error) {
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
	env *azldev.Env, repo *git.Repo, root, path, name string,
) (lastRender, error) {
	var history lastRender

	head, err := repo.Head(env)
	if err != nil {
		return history, fmt.Errorf("reading project HEAD:\n%w", err)
	}

	if head == "" {
		return history, nil
	}

	tree, err := repo.Tree(env, head)
	if err != nil {
		return history, fmt.Errorf("reading tree for commit %#q:\n%w", head, err)
	}

	history.tree, err = tree.Subtree(env, path)
	if errors.Is(err, git.ErrPathNotFound) {
		return history, nil
	}

	if err != nil {
		return history, fmt.Errorf("reading committed dist-git directory:\n%w", err)
	}

	spec, err := tree.ReadFile(env, filepath.ToSlash(filepath.Join(path, name+".spec")))
	if err != nil {
		return history, fmt.Errorf("reading committed spec:\n%w", err)
	}

	history.spec = string(spec)

	lastHash, err := git.RunInDir(env, env, root,
		"--literal-pathspecs", "log", "-1", "--format=%H", "--end-of-options", head, "--", path+"/")
	if err != nil {
		return history, fmt.Errorf("finding last render commit:\n%w", err)
	}

	if lastHash == "" {
		return history, nil
	}

	history.commit = lastHash

	lastTree, err := repo.Tree(env, lastHash)
	if err != nil {
		return history, fmt.Errorf("reading last render commit:\n%w", err)
	}

	err = readRenderedUpstreamCommits(env, root, name, lastTree, &history)

	return history, err
}

func readRenderedUpstreamCommits(
	env *azldev.Env, root, name string, lastTree *git.Tree, history *lastRender,
) error {
	projectPath, err := git.RepoRelPath(root, env.ProjectDir())
	if err != nil {
		return fmt.Errorf("resolving project directory within repository:\n%w", err)
	}

	history.current, history.currentBranch, err = configuredRenderUpstreamCommit(env, name, false)
	if err != nil {
		return fmt.Errorf("reading current render upstream commit:\n%w", err)
	}

	history.previous, history.previousBranch, err = renderUpstreamCommit(env, lastTree, projectPath, name)
	if err != nil {
		slog.Warn("Cannot read last render upstream commit; treating upstream as changed",
			"component", name, "error", err)

		history.previous = ""
		history.previousBranch = ""
	}

	return nil
}

func renderUpstreamCommit(
	env *azldev.Env, tree *git.Tree, projectPath, name string,
) (commit string, branch string, err error) {
	historicalEnv, err := loadHistoricalEnv(env, tree, projectPath)
	if err != nil {
		return "", "", err
	}

	return configuredRenderUpstreamCommit(historicalEnv, name, true)
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

	opened, err = spec.OpenSpec(bytes.NewReader(content), spec.WithEditor(editor), spec.WithPreservedReleaseFormatting())
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

	upstream, err := singleReleaseTags(opened)
	if err != nil {
		return fmt.Errorf("checking upstream 'Release' tags:\n%w", err)
	}

	original := upstream[""]

	version, err := opened.GetLastTag("", "Version")
	if err != nil {
		return fmt.Errorf("reading upstream Version:\n%w", err)
	}

	versionChanged, err := locklessVersionChanged(env, autospec, dir, filepath.Base(path), version, history)
	if err != nil {
		return err
	}

	// A changed version takes its Release only from upstream, so the committed
	// Release tags are neither read nor validated.
	if !versionChanged {
		if _, err := committedReleaseTags(history, editor); err != nil {
			return err
		}
	}

	history, err = staticRenderHistory(env, autospec, dir, name, original, history, versionChanged, editor)
	if err != nil {
		return err
	}

	queried, err := queryLocklessSpec(env, autospec, dir, filepath.Base(path), original, history, versionChanged)
	if err != nil {
		return err
	}

	if err := queried.writeChangelog(env, dir); err != nil {
		return err
	}

	opened, err = carryLocklessInlineChangelog(opened, history.spec, original, editor)
	if err != nil {
		return err
	}

	err = prepareLocklessRelease(env, opened, upstream, queried.update, history, versionChanged, editor)
	if err != nil {
		return err
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

// locklessSpecQueries holds the prepared changelog and Release update, which are
// computed before any prepared file is written into the upstream checkout.
type locklessSpecQueries struct {
	changelog    string
	hasChangelog bool
	update       releaseUpdate
}

// queryLocklessSpec computes the prepared changelog and Release update. Only a
// first render or version change of an '%autorelease' spec queries host
// rpmautospec against the upstream checkout itself; those queries run with
// overlay edits temporarily amended into its HEAD (see [withAmendedUpstream]).
func queryLocklessSpec(
	env *azldev.Env, autospec renderAutorelease, dir, filename, release string, history lastRender,
	versionChanged bool,
) (locklessSpecQueries, error) {
	var queried locklessSpecQueries

	query := func() error {
		var err error

		queried.changelog, queried.hasChangelog, err = locklessChangelog(env, autospec, dir, filename, release, history)
		if err != nil {
			return err
		}

		queried.update, err = locklessRenderRelease(env, autospec, dir, filename, release, history, versionChanged)

		return err
	}

	if sources.ReleaseUsesAutorelease(release) && versionChanged {
		return queried, withAmendedUpstream(env, dir, query)
	}

	return queried, query()
}

// writeChangelog writes the prepared 'changelog' file, if any.
func (q locklessSpecQueries) writeChangelog(env *azldev.Env, dir string) error {
	if !q.hasChangelog {
		return nil
	}

	if err := fileutils.WriteFile(env.FS(), filepath.Join(dir, "changelog"), []byte(q.changelog),
		fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing prepared 'changelog':\n%w", err)
	}

	return nil
}

// prepareLocklessRelease updates the upstream 'Release' tags: '%autorelease'
// tags get update, which applies the calculated or carried base, and static
// tags are carried from the committed render only when the version is unchanged.
func prepareLocklessRelease(
	env *azldev.Env, opened *spec.Spec, upstream releaseTags, update releaseUpdate, history lastRender,
	versionChanged bool, editor spec.EditorMode,
) error {
	if !sources.ReleaseUsesAutorelease(upstream[""]) {
		if versionChanged {
			return nil
		}

		return carryStaticRenderRelease(env, opened, upstream, history, editor)
	}

	for pkg := range upstream {
		if err := opened.UpdateExistingTagFunc(pkg, "Release", update); err != nil {
			return fmt.Errorf("updating upstream Release for %s:\n%w", describeReleasePackage(pkg), err)
		}
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

	carried, err := spec.OpenSpec(bytes.NewReader(content), spec.WithEditor(editor), spec.WithPreservedReleaseFormatting())
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

// releaseUpdate computes the new value of one 'Release' tag from its current value.
type releaseUpdate func(release string) (string, error)

func keepRelease(release string) (string, error) {
	return release, nil
}

// autoreleaseBaseUpdate returns a [releaseUpdate] that applies the given
// autorelease base to each '%autorelease' 'Release' tag, preserving any other
// arguments on that tag. The base must already be validated.
func autoreleaseBaseUpdate(output string, omitDefault bool) releaseUpdate {
	return func(release string) (string, error) {
		if !sources.ReleaseUsesAutorelease(release) {
			return release, nil
		}

		return withAutoreleaseBase(release, output, omitDefault)
	}
}

func stripAutoreleaseBase(release string) (string, error) {
	return autoreleaseBaseArg.ReplaceAllString(release, ""), nil
}

func locklessRenderRelease(
	env *azldev.Env, autospec renderAutorelease, dir, filename, release string, history lastRender,
	versionChanged bool,
) (releaseUpdate, error) {
	if !sources.ReleaseUsesAutorelease(release) || env.DryRun() {
		return keepRelease, nil
	}

	if history.spec != "" && !versionChanged {
		return carryRenderedAutorelease(env, autospec, dir, filename, history)
	}

	calculated, err := autospec.CalculateBaseRelease(env, dir, filename)
	if err != nil {
		return nil, fmt.Errorf("calculating upstream release base:\n%w", err)
	}

	if _, _, err := parseAutoreleaseBase(calculated); err != nil {
		return nil, err
	}

	return autoreleaseBaseUpdate(calculated, true), nil
}

var (
	renderedAutoreleaseMarker = regexp.MustCompile(`(?m)^## RPMAUTOSPEC:[^\r\n]*autorelease`)
	renderedAutoreleaseBase   = regexp.MustCompile(`(?:^|\s)-b(?:\s+|=)?([^\s}]*)`)
	renderedReleaseNumber     = regexp.MustCompile(`^[0-9]+`)
)

func carryRenderedAutorelease(
	env *azldev.Env, autospec renderAutorelease, dir, filename string, history lastRender,
) (releaseUpdate, error) {
	if !renderedAutoreleaseMarker.MatchString(history.spec) {
		return carryUnprocessedAutorelease(history)
	}

	expanded, err := expandCommittedRelease(env, autospec, dir, filename, history)
	if err != nil {
		return nil, fmt.Errorf("expanding committed Release:\n%w", err)
	}

	base := renderedReleaseNumber.FindString(strings.TrimSpace(expanded))
	if _, _, err := parseAutoreleaseBase(base); err != nil {
		return nil, fmt.Errorf("using committed Release %#q as autorelease base:\n%w", expanded, err)
	}

	return autoreleaseBaseUpdate(base, true), nil
}

func carryUnprocessedAutorelease(history lastRender) (releaseUpdate, error) {
	previous, err := spec.OpenSpec(strings.NewReader(history.spec))
	if err != nil {
		return nil, fmt.Errorf("parsing committed autorelease spec:\n%w", err)
	}

	previousRelease, err := previous.GetLastTag("", "Release")
	if err != nil {
		return nil, fmt.Errorf("reading committed Release:\n%w", err)
	}

	if !sources.ReleaseUsesAutorelease(previousRelease) {
		return keepRelease, nil
	}

	matches := renderedAutoreleaseBase.FindStringSubmatch(previousRelease)
	if matches == nil {
		return stripAutoreleaseBase, nil
	}

	if _, _, err := parseAutoreleaseBase(matches[1]); err != nil {
		return nil, fmt.Errorf("using committed autorelease base:\n%w", err)
	}

	return autoreleaseBaseUpdate(matches[1], false), nil
}

// locklessChangelog returns the content of the prepared 'changelog' file for an
// '%autorelease' spec, and whether the file should be written.
func locklessChangelog(
	env *azldev.Env, autospec renderAutorelease, dir, filename, release string, history lastRender,
) (changelog string, write bool, err error) {
	if !sources.ReleaseUsesAutorelease(release) || env.DryRun() {
		return "", false, nil
	}

	if history.spec != "" && !renderedAutoreleaseMarker.MatchString(history.spec) {
		return committedUnprocessedChangelog(env, history)
	}

	if history.spec == "" {
		changelog, err = autospec.GenerateChangelog(env, dir, filename)
		if err != nil {
			return "", false, fmt.Errorf("generating initial upstream changelog:\n%w", err)
		}

		return changelog, true, nil
	}

	changelog, _, err = committedChangelogSection(history.spec)
	if err != nil {
		return "", false, err
	}

	return stripChangelogBoundaryComments(changelog), true, nil
}

// committedChangelogSection returns the body of a committed spec's '%changelog'
// section, and whether the spec has one.
func committedChangelogSection(content string) (changelog string, found bool, err error) {
	previous := []byte(content)

	start, end, err := renderChangelogBounds(previous, spec.EditorLegacy)
	if err != nil {
		return "", false, err
	}

	if start == len(previous) {
		return "", false, nil
	}

	_, changelog, _ = strings.Cut(string(previous[start:end]), "\n")

	return changelog, true, nil
}

// committedUnprocessedChangelog returns the 'changelog' file content carried
// from a committed spec that rpmautospec did not process: the committed
// 'changelog' file for an '%autorelease' spec, or the committed '%changelog'
// section body for a static Release spec.
func committedUnprocessedChangelog(
	ctx context.Context, history lastRender,
) (changelog string, write bool, err error) {
	previous, err := spec.OpenSpec(strings.NewReader(history.spec))
	if err != nil {
		return "", false, fmt.Errorf("parsing committed autorelease spec:\n%w", err)
	}

	release, err := previous.GetLastTag("", "Release")
	if err != nil {
		return "", false, fmt.Errorf("reading committed Release:\n%w", err)
	}

	if !sources.ReleaseUsesAutorelease(release) {
		return committedChangelogSection(history.spec)
	}

	if history.tree == nil {
		return "", false, errors.New("missing committed dist-git tree for 'changelog'")
	}

	content, err := history.tree.ReadFile(ctx, "changelog")
	if err != nil {
		return "", false, fmt.Errorf("reading committed 'changelog':\n%w", err)
	}

	return string(content), true, nil
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
		if err := copyTreeToFS(env, history.tree, env.FS(), snapshot); err != nil {
			return "", fmt.Errorf("copying committed dist-git snapshot:\n%w", err)
		}
	}

	if err := fileutils.WriteFile(env.FS(), filepath.Join(snapshot, filename), []byte(history.spec),
		fileperms.PublicFile); err != nil {
		return "", fmt.Errorf("writing committed spec snapshot:\n%w", err)
	}

	return snapshot, nil
}

// locklessVersionChanged reports whether the upstream Version differs from the
// committed one. Tags using macros are expanded in their own spec context, so a
// changed macro definition is detected even when the raw tag text is identical.
func locklessVersionChanged(
	env *azldev.Env, autospec renderAutorelease, dir, filename, version string, history lastRender,
) (changed bool, err error) {
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

	if !strings.Contains(oldVersion, "%") && !strings.Contains(version, "%") {
		return oldVersion != version, nil
	}

	newExpanded, err := autospec.ExpandVersion(env, dir, filename)
	if err != nil {
		return false, fmt.Errorf("querying upstream Version:\n%w", err)
	}

	snapshot, err := committedRenderSnapshot(env, dir, filename, history)
	if err != nil {
		return false, err
	}
	defer fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), snapshot, &err)

	oldExpanded, err := autospec.ExpandVersion(env, snapshot, filename)
	if err != nil {
		return false, fmt.Errorf("querying committed Version:\n%w", err)
	}

	return oldExpanded != newExpanded, nil
}

var autoreleaseBaseArg = regexp.MustCompile(`(^|\s)-b(?:\s+|=)?[0-9]+`)

// parseAutoreleaseBase returns the positive release number at the end of output.
func parseAutoreleaseBase(output string) (number string, value int, err error) {
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return "", 0, errors.New("empty autorelease base")
	}

	number = fields[len(fields)-1]

	value, err = strconv.Atoi(number)
	if err != nil || value < 1 {
		return "", 0, fmt.Errorf("invalid autorelease base %#q", output)
	}

	return number, value, nil
}

func withAutoreleaseBase(release, output string, omitDefault bool) (string, error) {
	number, value, err := parseAutoreleaseBase(output)
	if err != nil {
		return "", err
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

		update.unchanged = true
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

// validateRenderNEVR rejects replacing rendered output with a lower NEVR.
// Unprocessed %autorelease specs are resolved against the component's filtered
// dist-git history, as lockless builds resolve them, instead of plain rpmspec.
func validateRenderNEVR(env *azldev.Env, preparedDir string, prep *preparedComponent) error {
	existingPath := filepath.Join(prep.compOutputDir, prep.specFilename)
	newPath := filepath.Join(preparedDir, prep.specFilename)

	existingHistory, err := renderSpecNeedsHistory(env, existingPath)
	if err != nil {
		return fmt.Errorf("%w: checking existing spec autorelease:\n%w", errRenderNEVRValidation, err)
	}

	newHistory, err := renderSpecNeedsHistory(env, newPath)
	if err != nil {
		return fmt.Errorf("%w: checking new spec autorelease:\n%w", errRenderNEVRValidation, err)
	}

	existing, rendered, err := queryRenderNEVRs(env, preparedDir, prep, existingHistory, newHistory)
	if err != nil {
		return err
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

func queryRenderNEVRs(
	env *azldev.Env, preparedDir string, prep *preparedComponent, existingHistory, newHistory bool,
) (existing, rendered *rpm.SpecInfo, err error) {
	var history renderNEVRHistory

	if existingHistory || newHistory {
		history, err = openRenderNEVRHistory(env, filepath.Dir(preparedDir), prep.compOutputDir)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: reading dist-git history:\n%w", errRenderNEVRValidation, err)
		}

		defer func() {
			if removeErr := history.Remove(); removeErr != nil && err == nil {
				err = fmt.Errorf("%w:\n%w", errRenderNEVRValidation, removeErr)
			}
		}()
	}

	if existingHistory {
		existing, err = history.Committed(prep.specFilename)
	} else {
		existing, err = queryRenderSpecNEVR(env, filepath.Join(prep.compOutputDir, prep.specFilename),
			prep.compOutputDir)
	}

	if err != nil {
		return nil, nil, fmt.Errorf("%w: querying existing NEVR:\n%w", errRenderNEVRValidation, err)
	}

	if newHistory {
		rendered, err = history.Rendered(preparedDir, prep.specFilename)
	} else {
		rendered, err = queryRenderSpecNEVR(env, filepath.Join(preparedDir, prep.specFilename), preparedDir)
	}

	if err != nil {
		return nil, nil, fmt.Errorf("%w: querying new NEVR:\n%w", errRenderNEVRValidation, err)
	}

	return existing, rendered, nil
}

// renderSpecNeedsHistory reports whether a spec uses %autorelease without
// having been processed by rpmautospec, so its release depends on history.
func renderSpecNeedsHistory(env *azldev.Env, path string) (bool, error) {
	content, err := fileutils.ReadFile(env.FS(), path)
	if err != nil {
		return false, fmt.Errorf("reading spec %#q:\n%w", path, err)
	}

	if renderedAutoreleaseMarker.Match(content) {
		return false, nil
	}

	opened, err := spec.OpenSpec(bytes.NewReader(content))
	if err != nil {
		return false, fmt.Errorf("parsing spec %#q:\n%w", path, err)
	}

	release, err := opened.GetLastTag("", "Release")
	if errors.Is(err, spec.ErrNoSuchTag) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("reading Release from %#q:\n%w", path, err)
	}

	return sources.ReleaseUsesAutorelease(release), nil
}

func queryRenderSpecNEVR(env *azldev.Env, path, dir string) (*rpm.SpecInfo, error) {
	if err := prereqs.RequireExecutable(env, "rpmspec", nil); err != nil {
		return nil, fmt.Errorf("lockless render NEVR validation requires 'rpmspec':\n%w", err)
	}

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

// resolveLocklessMessage sets the changelog text when '--commit-message' was not
// given: [renderNoChangeMessage] for an unchanged render, otherwise the text
// detected by [renderMessageDetector] (if any).
func resolveLocklessMessage(update *locklessRenderUpdate) error {
	if update.message != "" {
		return nil
	}

	if update.unchanged {
		update.message = renderNoChangeMessage

		return nil
	}

	if update.detector == nil {
		return nil
	}

	message, err := update.detector.detect(update.name, update.lastCommit)
	if err != nil {
		return err
	}

	update.message = message

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

	if err := resolveLocklessMessage(update); err != nil {
		return false, err
	}

	masked, err := maskChangelogReleaseLines(opened, before)
	if err != nil {
		return false, err
	}

	if masked == nil {
		return runLocklessBumpspec(env, path, update, before)
	}

	if err := fileutils.WriteFile(env.FS(), path, masked, fileperms.PublicFile); err != nil {
		return false, fmt.Errorf("masking %%changelog Release lines before release bump:\n%w", err)
	}

	changed, err = runLocklessBumpspec(env, path, update, before)
	if err != nil {
		if restoreErr := fileutils.WriteFile(env.FS(), path, before, fileperms.PublicFile); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restoring spec after failed release bump:\n%w", restoreErr))
		}

		return false, err
	}

	return changed, nil
}

// changelogReleaseMask prefixes '%changelog' lines that rpmdev-bumpspec would otherwise
// treat as Release tags; rpmdev-bumpspec does not distinguish spec sections.
const changelogReleaseMask = "azldev-masked-changelog-line:"

var changelogReleaseLinePattern = regexp.MustCompile(`(?i)^release\s*:`)

// maskChangelogReleaseLines returns the spec with '%changelog' lines starting with 'Release:'
// masked, or nil when there are no such lines.
func maskChangelogReleaseLines(opened *spec.Spec, original []byte) ([]byte, error) {
	masked := false

	err := opened.Visit(func(ctx *spec.Context) error {
		if ctx.Target.TargetType != spec.SectionLineTarget || ctx.RawLine == nil ||
			ctx.CurrentSection.SectType != spec.ChangelogSection ||
			!changelogReleaseLinePattern.MatchString(*ctx.RawLine) {
			return nil
		}

		ctx.ReplaceLine(changelogReleaseMask + *ctx.RawLine)

		masked = true

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("finding %%changelog Release lines:\n%w", err)
	}

	if !masked {
		return nil, nil
	}

	if bytes.Contains(original, []byte(changelogReleaseMask)) {
		return nil, fmt.Errorf("spec already contains reserved text %#q", changelogReleaseMask)
	}

	var buffer bytes.Buffer
	if err := opened.Serialize(&buffer); err != nil {
		return nil, fmt.Errorf("masking %%changelog Release lines:\n%w", err)
	}

	return buffer.Bytes(), nil
}

// runLocklessBumpspec runs rpmdev-bumpspec, unmasks any masked '%changelog' lines, and
// reports whether the spec differs from before.
func runLocklessBumpspec(
	env *azldev.Env, path string, update *locklessRenderUpdate, before []byte,
) (changed bool, err error) {
	raw := exec.CommandContext(env, sources.RPMDevBumpspecBinary, "-c",
		locklessBumpspecComment(update.message, update.changes), path)
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

	if bytes.Contains(after, []byte(changelogReleaseMask)) {
		after = bytes.ReplaceAll(after, []byte(changelogReleaseMask), nil)
		if err := fileutils.WriteFile(env.FS(), path, after, fileperms.PublicFile); err != nil {
			return false, fmt.Errorf("unmasking %%changelog Release lines after release bump:\n%w", err)
		}
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

func finalizeLocklessRenders(
	env *azldev.Env, prepared []*preparedComponent, results []*RenderResult, checkOnly bool,
) {
	if checkOnly || env.DryRun() {
		return
	}

	for _, prep := range prepared {
		result := results[prep.index]

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
