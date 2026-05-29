// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
)

// historyContext holds resolved repo state shared across components.
type historyContext struct {
	repoRoot string
	lockDir  string
}

// newHistoryContext opens the project repository once just to resolve the
// worktree root, then discards it: go-git's *Repository is not safe to
// share across goroutines (see synthistory.go which always opens a fresh
// repo per component). Per-worker repos are reopened inline.
func newHistoryContext(env *azldev.Env) (*historyContext, error) {
	cfg := env.Config()
	if cfg == nil {
		return nil, errors.New("no project configuration loaded")
	}

	repo, err := git.OpenProjectRepo(env.ProjectDir())
	if err != nil {
		return nil, fmt.Errorf("opening project repository:\n%w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("getting project worktree:\n%w", err)
	}

	return &historyContext{
		repoRoot: worktree.Filesystem.Root(),
		lockDir:  cfg.Project.LockDir,
	}, nil
}

// countTomlSharing returns the number of components that point at each
// source TOML path. Used to detect shared files (where toml-commit counts
// are coarse).
func countTomlSharing(allComponents map[string]projectconfig.ComponentConfig) map[string]int {
	sharing := make(map[string]int)

	for _, cfg := range allComponents {
		if cfg.SourceConfigFile == nil {
			continue
		}

		path := cfg.SourceConfigFile.SourcePath()
		if path == "" {
			continue
		}

		sharing[path]++
	}

	return sharing
}

// tomlMetrics is one entry in the precomputed cache populated by
// [precomputeTomlMetrics]. Keyed by repo-relative TOML path. A non-nil err
// records a real `git log` failure so [populateTomlMetrics] can surface a
// warning, keeping it distinguishable from a genuine zero-commit history.
type tomlMetrics struct {
	count  int
	latest time.Time
	err    error
}

// precomputeTomlMetricsForStubs runs `git log` once per *unique* source-
// TOML path across the selected stubs and returns the results keyed by
// repo-relative path. This is the central performance optimization: in
// real projects (e.g., azurelinux) thousands of components share a single
// components.toml file, and without de-duplicating we'd re-run the same
// `git log` thousands of times.
//
// Paths that resolve outside the repo are skipped. Failures are likewise
// tolerated -- the affected component just shows a zero count, same as
// if the file had no history.
func precomputeTomlMetricsForStubs(
	workerEnv *azldev.Env,
	env *azldev.Env,
	ctx *historyContext,
	stubs []historyStub,
	since time.Time,
) (map[string]tomlMetrics, error) {
	uniqueRelPaths := collectUniqueTomlRelPathsFromStubs(ctx.repoRoot, stubs)
	if len(uniqueRelPaths) == 0 {
		return map[string]tomlMetrics{}, nil
	}

	progressEvent := env.StartEvent("Counting TOML commit history", "uniqueFiles", len(uniqueRelPaths))
	defer progressEvent.End()

	total := int64(len(uniqueRelPaths))

	parmapResults := parmap.Map(
		workerEnv,
		env.IOBoundConcurrency(),
		uniqueRelPaths,
		func(done, _ int) { progressEvent.SetProgress(int64(done), total) },
		func(_ context.Context, relPath string) tomlMetrics {
			count, latest, err := git.CountCommitsTouchingFile( //nolint:contextcheck // env carries the ctx
				workerEnv, workerEnv, ctx.repoRoot, relPath, since,
			)
			if err != nil {
				// Cache the failure rather than failing the whole command --
				// populateTomlMetrics surfaces it as a warning so a real error
				// (corrupt repo, permission denied) stays distinguishable from a
				// genuine zero-commit history.
				return tomlMetrics{err: err}
			}

			return tomlMetrics{count: count, latest: latest}
		},
	)

	cache := make(map[string]tomlMetrics, len(uniqueRelPaths))

	for idx, parmapRes := range parmapResults {
		if parmapRes.Cancelled {
			continue
		}

		cache[uniqueRelPaths[idx]] = parmapRes.Value
	}

	return cache, nil
}

// collectUniqueTomlRelPathsFromStubs returns the deduplicated set of in-
// repo, repo-relative source-TOML paths across the given stubs.
func collectUniqueTomlRelPathsFromStubs(repoRoot string, stubs []historyStub) []string {
	seen := make(map[string]struct{})

	relPaths := make([]string, 0)

	for _, stub := range stubs {
		config := stub.component.GetConfig()
		if config.SourceConfigFile == nil {
			continue
		}

		absPath := config.SourceConfigFile.SourcePath()
		if absPath == "" {
			continue
		}

		relPath, err := repoRelPath(repoRoot, absPath)
		if err != nil {
			continue
		}

		if _, dup := seen[relPath]; dup {
			continue
		}

		seen[relPath] = struct{}{}

		relPaths = append(relPaths, relPath)
	}

	return relPaths
}

// buildHistoryResult assembles a single [HistoryResult] for a stub. The
// stub already carries the precomputed customization items; this function
// fills in the git-driven metrics (toml-commits via cache, fingerprint-changes via
// per-call repo).
func buildHistoryResult(
	env *azldev.Env,
	stub historyStub,
	ctx *historyContext,
	tomlSharing map[string]int,
	tomlCache map[string]tomlMetrics,
	since time.Time,
	sharedMode string,
	explicit bool,
) HistoryResult {
	result := HistoryResult{
		Name:               stub.component.GetName(),
		CustomizationItems: stub.customizationItems,
		Customizations:     len(stub.customizationItems),
	}

	populateTomlMetrics(stub.component, ctx, tomlSharing, tomlCache, sharedMode, explicit, &result)
	populateLockMetrics(env, stub.component, ctx, since, &result)

	return result
}

// populateTomlMetrics fills in TomlCommits, SharedToml, TomlPath,
// LatestCommit from the precomputed [tomlMetrics] cache.
func populateTomlMetrics(
	comp components.Component,
	ctx *historyContext,
	tomlSharing map[string]int,
	tomlCache map[string]tomlMetrics,
	sharedMode string,
	explicit bool,
	result *HistoryResult,
) {
	config := comp.GetConfig()

	if config.SourceConfigFile == nil || config.SourceConfigFile.SourcePath() == "" {
		return
	}

	tomlAbsPath := config.SourceConfigFile.SourcePath()
	result.SharedToml = tomlSharing[tomlAbsPath] > 1

	tomlRelPath, err := repoRelPath(ctx.repoRoot, tomlAbsPath)
	if err != nil {
		// A TOML file outside the repo isn't a hard error -- record a
		// warning and leave path/commit counts empty.
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("source TOML %q is outside the git repository; toml-commits skipped: %v",
				tomlAbsPath, err))

		return
	}

	result.TomlPath = tomlRelPath

	// --shared=omit suppresses the (coarse) count for shared TOMLs, but an
	// explicitly-named component is the user asking for that component
	// specifically -- give them the real count, mirroring the row-keep
	// override in [ComponentHistory].
	if result.SharedToml && sharedMode == sharedTomlModeOmit && !explicit {
		return
	}

	metrics, ok := tomlCache[tomlRelPath]
	if !ok {
		// Precompute didn't run for this path (e.g., out-of-repo TOML
		// or a precompute failure that was tolerated). Surface so the
		// user can tell zero-counts apart from missing-data.
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("no TOML commit metrics cached for %q; toml-commits left at zero", tomlRelPath))

		return
	}

	if metrics.err != nil {
		// A real `git log` failure was cached during precompute. Surface it
		// rather than silently reporting zero commits (mirrors the lock-path
		// warning behavior).
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("counting TOML commits for %q failed; toml-commits left at zero: %v",
				tomlRelPath, metrics.err))

		return
	}

	result.TomlCommits = metrics.count
	result.LatestCommit = metrics.latest
}

