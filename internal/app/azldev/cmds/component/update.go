// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/fingerprint"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/spf13/cobra"
)

// UpdateComponentOptions holds options for the component update command.
type UpdateComponentOptions struct {
	ComponentFilter components.ComponentFilter
	// Bump increments the manual-rebuild counter on matched components'
	// lock files. Used for mass-rebuild scenarios.
	Bump bool
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
toolchain bug, static library update). Orphan pruning is skipped under --bump.`,
		Example: `  # Update all components
  azldev component update -a

  # Update a single component
  azldev component update -p curl

  # Update components in a group
  azldev component update -g core

  # Bump rebuild counter for a component (triggers new release)
  azldev component update --bump curl`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			// Skip lock validation — update is the lock file writer.
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
}

// UpdateComponents resolves source identities for all selected components and
// writes the results to per-component lock files under locks/.
func UpdateComponents(env *azldev.Env, options *UpdateComponentOptions) ([]UpdateResult, error) {
	resolver := components.NewResolver(env)
	// Suppress staleness warnings — we're about to refresh the locks ourselves,
	// so warning the user to "run component update" would be self-referential noise.
	resolver.SuppressLockWarnings = true

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
	if err := saveComponentLocks(env, store, results); err != nil {
		return results, err
	}

	// Log summary after save so Changed counts include fingerprint-only diffs.
	logUpdateSummary(results)

	// Prune orphan lock files when updating all components.
	// Use the resolved component set (not raw config) to include
	// spec-glob-discovered components that aren't in config directly.
	// Lock files are version controlled, so pruning is safe even if the
	// resolved set is empty (e.g., all components removed from config).
	if options.ComponentFilter.IncludeAllComponents {
		if len(comps) == 0 {
			slog.Warn("No components resolved; all existing lock files will be treated as orphans")
		}

		resolvedNames := make(map[string]projectconfig.ComponentConfig, len(comps))
		for _, comp := range comps {
			resolvedNames[comp.GetName()] = *comp.GetConfig()
		}

		pruned, pruneErr := store.PruneOrphans(resolvedNames)
		if pruneErr != nil {
			return results, fmt.Errorf("pruning orphan lock files:\n%w", pruneErr)
		}

		if pruned > 0 {
			slog.Info("Pruned orphan lock files", "count", pruned)
		}
	}

	// Filter results for table output: show changed and skipped components.
	return filterDisplayResults(results), nil
}

// saveComponentLocks recomputes fingerprints and writes lock files for all
// resolved components. A lock file is saved when either the upstream commit
// or the input fingerprint has changed. The fingerprint is recomputed on every
// update, so config/overlay changes are detected even when the upstream commit
// stays the same.
func saveComponentLocks(env *azldev.Env, store *lockfile.Store, results []UpdateResult) error {
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
		if results[idx].Error != "" || results[idx].Skipped {
			continue
		}

		lock, lockErr := store.GetOrNew(results[idx].Component)
		if lockErr != nil {
			retErr = fmt.Errorf("loading lock for %#q:\n%w", results[idx].Component, lockErr)

			return retErr
		}

		lock.UpstreamCommit = results[idx].UpstreamCommit

		// Clear upstream-only fields for local components so a source-type
		// transition (upstream → local) doesn't leave stale data in the lock.
		if results[idx].config != nil &&
			results[idx].config.Spec.SourceType != projectconfig.SpecSourceTypeUpstream {
			lock.ImportCommit = ""
		}

		// Recompute fingerprint from resolved config + lock state.
		if results[idx].config == nil {
			retErr = fmt.Errorf("no resolved config for %#q; cannot compute fingerprint", results[idx].Component)

			return retErr
		}

		// Resolve per-component distro for ReleaseVer, matching the
		// per-component resolution used by render/build/prepare-sources.
		releaseVer, distroErr := resolveReleaseVer(env, results[idx].config)
		if distroErr != nil {
			retErr = fmt.Errorf("resolving distro for %#q:\n%w", results[idx].Component, distroErr)

			return retErr
		}

		identity, fpErr := fingerprint.ComputeIdentity(
			env.FS(),
			*results[idx].config,
			releaseVer,
			fingerprint.IdentityOptions{
				ManualBump:     lock.ManualBump,
				SourceIdentity: results[idx].sourceIdentity,
			},
		)
		if fpErr != nil {
			retErr = fmt.Errorf("computing fingerprint for %#q:\n%w", results[idx].Component, fpErr)

			return retErr
		}

		// Mark as changed if fingerprint differs (catches config/overlay edits
		// even when the upstream commit is unchanged).
		if lock.InputFingerprint != identity.Fingerprint {
			results[idx].Changed = true
		}

		lock.InputFingerprint = identity.Fingerprint

		// Only write if something actually changed.
		if !results[idx].Changed {
			continue
		}

		if saveErr := store.Save(results[idx].Component, lock); saveErr != nil {
			retErr = fmt.Errorf("saving lock file for %#q:\n%w", results[idx].Component, saveErr)

			return retErr
		}

		saved = append(saved, results[idx].Component)
	}

	return nil
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
	var changed, skipped int

	for idx := range results {
		switch {
		case results[idx].Skipped:
			skipped++
		case results[idx].Changed:
			changed++
		}
	}

	slog.Info("Update complete",
		"total", len(results),
		"changed", changed,
		"skipped", skipped)
}

// filterDisplayResults returns changed and skipped results for table display.
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

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	var waitGroup sync.WaitGroup

	// Each resolution may involve network I/O (upstream git clone) or
	// filesystem traversal (local spec-dir hashing), so we parallelize.
	semaphore := make(chan struct{}, env.FastConcurrency())

	for idx, comp := range comps {
		results[idx].Component = comp.GetName()

		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			// Context-aware semaphore acquisition.
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-workerEnv.Done():
				results[idx].Skipped = true
				results[idx].SkipReason = "cancelled"

				return
			}

			// Drop populated lock data so the source provider re-resolves
			// from upstream (snapshot/HEAD or pinned commit) or re-hashes
			// local spec content instead of short-circuiting with stale
			// locked values. We're about to overwrite the lock anyway.
			comp.GetConfig().Locked = nil

			identity, resolveErr := resolveOneSourceIdentity(workerEnv, comp)
			if resolveErr != nil {
				results[idx].Error = resolveErr.Error()

				// Cancel remaining goroutines on first real failure.
				cancel()

				return
			}

			results[idx].sourceIdentity = identity
			results[idx].config = comp.GetConfig()

			// For upstream components, the identity IS the commit hash.
			// For local components, UpstreamCommit stays empty.
			if comp.GetConfig().Spec.SourceType == projectconfig.SpecSourceTypeUpstream {
				results[idx].UpstreamCommit = identity
			}

			// Check existing lock to determine if the component changed.
			checkLockChanged(store, comp.GetName(), &results[idx])
		}()
	}

	waitGroup.Wait()

	return results
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

	identity, err := sourceManager.ResolveSourceIdentity(env.Context(), comp)
	if err != nil {
		return "", fmt.Errorf("resolving identity for %#q:\n%w", componentName, err)
	}

	slog.Info("Resolved source identity", "component", componentName, "identity", identity)

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
