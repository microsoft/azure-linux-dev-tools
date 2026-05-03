// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/spf13/cobra"
)

// ChangedComponentOptions holds options for the component changed command.
type ChangedComponentOptions struct {
	ComponentFilter components.ComponentFilter
	// From is the git ref to compare from (e.g., branch, tag, commit hash).
	From string
	// To is the git ref to compare to. Defaults to HEAD.
	To string
	// IncludeUnchanged includes unchanged components in the output.
	IncludeUnchanged bool
}

func changedOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewChangedCmd())
}

// NewChangedCmd constructs a [cobra.Command] for the "component changed" CLI subcommand.
func NewChangedCmd() *cobra.Command {
	options := &ChangedComponentOptions{}

	cmd := &cobra.Command{
		Use:   "changed",
		Short: "Detect which components changed between two git refs",
		Long: `Compare component lock files and rendered sources between two git refs to
determine which components changed. For each component, reports whether its
input fingerprint changed (any change) and whether its rendered sources file
changed (sources change).

This is useful for CI/CD pipelines to determine which components need to be
rebuilt or have their lookaside tarballs re-uploaded after a PR merge.

Note: component selection and directory paths (lock-dir, rendered-specs-dir)
are resolved from the current checkout's configuration, not from the compared
refs. For accurate results, run this command from a checkout that matches the
--to ref (e.g., after merging a PR). Components not in the current config are
detected via lock file presence in the compared refs when using -a.

When rendered-specs-dir is not configured, sources change reports "unknown".`,
		Example: `  # Show changed components between a branch and HEAD
  azldev component changed --from main -a

  # Show changes between two specific refs
  azldev component changed --from v1.0 --to v2.0 -a

  # Include unchanged components in output
  azldev component changed --from main -a --include-unchanged

  # JSON output for scripting
  azldev component changed --from main -a -q -O json`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

			// Skip lock validation — this command inspects historical locks at
			// arbitrary refs, so HEAD-state validation is irrelevant.
			options.ComponentFilter.SkipLockValidation = true

			return ChangedComponents(env, options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().StringVar(&options.From, "from", "", "Git ref to compare from (required)")
	cmd.Flags().StringVar(&options.To, "to", "HEAD", "Git ref to compare to")
	cmd.Flags().BoolVar(&options.IncludeUnchanged, "include-unchanged", false, "Include unchanged components in output")

	_ = cmd.MarkFlagRequired("from")

	azldev.ExportAsMCPTool(cmd)

	return cmd
}

// ChangedResult holds the change status for a single component.
type ChangedResult struct {
	Component     string `json:"component"`
	ChangeType    string `json:"changeType"`
	SourcesChange string `json:"sourcesChange"`
}

// Change type constants for [ChangedResult.ChangeType].
const (
	changeTypeAdded     = "added"
	changeTypeChanged   = "changed"
	changeTypeUnchanged = "unchanged"
	changeTypeDeleted   = "deleted"
)

// Sources change constants for [ChangedResult.SourcesChange].
const (
	sourcesChangeTrue    = "true"
	sourcesChangeFalse   = "false"
	sourcesChangeUnknown = "unknown"
)

// ChangedComponents compares component lock files and rendered sources between
// two git refs to determine which changed.
func ChangedComponents(
	env *azldev.Env, options *ChangedComponentOptions,
) ([]ChangedResult, error) {
	resolver := components.NewResolver(env)

	comps, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("resolving components:\n%w", err)
	}

	ctx, err := newChangedContext(env)
	if err != nil {
		return nil, err
	}

	fromHash, err := resolveCommitHash(ctx.repo, options.From)
	if err != nil {
		return nil, fmt.Errorf("resolving --from ref %#q:\n%w", options.From, err)
	}

	toHash, err := resolveCommitHash(ctx.repo, options.To)
	if err != nil {
		return nil, fmt.Errorf("resolving --to ref %#q:\n%w", options.To, err)
	}

	// Batch-read all locks at both refs.
	fromLocks, err := lockfile.ReadAllAtCommit(ctx.repo, fromHash, ctx.lockRelDir)
	if err != nil {
		return nil, fmt.Errorf("reading locks at --from:\n%w", err)
	}

	toLocks, err := lockfile.ReadAllAtCommit(ctx.repo, toHash, ctx.lockRelDir)
	if err != nil {
		return nil, fmt.Errorf("reading locks at --to:\n%w", err)
	}

	// Resolve trees for sources comparison (raw file reads).
	fromTree, err := resolveTree(ctx.repo, fromHash)
	if err != nil {
		return nil, fmt.Errorf("resolving tree for --from:\n%w", err)
	}

	toTree, err := resolveTree(ctx.repo, toHash)
	if err != nil {
		return nil, fmt.Errorf("resolving tree for --to:\n%w", err)
	}

	return buildResults(
		comps, fromLocks, toLocks, ctx, fromTree, toTree,
		options.IncludeUnchanged, options.ComponentFilter.IncludeAllComponents,
	)
}

// changedContext holds resolved repo state for change detection.
type changedContext struct {
	repo             *gogit.Repository
	repoRoot         string
	lockRelDir       string
	renderedSpecsDir string
}

// newChangedContext opens the project repository and resolves paths.
func newChangedContext(env *azldev.Env) (*changedContext, error) {
	repo, err := git.OpenProjectRepo(env.ProjectDir())
	if err != nil {
		return nil, fmt.Errorf("opening project repository:\n%w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("getting project worktree:\n%w", err)
	}

	repoRoot := worktree.Filesystem.Root()

	lockRelDir, err := repoRelPath(repoRoot, env.Config().Project.LockDir)
	if err != nil {
		return nil, fmt.Errorf("computing repo-relative lock dir:\n%w", err)
	}

	return &changedContext{
		repo:             repo,
		repoRoot:         repoRoot,
		lockRelDir:       lockRelDir,
		renderedSpecsDir: env.Config().Project.RenderedSpecsDir,
	}, nil
}

// buildResults compares all components and detects deletions.
func buildResults(
	comps *components.ComponentSet,
	fromLocks, toLocks map[string]lockfile.ComponentLock,
	ctx *changedContext,
	fromTree, toTree *object.Tree,
	includeUnchanged, includeAllComponents bool,
) ([]ChangedResult, error) {
	configNames := make(map[string]bool, comps.Len())
	results := make([]ChangedResult, 0, comps.Len())

	for _, comp := range comps.Components() {
		name := comp.GetName()
		configNames[name] = true

		result := classifyComponent(name, fromLocks, toLocks)

		// Components in the current config that have their lock removed
		// between refs are "changed" (need rebuild), not "deleted" — they
		// still exist in the project.
		if result.ChangeType == changeTypeDeleted {
			result.ChangeType = changeTypeChanged
		}

		sourcesChange, srcErr := compareSources(ctx.repoRoot, fromTree, toTree, ctx.renderedSpecsDir, name)
		if srcErr != nil {
			return nil, fmt.Errorf("comparing sources for %#q:\n%w", name, srcErr)
		}

		result.SourcesChange = sourcesChange

		// Only filter unchanged rows during broad scans (-a). When the user
		// explicitly selected components (-p, -g, -s, positional args), always
		// show them — the user asked for data on those specific packages.
		if includeAllComponents && !includeUnchanged &&
			result.ChangeType == changeTypeUnchanged &&
			result.SourcesChange != sourcesChangeTrue {
			continue
		}

		results = append(results, result)
	}

	// Skip non-config component detection for filtered runs (-p, -g, -s,
	// positional args) — only check historical locks when scanning all
	// components (-a).
	if !includeAllComponents {
		// Sort for deterministic output across runs.
		sort.Slice(results, func(i, j int) bool {
			return results[i].Component < results[j].Component
		})

		return results, nil
	}

	nonConfigResults, err := buildNonConfigResults(
		fromLocks, toLocks, configNames, ctx, fromTree, toTree, includeUnchanged,
	)
	if err != nil {
		return nil, err
	}

	results = append(results, nonConfigResults...)

	// Sort for deterministic output across runs.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Component < results[j].Component
	})

	return results, nil
}

