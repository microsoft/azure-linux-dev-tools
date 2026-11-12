// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
)

// renderNEVRHistory resolves %autorelease specs against the component's
// filtered dist-git history, which is the history lockless builds use.
type renderNEVRHistory interface {
	// Committed returns the NEVR of the committed rendered output.
	Committed(filename string) (*rpm.SpecInfo, error)
	// Rendered returns the NEVR the prepared output would have once committed.
	Rendered(preparedDir, filename string) (*rpm.SpecInfo, error)
	Remove() error
}

//nolint:gochecknoglobals // Replaced by isolated NEVR tests that do not run git or rpmautospec.
var openRenderNEVRHistory = func(env *azldev.Env, parent, outputDir string) (renderNEVRHistory, error) {
	return newRenderHistoryRepo(env, parent, outputDir)
}

const renderHistoryProcessedSpec = "azldev-processed.spec"

type renderHistoryRepo struct {
	env *azldev.Env
	dir string
	tip string
}

func newRenderHistoryRepo(env *azldev.Env, parent, outputDir string) (repo *renderHistoryRepo, err error) {
	if err := prereqs.RequireExecutable(env, "git", nil); err != nil {
		return nil, fmt.Errorf("lockless render NEVR validation requires 'git':\n%w", err)
	}

	repoRoot, err := git.RunInDir(env, env, env.ProjectDir(), "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("finding project repository:\n%w", err)
	}

	subdir, err := projectSpecRepoRelPath(env, repoRoot, outputDir)
	if err != nil {
		return nil, err
	}

	dir, err := fileutils.MkdirTemp(env.FS(), parent, "azldev-render-history-")
	if err != nil {
		return nil, fmt.Errorf("creating dist-git history directory:\n%w", err)
	}

	repo = &renderHistoryRepo{env: env, dir: dir}

	defer func() {
		if err != nil {
			fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), dir, &err)
		}
	}()

	merge, err := git.RunInDir(env, env, repoRoot, "rev-list", "--merges", "-n1", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("checking project history for merges:\n%w", err)
	}

	if merge != "" {
		err = repo.filter(repoRoot, filepath.ToSlash(subdir))
	} else {
		err = repo.replay(repoRoot, filepath.ToSlash(subdir))
	}

	if err != nil {
		return nil, err
	}

	return repo, nil
}

// replay rebuilds the component's 'git filter-repo --subdirectory-filter'
// history without rewriting the whole project. It requires merge-free history,
// where the path-limited commit list matches the commits filter-repo keeps.
// The repository borrows project objects read-only through alternates.
func (r *renderHistoryRepo) replay(repoRoot, subdir string) error {
	format, err := git.RunInDir(r.env, r.env, repoRoot, "rev-parse", "--show-object-format")
	if err != nil {
		return fmt.Errorf("reading project object format:\n%w", err)
	}

	objects, err := git.RunInDir(r.env, r.env, repoRoot, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return fmt.Errorf("finding project objects:\n%w", err)
	}

	if _, err := git.RunInDir(r.env, r.env, r.dir, "init", "-q", "--object-format="+format); err != nil {
		return fmt.Errorf("creating dist-git history repository:\n%w", err)
	}

	if err := fileutils.WriteFile(r.env.FS(), filepath.Join(r.dir, ".git", "objects", "info", "alternates"),
		[]byte(objects+"\n"), fileperms.PublicFile); err != nil {
		return fmt.Errorf("sharing project objects:\n%w", err)
	}

	if _, err := r.git("config", "gc.auto", "0"); err != nil {
		return fmt.Errorf("disabling history repository maintenance:\n%w", err)
	}

	list, err := git.RunInDir(r.env, r.env, repoRoot, "--literal-pathspecs",
		"rev-list", "--reverse", "HEAD", "--", subdir)
	if err != nil {
		return fmt.Errorf("listing component dist-git commits:\n%w", err)
	}

	for _, commit := range strings.Fields(list) {
		if r.tip, err = r.replayCommit(commit, subdir); err != nil {
			return fmt.Errorf("replaying commit %#q:\n%w", commit, err)
		}
	}

	if r.tip == "" {
		return nil
	}

	if _, err := r.git("update-ref", "HEAD", r.tip); err != nil {
		return fmt.Errorf("updating dist-git history branch:\n%w", err)
	}

	return nil
}

func (r *renderHistoryRepo) replayCommit(commit, subdir string) (string, error) {
	raw, err := r.git("cat-file", "commit", commit)
	if err != nil {
		return "", fmt.Errorf("reading commit:\n%w", err)
	}

	tree, err := r.componentTree(commit, subdir)
	if err != nil {
		return "", err
	}

	path := filepath.Join(r.dir, ".git", "azldev-commit")
	if err := fileutils.WriteFile(r.env.FS(), path, []byte(rewriteRenderHistoryCommit(raw, tree, r.tip)),
		fileperms.PublicFile); err != nil {
		return "", fmt.Errorf("writing rewritten commit:\n%w", err)
	}

	hash, err := r.git("hash-object", "-t", "commit", "-w", "--", path)
	if err != nil {
		return "", fmt.Errorf("writing rewritten commit:\n%w", err)
	}

	return hash, nil
}

func (r *renderHistoryRepo) componentTree(commit, subdir string) (string, error) {
	entry, err := r.git("ls-tree", "--full-tree", commit, "--", subdir)
	if err != nil {
		return "", fmt.Errorf("reading component tree:\n%w", err)
	}

	if fields := strings.Fields(entry); len(fields) >= 3 && fields[1] == "tree" {
		return fields[2], nil
	}

	// filter-repo keeps a deletion of the directory as an empty-tree commit.
	tree, err := r.git("hash-object", "-t", "tree", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("writing empty tree:\n%w", err)
	}

	return tree, nil
}

