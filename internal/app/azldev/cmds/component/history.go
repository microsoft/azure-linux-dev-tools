// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
	"github.com/spf13/cobra"
)

// HistoryOptions holds options for the component history command.
type HistoryOptions struct {
	ComponentFilter components.ComponentFilter
	// SharedTomlMode controls how toml-commit counts are reported for
	// components that share their source TOML file with at least one other
	// component:
	//   "show" (default): include the row, report the count, set SharedToml=true
	//   "omit":           drop the row entirely
	//
	// JSON consumers always see the raw TomlCommits + SharedToml fields and
	// can apply their own presentation (e.g., zero out shared rows) via jq.
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
	sharedTomlModeOmit = "omit"
)

func historyOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewHistoryCmd())
}

// NewHistoryCmd constructs a [cobra.Command] for the "component history" CLI subcommand.
func NewHistoryCmd() *cobra.Command {
	options := &HistoryOptions{
		SharedTomlMode: sharedTomlModeShow,
	}

	cmd := &cobra.Command{
		Use:     "history",
		Aliases: []string{"hist"},
		Short:   "Report per-component change activity and customization detail",
		Long: `Report three independent change-activity signals per component:

  - toml-commits:         commits to the component's source TOML file
  - customizations:       count of explicit customization items in the config
  - fingerprint-changes:  commits where the lock file's input-fingerprint changed

Use this to find which packages get the most attention (for documentation,
review prioritization, or refactoring planning).

When a component shares its source TOML with other components (e.g., a bare
entry in a shared components.toml), the toml-commit count is coarse and the
component is marked 'toml-shared'. Use --shared=omit to drop those rows.

When exactly one component is selected the customization items are printed
inline below the row, showing kind, value and description — useful for
hand-picking entries to document.`,
		Example: `  # Heatmap of an entire project
  azldev component history -a

  # JSON for downstream tooling
  azldev component history -a -O json

  # Drill into a single component (auto-expands customization details)
  azldev component history bash`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

			results, err := ComponentHistory(env, options)
			if err != nil {
				return nil, err
			}

			// Card view side-channel: when exactly one component is being
			// reported in a human-readable format, render a vertical card
			// ourselves and short-circuit the standard table renderer.
			// reportResults treats a `true` return as a no-op (see
			// reportResultsViaReflectable in azldev/command.go) which is
			// how we suppress the would-be 1-row table.
			//
			// CAVEAT: the trigger is implicit (single result + human
			// format) so a broad -a query that happens to narrow to one
			// component silently switches output shape. JSON / CSV
			// consumers always get the raw slice unchanged.
			if shouldRenderCardView(env, results) {
				renderCardView(env.ReportFile(), results[0])

				return true, nil
			}

			return results, nil
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().StringVar(&options.SharedTomlMode, "shared", sharedTomlModeShow,
		"How to report rows for components that share a TOML file with others: "+
			"show (keep row, count is coarse), omit (drop row).")
	// Shell completion advertises the valid choices. Note: the MCP tool
	// schema does not yet derive an `enum` constraint from cobra flag
	// completion functions (see internal/app/azldev/core/mcp/mcpserver.go),
	// so MCP agents see this as an unconstrained string until that gap is
	// closed. Runtime validation happens in [validateSharedTomlMode].
	_ = cmd.RegisterFlagCompletionFunc("shared",
		func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return []string{sharedTomlModeShow, sharedTomlModeOmit},
				cobra.ShellCompDirectiveNoFileComp
		})
	cmd.Flags().BoolVar(&options.IncludeBare, "include-bare", false,
		"Include components with zero customizations in the output. "+
			"By default they are hidden -- their config inherits everything from defaults, "+
			"and computing their git metrics is the dominant cost on large projects.")

	// History is read-only; the lock validation flag is meaningless here.
	_ = cmd.Flags().MarkHidden("skip-lock-validation")

	azldev.ExportAsReadOnlyMCPTool(cmd)

	return cmd
}

