// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
)

// Options for listing components within the environment.
type ListComponentOptions struct {
	// Standard filter for selecting components.
	ComponentFilter components.ComponentFilter
	// SkipLockPopulation controls whether lock files are read during resolution.
	// Only applicable when component list is explicitly requested by the user.
	SkipLockPopulation bool
}

func listOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewComponentListCommand())
}

// Constructs a [cobra.Command] for "component list" CLI subcommand.
func NewComponentListCommand() *cobra.Command {
	options := &ListComponentOptions{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List components in this project",
		Long: `List components defined in the project configuration.

By default only matching components are shown. Use -a to list all components.
Component name patterns support glob syntax (*, ?, []).`,
		Example: `  # List all components
  azldev component list -a

  # List a specific component
  azldev component list -p curl

  # List components matching a pattern
  azldev component list -p "azure*"

  # Output as JSON for scripting
  azldev component list -a -q -O json`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

			return ListComponentConfigs(env, options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	azldev.ExportAsMCPTool(cmd)

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	// Add skip-lock-population flag for list command
	cmd.Flags().BoolVar(&options.SkipLockPopulation, "skip-lock-population",
		true,
		"skip lock file population (default: true; set to false to read lock data)")

	return cmd
}

// ListComponentConfigs lists components in the env, in accordance with options.
// Lock validation is always skipped since list is read-only.
func ListComponentConfigs(
	env *azldev.Env, options *ListComponentOptions,
) (results []projectconfig.ComponentConfig, err error) {
	var comps *components.ComponentSet

	// List is read-only — always skip lock validation.
	// Determine lock mode based on user preference for population.
	if options.SkipLockPopulation {
		options.ComponentFilter.LockMode = components.LockModeSkipBoth
	} else {
		options.ComponentFilter.LockMode = components.LockModeSkipValidationPopulate
	}

	resolver := components.NewResolver(env)
	// Set the resolver's SkipLockPopulation to match the filter's LockMode
	resolver.SkipLockPopulation = options.SkipLockPopulation

	comps, err = resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return results, fmt.Errorf("failed to resolve components:\n%w", err)
	}

	// Extract the component configs from the resolved components, and return them in a slice.
	return lo.Map(comps.Components(), func(component components.Component, _ int) projectconfig.ComponentConfig {
		return *component.GetConfig()
	}), nil
}