// rewriteRenderHistoryCommit replaces a raw commit's tree and parent and drops
// signatures, as filter-repo does, keeping all other headers and the message.
func rewriteRenderHistoryCommit(raw, tree, parent string) string {
	headers, message, _ := strings.Cut(raw, "\n\n")
	rewritten := []string{"tree " + tree}

	if parent != "" {
		rewritten = append(rewritten, "parent "+parent)
	}

	skipping := false

	for _, line := range strings.Split(headers, "\n") {
		if !strings.HasPrefix(line, " ") {
			name, _, _ := strings.Cut(line, " ")
			skipping = slices.Contains([]string{"tree", "parent", "gpgsig", "gpgsig-sha256", "mergetag"}, name)
		}

		if !skipping {
			rewritten = append(rewritten, line)
		}
	}

	return strings.Join(rewritten, "\n") + "\n\n" + message + "\n"
}

// filter uses the same filtering lockless builds use, for histories with merges.
func (r *renderHistoryRepo) filter(repoRoot, subdir string) error {
	if err := requireGitFilterRepo(r.env); err != nil {
		return err
	}

	if _, err := git.RunInDir(r.env, r.env, repoRoot,
		"clone", "-q", "--single-branch", "--no-local", "--no-checkout", "--", repoRoot, r.dir); err != nil {
		return fmt.Errorf("cloning project repository:\n%w", err)
	}

	if _, err := r.git("filter-repo", "--force", "--subdirectory-filter", subdir); err != nil {
		return fmt.Errorf("filtering component dist-git history:\n%w", err)
	}

	// An empty filtered history leaves no HEAD commit; treat it as no history.
	if tip, err := r.git("rev-parse", "--verify", "-q", "HEAD"); err == nil {
		r.tip = tip
	}

	return nil
}

func (r *renderHistoryRepo) Committed(filename string) (*rpm.SpecInfo, error) {
	if r.tip == "" {
		return nil, errors.New("no committed dist-git history")
	}

	return r.query(filename)
}

func (r *renderHistoryRepo) Rendered(preparedDir, filename string) (*rpm.SpecInfo, error) {
	// Stage the prepared output (git skips its '.git') as the next render commit.
	if _, err := r.git("--work-tree="+preparedDir, "add", "-A"); err != nil {
		return nil, fmt.Errorf("staging prepared output:\n%w", err)
	}

	tree, err := r.git("write-tree")
	if err != nil {
		return nil, fmt.Errorf("writing prepared output tree:\n%w", err)
	}

	previous := ""
	if r.tip != "" {
		if previous, err = r.git("rev-parse", r.tip+"^{tree}"); err != nil {
			return nil, fmt.Errorf("reading committed tree:\n%w", err)
		}
	}

	// Unchanged output produces no render commit, so the committed history stays current.
	if tree != previous {
		args := []string{
			"-c", "user.name=azldev", "-c", "user.email=azldev@localhost",
			"commit-tree", "--no-gpg-sign", "-m", "azldev render NEVR validation",
		}
		if r.tip != "" {
			args = append(args, "-p", r.tip)
		}

		commit, err := r.git(append(args, tree)...)
		if err != nil {
			return nil, fmt.Errorf("committing prepared output:\n%w", err)
		}

		if _, err := r.git("update-ref", "HEAD", commit); err != nil {
			return nil, fmt.Errorf("updating dist-git history branch:\n%w", err)
		}

		r.tip = commit
	}

	return r.query(filename)
}

// query checks out the history tip and resolves its spec as the mock rpmautospec plugin does.
func (r *renderHistoryRepo) query(filename string) (*rpm.SpecInfo, error) {
	// Rebuild the index so every file is written, not only those differing from staged stat data.
	for _, args := range [][]string{
		{"read-tree", "--empty"}, {"clean", "-q", "-f", "-d", "-x"}, {"read-tree", "--reset", "-u", r.tip},
	} {
		if _, err := r.git(args...); err != nil {
			return nil, fmt.Errorf("checking out dist-git history:\n%w", err)
		}
	}

	if err := prereqs.RequireExecutable(r.env, "rpmautospec", nil); err != nil {
		return nil, fmt.Errorf("lockless render NEVR validation requires 'rpmautospec':\n%w", err)
	}

	processed := filepath.Join(r.dir, ".git", renderHistoryProcessedSpec)

	raw := exec.CommandContext(r.env, "rpmautospec", "process-distgit", filename, processed)
	raw.Dir = r.dir

	var stderr bytes.Buffer

	raw.Stderr = &stderr

	command, err := r.env.Command(raw)
	if err != nil {
		return nil, fmt.Errorf("creating rpmautospec command:\n%w", err)
	}

	if _, err := command.RunAndGetOutput(r.env); err != nil {
		return nil, fmt.Errorf("processing %#q with rpmautospec (stderr: %#q):\n%w", filename, stderr.String(), err)
	}

	return queryRenderSpecNEVR(r.env, processed, r.dir)
}

func (r *renderHistoryRepo) Remove() error {
	if err := r.env.FS().RemoveAll(r.dir); err != nil {
		return fmt.Errorf("removing dist-git history directory %#q:\n%w", r.dir, err)
	}

	return nil
}

func (r *renderHistoryRepo) git(args ...string) (string, error) {
	output, err := git.RunInDir(r.env, r.env, r.dir, args...)
	if err != nil {
		return "", fmt.Errorf("running git in dist-git history:\n%w", err)
	}

	return output, nil
}
