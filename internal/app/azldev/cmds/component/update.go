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
	"github.com/microsoft/azure-linux-dev-tools/internal/fingerprint"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
	"github.com/spf13/cobra"
)

// UpdateComponentOptions holds options for the component update command.
type UpdateComponentOptions struct {
	ComponentFilter components.ComponentFilter
	// Bump increments the manual-rebuild counter on matched components'
	// lock files. Used for mass-rebuild scenarios.
	Bump bool
	// CheckOnly runs the full update pipeline (resolve identities,
	// recompute fingerprints) but does not write lock files or prune
	// orphans. Returns a non-nil error when any component would be
	// changed or any lock file would be pruned. Intended for CI gates:
	// `azldev component update -a --check-only` exits 0 when locks are
	// fresh and 1 when something is stale.
	CheckOnly bool
	// ForceRecalculate disables freshness optimizations that skip
	// re-resolution for unchanged components. When set, all components
	// are re-resolved regardless of their freshness status.
	ForceRecalculate bool
}

func updateOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewUpdateCmd())
}

// NewUpdateCmd constructs a [cobra.Command] for the "component update" CLI subcommand.
func NewUpdateCmd() *cobra.Command {
	options := &UpdateComponentOptions{}

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Resolve and lock source identities for components",
		Long: `Resolve source identities for components and write them to per-component lock files.

For upstream components, this resolves the effective commit hash using the
distro snapshot time or explicit pin, then records it in locks/<name>.lock.
For local components, this computes a content hash of the spec directory.
Subsequent commands (render, build) use the locked state for deterministic,
reproducible results.

When updating all components (-a), orphan lock files (locks for components
that no longer exist in the project config) are automatically pruned.
Orphan pruning is skipped when updating individual components to avoid
accidentally removing lock files for components not included in the filter.

The --bump flag updates matching lock files to increment the manual-rebuild
counter, triggering a new release. Useful for mass-rebuild scenarios (e.g.,
toolchain bug, static library update). Orphan pruning is skipped under --bump.

The --check-only flag runs the full pipeline but does NOT write lock files or
prune orphans. The command exits 0 when nothing would change and exits 1 when
any component is stale or any lock would be pruned. Intended for CI gates.
Cannot be combined with --bump.`,
		Example: `  # Update all components
  azldev component update -a

  # Update a single component
  azldev component update -p curl

  # Update components in a group
  azldev component update -g core

  # Bump rebuild counter for a component (triggers new release)
  azldev component update --bump curl

  # CI gate: exit 0 if locks are fresh, 1 if anything would change
  azldev component update -a --check-only -q`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			// Skip lock validation -- update is the lock file writer.
			options.ComponentFilter.SkipLockValidation = true

			options.ComponentFilter.ComponentNamePatterns = append(
				args, options.ComponentFilter.ComponentNamePatterns...,
			)

			return UpdateComponents(env, options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().BoolVar(&options.Bump, "bump", false,
		"increment the manual-rebuild counter to trigger a new release")
	cmd.Flags().BoolVar(&options.CheckOnly, "check-only", false,
		"resolve identities and recompute fingerprints but do not write lock files "+
			"or prune orphans. Exits 0 when nothing would change and 1 when any "+
			"component is stale (or, with --all-components, when any orphan lock "+
			"would be pruned). Intended for CI gates. Cannot be combined with --bump")
	cmd.Flags().BoolVar(&options.ForceRecalculate, "force-recalculate", false,
		"force re-resolution of all components, ignoring freshness checks that "+
			"would skip unchanged components. Use when upstream state may have "+
			"changed independently of the snapshot time and the new commit is "+
			"preferred")

	cmd.MarkFlagsMutuallyExclusive("bump", "check-only")

	return cmd
}

// UpdateResult is the per-component output for the update command.
type UpdateResult struct {
	Component      string `json:"component"                table:",sortkey"`
	UpstreamCommit string `json:"upstreamCommit,omitempty"`
	PreviousCommit string `json:"previousCommit,omitempty" table:"-"`
	// Changed is set by checkLockChanged (commit diff) or saveComponentLocks (fingerprint diff).
	Changed    bool   `json:"changed"`
	Skipped    bool   `json:"skipped,omitempty"`
	SkipReason string `json:"skipReason,omitempty" table:",omitempty"`
	Error      string `json:"error,omitempty"      table:",omitempty"`

	// config is the resolved component config, used for fingerprint computation.
	// Not serialized — only needed during the update pipeline.
	config *projectconfig.ComponentConfig `json:"-" table:"-"`

	// sourceIdentity is the opaque identity string from the source provider.
	// For upstream components this is the resolved commit hash (same as UpstreamCommit);
	// for local components this is a content hash of the spec directory.
	// Used as SourceIdentity input for fingerprint computation.
	sourceIdentity string `json:"-" table:"-"`

	// upToDate is set by the freshness check (Case 1) when both the input
	// fingerprint and resolution hash match the lock. Components marked
	// upToDate are skipped by [saveComponentLocks] — no re-fingerprinting
	// or lock rewrite is needed.
	upToDate bool `json:"-" table:"-"`
}

// UpdateComponents resolves source identities for all selected components and
// writes the results to per-component lock files under locks/.
func UpdateComponents(env *azldev.Env, options *UpdateComponentOptions) ([]UpdateResult, error) {
	if options.Bump && options.CheckOnly {
		return nil, fmt.Errorf("%w: --bump and --check-only are mutually exclusive", azldev.ErrInvalidUsage)
	}

	resolver := components.NewResolver(env)
	// Suppress staleness warnings — we're about to refresh the locks ourselves,
	// so warning the user to "run component update" would be self-referential noise.
	resolver.SuppressLockWarnings = true
	// Enable freshness checking so the resolver computes FreshnessStatus for
	// each component. This lets resolveSourceIdentitiesParallel skip
	// re-resolution for components whose resolution inputs haven't changed.
	// Disabled when --force-recalculate is set.
	resolver.CheckFreshness = !options.ForceRecalculate

	resolved, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("resolving components:\n%w", err)
	}

	comps := resolved.Components()
	if len(comps) == 0 && !options.ComponentFilter.IncludeAllComponents {
		return nil, errors.New("no components matched the filter")
	}

	// Resolve upstream commits in parallel (no-op for empty list).
	store := env.LockStore()
	if store == nil {
		return nil, errors.New("no project directory configured; cannot update lock files")
	}

	// --bump: re-fingerprint existing locks with an incremented ManualBump.
	// Does not contact upstream. Skips orphan pruning — bump only touches
	// existing locks and should not delete entries for components that may
	// have been removed.
	if options.Bump {
		results, bumpErr := bumpComponents(env, store, comps, options)
		if bumpErr != nil {
			return results, bumpErr
		}

		logUpdateSummary(results)

		return filterDisplayResults(results), nil
	}

	results := resolveSourceIdentitiesParallel(env, comps, store)

	// Don't save if the context was cancelled (Ctrl+C).
	if env.Context().Err() != nil {
		return results, errors.New("update cancelled; lock files not updated")
	}

	// Check results and bail on errors before saving.
	if err := checkUpdateErrors(results); err != nil {
		return results, err
	}

	// Write per-component lock files only on full success.
	// saveComponentLocks may flip Changed for fingerprint-only diffs.
	// In --check-only mode it computes everything but skips disk writes.
	if err := saveComponentLocks(env, store, results, options.CheckOnly); err != nil {
		return results, err
	}

	// Log summary after save so Changed counts include fingerprint-only diffs.
	// Skipped in --check-only mode -- the "changed" counter would lie about a
	// run that wrote nothing, and the structured error returned below already
	// names every affected component.
	if !options.CheckOnly {
		logUpdateSummary(results)
	}

	// Prune orphan lock files when updating all components.
	// Use the resolved component set (not raw config) to include
	// spec-glob-discovered components that aren't in config directly.
	// Lock files are version controlled, so pruning is safe even if the
	// resolved set is empty (e.g., all components removed from config).
	wouldPrune, orphanErr := handleOrphanLocks(store, comps, options)
	if orphanErr != nil {
		return results, orphanErr
	}

	if options.CheckOnly {
		return checkOnlyResult(results, wouldPrune)
	}

	// Filter results for table output: show changed and skipped components.
	return filterDisplayResults(results), nil
}

