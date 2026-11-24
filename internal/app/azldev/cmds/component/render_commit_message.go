// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/spf13/afero"
)

const (
	// renderInitialMessage is the detected changelog text for a component's first render.
	renderInitialMessage = "Initial import from upstream."
	// renderNoChangeMessage is the changelog text for an unchanged render with
	// '--no-change-rebuild'.
	renderNoChangeMessage = "No-change rebuild."
)

var (
	errUncommittedComponentChanges = errors.New(
		"uncommitted project changes modify the component; commit them or use '--commit-message'")
	errNoComponentCommits = errors.New(
		"rendered output changed but no project commit since the last render changed the component; " +
			"use '--commit-message'")
)

// workingSnapshot is the cache key for the working-tree snapshot.
const workingSnapshot = ""

// renderMessageDetector detects lockless rpmdev-bumpspec changelog text from the
// project commits that changed a component since its last render. It is shared by
// all components of one render and caches each loaded project snapshot.
type renderMessageDetector struct {
	env *azldev.Env

	mutex         sync.Mutex
	initialized   bool
	root          string
	projectRelDir string
	skipDir       string
	envs          map[string]*azldev.Env
	inputs        map[string][]byte
}

func newRenderMessageDetector(env *azldev.Env) *renderMessageDetector {
	return &renderMessageDetector{
		env:    env,
		envs:   make(map[string]*azldev.Env),
		inputs: make(map[string][]byte),
	}
}

// detect returns the changelog text for the component's release bump:
//   - [renderInitialMessage] when the component has no previous render commit;
//   - an error when uncommitted project changes modify the component;
//   - the subjects (oldest first) of commits since the last render commit that
//     changed the component, joined as rpmdev-bumpspec changelog lines;
//   - otherwise [errNoComponentCommits], since changed output needs a message.
func (d *renderMessageDetector) detect(name, lastCommit string) (string, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	if lastCommit == "" {
		return renderInitialMessage, nil
	}

	if err := d.initialize(); err != nil {
		return "", err
	}

	head, err := git.RunInDir(d.env, d.env, d.root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolving project HEAD:\n%w", err)
	}

	if err := d.checkWorkingTree(name, head); err != nil {
		return "", err
	}

	subjects, err := d.changedSubjects(name, lastCommit)
	if err != nil {
		return "", err
	}

	if len(subjects) == 0 {
		return "", fmt.Errorf("detecting changelog text for %#q:\n%w", name, errNoComponentCommits)
	}

	return strings.Join(subjects, "\n- "), nil
}

func (d *renderMessageDetector) initialize() error {
	if d.initialized {
		return nil
	}

	_, root, err := openRenderRepo(d.env)
	if err != nil {
		return err
	}

	d.root = root

	d.projectRelDir, err = git.RepoRelPath(root, d.env.ProjectDir())
	if err != nil {
		return fmt.Errorf("resolving project directory within repository:\n%w", err)
	}

	if renderedDir := d.env.Config().Project.RenderedSpecsDir; renderedDir != "" {
		if rel, relErr := git.RepoRelPath(root, renderedDir); relErr == nil && rel != "." {
			d.skipDir = filepath.ToSlash(rel)
		}
	}

	d.initialized = true

	return nil
}

// changedSubjects walks first-parent commits from HEAD back to (excluding)
// lastCommit and returns, oldest first, the subjects of commits that changed the
// component relative to the preceding commit. The walk stops at the first commit
// whose component matches the one at lastCommit.
func (d *renderMessageDetector) changedSubjects(name, lastCommit string) ([]string, error) {
	log, err := git.RunInDir(d.env, d.env, d.root, "log", "--first-parent", "--format=%H%x00%s",
		"--end-of-options", lastCommit+"..HEAD", "--")
	if err != nil {
		return nil, fmt.Errorf("listing commits since the last render:\n%w", err)
	}

	var commits, subjects []string

	for _, line := range strings.Split(log, "\n") {
		if hash, subject, found := strings.Cut(line, "\x00"); found {
			commits = append(commits, hash)
			subjects = append(subjects, subject)
		}
	}

	var changed []string

	for idx, commit := range commits {
		same, err := d.sameComponent(name, commit, lastCommit)
		if err != nil {
			return nil, err
		}

		if same {
			break
		}

		previous := lastCommit
		if idx+1 < len(commits) {
			previous = commits[idx+1]
		}

		same, err = d.sameComponent(name, commit, previous)
		if err != nil {
			return nil, err
		}

		if !same {
			changed = append(changed, subjects[idx])
		}
	}

	slices.Reverse(changed)

	return changed, nil
}

// sameComponent reports whether the component's comparison inputs are equal at
// two commits. Commits differing only in the rendered-specs directory are equal.
func (d *renderMessageDetector) sameComponent(name, left, right string) (bool, error) {
	if left == right {
		return true, nil
	}

	paths, err := git.RunInDir(d.env, d.env, d.root, "diff-tree", "-r", "-z", "--name-only",
		"--end-of-options", left, right, "--")
	if err != nil {
		return false, fmt.Errorf("comparing commits %#q and %#q:\n%w", left, right, err)
	}

	if !d.relevantPaths(strings.Split(paths, "\x00")) {
		return true, nil
	}

	leftInputs, err := d.componentInputs(name, left)
	if err != nil {
		return false, err
	}

	rightInputs, err := d.componentInputs(name, right)
	if err != nil {
		return false, err
	}

	return bytes.Equal(leftInputs, rightInputs), nil
}