// buildNonConfigResults detects components not in the current config that
// changed between refs — deleted, added, or modified historical components.
func buildNonConfigResults(
	fromLocks, toLocks map[string]lockfile.ComponentLock,
	configNames map[string]bool,
	ctx *changedContext,
	fromTree, toTree *object.Tree,
	includeUnchanged bool,
) ([]ChangedResult, error) {
	nonConfigNames := make(map[string]bool)

	for name := range fromLocks {
		if !configNames[name] {
			nonConfigNames[name] = true
		}
	}

	for name := range toLocks {
		if !configNames[name] {
			nonConfigNames[name] = true
		}
	}

	sortedNames := make([]string, 0, len(nonConfigNames))
	for name := range nonConfigNames {
		sortedNames = append(sortedNames, name)
	}

	sort.Strings(sortedNames)

	var results []ChangedResult //nolint:prealloc // size not known ahead of time.

	for _, name := range sortedNames {
		result := classifyComponent(name, fromLocks, toLocks)

		sourcesChange, srcErr := compareSources(ctx.repoRoot, fromTree, toTree, ctx.renderedSpecsDir, name)
		if srcErr != nil {
			return nil, fmt.Errorf("comparing sources for non-config component %#q:\n%w", name, srcErr)
		}

		result.SourcesChange = sourcesChange

		if !includeUnchanged &&
			result.ChangeType == changeTypeUnchanged &&
			result.SourcesChange != sourcesChangeTrue {
			continue
		}

		results = append(results, result)
	}

	return results, nil
}