// handleOrphanLocks reconciles the lockfile directory with the resolved
// component set. In normal mode it deletes orphan locks; in --check-only
// mode it returns the list of orphans that would be deleted without
// touching disk. Returns (nil, nil) when not running with --all-components,
// since orphan handling is scoped to whole-set updates.
func handleOrphanLocks(
	store *lockfile.Store,
	comps []components.Component,
	options *UpdateComponentOptions,
) ([]string, error) {
	if !options.ComponentFilter.IncludeAllComponents {
		return nil, nil
	}

	if len(comps) == 0 {
		if options.CheckOnly {
			slog.Warn("No components resolved; all existing lock files would be treated as orphans")
		} else {
			slog.Warn("No components resolved; all existing lock files will be treated as orphans")
		}
	}

	resolvedNames := make(map[string]projectconfig.ComponentConfig, len(comps))
	for _, comp := range comps {
		resolvedNames[comp.GetName()] = *comp.GetConfig()
	}

	if options.CheckOnly {
		orphans, findErr := store.FindOrphanLockFiles(resolvedNames)
		if findErr != nil {
			return nil, fmt.Errorf("finding orphan lock files:\n%w", findErr)
		}

		return orphans, nil
	}

	pruned, pruneErr := store.PruneOrphans(resolvedNames)
	if pruneErr != nil {
		return nil, fmt.Errorf("pruning orphan lock files:\n%w", pruneErr)
	}

	if pruned > 0 {
		slog.Info("Pruned orphan lock files", "count", pruned)
	}

	return nil, nil
}

