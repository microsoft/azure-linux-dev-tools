// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
)

// Options for querying components from the environment.
type QueryComponentsOptions struct {
	// Standard filter for selecting components.
	ComponentFilter components.ComponentFilter
}

func queryOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewComponentQueryCommand())
}

// Constructs a [cobra.Command] for "component query" CLI subcommand.
func NewComponentQueryCommand() *cobra.Command {
	options := &QueryComponentsOptions{}

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query info from locally rendered component specs",
		Long: `Query detailed information for components from their locally rendered specs.

This command reads the post-overlay specs from the project's rendered-specs-dir
(produced by 'azldev component render') and runs rpmspec against them in a
single shared mock chroot, batching all specs into one chroot invocation with
parallel per-spec processing. For each component, it reports the source NEVR
and the list of binary subpackages the spec would produce when built.

The rendered-specs-dir must exist on disk; if it doesn't, run
'azldev component render' first. Components that previously failed to render
(those with a RENDER_FAILED marker file) are skipped with a warning.`,
		Example: `  # Query a single component
  azldev component query -p curl

  # Query with JSON output
  azldev component query -p bash -q -O json`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

			return QueryComponents(env, options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	return cmd
}

// componentDetails encapsulates detailed information about a component.
type componentDetails struct {
	specs.ComponentSpecDetails
}

// QueryComponents queries info for selected components by reading the locally
// rendered specs and running rpmspec against them in a single shared mock
// chroot. Returns one entry per successfully queried component, in the order
// returned by the resolver. Components with a RENDER_FAILED marker are
// skipped with a loud warning. Per-component rpmspec failures are surfaced
// as warnings; the corresponding entry is omitted from the result list and
// the function returns an aggregated error after attempting every component.
//
//nolint:cyclop,funlen // Linear pipeline; further splitting hurts readability.
func QueryComponents(
	env *azldev.Env, options *QueryComponentsOptions,
) ([]*componentDetails, error) {
	renderedSpecsDir := env.Config().Project.RenderedSpecsDir
	if renderedSpecsDir == "" {
		return nil, errors.New(
			"project.rendered-specs-dir is not configured; " +
				"set it in the project config and run 'azldev component render' first")
	}

	dirExists, err := fileutils.DirExists(env.FS(), renderedSpecsDir)
	if err != nil {
		return nil, fmt.Errorf("checking rendered-specs-dir %#q:\n%w", renderedSpecsDir, err)
	}

	if !dirExists {
		return nil, fmt.Errorf(
			"rendered-specs-dir %#q does not exist; run 'azldev component render' first",
			renderedSpecsDir)
	}

	resolver := components.NewResolver(env)

	comps, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve components:\n%w", err)
	}

	if comps.Len() == 0 {
		return nil, errors.New("no components were selected; " +
			"please use command-line options to indicate which components to query")
	}

	inputs, skipped, err := buildSpecQueryInputs(env, comps.Components(), renderedSpecsDir)
	if err != nil {
		return nil, err
	}

	if len(inputs) == 0 {
		return nil, fmt.Errorf("no components have a rendered spec on disk; skipped %d", skipped)
	}

	mockProcessor := createMockProcessor(env, mockPackagesForQuery())
	if mockProcessor == nil {
		return nil, errors.New(
			"mock config required for querying; ensure the project has a valid distro with mock config")
	}

	defer mockProcessor.Destroy(env)

	if err := env.FS().MkdirAll(env.WorkDir(), fileperms.PublicDir); err != nil {
		return nil, fmt.Errorf("creating work directory:\n%w", err)
	}

	scratchDir, err := fileutils.MkdirTemp(env.FS(), env.WorkDir(), "azldev-query-scratch-")
	if err != nil {
		return nil, fmt.Errorf("creating scratch directory:\n%w", err)
	}

	defer func() {
		if removeErr := env.FS().RemoveAll(scratchDir); removeErr != nil {
			slog.Debug("Failed to clean up scratch directory", "path", scratchDir, "error", removeErr)
		}
	}()

	queryResults, err := mockProcessor.BatchQuerySpecs(
		env, env, renderedSpecsDir, scratchDir, inputs, env.FS(), env.CPUBoundConcurrency(),
	)
	if err != nil {
		return nil, fmt.Errorf("batch-querying rendered specs:\n%w", err)
	}

	allDetails := make([]*componentDetails, 0, len(queryResults))

	var failed int

	for _, queryResult := range queryResults {
		if queryResult.Error != nil {
			slog.Warn("Failed to query rendered spec",
				"component", queryResult.Name, "error", queryResult.Error)

			failed++

			continue
		}

		allDetails = append(allDetails, &componentDetails{
			ComponentSpecDetails: specs.ComponentSpecDetails{
				SpecInfo: *queryResult.Info,
			},
		})
	}

	if failed > 0 {
		return allDetails, fmt.Errorf("%d component(s) failed to query (see warnings)", failed)
	}

	return allDetails, nil
}