// populateLockMetrics fills in FingerprintChanges, FingerprintChangeDetails,
// HasLock, HasImport, ManualBump.
// A missing lock file is "no data", not an error; a genuine read failure
// (corrupt/unparseable lock) is surfaced via result.Warnings so a
// tomlCommits/fingerprintChanges of 0 can't be silently confused with a
// real failure.
//
// FingerprintChangeDetails is always populated here; the caller strips it
// when more than one component is reported. See [ComponentHistory] for the
// rationale.
func populateLockMetrics(
	env *azldev.Env,
	comp components.Component,
	ctx *historyContext,
	since time.Time,
	result *HistoryResult,
) {
	name := comp.GetName()

	lockReader := env.LockReader()
	if lockReader != nil {
		lock, lockErr := lockReader.Get(name)

		switch {
		case lockErr == nil && lock != nil:
			result.HasLock = true
			result.HasImport = lock.ImportCommit != ""
			result.ManualBump = lock.ManualBump
		case lockErr != nil:
			// Distinguish a missing lock ("no data", expected) from a real
			// read failure (corrupt/unparseable lock). Mirror the store's
			// own not-found detection (Exists) since the wrapped fs error
			// isn't reliably errors.Is(os.ErrNotExist)-comparable. Only a
			// genuine failure earns a warning, so a fingerprintChanges of 0
			// can't be silently confused with a load error.
			exists, existsErr := lockReader.Exists(name)
			switch {
			case existsErr != nil:
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("reading lock file for %q: %v (existence check also failed: %v)",
						name, lockErr, existsErr))
			case exists:
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("reading lock file for %q: %v", name, lockErr))
			}
		}
	}

	lockAbsPath, err := lockfile.LockPath(ctx.lockDir, name)
	if err != nil {
		// Invalid component name for path resolution: skip lock metrics
		// rather than failing the whole report.
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("resolving lock path: %v", err))

		return
	}

	lockRelPath, err := repoRelPath(ctx.repoRoot, lockAbsPath)
	if err != nil {
		// Lock dir lives outside the repo: nothing to count.
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("lock file %q is outside the git repository; fingerprint-changes skipped: %v",
				lockAbsPath, err))

		return
	}

	fingerprintChanges, err := func() ([]sources.FingerprintChange, error) {
		// Open a fresh repo for this call -- go-git's *Repository is not
		// safe for concurrent use. Opening is cheap (just reads .git/config).
		repo, openErr := git.OpenProjectRepo(env.ProjectDir())
		if openErr != nil {
			return nil, fmt.Errorf("opening project repository:\n%w", openErr)
		}

		return sources.FindFingerprintChanges(env.Context(), env, repo, ctx.repoRoot, lockRelPath)
	}()
	if err != nil {
		// A lock file with no committed history is NOT an error here --
		// FindFingerprintChanges returns (nil, nil) in that case. This
		// branch only fires on real failures (git open, blob read, etc.).
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("computing fingerprint changes for %q: %v", lockRelPath, err))

		return
	}

	filtered := filterChangesSince(fingerprintChanges, since)
	result.FingerprintChanges = len(filtered)
	result.FingerprintChangeDetails = toFingerprintChanges(filtered)
}