// checkOnlyResult inspects the results of a --check-only update run and
// returns (results, error) when any component would change or any lock file
// would be pruned. The error names the affected components so CI logs are
// useful at a glance. Returns (results, nil) when nothing would change --
// the caller exits 0. Results are returned in both cases so structured
// consumers (e.g. -O json) retain the per-component data the pipeline just
// computed.
func checkOnlyResult(
	results []UpdateResult, wouldPrune []string,
) ([]UpdateResult, error) {
	var changed []string

	for idx := range results {
		if results[idx].Changed {
			changed = append(changed, results[idx].Component)
		}
	}

	display := filterDisplayResults(results)

	if len(changed) == 0 && len(wouldPrune) == 0 {
		return display, nil
	}

	var parts []string
	if len(changed) > 0 {
		parts = append(parts, fmt.Sprintf("%d component(s) would change: %s",
			len(changed), strings.Join(changed, ", ")))
	}

	if len(wouldPrune) > 0 {
		parts = append(parts, fmt.Sprintf("%d orphan lock file(s) would be pruned: %s",
			len(wouldPrune), strings.Join(wouldPrune, ", ")))
	}

	return display, fmt.Errorf("lock files are stale; %s. Run 'azldev component update -a' to refresh",
		strings.Join(parts, "; "))
}