// CustomizationItem captures one user-authored customization on a component.
//
// Kind is a dotted-namespace string forming part of the JSON wire contract
// (downstream `jq`/`gjson` consumers key on it). It is either an overlay
// type emitted verbatim (e.g. "spec-remove-tag", "patch-add") or a fixed
// token derived from the structured TOML path. The fixed set:
//
//	build.with, build.without, build.defines, build.undefines,
//	build.check.skip, spec.source-type, spec.upstream-commit,
//	spec.upstream-name, spec.upstream-distro, release.calculation,
//	render.skip-file-filter, packages, source-files,
//	source-files.replace-upstream
//
// Adding a Kind is non-breaking; renaming or removing one is breaking.
// Value is a short summary suitable for table cells; Description is the
// human-readable rationale from the config (overlay.description,
// check.skip_reason, etc.).
type CustomizationItem struct {
	Kind        string `json:"kind"`
	Value       string `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
}

// HistoryResult is the per-component output row.
//
// This is the stable wire contract that downstream tooling pins against:
// adding a field is non-breaking; renaming or removing one is a breaking
// change. Keep JSON tags stable.
type HistoryResult struct {
	// Name of the component. We intentionally do *not* tag this with
	// 'sortkey' -- the reflectable table writer would otherwise re-sort
	// by name and stomp our customizations-first sort.
	Name string `json:"name"`

	// TomlCommits is the number of commits touching the component's source
	// TOML file. When shared-mode = "omit" and the component shares its TOML
	// with another component, the count is suppressed to zero (with
	// SharedToml=true) -- unless the component was named explicitly, which
	// always reports the real count.
	TomlCommits int `json:"tomlCommits"`

	// SharedToml is true when at least one other component anywhere in the
	// project (not just within the current selection) uses the same source
	// TOML file. The TomlCommits count is then coarse because git history
	// is on a per-file basis -- the count includes commits that touched the
	// shared file for any reason, not just for this component.
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

	// FingerprintChanges is the number of commits where the lock file's
	// input-fingerprint actually changed.
	FingerprintChanges int `json:"fingerprintChanges"`

	// FingerprintChangeDetails is the per-commit metadata for each
	// fingerprint change counted in [FingerprintChanges] (oldest first).
	// Hidden from the human-readable table -- use JSON output to consume
	// them (e.g., to hand-author changelog entries).
	//
	// Each entry is populated from [sources.FingerprintChange] via an
	// explicit field-by-field copy in [populateLockMetrics]. The
	// gathering algorithm is shared with the synthetic dist-git history
	// flow; the wire-level type is local so that:
	//   - the JSON contract for this command lives in this file, and
	//   - removing a field from [sources.FingerprintChange] /
	//     [sources.CommitMetadata] surfaces as a compile error at the
	//     copy site rather than silently dropping changelog metadata.
	// The compile-error guard is one-directional (it catches REMOVED
	// upstream fields); a NEWLY ADDED upstream field is caught instead by
	// TestFingerprintChangeDTOMirrorsSource.
	FingerprintChangeDetails []FingerprintChange `json:"fingerprintChangeDetails,omitempty" table:"-"`

	// HasLock is true when a lock file currently exists for this component.
	HasLock bool `json:"hasLock,omitempty" table:"-"`

	// HasImport is true when the lock file records a non-empty
	// import-commit (i.e., the component was forked from upstream).
	HasImport bool `json:"hasImport,omitempty" table:"-"`

	// ManualBump is the lock file's manual-bump counter. Always emitted
	// (no omitempty) so a real bump of 0 isn't indistinguishable from an
	// absent field; pair it with HasLock to tell "no lock" from "bump 0".
	ManualBump int `json:"manualBump" table:"-"`

	// Warnings collects per-component diagnostics for failure paths that
	// were swallowed to keep the overall report rendering. Empty when no
	// problems were encountered. Surfaces in the single-component card
	// view and in JSON; hidden from the human-readable table.
	Warnings []string `json:"warnings,omitempty" table:"-"`
}

// FingerprintChange is the wire-level representation of one lock-file
// fingerprint change for the [HistoryResult.FingerprintChangeDetails]
// field. It mirrors the fields of [sources.FingerprintChange] (and its
// embedded [sources.CommitMetadata]) that consumers of `azldev component
// history` JSON output care about.
//
// The fields are copied explicitly in [populateLockMetrics] rather than
// embedding [sources.FingerprintChange] directly so that:
//   - the JSON contract for this command is owned by this package, and
//   - dropping a field from the synthetic-history source type produces a
//     compile error at the copy site instead of silently emptying the
//     downstream changelog data.
type FingerprintChange struct {
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
//
// When the user explicitly names component(s) (via positional args, --component,
// or --spec-path) the bare filter is force-disabled regardless of IncludeBare,
// so `azldev component history nano` always returns a row for nano even when
// nano has zero customizations. The perf rationale for the default does not
// apply to scope-limiting explicit selections.
func ComponentHistory(env *azldev.Env, options *HistoryOptions) ([]HistoryResult, error) {
	if err := validateSharedTomlMode(options.SharedTomlMode); err != nil {
		return nil, err
	}

	// History is read-only; skip lock validation so stale or missing locks
	// don't block reporting.
	options.ComponentFilter.SkipLockValidation = true

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
	// with zero customizations before any expensive work runs -- unless
	// the user explicitly named components, in which case they get what
	// they asked for regardless.
	explicit := hasExplicitComponentSelection(&options.ComponentFilter)
	effectiveIncludeBare := options.IncludeBare || explicit

	stubs := buildHistoryStubs(env, comps.Components(), effectiveIncludeBare)
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
	tomlCache, err := precomputeTomlMetricsForStubs(workerEnv, env, ctx, stubs)
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
		func(_ context.Context, stub historyStub) HistoryResult {
			// workerEnv carries the cancellable ctx; the parmap-supplied
			// ctx is identical (parmap derives it from workerEnv) and
			// unused here. Mirrors how render.go does this.
			return buildHistoryResult( //nolint:contextcheck // env carries the ctx
				workerEnv, stub, ctx, tomlSharing, tomlCache, options.SharedTomlMode, explicit,
			)
		},
	)

	results := make([]HistoryResult, 0, len(stubs))

	for _, parmapRes := range parmapResults {
		if parmapRes.Cancelled {
			continue
		}

		// --shared=omit drops the row entirely for components whose source
		// TOML is shared with at least one other component -- unless the user
		// explicitly named it, in which case they get the row regardless
		// (mirroring the --include-bare override above; sharing is a
		// presentation default, not the user's intent).
		if options.SharedTomlMode == sharedTomlModeOmit && parmapRes.Value.SharedToml && !explicit {
			continue
		}

		results = append(results, parmapRes.Value)
	}

	// FingerprintChangeDetails is potentially the largest field in the
	// payload (one entry per fingerprint change per component, each with
	// commit metadata); JSON consumers on -a runs at azurelinux scale would
	// otherwise get multi-MB responses. The details exist for drilling into
	// a single component to author a changelog, so keep them only when
	// exactly one component survives filtering. This is decided AFTER the
	// --shared=omit drop above so a single surviving row always carries its
	// details (the same len()==1 predicate the card view keys off).
	if len(results) != 1 {
		for i := range results {
			results[i].FingerprintChangeDetails = nil
		}
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

// hasExplicitComponentSelection reports whether the user pinpointed
// individual components (vs asking for everything, a group, or relying on
// no-criteria defaults). Used by [ComponentHistory] to override
// --include-bare (and the --shared=omit count suppression) in the explicit
// case so that `azldev component history nano` always returns a row for nano
// with its real count.
//
// Only an *exact* name (or a spec path) counts as explicit. A glob pattern
// (e.g. -p '*') can select the whole project, so it carries no more intent
// than -a or --component-group and must not defeat those filters' perf
// rationale. Wildcard detection mirrors the resolver (see
// [components.Resolver]).
//
// Group selection (--component-group) is likewise NOT treated as explicit --
// groups can contain hundreds of components.
func hasExplicitComponentSelection(filter *components.ComponentFilter) bool {
	for _, pattern := range filter.ComponentNamePatterns {
		if !strings.ContainsAny(pattern, "*?[") {
			return true
		}
	}

	return len(filter.SpecPaths) > 0
}

// validateSharedTomlMode rejects unrecognized --shared values.
func validateSharedTomlMode(mode string) error {
	switch mode {
	case sharedTomlModeShow, sharedTomlModeOmit:
		return nil
	default:
		return fmt.Errorf(
			"invalid --shared value %#q (want one of: %s, %s)",
			mode, sharedTomlModeShow, sharedTomlModeOmit)
	}
}