// toFingerprintChanges copies each [sources.FingerprintChange] into the
// local [FingerprintChange] wire type by naming every field explicitly.
// Removing a field from [sources.FingerprintChange] or
// [sources.CommitMetadata] trips a compile error here, alerting us to a
// quietly-shrunk changelog payload.
func toFingerprintChanges(changes []sources.FingerprintChange) []FingerprintChange {
	if len(changes) == 0 {
		return nil
	}

	out := make([]FingerprintChange, len(changes))
	for i, change := range changes {
		out[i] = FingerprintChange{
			Hash:           change.Hash,
			Author:         change.Author,
			AuthorEmail:    change.AuthorEmail,
			Timestamp:      change.Timestamp,
			Message:        change.Message,
			UpstreamCommit: change.UpstreamCommit,
		}
	}

	return out
}

// filterChangesSince returns the subset of changes with a timestamp strictly
// greater than since (matching 'git log --since', which excludes the boundary
// second, so the toml-commit and fingerprint-change metrics agree on the
// cutoff edge). When since is zero, the input slice is returned unchanged. The
// returned slice retains the input ordering (oldest first, per
// [sources.FindFingerprintChanges]).
func filterChangesSince(changes []sources.FingerprintChange, since time.Time) []sources.FingerprintChange {
	if since.IsZero() {
		return changes
	}

	cutoff := since.Unix()

	filtered := make([]sources.FingerprintChange, 0, len(changes))

	for _, change := range changes {
		// Strict '>' mirrors 'git log --since', which excludes the boundary
		// second, so the two metrics agree on the cutoff edge.
		if change.Timestamp > cutoff {
			filtered = append(filtered, change)
		}
	}

	return filtered
}