// checkWorkingTree fails when uncommitted project changes outside the
// rendered-specs directory modify the component relative to head.
func (d *renderMessageDetector) checkWorkingTree(name, head string) error {
	tracked, err := git.RunInDir(d.env, d.env, d.root, "diff", "--name-only", "-z", "HEAD", "--")
	if err != nil {
		return fmt.Errorf("listing uncommitted project changes:\n%w", err)
	}

	untracked, err := git.RunInDir(d.env, d.env, d.root, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return fmt.Errorf("listing untracked project files:\n%w", err)
	}

	if !d.relevantPaths(strings.Split(tracked+"\x00"+untracked, "\x00")) {
		return nil
	}

	committed, err := d.componentInputs(name, head)
	if err != nil {
		return err
	}

	working, err := d.componentInputs(name, workingSnapshot)
	if err != nil {
		return err
	}

	if !bytes.Equal(committed, working) {
		return fmt.Errorf("detecting changelog text for %#q:\n%w", name, errUncommittedComponentChanges)
	}

	return nil
}

func (d *renderMessageDetector) relevantPaths(paths []string) bool {
	return slices.ContainsFunc(paths, func(path string) bool {
		return path != "" && !pathWithinDir(path, d.skipDir)
	})
}

// componentInputs returns the serialized comparison inputs (see
// [buildComponentComparisonInputs]) for the component at commit, or at the
// working tree for [workingSnapshot]. A missing component yields nil.
func (d *renderMessageDetector) componentInputs(name, commit string) ([]byte, error) {
	key := commit + "\x00" + name
	if inputs, found := d.inputs[key]; found {
		return inputs, nil
	}

	snapshotEnv, err := d.snapshotEnv(commit)
	if err != nil {
		return nil, err
	}

	var inputs []byte

	if _, found := snapshotEnv.Config().Components[name]; found {
		comp, err := components.NewResolver(snapshotEnv).GetComponentByName(name)
		if err != nil {
			return nil, fmt.Errorf("resolving component %#q:\n%w", name, err)
		}

		comparison, err := buildComponentComparisonInputs(snapshotEnv.FS(), snapshotEnv, comp.GetConfig())
		if err != nil {
			return nil, fmt.Errorf("building change inputs for component %#q:\n%w", name, err)
		}

		inputs, err = json.Marshal(comparison)
		if err != nil {
			return nil, fmt.Errorf("serializing component %#q:\n%w", name, err)
		}
	}

	d.inputs[key] = inputs

	return inputs, nil
}

func (d *renderMessageDetector) snapshotEnv(commit string) (*azldev.Env, error) {
	if snapshotEnv, found := d.envs[commit]; found {
		return snapshotEnv, nil
	}

	var (
		snapshotEnv *azldev.Env
		err         error
	)

	if commit == workingSnapshot {
		snapshotEnv, err = d.loadWorkingEnv()
	} else {
		snapshotEnv, err = d.loadCommitEnv(commit)
	}

	if err != nil {
		return nil, err
	}

	d.envs[commit] = snapshotEnv

	return snapshotEnv, nil
}

func (d *renderMessageDetector) loadCommitEnv(commit string) (*azldev.Env, error) {
	repo, _, err := openRenderRepo(d.env)
	if err != nil {
		return nil, err
	}

	tree, err := resolveTree(repo, commit)
	if err != nil {
		return nil, err
	}

	snapshotEnv, err := loadHistoricalEnvExcept(d.env, tree, d.projectRelDir, d.skipDir)
	if err != nil {
		return nil, fmt.Errorf("loading project configuration at %#q:\n%w", commit, err)
	}

	return snapshotEnv, nil
}

// loadWorkingEnv loads the project configuration from the tracked and untracked
// (non-ignored) working-tree files, excluding the rendered-specs directory.
func (d *renderMessageDetector) loadWorkingEnv() (*azldev.Env, error) {
	files, err := git.RunInDir(d.env, d.env, d.root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("listing working project files:\n%w", err)
	}

	snapshotFS := afero.NewMemMapFs()

	for _, file := range strings.Split(files, "\x00") {
		if file == "" || pathWithinDir(file, d.skipDir) {
			continue
		}

		source := filepath.Join(d.root, filepath.FromSlash(file))

		info, err := d.env.FS().Stat(source)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("reading working file %#q:\n%w", file, err)
		}

		if info.IsDir() {
			continue
		}

		content, err := fileutils.ReadFile(d.env.FS(), source)
		if err != nil {
			return nil, fmt.Errorf("reading working file %#q:\n%w", file, err)
		}

		destination := filepath.Join(snapshotRepoRoot, filepath.FromSlash(file))
		if err := fileutils.MkdirAll(snapshotFS, filepath.Dir(destination)); err != nil {
			return nil, fmt.Errorf("creating parent directory for %#q:\n%w", file, err)
		}

		if err := fileutils.WriteFile(snapshotFS, destination, content, fileperms.PublicFile); err != nil {
			return nil, fmt.Errorf("writing %#q:\n%w", file, err)
		}
	}

	snapshotEnv, err := loadSnapshotEnv(d.env, snapshotFS, d.projectRelDir)
	if err != nil {
		return nil, fmt.Errorf("loading working project configuration:\n%w", err)
	}

	return snapshotEnv, nil
}
