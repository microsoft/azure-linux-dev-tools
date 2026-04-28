// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components

import (
	"os"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// Describes a filter that selects a subset of components known within the environment's
// loaded configuration.
type ComponentFilter struct {
	// Exact names to match against component groups. Does not support patterns or wildcards.
	ComponentGroupNames []string
	// Patterns to match against components; if no wildcards are present in the patterns, then an exact match
	// is required, otherwise lack of matches is still success.
	ComponentNamePatterns []string
	// Paths to individual spec files. Does not support patterns or wild-carding.
	SpecPaths []string
	// If true, then *all* known components are included in the result set.
	IncludeAllComponents bool
	// SkipLockValidation disables lock file consistency checks for this
	// filter's resolution. Commands that write lock files (update) or are
	// read-only (list) set this to true. The '--skip-lock-validation' flag
	// defaults based on AZLDEV_ENABLE_LOCK_VALIDATION during rollout.
	//nolint:godox // tracked by TODO(lockfiles) tag.
	// TODO(lockfiles): remove env var gate; default to false (validation on).
	SkipLockValidation bool
}

// HasNoCriteria returns true if the filter has no criteria set, meaning that it will never
// match any components.
func (f ComponentFilter) HasNoCriteria() bool {
	return len(f.ComponentGroupNames) == 0 &&
		len(f.ComponentNamePatterns) == 0 &&
		len(f.SpecPaths) == 0 &&
		!f.IncludeAllComponents
}

// Adds to the given command a standardized set of command options for selecting components
// from the loaded configuration. This allows consistency across the commands that operate on
// components in some form. Those commands that only support operating on a fixed number
// of components (say, 1) can further extend the logic by adding their own checks.
func AddComponentFilterOptionsToCommand(cmd *cobra.Command, filter *ComponentFilter) {
	cmd.Flags().BoolVarP(&filter.IncludeAllComponents, "all-components", "a", false, "Include all components")

	cmd.Flags().StringArrayVarP(&filter.ComponentGroupNames, "component-group", "g", []string{}, "Component group name")
	_ = cmd.RegisterFlagCompletionFunc("component-group", GenerateComponentGroupNameCompletions)

	cmd.Flags().StringArrayVarP(&filter.ComponentNamePatterns, "component", "p", []string{}, "Component name pattern")
	_ = cmd.RegisterFlagCompletionFunc("component", GenerateComponentNameCompletions)

	cmd.Flags().StringArrayVarP(&filter.SpecPaths, "spec-path", "s", []string{}, "Spec path")
	_ = cmd.MarkFlagFilename("spec-path", ".spec")

	cmd.Flags().BoolVar(&filter.SkipLockValidation, "skip-lock-validation",
		os.Getenv("AZLDEV_ENABLE_LOCK_VALIDATION") != "1",
		"skip lock file consistency checks")
}

// Function suitable for use as a [cobra.ValidArgsFunction] in a [cobra.Command]. Intended for use
// in generating completions for commands that take component names as positional arguments.
func GenerateComponentNameCompletions(
	cmd *cobra.Command, args []string, toComplete string,
) (completions []string, directive cobra.ShellCompDirective) {
	// Default to returning an error.
	directive = cobra.ShellCompDirectiveError

	env, err := azldev.GetEnvFromCommand(cmd)
	if err != nil {
		return completions, directive
	}

	resolver := NewResolver(env)

	components, err := resolver.FindComponentsByNamePattern(toComplete + "*")
	if err != nil {
		return completions, directive
	}

	return components.Names(), cobra.ShellCompDirectiveNoFileComp
}

// Function suitable for use as a [cobra.ValidArgsFunction] in a [cobra.Command]. Intended for use
// in generating completions for commands that take component group names as positional arguments.
func GenerateComponentGroupNameCompletions(
	cmd *cobra.Command, args []string, toComplete string,
) (completions []string, directive cobra.ShellCompDirective) {
	// Default to returning an error.
	directive = cobra.ShellCompDirectiveError

	env, err := azldev.GetEnvFromCommand(cmd)
	if err != nil {
		return completions, directive
	}

	cfg := env.Config()
	if cfg == nil {
		return completions, directive
	}

	for groupName := range cfg.ComponentGroups {
		if toComplete == "" || strings.HasPrefix(groupName, toComplete) {
			completions = append(completions, groupName)
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}
