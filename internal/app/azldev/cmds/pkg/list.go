// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package pkg

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/spf13/cobra"
)

// ListPackageOptions controls which packages are enumerated by [ListPackages].
type ListPackageOptions struct {
	// All selects all packages that appear in any package-group or component package override.
	All bool

	// PackageNames contains specific binary package names to look up.
	// If a package is not in any explicit config it is still resolved using project defaults.
	PackageNames []string
}

func listOnAppInit(_ *azldev.App, parent *cobra.Command) {
	parent.AddCommand(NewPackageListCommand())
}

// NewPackageListCommand constructs the [cobra.Command] for "package list".
func NewPackageListCommand() *cobra.Command {
	options := &ListPackageOptions{}

	cmd := &cobra.Command{
		Use:   "list [package-name...]",
		Short: "List resolved configuration for binary packages",
		Long: `List resolved configuration for binary packages.

Use -a to enumerate all packages that have explicit configuration (via
package-groups or component package overrides). Use -p (or positional args)
to look up one or more specific packages by exact name — including packages
that are not explicitly configured (they resolve using only project defaults).

Resolution order (lowest to highest priority):
  1. Project default-package-config
  2. Package group default-package-config
  3. Component default-package-config
  4. Component packages.<name> override`,
		Example: `  # List all explicitly-configured packages
  azldev package list -a

  # Look up a specific package
  azldev package list -p curl

  # Look up multiple packages
  azldev package list -p curl -p wget

  # Output as JSON for scripting
  azldev package list -a -q -O json`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.PackageNames = append(args, options.PackageNames...)

			return ListPackages(env, options)
		}),
	}

	cmd.Flags().BoolVarP(&options.All, "all-packages", "a", false, "List all explicitly-configured binary packages")
	cmd.Flags().StringArrayVarP(&options.PackageNames, "package", "p", []string{}, "Package name to look up (repeatable)")

	azldev.ExportAsMCPTool(cmd)

	return cmd
}

// PackageListResult holds the resolved configuration for a single binary package.
type PackageListResult struct {
	// PackageName is the binary package name (RPM Name tag).
	PackageName string `json:"packageName" table:"Package"`

	// Group is the package-group this package belongs to, or empty if it is not in any group.
	Group string `json:"group" table:"Group"`

	// Component is the component that has an explicit per-package override for this package,
	// or empty if the package is only configured via a group or project default.
	Component string `json:"component" table:"Component"`

	// Channel is the resolved publish channel after applying all config layers.
	// Empty means no channel has been configured.
	Channel string `json:"publishChannel" table:"Publish Channel"`
}

// buildComponentPackageIndex builds a map from binary package name to the component
// that declares an explicit override for it in its [projectconfig.ComponentConfig.Packages] map.
// Returns an error if the same package name appears in more than one component, which would
// otherwise make the resolved configuration nondeterministic.
func buildComponentPackageIndex(components map[string]projectconfig.ComponentConfig) (map[string]string, error) {
	compOf := make(map[string]string)

	for compName, comp := range components {
		for pkg := range comp.Packages {
			if existingComp, exists := compOf[pkg]; exists && existingComp != compName {
				return nil, fmt.Errorf(
					"package %#q has component overrides in multiple components: %#+q and %#+q",
					pkg, existingComp, compName,
				)
			}

			compOf[pkg] = compName
		}
	}

	return compOf, nil
}

// ListPackages returns the resolved [PackageListResult] for the packages selected by options.
//
// If [ListPackageOptions.All] is true, all packages with explicit configuration (via
// package groups or component [projectconfig.ComponentConfig.Packages] maps) are included.
// Specific package names in [ListPackageOptions.PackageNames] are always included regardless
// of whether they have explicit configuration.
func ListPackages(env *azldev.Env, options *ListPackageOptions) ([]PackageListResult, error) {
	proj := env.Config()

	// Build an index: pkgName → groupName for packages in any package-group.
	groupOf := make(map[string]string)

	for groupName, group := range proj.PackageGroups {
		for _, pkg := range group.Packages {
			groupOf[pkg] = groupName
		}
	}

	// Build an index: pkgName → componentName for packages with explicit component overrides.
	compOf, err := buildComponentPackageIndex(proj.Components)
	if err != nil {
		return nil, err
	}

	// Collect the set of package names to resolve.
	toResolve := make(map[string]struct{})

	if options.All {
		for pkg := range groupOf {
			toResolve[pkg] = struct{}{}
		}

		for pkg := range compOf {
			toResolve[pkg] = struct{}{}
		}
	}

	for _, name := range options.PackageNames {
		toResolve[name] = struct{}{}
	}

	if len(toResolve) == 0 {
		slog.Warn("No package selection options were given, no packages will be listed.")

		return nil, nil
	}

	results := make([]PackageListResult, 0, len(toResolve))

	for pkgName := range toResolve {
		// Resolve using the component's config if one is known; otherwise use an empty config
		// so that only the project default and group layers are applied.
		compName := compOf[pkgName]
		compConfig := &projectconfig.ComponentConfig{}

		if compName != "" {
			if c, ok := proj.Components[compName]; ok {
				compConfig = &c
			}
		}

		pkgConfig, err := projectconfig.ResolvePackageConfig(pkgName, compConfig, proj)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve config for package %#q:\n%w", pkgName, err)
		}

		results = append(results, PackageListResult{
			PackageName: pkgName,
			Group:       groupOf[pkgName],
			Component:   compName,
			Channel:     pkgConfig.Publish.Channel,
		})
	}

	// Sort by package name for deterministic, readable output.
	sort.Slice(results, func(i, j int) bool {
		return results[i].PackageName < results[j].PackageName
	})

	return results, nil
}
