// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
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
		Short: "Query info for components in this project",
		Long: `Query detailed information for components by fetching and parsing their spec files.

Unlike 'list', which only shows configuration metadata, 'query' resolves
upstream sources and parses the RPM spec to report version, release,
subpackages, dependencies, and other spec-level details. This makes it
slower than 'list' but more informative.`,
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

type queryComponent struct {
	components.Component
	config projectconfig.ComponentConfig
}

func (c *queryComponent) GetConfig() *projectconfig.ComponentConfig {
	return &c.config
}

// Queries env for component details, in accordance with options. Returns the found components.
func QueryComponents(
	env *azldev.Env, options *QueryComponentsOptions,
) (results []*componentDetails, err error) {
	var comps *components.ComponentSet

	resolver := components.NewResolver(env)

	comps, err = resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return results, fmt.Errorf("failed to resolve components:\n%w", err)
	}

	allDetails := make([]*componentDetails, 0, comps.Len())

	for _, comp := range comps.Components() {
		specInfo, err := parseComponentSpec(env, comp)
		if err != nil {
			return nil, fmt.Errorf("failed to parse spec for component %q:\n%w", comp.GetName(), err)
		}

		details := &componentDetails{
			ComponentSpecDetails: *specInfo,
		}

		allDetails = append(allDetails, details)
	}

	return allDetails, nil
}

func parseComponentSpec(env *azldev.Env, comp components.Component) (*specs.ComponentSpecDetails, error) {
	if comp.GetConfig().Spec.SourceType == projectconfig.SpecSourceTypeLocal {
		return comp.GetSpec().Parse()
	}

	componentForPrep := comp
	if comp.GetConfig().Spec.SourceType == projectconfig.SpecSourceTypeUnspecified {
		normalizedConfig := *comp.GetConfig()
		normalizedConfig.Spec.SourceType = projectconfig.SpecSourceTypeUpstream
		componentForPrep = &queryComponent{
			Component: comp,
			config:    normalizedConfig,
		}
	}

	distro, err := sourceproviders.ResolveDistro(env, componentForPrep)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve distro for component %q:\n%w", comp.GetName(), err)
	}

	sourceManager, err := sourceproviders.NewSourceManager(env, distro)
	if err != nil {
		return nil, fmt.Errorf("failed to create source manager:\n%w", err)
	}

	preparer, err := sources.NewPreparer(sourceManager, env.FS(), env, env, sources.WithSkipLookaside())
	if err != nil {
		return nil, fmt.Errorf("failed to create source preparer:\n%w", err)
	}

	workDirFactory, err := workdir.NewFactory(env.FS(), env.WorkDir(), env.ConstructionTime())
	if err != nil {
		return nil, fmt.Errorf("failed to create work dir factory:\n%w", err)
	}

	preparedSourcesDir, err := workDirFactory.Create(comp.GetName(), "query-spec")
	if err != nil {
		return nil, fmt.Errorf("failed to create work dir for component %#q:\n%w", comp.GetName(), err)
	}

	if err := preparer.PrepareSources(env, componentForPrep, preparedSourcesDir, true /* applyOverlays */); err != nil {
		return nil, fmt.Errorf("failed to prepare sources for component %#q:\n%w", comp.GetName(), err)
	}

	preparedConfig := *componentForPrep.GetConfig()
	preparedConfig.Spec.SourceType = projectconfig.SpecSourceTypeLocal
	preparedConfig.Spec.Path = filepath.Join(preparedSourcesDir, comp.GetName()+".spec")

	return specs.NewSpec(env, preparedConfig).Parse()
}