// saveComponentLocks recomputes fingerprints and writes lock files for all
// resolved components. A lock file is saved when either the upstream commit
// or the input fingerprint has changed. The fingerprint is recomputed on every
// update, so config/overlay changes are detected even when the upstream commit
// stays the same.
//
// When checkOnly is true, fingerprints are still recomputed and Changed flags
// are still set on the results, but no lock files are written to disk.
func saveComponentLocks(env *azldev.Env, store *lockfile.Store, results []UpdateResult, checkOnly bool) error {
	saved := make([]string, 0, len(results))

	// Log partially-saved components on any error so the user knows which
	// lock files were written before the failure.
	var retErr error

	defer func() {
		if retErr != nil && len(saved) > 0 {
			slog.Info("Lock files saved before failure", "components", saved)
		}
	}()

	for idx := range results {
		if results[idx].Error != "" || results[idx].Skipped || results[idx].upToDate {
			continue
		}

		written, err := updateComponentLock(env, store, &results[idx], checkOnly)
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

// updateComponentLock recomputes the fingerprint for a single component and
// writes its lock file if anything changed. Returns true when the lock file
// was written.
//
// When checkOnly is true, the fingerprint is still recomputed and result.Changed
// is still flipped if anything would change, but no lock file is written. The
// returned 'written' flag is always false in check-only mode.
func updateComponentLock(env *azldev.Env, store *lockfile.Store, result *UpdateResult, checkOnly bool) (bool, error) {
	lock, lockErr := store.GetOrNew(result.Component)
	if lockErr != nil {
		return false, fmt.Errorf("loading lock for %#q:\n%w", result.Component, lockErr)
	}

	lock.UpstreamCommit = result.UpstreamCommit

	// Clear upstream-only fields for local components so a source-type
	// transition (upstream → local) doesn't leave stale data in the lock.
	if result.config != nil &&
		result.config.Spec.SourceType != projectconfig.SpecSourceTypeUpstream {
		lock.ImportCommit = ""
	}

	// Seed import-commit on first update so the synthetic history walk
	// has a bounded starting point instead of walking the entire repo.
	if lock.ImportCommit == "" && result.config != nil &&
		result.config.Spec.SourceType == projectconfig.SpecSourceTypeUpstream {
		lock.ImportCommit = result.UpstreamCommit
	}

	// Recompute fingerprint from resolved config + lock state.
	if result.config == nil {
		return false, fmt.Errorf("no resolved config for %#q; cannot compute fingerprint", result.Component)
	}

	// Resolve per-component distro for ReleaseVer, matching the
	// per-component resolution used by render/build/prepare-sources.
	releaseVer, distroErr := resolveReleaseVer(env, result.config)
	if distroErr != nil {
		return false, fmt.Errorf("resolving distro for %#q:\n%w", result.Component, distroErr)
	}

	identity, fpErr := fingerprint.ComputeIdentity(
		env.FS(),
		*result.config,
		releaseVer,
		fingerprint.IdentityOptions{
			ManualBump:     lock.ManualBump,
			SourceIdentity: result.sourceIdentity,
		},
	)
	if fpErr != nil {
		return false, fmt.Errorf("computing fingerprint for %#q:\n%w", result.Component, fpErr)
	}

	// Mark as changed if fingerprint differs (catches config/overlay edits
	// even when the upstream commit is unchanged). This is the user-visible
	// "changed" flag — it drives render/build decisions.
	if lock.InputFingerprint != identity.Fingerprint {
		result.Changed = true
	}

	lock.InputFingerprint = identity.Fingerprint

	// Update resolution input hash for upstream components.
	resHashChanged := updateResolutionHash(env, result.config, lock)

	// Write if either hash changed. ResolutionInputHash updates are silent
	// (don't set Changed) since they don't affect build outputs.
	if !result.Changed && !resHashChanged {
		return false, nil
	}

	// In check-only mode the caller wants to know what *would* change without
	// touching disk. Skip the write but keep result.Changed flipped so the
	// caller can build the user-visible diff list.
	if checkOnly {
		return false, nil
	}

	if saveErr := store.Save(result.Component, lock); saveErr != nil {
		return false, fmt.Errorf("saving lock file for %#q:\n%w", result.Component, saveErr)
	}

	return true, nil
}

// updateResolutionHash computes and stores the resolution input hash for
// upstream components. Returns true if the hash changed. Local components
// don't use upstream resolution, so the hash is left empty for them.
func updateResolutionHash(
	env *azldev.Env, config *projectconfig.ComponentConfig, lock *lockfile.ComponentLock,
) bool {
	if config.Spec.SourceType != projectconfig.SpecSourceTypeUpstream {
		return false
	}

	resInputs, resErr := components.BuildUpstreamCommitResolutionInputs(env, config)
	if resErr != nil {
		slog.Debug("Cannot compute resolution hash",
			"component", config.Name, "error", resErr)

		return false
	}

	resHash := fingerprint.ComputeResolutionHash(resInputs)
	changed := lock.ResolutionInputHash != resHash
	lock.ResolutionInputHash = resHash

	return changed
}

// bumpComponents re-fingerprints each matched component's lock file with an
// incremented ManualBump counter. Does not contact upstream. Triggers a new
// release without any other input change - used for mass-rebuild scenarios.
func bumpComponents(
	env *azldev.Env, store *lockfile.Store, comps []components.Component, options *UpdateComponentOptions,
) ([]UpdateResult, error) {
	results := make([]UpdateResult, 0, len(comps))
	saved := make([]string, 0, len(comps))

	for _, comp := range comps {
		// Check for cancellation (Ctrl+C) between components.
		if env.Context().Err() != nil {
			if len(saved) > 0 {
				slog.Info("Lock files bumped before cancellation", "components", saved)
			}

			return results, fmt.Errorf("bump cancelled; %d of %d components bumped", len(saved), len(comps))
		}

		name := comp.GetName()

		// Require an existing lock file - bump only makes sense for
		// components that have already been updated at least once.
		// Use Get (not GetOrNew) so missing locks produce a clear error
		// instead of silently creating an empty lock.
		lock, lockErr := store.Get(name)
		if lockErr != nil {
			if options.ComponentFilter.IncludeAllComponents {
				env.AddFixSuggestion("run 'azldev component update -a' first to populate lock files")
			} else {
				env.AddFixSuggestion(fmt.Sprintf("run 'azldev component update -p %s' first", name))
			}

			return results, fmt.Errorf("cannot bump %#q:\n%w", name, lockErr)
		}

		lock.ManualBump++

		slog.Info("Bumping component", "component", name, "manualBump", lock.ManualBump)

		// Resolve per-component distro for ReleaseVer, matching the
		// per-component resolution used by render/build/prepare-sources.
		releaseVer, distroErr := resolveReleaseVer(env, comp.GetConfig())
		if distroErr != nil {
			return results, fmt.Errorf("resolving distro for %#q:\n%w", name, distroErr)
		}

		// Determine source identity for fingerprint recomputation.
		srcIdentity, identityErr := resolveLockedSourceIdentity(env, comp, lock)
		if identityErr != nil {
			return results, identityErr
		}

		// Recompute fingerprint with the new ManualBump.
		identity, fpErr := fingerprint.ComputeIdentity(
			env.FS(),
			*comp.GetConfig(),
			releaseVer,
			fingerprint.IdentityOptions{
				ManualBump:     lock.ManualBump,
				SourceIdentity: srcIdentity,
			},
		)
		if fpErr != nil {
			return results, fmt.Errorf("computing fingerprint for %#q:\n%w", name, fpErr)
		}

		lock.InputFingerprint = identity.Fingerprint

		if saveErr := store.Save(name, lock); saveErr != nil {
			if len(saved) > 0 {
				slog.Info("Lock files bumped before failure", "components", saved)
			}

			return results, fmt.Errorf("saving lock for %#q:\n%w", name, saveErr)
		}

		saved = append(saved, name)

		results = append(results, UpdateResult{
			Component:      name,
			UpstreamCommit: lock.UpstreamCommit,
			Changed:        true,
		})
	}

	return results, nil
}

// resolveLockedSourceIdentity returns the source identity to use when
// recomputing a component's fingerprint during bump. For upstream components,
// this is the locked commit (bump doesn't change it). For local components,
// it re-hashes the spec directory. Does not perform network I/O.
func resolveLockedSourceIdentity(
	env *azldev.Env, comp components.Component, lock *lockfile.ComponentLock,
) (string, error) {
	name := comp.GetName()
	sourceType := comp.GetConfig().Spec.SourceType

	switch sourceType {
	case projectconfig.SpecSourceTypeUpstream:
		if lock.UpstreamCommit == "" {
			return "", fmt.Errorf(
				"lock file for upstream component %#q has no upstream-commit; "+
					"run 'azldev component update -p %s' to populate it before bumping",
				name, name)
		}

		return lock.UpstreamCommit, nil

	case projectconfig.SpecSourceTypeLocal, projectconfig.SpecSourceTypeUnspecified:
		specPath := comp.GetConfig().Spec.Path
		if specPath == "" {
			return "", fmt.Errorf("component %#q has no spec path configured", name)
		}

		identity, err := sourceproviders.ResolveLocalSourceIdentity(env.FS(), filepath.Dir(specPath))
		if err != nil {
			return "", fmt.Errorf("resolving local source identity for %#q:\n%w", name, err)
		}

		return identity, nil

	default:
		return "", fmt.Errorf("unsupported source type %#q for component %#q", sourceType, name)
	}
}

// checkUpdateErrors returns an error if any component failed to resolve.
// Does NOT log a summary — call [logUpdateSummary] after saves are complete
// so that Changed counts include fingerprint-only diffs.
func checkUpdateErrors(results []UpdateResult) error {
	var failedNames []string

	for idx := range results {
		if results[idx].Error != "" {
			failedNames = append(failedNames, results[idx].Component)
		}
	}

	if len(failedNames) > 0 {
		slog.Error("Update failed",
			"total", len(results),
			"errors", len(failedNames))

		return fmt.Errorf(
			"%d component(s) failed to resolve; lock files not updated:\n  %s",
			len(failedNames), strings.Join(failedNames, "\n  "))
	}

	return nil
}

// logUpdateSummary logs the final update summary. Called after saveComponentLocks
// so that Changed counts reflect fingerprint-only diffs.
func logUpdateSummary(results []UpdateResult) {
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

	slog.Info("Update complete",
		"total", len(results),
		"changed", changed,
		"upToDate", upToDate,
		"skipped", skipped)
}

// filterDisplayResults returns changed and skipped results for table display.
// Up-to-date components (not Changed, not Skipped) are excluded — they
// represent the common "nothing to do" case and would dominate the output.
func filterDisplayResults(results []UpdateResult) []UpdateResult {
	var tableResults []UpdateResult

	for idx := range results {
		if results[idx].Changed || results[idx].Skipped {
			tableResults = append(tableResults, results[idx])
		}
	}

	return tableResults
}

func resolveSourceIdentitiesParallel(
	env *azldev.Env,
	comps []components.Component,
	store *lockfile.Store,
) []UpdateResult {
	results := make([]UpdateResult, len(comps))

	progressEvent := env.StartEvent("Resolving source identities", "count", len(comps))
	defer progressEvent.End()

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	total := int64(len(comps))

	// Pre-filter: cases 1 and 2 fill results synchronously (no upstream
	// contact needed); case 3 needs parallel resolution.
	parallel, syncCompleted := classifyForResolution(comps, results)

	// Surface sync-skip progress immediately so users see movement even when
	// the parallel batch is empty or slow to start.
	if syncCompleted > 0 {
		progressEvent.SetProgress(syncCompleted, total)
	}

	// Each resolution may involve network I/O (upstream git clone) or
	// filesystem traversal (local spec-dir hashing), so we parallelize.
	parmapResults := parmap.Map(
		workerEnv,
		env.FastConcurrency(),
		parallel,
		func(done, _ int) {
			progressEvent.SetProgress(syncCompleted+int64(done), total)
		},
		func(ctx context.Context, item parallelItem) struct{} {
			resolveAndRecordIdentity(ctx, workerEnv, cancel, item.comp, store, &results[item.idx])

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

// parallelItem pairs a component with its result index for parmap workers.
type parallelItem struct {
	idx  int
	comp components.Component
}

// classifyForResolution applies the three-way freshness check to each
// component. Cases 1 and 2 (fully fresh / build-input-only changes) are
// resolved synchronously by mutating results in place. Case 3 components are
// returned in the parallel slice for upstream resolution. syncCompleted is
// the count of cases 1+2, used to seed the progress event.
//
// Only upstream components qualify for the freshness shortcut — local
// components resolve via filesystem hashing (cheap, no network), so skipping
// gains little and their empty UpstreamCommit can't serve as source identity.
//
//  1. FreshnessCurrent → nothing changed, skip entirely (no re-resolution).
//  2. FreshnessStale + resolution fresh → only build inputs changed
//     (e.g., overlay edit). Reuse locked commit, but enter save path
//     to update the fingerprint.
//  3. FreshnessStale + resolution stale → resolution inputs changed
//     (e.g., snapshot bump). Must re-resolve upstream.
func classifyForResolution(
	comps []components.Component, results []UpdateResult,
) (parallel []parallelItem, syncCompleted int64) {
	parallel = make([]parallelItem, 0, len(comps))

	for idx, comp := range comps {
		results[idx].Component = comp.GetName()

		locked := comp.GetConfig().Locked
		isUpstream := comp.GetConfig().Spec.SourceType == projectconfig.SpecSourceTypeUpstream

		if isUpstream && locked != nil && locked.Freshness == projectconfig.FreshnessCurrent {
			// Case 1: fully up-to-date — skip.
			results[idx].UpstreamCommit = locked.UpstreamCommit
			results[idx].sourceIdentity = comp.GetConfig().EffectiveUpstreamCommit()
			results[idx].upToDate = true
			syncCompleted++

			continue
		}

		if isUpstream && locked != nil && locked.Freshness == projectconfig.FreshnessStale &&
			!locked.ResolutionStale && locked.UpstreamCommit != "" {
			// Case 2: build inputs changed but resolution inputs unchanged.
			// Reuse the locked commit — re-resolving would yield the same hash.
			results[idx].UpstreamCommit = locked.UpstreamCommit
			results[idx].PreviousCommit = locked.UpstreamCommit
			results[idx].sourceIdentity = comp.GetConfig().EffectiveUpstreamCommit()
			results[idx].config = comp.GetConfig()
			syncCompleted++

			continue
		}

		// Case 3: resolution stale, unknown, or no lock — must re-resolve.
		parallel = append(parallel, parallelItem{idx: idx, comp: comp})
	}

	return parallel, syncCompleted
}

// resolveAndRecordIdentity resolves a single component's source identity and
// records the result. Called from a parmap worker in [resolveSourceIdentitiesParallel].
func resolveAndRecordIdentity(
	ctx context.Context,
	env *azldev.Env,
	cancel context.CancelFunc,
	comp components.Component,
	store *lockfile.Store,
	result *UpdateResult,
) {
	// Drop populated lock data so the source provider re-resolves
	// from upstream (snapshot/HEAD or pinned commit) or re-hashes
	// local spec content instead of short-circuiting with stale
	// locked values. We're about to overwrite the lock anyway.
	comp.GetConfig().Locked = nil

	identity, resolveErr := resolveOneSourceIdentity(ctx, env, comp)
	if resolveErr != nil {
		result.Error = resolveErr.Error()

		// Cancel remaining goroutines on first real failure.
		cancel()

		return
	}

	result.sourceIdentity = identity
	result.config = comp.GetConfig()

	// For upstream components, the identity IS the commit hash.
	// For local components, UpstreamCommit stays empty.
	if comp.GetConfig().Spec.SourceType == projectconfig.SpecSourceTypeUpstream {
		result.UpstreamCommit = identity
	}

	// Check existing lock to determine if the component changed.
	checkLockChanged(store, comp.GetName(), result)
}

// checkLockChanged compares the resolved upstream commit against the existing
// lock file to determine if the component changed. For new components (no lock
// file), marks as Changed unconditionally. For existing locks, compares
// UpstreamCommit values — for local components both sides are empty, so
// Changed stays false. The fingerprint comparison in [saveComponentLocks] is
// the definitive source of truth and will flip Changed to true when content
// actually changed.
func checkLockChanged(store *lockfile.Store, componentName string, result *UpdateResult) {
	exists, existsErr := store.Exists(componentName)
	if existsErr != nil {
		result.Error = fmt.Sprintf("checking lock: %v", existsErr)

		return
	}

	if !exists {
		result.Changed = true

		return
	}

	existingLock, loadErr := store.Get(componentName)
	if loadErr != nil {
		result.Error = fmt.Sprintf("loading lock: %v", loadErr)

		return
	}

	result.PreviousCommit = existingLock.UpstreamCommit
	result.Changed = existingLock.UpstreamCommit != result.UpstreamCommit
}

func resolveOneSourceIdentity(
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

	identity, err := sourceManager.ResolveSourceIdentity(ctx, comp)
	if err != nil {
		return "", fmt.Errorf("resolving identity for %#q:\n%w", componentName, err)
	}

	slog.Debug("Resolved source identity", "component", componentName, "identity", identity)

	return identity, nil
}

// resolveReleaseVer resolves the distro release version for a component,
// respecting per-component distro overrides. Falls back to the project's
// default distro when the component doesn't specify one — matching the
// resolution logic in sourceproviders.ResolveDistro.
func resolveReleaseVer(env *azldev.Env, config *projectconfig.ComponentConfig) (string, error) {
	ref := config.Spec.UpstreamDistro
	if ref.Name == "" {
		ref = env.Config().Project.DefaultDistro
	}

	_, distroVer, err := env.ResolveDistroRef(ref)
	if err != nil {
		return "", fmt.Errorf("resolving distro ref %#q:\n%w", ref.Name, err)
	}

	return distroVer.ReleaseVer, nil
}
