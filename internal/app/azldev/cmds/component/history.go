// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
	"github.com/spf13/cobra"
)

// HistoryOptions holds options for the component history command.
type HistoryOptions struct {
	ComponentFilter components.ComponentFilter
	// Since limits how far back to count commits. Accepts Go duration syntax
	// like "720h" (= 30d). Empty means "all history".
	Since string
	// SharedTomlMode controls how toml-commit counts are reported for components
	// that share their source TOML file with at least one other component:
	//   "show" (default): include the row, report the count, set SharedToml=true
	//   "zero":           include the row but force toml-commits to 0
	//   "omit":           drop the row entirely
	SharedTomlMode string
	// IncludeBare, when true, keeps components with zero customizations in
	// the output. By default they are filtered out -- they have no
	// per-component config worth reporting, and computing their git
	// metrics across all selected components is the dominant cost on
	// large projects (e.g., azurelinux).
	IncludeBare bool
}

const (
	sharedTomlModeShow = "show"
	sharedTomlModeZero = "zero"
	sharedTomlModeOmit = "omit"
)

func historyOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewHistoryCmd())
}

// NewHistoryCmd constructs a [cobra.Command] for the "component history" CLI subcommand.
func NewHistoryCmd() *cobra.Command {
	options := &HistoryOptions{
		Since:          "",
		SharedTomlMode: sharedTomlModeShow,
	}

	cmd := &cobra.Command{
		Use:     "history",
		Aliases: []string{"hist"},
		Short:   "Report per-component change activity and customization detail",
		Long: `Report three independent change-activity signals per component:

  - toml-commits:    commits to the component's source TOML file
  - customizations:  count of explicit customization items in the config
  - fp-changes:      commits where the lock file's input-fingerprint changed

Use this to find which packages get the most attention (for documentation,
review prioritization, or refactoring planning).

When a component shares its source TOML with other components (e.g., a bare
entry in a shared components.toml), the toml-commit count is coarse and the
component is marked 'toml-shared'. Use --shared=zero or --shared=omit to
suppress those counts.

When exactly one component is selected the customization items are printed
inline below the row, showing kind, value and description — useful for
hand-picking entries to document.`,
		Example: `  # Heatmap of an entire project
  azldev component history -a

  # JSON for downstream tooling
  azldev component history -a -O json

  # Drill into a single component (auto-expands customization details)
  azldev component history bash

  # Last 30 days only (720h)
  azldev component history -a --since=720h`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

			results, err := ComponentHistory(env, options)
			if err != nil {
				return nil, err
			}

			// When exactly one component is selected in a human-readable
			// format, render a card view ourselves and tell the standard
			// reporter to skip its 1-row table (returning a bool is treated
			// as a no-op by reportResults). JSON/CSV callers still get the
			// full slice unchanged.
			if shouldRenderCardView(env, results) {
				renderCardView(env.ReportFile(), results[0])

				return true, nil
			}

			return results, nil
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().StringVar(&options.Since, "since", "",
		"Only count commits newer than this (Go duration syntax, e.g. 720h). Empty = all history.")
	cmd.Flags().StringVar(&options.SharedTomlMode, "shared", sharedTomlModeShow,
		"How to report rows for components that share a TOML file with others: "+
			"show (keep row, count is coarse), zero (keep row, force count to 0), omit (drop row).")
	cmd.Flags().BoolVar(&options.IncludeBare, "include-bare", false,
		"Include components with zero customizations in the output. "+
			"By default they are hidden -- their config inherits everything from defaults, "+
			"and computing their git metrics is the dominant cost on large projects.")

	// History is read-only; the lock validation flag is meaningless here.
	_ = cmd.Flags().MarkHidden("skip-lock-validation")

	azldev.ExportAsMCPTool(cmd)

	return cmd
}

// CustomizationItem captures one user-authored customization on a component.
// Kind names use the overlay type for overlay items (e.g. "spec-remove-tag",
// "patch-add") and the structured TOML path for other items
// (e.g. "build.with", "spec.upstream-commit"). Value is a short summary
// suitable for table cells; Description is the human-readable rationale
// from the config (overlay.description, check.skip_reason, etc.).
type CustomizationItem struct {
	Kind        string `json:"kind"`
	Value       string `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
}

// HistoryResult is the per-component output row.
type HistoryResult struct {
	// Name of the component. We intentionally do *not* tag this with
	// 'sortkey' -- the reflectable table writer would otherwise re-sort
	// by name and stomp our customizations-first sort.
	Name string `json:"name"`

	// TomlCommits is the number of commits touching the component's source
	// TOML file. Zero (and SharedToml=true) when shared-mode = "omit" and the
	// component shares its TOML with another component.
	TomlCommits int `json:"tomlCommits"`

	// SharedToml is true when at least one other selected component has the
	// same source-config-file path; the TomlCommits count is then coarse.
	SharedToml bool `json:"sharedToml,omitempty"`

	// TomlPath is the repo-relative path of the component's source TOML file.
	TomlPath string `json:"tomlPath,omitempty"`

	// LatestCommit is the timestamp of the most recent commit to the TOML
	// file. Zero if no commits found. Uses 'omitzero' (Go 1.24+) rather than
	// 'omitempty' because the latter is a no-op for struct types and would
	// serialize as "0001-01-01T00:00:00Z" for components with no history.
	LatestCommit time.Time `json:"latestCommit,omitzero" table:"-"`

	// Customizations is the count of customization items (len of the
	// Customization slice).
	Customizations int `json:"customizations"`

	// CustomizationItems are the individual customization records;
	// rendered as JSON detail and as inline expansion for single-component
	// invocations.
	CustomizationItems []CustomizationItem `json:"customizationItems,omitempty" table:"-"`

	// FpChanges is the number of commits where the lock file's
	// input-fingerprint actually changed.
	FpChanges int `json:"fpChanges"`

	// FpChangeDetails is the per-commit metadata for each fingerprint
	// change counted in [FpChanges] (oldest first). Hidden from the
	// human-readable table -- use JSON output to consume them (e.g., to
	// hand-author changelog entries).
	//
	// Each entry is populated from [sources.FingerprintChange] via an
	// explicit field-by-field copy in [populateLockMetrics]. The
	// gathering algorithm is shared with the synthetic dist-git history
	// flow; the wire-level type is local so that:
	//   - the JSON contract for this command lives in this file, and
	//   - removing a field from [sources.FingerprintChange] /
	//     [sources.CommitMetadata] surfaces as a compile error at the
	//     copy site rather than silently dropping changelog metadata.
	FpChangeDetails []FpChange `json:"fpChangeDetails,omitempty" table:"-"`

	// HasLock is true when a lock file currently exists for this component.
	HasLock bool `json:"hasLock,omitempty" table:"-"`

	// HasImport is true when the lock file records a non-empty
	// import-commit (i.e., the component was forked from upstream).
	HasImport bool `json:"hasImport,omitempty" table:"-"`

	// ManualBump is the lock file's manual-bump counter.
	ManualBump int `json:"manualBump,omitempty" table:"-"`
}

// FpChange is the wire-level representation of one lock-file fingerprint
// change for the [HistoryResult.FpChangeDetails] field. It mirrors the
// fields of [sources.FingerprintChange] (and its embedded
// [sources.CommitMetadata]) that consumers of `azldev component history`
// JSON output care about.
//
// The fields are copied explicitly in [populateLockMetrics] rather than
// embedding [sources.FingerprintChange] directly so that:
//   - the JSON contract for this command is owned by this package, and
//   - dropping a field from the synthetic-history source type produces a
//     compile error at the copy site instead of silently emptying the
//     downstream changelog data.
type FpChange struct {
	Hash           string `json:"hash"`
	Author         string `json:"author"`
	AuthorEmail    string `json:"authorEmail"`
	Timestamp      int64  `json:"timestamp"`
	Message        string `json:"message"`
	UpstreamCommit string `json:"upstreamCommit,omitempty"`
}

// ComponentHistory computes the per-component history data for the components
// matching options.ComponentFilter. Per-component work runs in parallel; a
// progress event tracks completion for the (often slow) -a case.
//
// By default, components with zero customizations are skipped before any
// git work runs (set IncludeBare to keep them). This is the dominant
// performance lever on large projects -- the vast majority of components
// in real distros inherit everything from defaults and have no
// per-component history worth reporting.
func ComponentHistory(env *azldev.Env, options *HistoryOptions) ([]HistoryResult, error) {
	if err := validateSharedTomlMode(options.SharedTomlMode); err != nil {
		return nil, err
	}

	// History is read-only; skip lock validation so stale or missing locks
	// don't block reporting.
	options.ComponentFilter.SkipLockValidation = true

	since, err := parseSince(options.Since)
	if err != nil {
		return nil, err
	}

	resolver := components.NewResolver(env)

	comps, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("resolving components:\n%w", err)
	}

	ctx, err := newHistoryContext(env)
	if err != nil {
		return nil, err
	}

	// Phase 0: compute customizations for every selected component
	// (sync, fast, no git). When --include-bare is off, drop components
	// with zero customizations before any expensive work runs.
	stubs := buildHistoryStubs(env, comps.Components(), options.IncludeBare)
	if len(stubs) == 0 {
		return nil, nil
	}

	tomlSharing := countTomlSharing(env.Config().Components)

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	// Phase A: memoize toml-commit counts per unique source TOML path.
	// In real projects (e.g., azurelinux) thousands of components share a
	// single components.toml; without this we'd re-run the same `git log`
	// thousands of times.
	tomlCache, err := precomputeTomlMetricsForStubs(workerEnv, env, ctx, stubs, since)
	if err != nil {
		return nil, err
	}

	// Phase B: build per-component results in parallel.
	progressEvent := env.StartEvent("Computing component history", "count", len(stubs))
	defer progressEvent.End()

	total := int64(len(stubs))

	parmapResults := parmap.Map(
		workerEnv,
		// Each worker shells out to git; that's I/O-bound work, matching the
		// concurrency model used by render/update on similar workloads.
		env.IOBoundConcurrency(),
		stubs,
		func(done, _ int) { progressEvent.SetProgress(int64(done), total) },
		func(_ context.Context, stub historyStub) buildOutcome {
			// workerEnv carries the cancellable ctx; the parmap-supplied
			// ctx is identical (parmap derives it from workerEnv) and
			// unused here. Mirrors how render.go does this.
			res := buildHistoryResult( //nolint:contextcheck // env carries the ctx
				workerEnv, stub, ctx, tomlSharing, tomlCache, since, options.SharedTomlMode,
			)

			return buildOutcome{result: res}
		},
	)

	results := make([]HistoryResult, 0, len(stubs))

	for _, parmapRes := range parmapResults {
		if parmapRes.Cancelled {
			continue
		}

		// --shared=omit drops the row entirely for components whose source
		// TOML is shared with at least one other component.
		if options.SharedTomlMode == sharedTomlModeOmit && parmapRes.Value.result.SharedToml {
			continue
		}

		results = append(results, parmapRes.Value.result)
	}

	sortHistoryResults(results)

	return results, nil
}

// historyStub carries the cheap, sync-computed slice of work for one
// component: customization items (pre-collected) plus the underlying
// Component handle for later git-metric work. Keyed by component name.
type historyStub struct {
	component          components.Component
	customizationItems []CustomizationItem
}

// buildHistoryStubs computes customization items for every selected
// component synchronously. When includeBare is false, components with no
// customizations are excluded so that the expensive parallel phases
// don't run on them at all.
func buildHistoryStubs(
	env *azldev.Env, comps []components.Component, includeBare bool,
) []historyStub {
	stubs := make([]historyStub, 0, len(comps))

	for _, comp := range comps {
		name := comp.GetName()

		// Read the raw per-component config (as authored in TOML), not the
		// resolved one returned by comp.GetConfig() -- the resolver
		// pre-merges project- and group-level defaults, which would
		// otherwise look like per-component customizations.
		var items []CustomizationItem
		if raw, ok := env.Config().Components[name]; ok {
			items = collectCustomizations(name, &raw)
		}

		if !includeBare && len(items) == 0 {
			continue
		}

		stubs = append(stubs, historyStub{component: comp, customizationItems: items})
	}

	return stubs
}

// buildOutcome is the per-item return value from the parmap.Map worker.
type buildOutcome struct {
	result HistoryResult
}

// validateSharedTomlMode rejects unrecognized --shared values.
func validateSharedTomlMode(mode string) error {
	switch mode {
	case sharedTomlModeShow, sharedTomlModeZero, sharedTomlModeOmit:
		return nil
	default:
		return fmt.Errorf(
			"invalid --shared value %#q (want one of: %s, %s, %s)",
			mode, sharedTomlModeShow, sharedTomlModeZero, sharedTomlModeOmit)
	}
}

// parseSince converts the user-supplied --since string into a cutoff timestamp.
// Returns a zero time when raw is empty (i.e., no filtering).
func parseSince(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}

	dur, err := time.ParseDuration(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"parsing --since %#q (Go duration syntax, e.g. 720h):\n%w", raw, err)
	}

	if dur <= 0 {
		return time.Time{}, fmt.Errorf("--since must be a positive duration, got %#q", raw)
	}

	return time.Now().Add(-dur), nil
}

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
// [precomputeTomlMetrics]. Keyed by repo-relative TOML path.
type tomlMetrics struct {
	count  int
	latest time.Time
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
				// Treat as "no history" rather than failing the whole
				// command -- a missing or untracked TOML is non-fatal.
				return tomlMetrics{}
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
// fills in the git-driven metrics (toml-commits via cache, fp-changes via
// per-call repo).
func buildHistoryResult(
	env *azldev.Env,
	stub historyStub,
	ctx *historyContext,
	tomlSharing map[string]int,
	tomlCache map[string]tomlMetrics,
	since time.Time,
	sharedMode string,
) HistoryResult {
	result := HistoryResult{
		Name:               stub.component.GetName(),
		CustomizationItems: stub.customizationItems,
		Customizations:     len(stub.customizationItems),
	}

	populateTomlMetrics(stub.component, ctx, tomlSharing, tomlCache, sharedMode, &result)
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
		// A TOML file outside the repo isn't a hard error -- just leave
		// path/commit counts empty.
		return
	}

	result.TomlPath = tomlRelPath

	if result.SharedToml && sharedMode == sharedTomlModeOmit {
		return
	}

	metrics, ok := tomlCache[tomlRelPath]
	if !ok {
		// Precompute didn't run for this path (e.g., out-of-repo TOML).
		return
	}

	if result.SharedToml && sharedMode == sharedTomlModeZero {
		result.TomlCommits = 0
	} else {
		result.TomlCommits = metrics.count
	}

	result.LatestCommit = metrics.latest
}

// populateLockMetrics fills in FpChanges, HasLock, HasImport, ManualBump.
// All failure paths are deliberately swallowed: missing/unreadable lock
// state is "no data", not a hard error.
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
		if lockErr == nil && lock != nil {
			result.HasLock = true
			result.HasImport = lock.ImportCommit != ""
			result.ManualBump = lock.ManualBump
		}
	}

	lockAbsPath, err := lockfile.LockPath(ctx.lockDir, name)
	if err != nil {
		// Invalid component name for path resolution: skip lock metrics
		// rather than failing the whole report.
		return
	}

	lockRelPath, err := repoRelPath(ctx.repoRoot, lockAbsPath)
	if err != nil {
		// Lock dir lives outside the repo: nothing to count.
		return
	}

	fpChanges, err := func() ([]sources.FingerprintChange, error) {
		// Open a fresh repo for this call -- go-git's *Repository is not
		// safe for concurrent use. Opening is cheap (just reads .git/config).
		repo, openErr := git.OpenProjectRepo(env.ProjectDir())
		if openErr != nil {
			return nil, fmt.Errorf("opening project repository:\n%w", openErr)
		}

		return sources.FindFingerprintChanges(env.Context(), env, repo, ctx.repoRoot, lockRelPath)
	}()
	if err != nil {
		// Lock file with no committed history (e.g., never committed) is
		// expected for fresh components -- leave count at zero.
		return
	}

	filtered := filterChangesSince(fpChanges, since)
	result.FpChanges = len(filtered)
	result.FpChangeDetails = toFpChanges(filtered)
}

// toFpChanges copies each [sources.FingerprintChange] into the local
// [FpChange] wire type by naming every field explicitly. Removing a
// field from [sources.FingerprintChange] or [sources.CommitMetadata]
// trips a compile error here, alerting us to a quietly-shrunk changelog
// payload.
func toFpChanges(changes []sources.FingerprintChange) []FpChange {
	if len(changes) == 0 {
		return nil
	}

	out := make([]FpChange, len(changes))
	for i, change := range changes {
		out[i] = FpChange{
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

// filterChangesSince returns the subset of changes with a timestamp >= since.
// When since is zero, the input slice is returned unchanged. The returned
// slice retains the input ordering (oldest first, per
// [sources.FindFingerprintChanges]).
func filterChangesSince(changes []sources.FingerprintChange, since time.Time) []sources.FingerprintChange {
	if since.IsZero() {
		return changes
	}

	cutoff := since.Unix()

	filtered := make([]sources.FingerprintChange, 0, len(changes))

	for _, change := range changes {
		if change.Timestamp >= cutoff {
			filtered = append(filtered, change)
		}
	}

	return filtered
}

// collectCustomizations gathers all customization items declared on the
// component's config into a uniform list. Items are emitted in a stable
// order (overlays first in declared order; then build, spec, release,
// packages, source-files in field order) so the output is deterministic.
func collectCustomizations(name string, config *projectconfig.ComponentConfig) []CustomizationItem {
	if config == nil {
		return nil
	}

	items := make([]CustomizationItem, 0)

	items = appendOverlayItems(items, config.Overlays)
	items = appendBuildItems(items, config.Build)
	items = appendSpecItems(items, name, config.Spec)
	items = appendReleaseItems(items, config.Release)
	items = appendRenderItems(items, config.Render)
	items = appendPackageItems(items, config.Packages)
	items = appendSourceFileItems(items, config.SourceFiles)

	return items
}

// appendRenderItems flags non-default render-config customizations.
func appendRenderItems(
	items []CustomizationItem, render projectconfig.ComponentRenderConfig,
) []CustomizationItem {
	if render.SkipFileFilter {
		items = append(items, CustomizationItem{
			Kind:  "render.skip-file-filter",
			Value: strconv.FormatBool(true),
		})
	}

	return items
}

// appendOverlayItems converts each overlay into a CustomizationItem.
func appendOverlayItems(
	items []CustomizationItem, overlays []projectconfig.ComponentOverlay,
) []CustomizationItem {
	for i := range overlays {
		overlay := &overlays[i]

		items = append(items, CustomizationItem{
			Kind:        string(overlay.Type),
			Value:       overlaySummary(overlay),
			Description: overlay.Description,
		})
	}

	return items
}

// overlaySummary returns a short human-readable identification of an overlay,
// suitable for the Value field of a CustomizationItem.
func overlaySummary(overlay *projectconfig.ComponentOverlay) string {
	switch {
	case overlay.Tag != "" && overlay.Value != "":
		return fmt.Sprintf("%s=%s", overlay.Tag, overlay.Value)
	case overlay.Tag != "":
		return overlay.Tag
	case overlay.EffectiveSourceName() != "":
		return overlay.EffectiveSourceName()
	case overlay.Filename != "":
		return overlay.Filename
	case overlay.SectionName != "":
		return overlay.SectionName
	case overlay.Regex != "":
		return overlay.Regex
	default:
		return ""
	}
}

// appendBuildItems converts non-default build-config fields into items.
func appendBuildItems(
	items []CustomizationItem, build projectconfig.ComponentBuildConfig,
) []CustomizationItem {
	for _, flag := range build.With {
		items = append(items, CustomizationItem{Kind: "build.with", Value: flag})
	}

	for _, flag := range build.Without {
		items = append(items, CustomizationItem{Kind: "build.without", Value: flag})
	}

	// Sort define keys so iteration order is deterministic.
	defineKeys := make([]string, 0, len(build.Defines))
	for key := range build.Defines {
		defineKeys = append(defineKeys, key)
	}

	sort.Strings(defineKeys)

	for _, key := range defineKeys {
		items = append(items, CustomizationItem{
			Kind:  "build.defines",
			Value: fmt.Sprintf("%s=%s", key, build.Defines[key]),
		})
	}

	for _, macro := range build.Undefines {
		items = append(items, CustomizationItem{Kind: "build.undefines", Value: macro})
	}

	if build.Check.Skip {
		items = append(items, CustomizationItem{
			Kind:        "build.check.skip",
			Value:       strconv.FormatBool(true),
			Description: build.Check.SkipReason,
		})
	}

	return items
}

// appendSpecItems captures spec-source customizations relative to the
// inherited default. We cannot perfectly know the inherited default without
// re-resolving, but we can flag the cases that are unambiguous (commit pin,
// upstream-name renamed away from the component name, upstream-distro set).
func appendSpecItems(
	items []CustomizationItem, name string, spec projectconfig.SpecSource,
) []CustomizationItem {
	// Only surface SourceType when explicitly set in the raw per-component
	// config -- components that inherit from group defaults leave it empty,
	// so this avoids inflating the customization count for every component.
	if spec.SourceType != "" {
		items = append(items, CustomizationItem{
			Kind:  "spec.source-type",
			Value: string(spec.SourceType),
		})
	}

	if spec.UpstreamCommit != "" {
		items = append(items, CustomizationItem{
			Kind:  "spec.upstream-commit",
			Value: spec.UpstreamCommit,
		})
	}

	if spec.UpstreamName != "" && spec.UpstreamName != name {
		items = append(items, CustomizationItem{
			Kind:  "spec.upstream-name",
			Value: spec.UpstreamName,
		})
	}

	if spec.UpstreamDistro.Name != "" {
		items = append(items, CustomizationItem{
			Kind:  "spec.upstream-distro",
			Value: spec.UpstreamDistro.String(),
		})
	}

	return items
}

// appendReleaseItems flags non-default release-calculation modes.
func appendReleaseItems(
	items []CustomizationItem, release projectconfig.ReleaseConfig,
) []CustomizationItem {
	if release.Calculation == "" || release.Calculation == projectconfig.ReleaseCalculationAuto {
		return items
	}

	return append(items, CustomizationItem{
		Kind:  "release.calculation",
		Value: string(release.Calculation),
	})
}

// appendPackageItems emits one item per binary package override.
func appendPackageItems(
	items []CustomizationItem, packages map[string]projectconfig.PackageConfig,
) []CustomizationItem {
	if len(packages) == 0 {
		return items
	}

	keys := make([]string, 0, len(packages))
	for key := range packages {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		items = append(items, CustomizationItem{Kind: "packages", Value: key})
	}

	return items
}

// appendSourceFileItems emits one item per declared source-file reference.
func appendSourceFileItems(
	items []CustomizationItem, sourceFiles []projectconfig.SourceFileReference,
) []CustomizationItem {
	for _, sourceFile := range sourceFiles {
		items = append(items, CustomizationItem{Kind: "source-files", Value: sourceFile.Filename})
	}

	return items
}

// sortHistoryResults orders results "most-customized first": highest
// customization count first, then fp-changes, then alphabetical by name.
// Customizations is the most direct signal of human attention paid to a
// component (and it's deterministic / fast); fp-changes and the name tie-
// break it for stable output.
func sortHistoryResults(results []HistoryResult) {
	sort.SliceStable(results, func(left, right int) bool {
		if results[left].Customizations != results[right].Customizations {
			return results[left].Customizations > results[right].Customizations
		}

		if results[left].FpChanges != results[right].FpChanges {
			return results[left].FpChanges > results[right].FpChanges
		}

		return results[left].Name < results[right].Name
	})
}

// shouldRenderCardView decides whether to print the per-component "card"
// view instead of falling through to the default table renderer. We only
// switch to the card for exactly one result and only in formats meant for
// human eyes (table, markdown); JSON / CSV consumers always get the
// machine-readable slice.
func shouldRenderCardView(env *azldev.Env, results []HistoryResult) bool {
	if len(results) != 1 {
		return false
	}

	switch env.DefaultReportFormat() {
	case azldev.ReportFormatTable, azldev.ReportFormatMarkdown:
		return true
	case azldev.ReportFormatCSV, azldev.ReportFormatJSON:
		return false
	default:
		return false
	}
}

// renderCardView prints a single-component card view: a vertical key/value
// header followed by an indented list of customization items (with their
// descriptions when present). This is what the user sees from
// `azldev component history <name>` and is intended to be the most useful
// view for hand-picking entries to document.
func renderCardView(writer io.Writer, result HistoryResult) {
	fmt.Fprintf(writer, "Component: %s\n", result.Name)

	if result.TomlPath != "" {
		fmt.Fprintf(writer, "  Source TOML:    %s\n", result.TomlPath)
	}

	sharedNote := ""
	if result.SharedToml {
		sharedNote = " (shared file -- count is coarse)"
	}

	latestNote := ""
	if !result.LatestCommit.IsZero() {
		latestNote = ", latest " + result.LatestCommit.Format(time.DateOnly)
	}

	fmt.Fprintf(writer, "  TOML commits:   %d%s%s\n", result.TomlCommits, sharedNote, latestNote)
	fmt.Fprintf(writer, "  Customizations: %d\n", result.Customizations)
	fmt.Fprintf(writer, "  Fp changes:     %d\n", result.FpChanges)

	if result.HasLock {
		fmt.Fprintf(writer,
			"  Lock state:     locked (manual-bump=%d, has-import=%t)\n",
			result.ManualBump, result.HasImport)
	} else {
		fmt.Fprintln(writer, "  Lock state:     no lock")
	}

	if len(result.CustomizationItems) == 0 {
		return
	}

	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Customizations:")

	for idx, item := range result.CustomizationItems {
		value := item.Value
		if value == "" {
			value = "(no value)"
		}

		fmt.Fprintf(writer, "  %d. [%s] %s\n", idx+1, item.Kind, value)

		if item.Description != "" {
			fmt.Fprintf(writer, "     %s\n", item.Description)
		}
	}
}