// classifyComponent determines the change type for a single component by
// comparing its presence and fingerprint in the from/to lock maps.
func classifyComponent(
	name string,
	fromLocks, toLocks map[string]lockfile.ComponentLock,
) ChangedResult {
	result := ChangedResult{
		Component:     name,
		ChangeType:    changeTypeUnchanged,
		SourcesChange: sourcesChangeUnknown,
	}

	fromLock, inFrom := fromLocks[name]
	toLock, inTo := toLocks[name]

	switch {
	case !inFrom && !inTo:
		result.ChangeType = changeTypeUnchanged
	case !inFrom:
		result.ChangeType = changeTypeAdded
	case !inTo:
		result.ChangeType = changeTypeDeleted
	default:
		if fromLock.InputFingerprint != toLock.InputFingerprint {
			result.ChangeType = changeTypeChanged
		}
	}

	return result
}

// compareSources compares the rendered sources file between two git trees.
// Returns [sourcesChangeTrue], [sourcesChangeFalse], or [sourcesChangeUnknown].
func compareSources(
	repoRoot string,
	fromTree, toTree *object.Tree,
	renderedSpecsDir, name string,
) (string, error) {
	renderedDir, err := components.RenderedSpecDir(renderedSpecsDir, name)
	if err != nil {
		return sourcesChangeUnknown, fmt.Errorf("resolving rendered spec dir:\n%w", err)
	}

	if renderedDir == "" {
		return sourcesChangeUnknown, nil
	}

	sourcesRelPath, err := repoRelPath(repoRoot, filepath.Join(renderedDir, "sources"))
	if err != nil {
		return sourcesChangeUnknown, fmt.Errorf("computing repo-relative sources path:\n%w", err)
	}

	fromSources, fromNotFound, fromErr := readFileFromTreeSafe(fromTree, sourcesRelPath)
	toSources, toNotFound, toErr := readFileFromTreeSafe(toTree, sourcesRelPath)

	if fromErr != nil {
		return sourcesChangeUnknown, fmt.Errorf("reading sources at --from:\n%w", fromErr)
	}

	if toErr != nil {
		return sourcesChangeUnknown, fmt.Errorf("reading sources at --to:\n%w", toErr)
	}

	switch {
	case fromNotFound && toNotFound:
		return sourcesChangeFalse, nil
	case fromNotFound || toNotFound:
		return sourcesChangeTrue, nil
	default:
		if bytes.Equal(fromSources, toSources) {
			return sourcesChangeFalse, nil
		}

		return sourcesChangeTrue, nil
	}
}

// repoRelPath computes a repo-relative path and rejects `..`-prefixed results
// that would escape the repository root.
func repoRelPath(repoRoot, absPath string) (string, error) {
	relPath, err := filepath.Rel(repoRoot, absPath)
	if err != nil {
		return "", fmt.Errorf("computing relative path from %#q to %#q:\n%w", repoRoot, absPath, err)
	}

	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %#q escapes repository root %#q", absPath, repoRoot)
	}

	return relPath, nil
}

// isFileNotFound returns true if the error indicates a missing file or
// directory in a git tree.
func isFileNotFound(err error) bool {
	return errors.Is(err, object.ErrFileNotFound) ||
		errors.Is(err, object.ErrDirectoryNotFound) ||
		errors.Is(err, object.ErrEntryNotFound)
}

// readFileFromTreeSafe reads a file from a git tree, distinguishing
// file-not-found from other errors.
func readFileFromTreeSafe(
	tree *object.Tree, relPath string,
) ([]byte, bool, error) {
	content, err := readFileFromTree(tree, relPath)
	if err != nil {
		if isFileNotFound(err) {
			return nil, true, nil
		}

		return nil, false, err
	}

	return content, false, nil
}

// resolveCommitHash resolves a git ref string to a commit hash string.
func resolveCommitHash(repo *gogit.Repository, ref string) (string, error) {
	hash, err := repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return "", fmt.Errorf("resolving ref %#q:\n%w", ref, err)
	}

	return hash.String(), nil
}

// resolveTree resolves a commit hash to a tree object.
func resolveTree(repo *gogit.Repository, commitHash string) (*object.Tree, error) {
	hash := plumbing.NewHash(commitHash)

	commit, err := repo.CommitObject(hash)
	if err != nil {
		return nil, fmt.Errorf("reading commit %#q:\n%w", commitHash, err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("reading tree for commit %#q:\n%w", commitHash, err)
	}

	return tree, nil
}

// readFileFromTree reads a file's contents from a git tree object.
func readFileFromTree(tree *object.Tree, relPath string) ([]byte, error) {
	file, err := tree.File(relPath)
	if err != nil {
		return nil, fmt.Errorf("reading %#q from tree:\n%w", relPath, err)
	}

	content, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("reading contents of %#q:\n%w", relPath, err)
	}

	return []byte(content), nil
}
