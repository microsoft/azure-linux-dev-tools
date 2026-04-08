// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/spf13/cobra"
)

// Options for listing components within the environment.
type ListComponentOptions struct {
	// Standard filter for selecting components.
	ComponentFilter components.ComponentFilter
}

// ComponentListEntry wraps a [projectconfig.ComponentConfig] with additional computed fields
// for the component list output.
type ComponentListEntry struct {
	projectconfig.ComponentConfig

	// RenderedSpecDir is the output directory for this component's rendered spec files.
	// Empty when rendered-specs-dir is not configured in the project.
	RenderedSpecDir string `json:"renderedSpecDir,omitempty" table:"Rendered Spec Dir"`
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

	return cmd
}

// Lists components in the env, in accordance with options. Returns the found components.
func ListComponentConfigs(
	env *azldev.Env, options *ListComponentOptions,
) (results []ComponentListEntry, err error) {
	var comps *components.ComponentSet

	resolver := components.NewResolver(env)

	comps, err = resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return results, fmt.Errorf("failed to resolve components:\n%w", err)
	}

	renderedSpecsDir := env.Config().Project.RenderedSpecsDir

	// Extract the component configs from the resolved components, compute the rendered spec
	// directory for each, and return them in a slice.
	entries := make([]ComponentListEntry, 0, comps.Len())
	for _, comp := range comps.Components() {
		entries = append(entries, ComponentListEntry{
			ComponentConfig: *comp.GetConfig(),
			RenderedSpecDir: components.RenderedSpecDir(renderedSpecsDir, comp.GetName()),
		})
	}

	return entries, nil
}
