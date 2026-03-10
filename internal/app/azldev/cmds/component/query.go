// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs"
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
		spec := comp.GetSpec()

		specInfo, err := spec.Parse()
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