// buildSpecQueryInputs walks the resolved components and constructs the list
// of [sources.SpecQueryInput] entries to pass to BatchQuerySpecs. Components
// whose rendered spec directory carries a RENDER_FAILED marker (or whose
// rendered .spec file is missing) are skipped with a loud warning and counted
// toward `skipped`.
func buildSpecQueryInputs(
	env *azldev.Env,
	componentList []components.Component,
	renderedSpecsDir string,
) (inputs []sources.SpecQueryInput, skipped int, err error) {
	inputs = make([]sources.SpecQueryInput, 0, len(componentList))

	for _, comp := range componentList {
		name := comp.GetName()
		cfg := comp.GetConfig()

		if cfg.RenderedSpecDir == "" {
			return nil, 0, fmt.Errorf(
				"component %#q has no rendered-spec dir; ensure project.rendered-specs-dir is set",
				name)
		}

		if hasMarker, markerErr := hasRenderFailedMarker(env, cfg.RenderedSpecDir); markerErr != nil {
			return nil, 0, fmt.Errorf("checking RENDER_FAILED marker for %#q:\n%w", name, markerErr)
		} else if hasMarker {
			slog.Warn(
				"Skipping component: RENDER_FAILED marker present; run 'azldev component render' to refresh",
				"component", name, "dir", cfg.RenderedSpecDir)

			skipped++

			continue
		}

		specPath := filepath.Join(cfg.RenderedSpecDir, name+".spec")

		specExists, statErr := fileutils.Exists(env.FS(), specPath)
		if statErr != nil {
			return nil, 0, fmt.Errorf("checking rendered spec %#q:\n%w", specPath, statErr)
		}

		if !specExists {
			slog.Warn(
				"Skipping component: rendered spec not found; run 'azldev component render' to produce it",
				"component", name, "expectedSpec", specPath)

			skipped++

			continue
		}

		relSpecPath, relErr := filepath.Rel(renderedSpecsDir, specPath)
		if relErr != nil {
			return nil, 0, fmt.Errorf("relativizing spec path %#q against %#q:\n%w",
				specPath, renderedSpecsDir, relErr)
		}

		inputs = append(inputs, sources.SpecQueryInput{
			Name:        name,
			SpecRelPath: relSpecPath,
			With:        cfg.Build.With,
			Without:     cfg.Build.Without,
			Defines:     cfg.Build.Defines,
		})
	}

	return inputs, skipped, nil
}

// hasRenderFailedMarker reports whether the given rendered-spec dir carries
// the marker file written by 'component render' on failure.
func hasRenderFailedMarker(env *azldev.Env, renderedSpecDir string) (bool, error) {
	markerPath := filepath.Join(renderedSpecDir, renderErrorMarkerFile)

	exists, err := fileutils.Exists(env.FS(), markerPath)
	if err != nil {
		return false, fmt.Errorf("checking %#q:\n%w", markerPath, err)
	}

	return exists, nil
}
