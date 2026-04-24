// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package pkg

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

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

	// SynthesizeDebugPackages, when true, augments the result list with synthetic
	// '-debuginfo' packages (one per reported package, using a parallel publish
	// channel derived from the original package's publish channel by appending
	// '-debuginfo', except when the original channel is "" or "none") and synthetic
	// '-debugsource' packages (one per component in the project configuration,
	// using the component's resolved publish channel after applying the same
	// debug-channel derivation logic).
	SynthesizeDebugPackages bool
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
  1. Project default-component-config publish settings
  2. Component-group default-component-config publish settings
  3. Component publish settings
  4. Package-group default-package-config
  5. Component packages.<name> override`,
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
	cmd.Flags().BoolVar(&options.SynthesizeDebugPackages, "synthesize-debug-packages", false,
		"Also synthesize '-debuginfo' packages (per reported package) and '-debugsource' packages (per component)")

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
		result, err := resolvePackageListResult(pkgName, compOf, groupOf, proj)
		if err != nil {
			return nil, err
		}

		results = append(results, result)
	}

	if options.SynthesizeDebugPackages {
		slog.Warn("'--synthesize-debug-packages' is a transitional flag and may change or be removed " +
			"once first-class debug-package configuration is supported.")

		results, err = synthesizeDebugPackages(results, proj)
		if err != nil {
			return nil, err
		}
	}

	// Sort by package name for deterministic, readable output.
	sort.Slice(results, func(i, j int) bool {
		return results[i].PackageName < results[j].PackageName
	})

	return results, nil
}

// resolvePackageListResult resolves the publish channel and component membership for a single
// package and returns a [PackageListResult].
func resolvePackageListResult(
	pkgName string,
	compOf map[string]string,
	groupOf map[string]string,
	proj *projectconfig.ProjectConfig,
) (PackageListResult, error) {
	compName := resolveComponentName(pkgName, compOf, proj)
	compConfig := resolveComponentConfig(compName, proj)

	// Apply inherited defaults (project → groups → component) so that
	// compConfig.Publish reflects the fully-merged publish channels.
	// No distro context is available here (pkg list operates on the raw project config),
	// so distro defaults are omitted.
	resolved, err := projectconfig.ResolveComponentConfig(
		compConfig,
		proj.DefaultComponentConfig,
		projectconfig.ComponentConfig{}, // no distro context at this call site
		proj.ComponentGroups,
		proj.GroupsByComponent[compName],
	)
	if err != nil {
		return PackageListResult{}, fmt.Errorf("failed to resolve defaults for component %#q:\n%w", compName, err)
	}

	channel, err := projectconfig.ResolvePackagePublishChannel(pkgName, &resolved, proj)
	if err != nil {
		return PackageListResult{}, fmt.Errorf("failed to resolve publish channel for package %#q:\n%w", pkgName, err)
	}

	return PackageListResult{
		PackageName: pkgName,
		Group:       groupOf[pkgName],
		Component:   compName,
		Channel:     channel,
	}, nil
}

// resolveComponentName returns the component name for a binary package. It first
// checks for an explicit per-package override in compOf, then falls back to
// treating the package name itself as a component name when a matching component
// or component-group member exists.
func resolveComponentName(
	pkgName string,
	compOf map[string]string,
	proj *projectconfig.ProjectConfig,
) string {
	if name := compOf[pkgName]; name != "" {
		return name
	}

	if _, ok := proj.Components[pkgName]; ok {
		return pkgName
	}

	if _, ok := proj.GroupsByComponent[pkgName]; ok {
		return pkgName
	}

	return ""
}

// resolveComponentConfig returns the [projectconfig.ComponentConfig] for a named
// component. If the component has an explicit entry in [projectconfig.ProjectConfig.Components],
// that entry is returned; otherwise a minimal config with just the Name set is returned so that
// [projectconfig.ResolveComponentConfig] can still look up group membership.
func resolveComponentConfig(compName string, proj *projectconfig.ProjectConfig) projectconfig.ComponentConfig {
	if compName == "" {
		return projectconfig.ComponentConfig{}
	}

	if c, ok := proj.Components[compName]; ok {
		return c
	}

	return projectconfig.ComponentConfig{Name: compName}
}

// synthesizeDebugPackages augments results with synthetic '-debuginfo' packages (one per
// already-resolved package, using a parallel publish channel derived from the original
// package's publish channel by appending '-debuginfo', except when the original channel is
// "" or "none") and '-debugsource' packages (one per component in the project, with the
// publish channel resolved through the normal component → project default chain).
//
// Note: '-debugsource' entries are emitted for every component in the project regardless of
// which packages were requested via '-p'. Components own packages via [ComponentConfig.Packages],
// but most listed packages come from package groups with no component association — so scoping
// debugsource emission to the requested package set cannot be done reliably with the current
// configuration model.
//
// Synthetic entries that collide with an already-present (real) package name are skipped so
// real configuration always wins. Source packages whose names already end in '-debuginfo' or
// '-debugsource' do not get a doubled suffix synthesized.
func synthesizeDebugPackages(
	results []PackageListResult, proj *projectconfig.ProjectConfig,
) ([]PackageListResult, error) {
	existing := make(map[string]struct{}, len(results))
	for _, result := range results {
		existing[result.PackageName] = struct{}{}
	}

	// One '-debuginfo' per originally-reported package, sharing its publish channel and
	// group/component attribution.
	debugInfoEntries := make([]PackageListResult, 0, len(results))

	for _, result := range results {
		if isDebugPackageName(result.PackageName) {
			continue
		}

		name := result.PackageName + "-debuginfo"
		if _, exists := existing[name]; exists {
			continue
		}

		existing[name] = struct{}{}
		debugInfoEntries = append(debugInfoEntries, PackageListResult{
			PackageName: name,
			Group:       result.Group,
			Component:   result.Component,
			Channel:     debugChannelName(result.Channel),
		})
	}

	results = append(results, debugInfoEntries...)

	// One '-debugsource' per component. Resolve the synthesized package using the
	// standard package-resolution chain for '<component>-debugsource' so the entry
	// reflects any explicit package override, package-group defaults, component
	// settings, or project defaults instead of an implicit empty channel.
	for compName, comp := range proj.Components {
		if isDebugPackageName(compName) {
			continue
		}

		name := compName + "-debugsource"
		if _, exists := existing[name]; exists {
			continue
		}

		existing[name] = struct{}{}

		compCopy := comp

		pkgConfig, err := projectconfig.ResolvePackageConfig(name, &compCopy, proj)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve config for synthesized package %#q:\n%w", name, err)
		}

		results = append(results, PackageListResult{
			PackageName: name,
			Component:   compName,
			Channel:     debugChannelName(pkgConfig.Publish.EffectiveRPMChannel()),
		})
	}

	return results, nil
}

// debugChannelName returns the publish-channel name to use for a synthesized debug package.
// Real (non-empty, non-"none") channels are suffixed with '-debuginfo' so debug artifacts are
// published to a parallel channel; "" and 'none' are passed through unchanged because they
// represent "default" and "do not publish" respectively.
func debugChannelName(channel string) string {
	if channel == "" || channel == "none" || strings.HasSuffix(channel, "-debuginfo") {
		return channel
	}

	return channel + "-debuginfo"
}

// isDebugPackageName reports whether name already has a '-debuginfo' or '-debugsource' suffix,
// so the caller can avoid synthesizing doubled-suffix names like 'foo-debuginfo-debuginfo'.
func isDebugPackageName(name string) bool {
	return strings.HasSuffix(name, "-debuginfo") || strings.HasSuffix(name, "-debugsource")
}
