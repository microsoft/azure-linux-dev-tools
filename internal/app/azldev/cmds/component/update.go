// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/spf13/cobra"
)

// UpdateComponentOptions holds options for the component update command.
type UpdateComponentOptions struct {
	ComponentFilter components.ComponentFilter
}

func updateOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewUpdateCmd())
}

// NewUpdateCmd constructs a [cobra.Command] for the "component update" CLI subcommand.
func NewUpdateCmd() *cobra.Command {
	options := &UpdateComponentOptions{}

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Resolve and lock upstream commits for components",
		Long: `Resolve upstream commit hashes for components and write them to per-component lock files.

For upstream components, this resolves the effective commit hash using the
distro snapshot time or explicit pin, then records it in locks/<name>.lock.
Subsequent commands (render, build) use the locked commit for deterministic,
reproducible results.

Local components are skipped — they have no upstream commit to resolve.

When updating all components (-a), orphan lock files (locks for components
that no longer exist in the project config) are automatically pruned.
Orphan pruning is skipped when updating individual components to avoid
accidentally removing lock files for components not included in the filter.`,
		Example: `  # Update all components
  azldev component update -a

  # Update a single component
  azldev component update -p curl

  # Update components in a group
  azldev component update -g core`,
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

	return cmd
}

// UpdateResult is the per-component output for the update command.
type UpdateResult struct {
	Component      string `json:"component"                table:",sortkey"`
	UpstreamCommit string `json:"upstreamCommit,omitempty"`
	PreviousCommit string `json:"previousCommit,omitempty" table:"-"`
	Changed        bool   `json:"changed"`
	Skipped        bool   `json:"skipped,omitempty"`
	SkipReason     string `json:"skipReason,omitempty"     table:",omitempty"`
	Error          string `json:"error,omitempty"          table:",omitempty"`
}

// UpdateComponents resolves upstream commits for all selected components and
// writes the results to per-component lock files under locks/.
func UpdateComponents(env *azldev.Env, options *UpdateComponentOptions) ([]UpdateResult, error) {
	resolver := components.NewResolver(env)

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

	results := resolveUpstreamCommitsParallel(env, comps, store)

	// Don't save if the context was cancelled (Ctrl+C).
	if env.Context().Err() != nil {
		return results, errors.New("update cancelled; lock files not updated")
	}

	// Check results and bail on errors before saving.
	if err := checkUpdateResults(results); err != nil {
		return results, err
	}

	// Write per-component lock files only on full success.
	if err := saveComponentLocks(store, results); err != nil {
		return results, err
	}

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

// saveComponentLocks writes a lock file for each changed component.
func saveComponentLocks(store *lockfile.Store, results []UpdateResult) error {
	saved := make([]string, 0, len(results))

	for idx := range results {
		if !results[idx].Changed || results[idx].Error != "" || results[idx].Skipped {
			continue
		}

		lock, lockErr := store.GetOrNew(results[idx].Component)
		if lockErr != nil {
			return fmt.Errorf("loading lock for %#q:\n%w", results[idx].Component, lockErr)
		}

		lock.UpstreamCommit = results[idx].UpstreamCommit

		if saveErr := store.Save(results[idx].Component, lock); saveErr != nil {
			if len(saved) > 0 {
				slog.Info("Lock files saved before failure", "components", saved)
			}

			return fmt.Errorf("saving lock file for %#q:\n%w", results[idx].Component, saveErr)
		}

		saved = append(saved, results[idx].Component)
	}

	return nil
}

// checkUpdateResults counts results, logs a summary, and returns an error if any
// component failed.
func checkUpdateResults(results []UpdateResult) error {
	var changed, skipped int

	var failedNames []string

	for idx := range results {
		switch {
		case results[idx].Error != "":
			failedNames = append(failedNames, results[idx].Component)
		case results[idx].Skipped:
			skipped++
		case results[idx].Changed:
			changed++
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

	slog.Info("Update complete",
		"total", len(results),
		"changed", changed,
		"skipped", skipped)

	return nil
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

func resolveUpstreamCommitsParallel(
	env *azldev.Env,
	comps []components.Component,
	store *lockfile.Store,
) []UpdateResult {
	results := make([]UpdateResult, len(comps))

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	var waitGroup sync.WaitGroup

	// Each resolution involves a metadata-only git clone, in practice highly-parallelizable.
	semaphore := make(chan struct{}, env.FastConcurrency())

	for idx, comp := range comps {
		results[idx].Component = comp.GetName()

		// Skip non-upstream components.
		sourceType := comp.GetConfig().Spec.SourceType
		if sourceType != projectconfig.SpecSourceTypeUpstream {
			results[idx].Skipped = true
			results[idx].SkipReason = fmt.Sprintf("source type %q is not upstream", sourceType)

			continue
		}

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

			commitHash, resolveErr := resolveOneUpstreamCommit(workerEnv, comp)
			if resolveErr != nil {
				results[idx].Error = resolveErr.Error()

				// Cancel remaining goroutines on first real failure.
				cancel()

				return
			}

			results[idx].UpstreamCommit = commitHash

			// Check existing lock to determine if the commit changed.
			checkLockChanged(store, comp.GetName(), &results[idx])
		}()
	}

	waitGroup.Wait()

	return results
}

// checkLockChanged compares the resolved commit against the existing lock file
// to determine if the component changed. Distinguishes "not found" (new
// component) from real errors (corrupt/unreadable lock file).
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

func resolveOneUpstreamCommit(
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

	slog.Info("Resolved upstream commit", "component", componentName, "commit", identity)

	return identity, nil
}
