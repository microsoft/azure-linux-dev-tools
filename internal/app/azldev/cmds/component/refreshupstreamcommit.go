// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/upstreamcommit"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
	"github.com/spf13/cobra"
)

// RefreshUpstreamCommitOptions holds options for the component refresh-upstream-commit command.
type RefreshUpstreamCommitOptions struct {
	ComponentFilter components.ComponentFilter
	// UpstreamCommitsDir is the project-relative or absolute directory containing
	// generated per-component TOML files.
	UpstreamCommitsDir string
	// CheckOnly resolves upstream commits but does not write TOML files or
	// prune orphans.
	CheckOnly bool
}

const defaultUpstreamCommitsDir = "base/upstream-commits"

func refreshUpstreamCommitOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewRefreshUpstreamCommitCmd())
}

// NewRefreshUpstreamCommitCmd constructs the "component refresh-upstream-commit" command.
func NewRefreshUpstreamCommitCmd() *cobra.Command {
	options := &RefreshUpstreamCommitOptions{}
	options.UpstreamCommitsDir = defaultUpstreamCommitsDir

	cmd := &cobra.Command{
		Use:   "refresh-upstream-commit",
		Short: "Resolve and record upstream commits for components",
		Long: `Resolve upstream commits for components and write normal per-component TOML configuration.

For upstream components, this resolves the effective commit hash using the
distro snapshot time, then records it as spec.upstream-commit in
base/upstream-commits/<name>.toml by default. Include that directory's TOML
files before component-specific TOML configuration so subsequent commands use
the generated commit unless the component configuration explicitly overrides
it.

All TOML configuration is resolved before the source type is checked. Selected
components whose effective spec.type is not "upstream" do not contact an
upstream provider, and any generated upstream-commit TOML for them is removed.
Configuration is loaded permissively for this command so stale generated pins
cannot prevent cleanup after a component is removed or changed to another
source type. Other configuration validation failures are reported as warnings.

When updating all components (-a), orphan generated TOML files are
automatically pruned.
Orphan pruning is skipped when updating individual components to avoid
accidentally removing files for components not included in the filter.

The --check-only flag runs the full pipeline but does NOT write TOML files or
prune orphans. The command exits 0 when nothing would change and exits 1 when
any component is stale or any generated TOML would be pruned. Intended for CI gates.`,
		Example: `  # Refresh all components
  azldev component refresh-upstream-commit -a

  # Refresh a single component
  azldev component refresh-upstream-commit -p curl

  # Refresh components in a group
  azldev component refresh-upstream-commit -g core

  # Write generated files to a custom directory
  azldev component refresh-upstream-commit -a --upstream-commits-dir config/commits

  # CI gate: exit 0 if commit TOMLs are current, 1 if anything would change
  azldev component refresh-upstream-commit -a --check-only -q`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(
				args, options.ComponentFilter.ComponentNamePatterns...,
			)

			return RefreshUpstreamCommits(env, options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
		Annotations: map[string]string{
			azldev.CommandAnnotationPermissiveConfig: "true",
		},
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().StringVar(&options.UpstreamCommitsDir, "upstream-commits-dir",
		defaultUpstreamCommitsDir,
		"directory for generated per-component upstream-commit TOML files")
	_ = cmd.MarkFlagDirname("upstream-commits-dir")
	cmd.Flags().BoolVar(&options.CheckOnly, "check-only", false,
		"resolve upstream commits but do not write TOML files or prune orphans. "+
			"Exits 0 when nothing would change and 1 when any component is stale "+
			"(or, with --all-components, when any orphan generated TOML "+
			"would be pruned). Intended for CI gates")

	return cmd
}

// RefreshUpstreamCommitResult is the per-component output for the refresh command.
type RefreshUpstreamCommitResult struct {
	Component      string `json:"component"                table:",sortkey"`
	UpstreamCommit string `json:"upstreamCommit,omitempty"`
	PreviousCommit string `json:"previousCommit,omitempty" table:"-"`
	Changed        bool   `json:"changed"`
	Removed        bool   `json:"removed,omitempty"        table:",omitempty"`
	Skipped        bool   `json:"skipped,omitempty"`
	SkipReason     string `json:"skipReason,omitempty"     table:",omitempty"`
	Error          string `json:"error,omitempty"          table:",omitempty"`
}

// RefreshUpstreamCommits resolves upstream commits for all selected components and
// writes the results to per-component TOML files.
func RefreshUpstreamCommits(
	env *azldev.Env, options *RefreshUpstreamCommitOptions,
) ([]RefreshUpstreamCommitResult, error) {
	resolver := components.NewResolver(env)

	resolved, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("resolving components:\n%w", err)
	}

	allComps := resolved.Components()
	if len(allComps) == 0 && !options.ComponentFilter.IncludeAllComponents {
		return nil, errors.New("no components matched the filter")
	}

	if env.ProjectDir() == "" {
		return nil, errors.New("no project directory configured; cannot refresh upstream commit TOML files")
	}

	commitDir := options.UpstreamCommitsDir
	if commitDir == "" {
		commitDir = defaultUpstreamCommitsDir
	}

	if !filepath.IsAbs(commitDir) {
		commitDir = filepath.Join(env.ProjectDir(), commitDir)
	}

	store := upstreamcommit.NewStore(env.FS(), commitDir)

	// Configuration is fully parsed and merged before deciding whether each
	// selected component has an upstream identity. Non-upstream components
	// never contact a provider; an existing generated pin is marked for
	// removal instead.
	upstreamComps, results, inspectErr := inspectSelectedComponents(allComps, store)
	if inspectErr != nil {
		return results, inspectErr
	}

	results = append(results, resolveUpstreamCommitsParallel(env, upstreamComps, store)...)

	// Don't save if the context was cancelled (Ctrl+C).
	if env.Context().Err() != nil {
		return results, errors.New("refresh cancelled; upstream commit TOML files not updated")
	}

	// Check results and bail on errors before saving.
	if err := checkRefreshErrors(results); err != nil {
		return filterRefreshDisplayResults(results), err
	}

	// Write per-component TOML files only on full success.
	if err := saveUpstreamCommitConfigs(store, results, options.CheckOnly); err != nil {
		return results, err
	}

	// Skipped in --check-only mode -- the "changed" counter would lie about a
	// run that wrote nothing, and the structured error returned below already
	// names every affected component.
	if !options.CheckOnly {
		logRefreshSummary(results)
	}

	// Prune orphan generated TOML files when updating all components.
	// Use the resolved component set (not raw config) to include
	// spec-glob-discovered components that aren't in config directly.
	// Generated TOMLs are version controlled, so pruning is safe even if the
	// resolved set is empty (e.g., all components removed from config).
	wouldPrune, orphanErr := handleOrphanConfigs(store, allComps, options)
	if orphanErr != nil {
		return filterRefreshDisplayResults(results), orphanErr
	}

	if options.CheckOnly {
		wouldPrune = excludePendingRemovals(wouldPrune, results)

		return refreshCheckOnlyResult(results, wouldPrune)
	}

	// Filter results for table output: show changed and skipped components.
	return filterRefreshDisplayResults(results), nil
}

func inspectSelectedComponents(
	comps []components.Component,
	store *upstreamcommit.Store,
) ([]components.Component, []RefreshUpstreamCommitResult, error) {
	upstream := make([]components.Component, 0, len(comps))

	var results []RefreshUpstreamCommitResult

	for _, comp := range comps {
		if comp.GetConfig().Spec.SourceType == projectconfig.SpecSourceTypeUpstream {
			upstream = append(upstream, comp)

			continue
		}

		exists, err := store.Exists(comp.GetName())
		if err != nil {
			return nil, results, fmt.Errorf(
				"checking generated upstream commit TOML for non-upstream component %#q:\n%w",
				comp.GetName(), err,
			)
		}

		if exists {
			results = append(results, RefreshUpstreamCommitResult{
				Component: comp.GetName(),
				Changed:   true,
				Removed:   true,
			})
		}
	}

	return upstream, results, nil
}

func excludePendingRemovals(
	orphans []string, results []RefreshUpstreamCommitResult,
) []string {
	removed := make(map[string]struct{})

	for idx := range results {
		if results[idx].Removed {
			removed[results[idx].Component] = struct{}{}
		}
	}

	filtered := make([]string, 0, len(orphans))
	for _, orphan := range orphans {
		if _, found := removed[orphan]; !found {
			filtered = append(filtered, orphan)
		}
	}

	return filtered
}

// handleOrphanConfigs reconciles the generated TOML directory with the resolved
// component set. In normal mode it deletes orphan files; in --check-only
// mode it returns the list of orphans that would be deleted without
// touching disk. Returns (nil, nil) when not running with --all-components,
// since orphan handling is scoped to whole-set updates.
func handleOrphanConfigs(
	store *upstreamcommit.Store,
	comps []components.Component,
	options *RefreshUpstreamCommitOptions,
) ([]string, error) {
	return handleOrphans(
		upstreamCommitOrphanStore{store: store},
		comps,
		options.ComponentFilter.IncludeAllComponents,
		options.CheckOnly,
		orphanMessages{
			noComponents: "all generated upstream commit TOMLs",
			pruned:       "Pruned orphan upstream commit TOMLs",
		},
	)
}

// upstreamCommitOrphanStore adapts [upstreamcommit.Store] to [orphanStore].
type upstreamCommitOrphanStore struct {
	store *upstreamcommit.Store
}

func (s upstreamCommitOrphanStore) findOrphans(
	resolved map[string]projectconfig.ComponentConfig,
) ([]string, error) {
	orphans, err := s.store.FindOrphans(resolved)
	if err != nil {
		return nil, fmt.Errorf("finding orphan upstream commit TOMLs:\n%w", err)
	}

	return orphans, nil
}

func (s upstreamCommitOrphanStore) pruneOrphans(
	resolved map[string]projectconfig.ComponentConfig,
) (int, error) {
	pruned, err := s.store.PruneOrphans(resolved)
	if err != nil {
		return 0, fmt.Errorf("pruning orphan upstream commit TOMLs:\n%w", err)
	}

	return pruned, nil
}

// refreshCheckOnlyResult inspects the results of a '--check-only' refresh run and
// returns (results, error) when any component would change or any generated TOML
// would be pruned. The error names the affected components so CI logs are
// useful at a glance. Returns (results, nil) when nothing would change --
// the caller exits 0. Results are returned in both cases so structured
// consumers (e.g. -O json) retain the per-component data the pipeline just
// computed.
func refreshCheckOnlyResult(
	results []RefreshUpstreamCommitResult, wouldPrune []string,
) ([]RefreshUpstreamCommitResult, error) {
	var changed []string

	for idx := range results {
		if results[idx].Changed {
			changed = append(changed, results[idx].Component)
		}
	}

	display := filterRefreshDisplayResults(results)

	if len(changed) == 0 && len(wouldPrune) == 0 {
		return display, nil
	}

	var parts []string
	if len(changed) > 0 {
		parts = append(parts, fmt.Sprintf("%d component(s) would change: %s",
			len(changed), strings.Join(changed, ", ")))
	}

	if len(wouldPrune) > 0 {
		parts = append(parts, fmt.Sprintf("%d orphan upstream commit TOML file(s) would be pruned: %s",
			len(wouldPrune), strings.Join(wouldPrune, ", ")))
	}

	return display, fmt.Errorf("upstream commit TOML files are stale; %s. "+
		"Run 'azldev component refresh-upstream-commit -a' to refresh",
		strings.Join(parts, "; "))
}

// saveUpstreamCommitConfigs writes TOML files for changed upstream commits.
func saveUpstreamCommitConfigs(
	store *upstreamcommit.Store, results []RefreshUpstreamCommitResult, checkOnly bool,
) error {
	saved := make([]string, 0, len(results))

	// Log partially-saved components on any error so the user knows which
	// TOML files were written before the failure.
	var retErr error

	defer func() {
		if retErr != nil && len(saved) > 0 {
			slog.Info("Upstream commit TOMLs saved before failure", "components", saved)
		}
	}()

	for idx := range results {
		if results[idx].Error != "" || results[idx].Skipped {
			continue
		}

		written, err := applyRefreshResult(store, &results[idx], checkOnly)
		if err != nil {
			retErr = err

			return retErr
		}

		if written {
			saved = append(saved, results[idx].Component)
		}
	}

	return nil
}

// applyRefreshResult writes one changed TOML file. The returned 'written'
// flag is always false in check-only mode.
func applyRefreshResult(
	store *upstreamcommit.Store, result *RefreshUpstreamCommitResult, checkOnly bool,
) (bool, error) {
	if !result.Changed {
		return false, nil
	}

	// In check-only mode the caller wants to know what *would* change without
	// touching disk. Skip the write but keep result.Changed flipped so the
	// caller can build the user-visible diff list.
	if checkOnly {
		return false, nil
	}

	if result.Removed {
		removed, removeErr := store.Remove(result.Component)
		if removeErr != nil {
			return false, fmt.Errorf(
				"removing upstream commit TOML for non-upstream component %#q:\n%w",
				result.Component, removeErr,
			)
		}

		return removed, nil
	}

	if saveErr := store.Save(result.Component, result.UpstreamCommit); saveErr != nil {
		return false, fmt.Errorf("saving upstream commit TOML for %#q:\n%w", result.Component, saveErr)
	}

	return true, nil
}

// checkRefreshErrors returns an error if any component failed to resolve.
// Does NOT log a summary; call [logRefreshSummary] after saves are complete.
func checkRefreshErrors(results []RefreshUpstreamCommitResult) error {
	var failedNames []string

	for idx := range results {
		if results[idx].Error != "" {
			failedNames = append(failedNames, results[idx].Component)
		}
	}

	if len(failedNames) > 0 {
		slog.Error("Refresh failed",
			"total", len(results),
			"errors", len(failedNames))

		return fmt.Errorf(
			"%d component(s) failed to resolve; upstream commit TOML files not updated:\n  %s",
			len(failedNames), strings.Join(failedNames, "\n  "))
	}

	return nil
}

// logRefreshSummary logs the final refresh summary.
func logRefreshSummary(results []RefreshUpstreamCommitResult) {
	var changed, skipped, upToDate int

	for idx := range results {
		switch {
		case results[idx].Skipped:
			skipped++
		case results[idx].Changed:
			changed++
		default:
			upToDate++
		}
	}

	slog.Info("Refresh complete",
		"total", len(results),
		"changed", changed,
		"upToDate", upToDate,
		"skipped", skipped)
}

// filterRefreshDisplayResults returns changed, skipped, and errored results for table
// display. Up-to-date components (not Changed, not Skipped, no Error) are
// excluded — they represent the common "nothing to do" case and would dominate
// the output. Errored entries are kept so the user can see what failed when
// the command exits non-zero via the partial-results-on-error path.
func filterRefreshDisplayResults(
	results []RefreshUpstreamCommitResult,
) []RefreshUpstreamCommitResult {
	var tableResults []RefreshUpstreamCommitResult

	for idx := range results {
		if results[idx].Changed || results[idx].Skipped || results[idx].Error != "" {
			tableResults = append(tableResults, results[idx])
		}
	}

	return tableResults
}

func resolveUpstreamCommitsParallel(
	env *azldev.Env,
	comps []components.Component,
	store *upstreamcommit.Store,
) []RefreshUpstreamCommitResult {
	results := make([]RefreshUpstreamCommitResult, len(comps))

	progressEvent := env.StartEvent("Resolving upstream commits", "count", len(comps))
	defer progressEvent.End()

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	// Resolve every selected upstream component instead of inferring freshness
	// from duplicated metadata. The provider is the authority for the commit
	// selected by the snapshot, and that result is compared directly with the
	// generated TOML before deciding whether a write is needed.
	parallel := make([]refreshParallelItem, len(comps))
	for idx, comp := range comps {
		results[idx].Component = comp.GetName()
		parallel[idx] = refreshParallelItem{idx: idx, comp: comp}
	}

	// Each resolution may involve network I/O, so we parallelize.
	parmapResults := parmap.Map(
		workerEnv,
		env.FastConcurrency(),
		parallel,
		func(done, _ int) {
			progressEvent.SetProgress(int64(done), int64(len(comps)))
		},
		func(ctx context.Context, item refreshParallelItem) struct{} {
			resolveAndRecordCommit(ctx, workerEnv, cancel, item.comp, store, &results[item.idx])

			return struct{}{}
		},
	)

	// Items that never acquired a worker slot (ctx cancelled mid-flight) get
	// marked Skipped — matches the legacy semaphore-select behaviour.
	for i, pr := range parmapResults {
		if pr.Cancelled {
			idx := parallel[i].idx
			results[idx].Skipped = true
			results[idx].SkipReason = "cancelled"
		}
	}

	return results
}

// refreshParallelItem pairs a component with its result index for parmap workers.
type refreshParallelItem struct {
	idx  int
	comp components.Component
}

// resolveAndRecordCommit resolves one component's upstream commit.
func resolveAndRecordCommit(
	ctx context.Context,
	env *azldev.Env,
	cancel context.CancelFunc,
	comp components.Component,
	store *upstreamcommit.Store,
	result *RefreshUpstreamCommitResult,
) {
	// Clear the loaded pin before asking the provider to resolve. Render and
	// build honor Spec.UpstreamCommit for reproducibility, but refresh is the
	// operation that advances that pin: leaving the old value in place would
	// make the provider return it immediately and a newer snapshot could never
	// move the component forward.
	comp.GetConfig().Spec.UpstreamCommit = ""

	commit, resolveErr := resolveUpstreamCommit(ctx, env, comp)
	if resolveErr != nil {
		result.Error = resolveErr.Error()

		// Cancel remaining goroutines on first real failure.
		cancel()

		return
	}

	result.UpstreamCommit = commit

	checkConfigChanged(store, comp.GetName(), result)
}

// checkConfigChanged compares the resolved commit with the generated TOML.
func checkConfigChanged(
	store *upstreamcommit.Store, componentName string, result *RefreshUpstreamCommitResult,
) {
	existingCommit, exists, loadErr := store.Get(componentName)
	if loadErr != nil {
		result.Error = fmt.Sprintf("loading upstream commit TOML: %v", loadErr)

		return
	}

	if !exists {
		result.Changed = true

		return
	}

	result.PreviousCommit = existingCommit
	result.Changed = existingCommit != result.UpstreamCommit
}

func resolveUpstreamCommit(
	ctx context.Context,
	env *azldev.Env,
	comp components.Component,
) (string, error) {
	componentName := comp.GetName()

	distro, err := sourceproviders.ResolveDistro(env, comp)
	if err != nil {
		return "", fmt.Errorf("resolving distro for %#q:\n%w", componentName, err)
	}

	sourceManager, err := sourceproviders.NewSourceManager(env, distro)
	if err != nil {
		return "", fmt.Errorf("creating source manager for %#q:\n%w", componentName, err)
	}

	commit, err := sourceManager.ResolveSourceIdentity(ctx, comp)
	if err != nil {
		return "", fmt.Errorf("resolving upstream commit for %#q:\n%w", componentName, err)
	}

	slog.Debug("Resolved upstream commit", "component", componentName, "commit", commit)

	return commit, nil
}
