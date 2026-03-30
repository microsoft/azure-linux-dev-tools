// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/fingerprint"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/spf13/cobra"
)

// Options for computing component identity fingerprints.
type IdentityComponentOptions struct {
	// Standard filter for selecting components.
	ComponentFilter components.ComponentFilter
}

func identityOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewComponentIdentityCommand())
}

// NewComponentIdentityCommand constructs a [cobra.Command] for "component identity" CLI subcommand.
func NewComponentIdentityCommand() *cobra.Command {
	options := &IdentityComponentOptions{}

	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Compute identity fingerprints for components",
		Long: `Compute a deterministic identity fingerprint for each selected component.

The fingerprint captures all resolved build inputs (config fields, spec file
content, overlay source files, distro context, and Affects commit count).
A change to any input produces a different fingerprint.

Use this with 'component diff-identity' to determine which components need
rebuilding between two commits.`,
		Example: `  # All components, JSON output for CI
  azldev component identity -a -O json > identity.json

  # Single component, table output for dev
  azldev component identity -p curl

  # Components in a group
  azldev component identity -g core`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(
				args, options.ComponentFilter.ComponentNamePatterns...,
			)

			return ComputeComponentIdentities(env, options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	return cmd
}

// ComponentIdentityResult is the per-component output for the identity command.
type ComponentIdentityResult struct {
	// Component is the component name.
	Component string `json:"component" table:",sortkey"`
	// Fingerprint is the overall identity hash string.
	Fingerprint string `json:"fingerprint"`
	// Inputs provides the individual input hashes (shown in JSON output).
	Inputs fingerprint.ComponentInputs `json:"inputs" table:"-"`
}

// ComputeComponentIdentities computes fingerprints for all selected components.
func ComputeComponentIdentities(
	env *azldev.Env, options *IdentityComponentOptions,
) ([]ComponentIdentityResult, error) {
	resolver := components.NewResolver(env)

	comps, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve components:\n%w", err)
	}

	distroRef := env.Config().Project.DefaultDistro

	// Resolve the distro definition (fills in default version for the fingerprint).
	distroRef, err = resolveDistroForIdentity(env, distroRef)
	if err != nil {
		slog.Debug("Could not resolve distro", "error", err)
	}

	return computeIdentitiesParallel(
		env, comps.Components(), distroRef,
	)
}

// maxConcurrentIdentity limits the number of concurrent identity computations.
// This bounds both git ls-remote calls and file I/O.
const maxConcurrentIdentity = 32

// computeIdentitiesParallel computes fingerprints for all components concurrently,
// including source identity resolution, affects count, and overlay file hashing.
func computeIdentitiesParallel(
	env *azldev.Env,
	comps []components.Component,
	distroRef projectconfig.DistroReference,
) ([]ComponentIdentityResult, error) {
	progressEvent := env.StartEvent("Computing component identities",
		"count", len(comps))
	defer progressEvent.End()

	// Create a cancellable child env so we can stop remaining goroutines on first error.
	workerEnv, cancel := env.WithCancel()
	defer cancel()

	type indexedResult struct {
		index  int
		result ComponentIdentityResult
		err    error
	}

	resultsChan := make(chan indexedResult, len(comps))
	semaphore := make(chan struct{}, maxConcurrentIdentity)

	var waitGroup sync.WaitGroup

	for compIdx, comp := range comps {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			// Context-aware semaphore acquisition.
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-workerEnv.Done():
				resultsChan <- indexedResult{index: compIdx, err: workerEnv.Err()}

				return
			}

			result, computeErr := computeSingleIdentity(
				workerEnv, comp, distroRef,
			)

			resultsChan <- indexedResult{index: compIdx, result: result, err: computeErr}
		}()
	}

	// Close channel when all goroutines complete.
	go func() { waitGroup.Wait(); close(resultsChan) }()

	// Collect results in order.
	results := make([]ComponentIdentityResult, len(comps))
	total := int64(len(comps))

	var (
		completed int64
		firstErr  error
	)

	for indexed := range resultsChan {
		if indexed.err != nil {
			if firstErr == nil {
				firstErr = indexed.err

				cancel()
			}

			// Drain remaining results so the closer goroutine can finish.
			continue
		}

		if firstErr == nil {
			results[indexed.index] = indexed.result
			completed++
			progressEvent.SetProgress(completed, total)
		}
	}

	if firstErr != nil {
		return nil, firstErr
	}

	return results, nil
}

