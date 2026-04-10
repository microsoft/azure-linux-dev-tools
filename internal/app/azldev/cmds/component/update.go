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
	parentCmd.AddCommand(newComponentUpdateCommand())
}

func newComponentUpdateCommand() *cobra.Command {
	options := &UpdateComponentOptions{}

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Resolve and lock upstream commits for components",
		Long: `Resolve upstream commit hashes for components and write them to azldev.lock.

For upstream components, this resolves the effective commit hash using the
distro snapshot time or explicit pin, then records it in the lock file.
Subsequent commands (render, build) use the locked commit for deterministic,
reproducible results.

Local components are skipped — they have no upstream commit to resolve.`,
		Example: `  # Update all components
  azldev component update -a

  # Update a single component
  azldev component update -p curl

  # Update components in a group
  azldev component update -g core`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
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
// writes the results to azldev.lock.
func UpdateComponents(env *azldev.Env, options *UpdateComponentOptions) ([]UpdateResult, error) {
	resolver := components.NewResolver(env)

	resolved, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("resolving components:\n%w", err)
	}

	comps := resolved.Components()
	if len(comps) == 0 {
		return nil, errors.New("no components matched the filter")
	}

	// Load existing lock file or create a new one.
	lockPath := filepath.Join(env.ProjectDir(), lockfile.FileName)

	lock, loadErr := lockfile.Load(env.FS(), lockPath)
	if loadErr != nil {
		slog.Debug("No existing lock file, creating new one", "error", loadErr)

		lock = lockfile.New()
	}

	// Resolve upstream commits in parallel.
	results := resolveUpstreamCommitsParallel(env, comps, lock)

	// Count changes for summary log.
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

	slog.Info("Update complete",
		"total", len(results),
		"changed", changed,
		"skipped", skipped,
		"errors", len(failedNames))

	// Fail hard on any errors — the lock file must be complete and consistent.
	// Don't write a partial lock file that could silently produce wrong builds.
	if len(failedNames) > 0 {
		return results, fmt.Errorf(
			"%d component(s) failed to resolve; lock file not updated:\n  %s",
			len(failedNames), strings.Join(failedNames, "\n  "))
	}

	// Don't save if the context was cancelled (Ctrl+C).
	if env.Context().Err() != nil {
		return results, errors.New("update cancelled; lock file not updated")
	}

	// Write updated lock file only on full success.
	if saveErr := lock.Save(env.FS(), lockPath); saveErr != nil {
		return results, fmt.Errorf("saving lock file:\n%w", saveErr)
	}

	// Filter results for table output: only show changed components.
	// JSON consumers get the full list via -O json.
	var tableResults []UpdateResult

	for idx := range results {
		if results[idx].Changed || results[idx].Skipped {
			tableResults = append(tableResults, results[idx])
		}
	}

	return tableResults, nil
}

func resolveUpstreamCommitsParallel(
	env *azldev.Env,
	comps []components.Component,
	lock *lockfile.LockFile,
) []UpdateResult {
	results := make([]UpdateResult, len(comps))

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	var waitGroup sync.WaitGroup

	var lockMutex sync.Mutex

	// Each resolution involves a metadata-only git clone (I/O-bound).
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

			lockMutex.Lock()
			defer lockMutex.Unlock()

			previous, hasPrevious := lock.GetUpstreamCommit(comp.GetName())
			if hasPrevious {
				results[idx].PreviousCommit = previous
			}

			results[idx].Changed = !hasPrevious || previous != commitHash

			lock.SetUpstreamCommit(comp.GetName(), commitHash)
		}()
	}

	waitGroup.Wait()

	return results
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