// computeSingleIdentity computes the identity for a single component, including
// source identity resolution, affects commit counting, and overlay file hashing.
func computeSingleIdentity(
	env *azldev.Env,
	comp components.Component,
	distroRef projectconfig.DistroReference,
) (ComponentIdentityResult, error) {
	config := comp.GetConfig()
	componentName := comp.GetName()

	identityOpts := fingerprint.IdentityOptions{
		AffectsCommitCount: countAffectsCommits(config, componentName),
	}

	// Resolve source identity, selecting the appropriate method based on source type (local vs. upstream etc.).
	sourceIdentity, err := resolveSourceIdentityForComponent(env, comp)
	if err != nil {
		return ComponentIdentityResult{}, fmt.Errorf(
			"source identity resolution failed for %#q:\n%w",
			componentName, err)
	}

	identityOpts.SourceIdentity = sourceIdentity

	identity, err := fingerprint.ComputeIdentity(env.FS(), *config, distroRef, identityOpts)
	if err != nil {
		return ComponentIdentityResult{}, fmt.Errorf("computing identity for component %#q:\n%w",
			componentName, err)
	}

	return ComponentIdentityResult{
		Component:   componentName,
		Fingerprint: identity.Fingerprint,
		Inputs:      identity.Inputs,
	}, nil
}

// resolveDistroForIdentity resolves the default distro reference, filling in the
// default version when unspecified.
func resolveDistroForIdentity(
	env *azldev.Env, distroRef projectconfig.DistroReference,
) (projectconfig.DistroReference, error) {
	distroDef, _, err := env.ResolveDistroRef(distroRef)
	if err != nil {
		return distroRef,
			fmt.Errorf("resolving distro %#q:\n%w", distroRef.Name, err)
	}

	// Fill in the resolved version if the ref didn't specify one.
	if distroRef.Version == "" {
		distroRef.Version = distroDef.DefaultVersion
	}

	return distroRef, nil
}

// countAffectsCommits counts the number of "Affects: <componentName>" commits in the
// project repo. Returns 0 if the count cannot be determined (e.g., no git repo).
func countAffectsCommits(config *projectconfig.ComponentConfig, componentName string,
) int {
	configFile := config.SourceConfigFile
	if configFile == nil || configFile.SourcePath() == "" {
		return 0
	}

	repo, err := sources.OpenProjectRepo(configFile.SourcePath())
	if err != nil {
		slog.Debug("Could not open project repo for Affects commits; defaulting to 0",
			"component", componentName, "error", err)

		return 0
	}

	commits, err := sources.FindAffectsCommits(repo, componentName)
	if err != nil {
		slog.Debug("Could not count Affects commits; defaulting to 0",
			"component", componentName, "error", err)

		return 0
	}

	return len(commits)
}

// resolveSourceIdentityForComponent returns a deterministic identity string for the
// component's source by delegating to [sourceproviders.SourceManager.ResolveSourceIdentity].
func resolveSourceIdentityForComponent(
	env *azldev.Env, comp components.Component,
) (string, error) {
	distro, err := sourceproviders.ResolveDistro(env, comp)
	if err != nil {
		return "", fmt.Errorf("resolving distro for component %#q:\n%w",
			comp.GetName(), err)
	}

	// A new source manager is created per component because each may reference a different
	// upstream distro.
	srcManager, err := sourceproviders.NewSourceManager(env, distro)
	if err != nil {
		return "", fmt.Errorf("creating source manager for component %#q:\n%w",
			comp.GetName(), err)
	}

	identity, err := srcManager.ResolveSourceIdentity(env.Context(), comp)
	if err != nil {
		return "", fmt.Errorf("resolving source identity for %#q:\n%w",
			comp.GetName(), err)
	}

	return identity, nil
}
