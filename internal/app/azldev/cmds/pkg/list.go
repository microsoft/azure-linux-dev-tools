// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package pkg

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
)

// ListPackageOptions controls which packages are enumerated by [ListPackages].
type ListPackageOptions struct {
	// All selects all packages that appear in any package-group or component package override.
	All bool

	// RPMFile is the path to a JSON RPM source map file (array of {packageName, sourcePackageName}
	// records). When set, all source packages (SRPMs) and their binary RPMs are resolved and listed.
	RPMFile string

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
		Short: "List resolved configuration for packages (RPMs and SRPMs)",
		Long: `List resolved configuration for packages (RPMs and SRPMs).

Use -a to enumerate all packages that have explicit configuration (via package-groups
or component package overrides).

Use --rpm-file <file> to enumerate all source packages (SRPMs) and their binary RPMs
from a JSON RPM source map file (an array of {"packageName":"bash","sourcePackageName":"bash"} records).
Each SRPM is resolved against the component with the same name; each binary RPM is
resolved using the full publish-channel stack. Results include a 'type' column
("srpm" or "rpm") to distinguish the two.

Use -p (or positional args) to look up one or more specific packages by exact name —
including packages that are not explicitly configured (they resolve using only project
defaults).

Resolution order (lowest to highest priority):
  1. Project default-component-config publish settings
  2. Component-group default-component-config publish settings
  3. Component publish settings
  4. Package-group default-package-config
  5. Component packages.<name> override`,
		Example: `  # List all explicitly-configured packages
  azldev package list -a

  # List all packages from an RPM source map file
  azldev package list --rpm-file rpm_source_map.json

  # Look up a specific package
  azldev package list -p curl

  # Look up multiple packages
  azldev package list -p curl -p wget

  # Output as JSON for scripting
  azldev package list -a -q -O json
  azldev package list --rpm-file rpm_source_map.json -q -O json`,
		Args: func(cmd *cobra.Command, args []string) error {
			// Positional package names aren't flags, so they can't participate in cobra's
			// flag-group machinery; enforce the '--rpm-file' incompatibility here.
			if cmd.Flags().Changed("rpm-file") && len(args) > 0 {
				return errors.New("'--rpm-file' cannot be used with positional package name arguments")
			}

			return nil
		},
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.PackageNames = append(args, options.PackageNames...)

			return ListPackages(env, options)
		}),
	}

	cmd.Flags().BoolVarP(&options.All, "all-packages", "a", false, "List all explicitly-configured binary packages")
	cmd.Flags().StringVar(&options.RPMFile, "rpm-file", "",
		"Path to a JSON RPM source map file (lists all SRPMs and their binary RPMs)")
	cmd.Flags().StringArrayVarP(&options.PackageNames, "package", "p", []string{}, "Package name to look up (repeatable)")
	cmd.Flags().BoolVar(&options.SynthesizeDebugPackages, "synthesize-debug-packages", false,
		"Also synthesize '-debuginfo' packages (per reported package) and '-debugsource' packages (per component)")

	// '--rpm-file' is mutually exclusive with the other selection / augmentation flags.
	cmd.MarkFlagsMutuallyExclusive("rpm-file", "all-packages")
	cmd.MarkFlagsMutuallyExclusive("rpm-file", "package")
	cmd.MarkFlagsMutuallyExclusive("rpm-file", "synthesize-debug-packages")

	// Help shells complete '--rpm-file' with .json paths.
	_ = cmd.MarkFlagFilename("rpm-file", "json")

	azldev.ExportAsMCPTool(cmd)

	return cmd
}

// PackageListResult holds the resolved configuration for a single binary package or source package.
type PackageListResult struct {
	// PackageName is the RPM Name tag (binary or source package name).
	PackageName string `json:"packageName" table:"Package"`

	// Type is the package type: [PackageTypeSRPM] for source packages, [PackageTypeRPM] for binary
	// packages. Always [PackageTypeRPM] for '-a' and '-p' lookups; either value for '--rpm-file'.
	Type string `json:"type" table:"Type"`

	// Group is the package-group this package belongs to, or empty if it is not in any group.
	Group string `json:"group" table:"Group"`

	// Component is the resolved component name for this package.
	// When using '-a' or '-p', it is the component with an explicit [projectconfig.ComponentConfig.Packages]
	// override for this package, or the component whose name matches the package name, or empty if
	// no component association can be determined.
	Component string `json:"component" table:"Component"`

	// Channel is the resolved publish channel after applying all config layers.
	// Empty means no channel has been configured.
	Channel string `json:"publishChannel" table:"Publish Channel"`
}

const (
	// PackageTypeSRPM is the [PackageListResult.Type] value for source packages (SRPMs).
	PackageTypeSRPM = "srpm"

	// PackageTypeRPM is the [PackageListResult.Type] value for binary packages (RPMs).
	PackageTypeRPM = "rpm"
)

// rpmSourceEntry is one record in the RPM source map JSON file.
// The file is a JSON array of these objects, each mapping a binary package name to the
// source package (SRPM) name that produced it.
type rpmSourceEntry struct {
	PackageName       string `json:"packageName"`
	SourcePackageName string `json:"sourcePackageName"`
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

// listPackagesFromRPMFile implements the '--rpm-file' path of [ListPackages].
// Mutual exclusivity with '-a', '-p', '--synthesize-debug-packages', and positional
// package name arguments is enforced by cobra (see [NewPackageListCommand]).
func listPackagesFromRPMFile(
	env *azldev.Env,
	options *ListPackageOptions,
	groupOf map[string]string,
	proj *projectconfig.ProjectConfig,
) ([]PackageListResult, error) {
	results, err := resolveFromRPMFile(env.FS(), options.RPMFile, groupOf, proj)
	if err != nil {
		return nil, err
	}

	// Sort for deterministic output. [resolveFromRPMFile] iterates over a map,
	// so the order of results is non-deterministic without this sort. Use
	// additional tie-breakers so entries that share the same package name (for
	// example, an SRPM and an RPM from '--rpm-file') also have a stable order.
	sort.Slice(results, func(left, right int) bool {
		if results[left].PackageName != results[right].PackageName {
			return results[left].PackageName < results[right].PackageName
		}

		if results[left].Type != results[right].Type {
			return results[left].Type < results[right].Type
		}

		return results[left].Component < results[right].Component
	})

	return results, nil
}

// ListPackages returns the resolved [PackageListResult] for the packages selected by options.
//
// If [ListPackageOptions.All] is true, all packages with explicit configuration (via
// package groups or component [projectconfig.ComponentConfig.Packages] maps) are included.
//
// If [ListPackageOptions.RPMFile] is set, all source packages and their binary RPMs from
// the JSON file are resolved. Each SRPM uses [projectconfig.ComponentPublishConfig.SRPMChannel]
// from its matching component; each RPM uses the full package-level publish-channel stack with
// the JSON-derived component association.
//
// Specific package names in [ListPackageOptions.PackageNames] are always resolved regardless
// of whether they have explicit configuration.
func ListPackages(env *azldev.Env, options *ListPackageOptions) ([]PackageListResult, error) {
	proj := env.Config()

	// Build an index: pkgName → groupName for packages in any package-group.
	// Used by '-a', '--rpm-file', and '-p' modes for group attribution and channel resolution.
	groupOf := make(map[string]string)

	for groupName, group := range proj.PackageGroups {
		for _, pkg := range group.Packages {
			groupOf[pkg] = groupName
		}
	}

	results := make([]PackageListResult, 0)

	if options.RPMFile != "" {
		return listPackagesFromRPMFile(env, options, groupOf, proj)
	}

	// Build an index: pkgName → componentName for packages with explicit component overrides.
	// Needed for '-a' and '-p' lookups.
	compOf, err := buildComponentPackageIndex(proj.Components)
	if err != nil {
		return nil, err
	}

	// Collect the set of package names to resolve for '-a' and '-p' modes.
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
		Type:        PackageTypeRPM,
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

// resolveSourcePackageListResult resolves the publish channel for a source package (SRPM).
// The SRPM name is always equal to the component name, so no compOf lookup is needed.
// It reads [projectconfig.ComponentPublishConfig.SRPMChannel] from the fully-merged component
// config, which already reflects the full inheritance chain:
// project default → component group → component.
func resolveSourcePackageListResult(
	srpmName string,
	proj *projectconfig.ProjectConfig,
) (PackageListResult, error) {
	// The SRPM name is the component name by definition.
	compName := srpmName
	compConfig := resolveComponentConfig(compName, proj)

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

	return PackageListResult{
		PackageName: srpmName,
		Type:        PackageTypeSRPM,
		Group:       "", // SRPMs have no package-group membership in the config model
		Component:   compName,
		Channel:     resolved.Publish.SRPMChannel,
	}, nil
}

// resolveFromRPMFile loads a source map file and resolves all SRPMs and their RPMs.
// Returns a flat list of SRPM and RPM entries derived from the file. The returned slice is
// not ordered by contract; callers may sort or otherwise reorder the flattened results. The
// JSON file is parsed once by [loadRPMFile] to produce both the SRPM → RPMs map and the
// authoritative RPM → component index.
func resolveFromRPMFile(
	fs opctx.FS,
	path string,
	groupOf map[string]string,
	proj *projectconfig.ProjectConfig,
) ([]PackageListResult, error) {
	srpmMap, rpmCompOf, err := loadRPMFile(fs, path)
	if err != nil {
		return nil, err
	}

	results := make([]PackageListResult, 0, len(srpmMap))

	for srpmName, rpmNames := range srpmMap {
		srpmResult, resolveErr := resolveSourcePackageListResult(srpmName, proj)
		if resolveErr != nil {
			return nil, resolveErr
		}

		results = append(results, srpmResult)

		for _, rpmName := range rpmNames {
			rpmResult, resolveErr := resolvePackageListResult(rpmName, rpmCompOf, groupOf, proj)
			if resolveErr != nil {
				return nil, resolveErr
			}

			results = append(results, rpmResult)
		}
	}

	return results, nil
}

// loadRPMFile reads and parses a JSON RPM source map from path on fs.
// The file is a JSON array of [rpmSourceEntry] records.
// Returns:
//   - srpmMap: source package name → ordered list of binary RPM names it produces
//   - rpmCompOf: binary RPM name → source package (component) name
//
// Both maps are built in a single pass over the JSON entries. If the same
// binary package name appears more than once, the first entry wins and later
// entries are skipped, keeping srpmMap and rpmCompOf aligned. Identical
// duplicates are skipped silently; conflicting duplicates (same packageName
// with a different sourcePackageName) emit a warning so operators can detect
// and remediate bad inputs.
func loadRPMFile(fs opctx.FS, path string) (srpmMap map[string][]string, rpmCompOf map[string]string, err error) {
	data, readErr := fileutils.ReadFile(fs, path)
	if readErr != nil {
		return nil, nil, fmt.Errorf("reading RPM source map %#q:\n%w", path, readErr)
	}

	var entries []rpmSourceEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, nil, fmt.Errorf("parsing RPM source map %#q:\n%w", path, err)
	}

	srpmMap = make(map[string][]string)
	rpmCompOf = make(map[string]string, len(entries))

	for idx, e := range entries {
		packageName := e.PackageName
		sourcePackageName := e.SourcePackageName

		if packageName == "" {
			return nil, nil, fmt.Errorf(
				"invalid RPM source map %#q entry %d:\nmissing non-empty 'packageName'",
				path,
				idx,
			)
		}

		if sourcePackageName == "" {
			return nil, nil, fmt.Errorf(
				"invalid RPM source map %#q entry %d for package %#q:\nmissing non-empty 'sourcePackageName'",
				path,
				idx,
				packageName,
			)
		}

		if existingSource, exists := rpmCompOf[packageName]; exists {
			// First mapping wins. Skipping later entries keeps [srpmMap] and
			// [rpmCompOf] aligned: each binary RPM appears exactly once in
			// [srpmMap] under exactly the source package recorded in [rpmCompOf].
			//
			//nolint:godox // intentional temporary workaround documented below.
			// TODO: this is a temporary workaround tolerating
			// upstream RPM source maps that list the same packageName under different
			// sourcePackageName values. Once those duplicates are resolved at the
			// source, restore the stricter behavior: error on conflicting
			// sourcePackageName, only dedup identical mappings.
			if existingSource != sourcePackageName {
				slog.Warn(
					"RPM source map contains conflicting source package mappings; first mapping wins",
					"path", path,
					"packageName", packageName,
					"keptSourcePackageName", existingSource,
					"skippedSourcePackageName", sourcePackageName,
				)
			}

			continue
		}

		srpmMap[sourcePackageName] = append(srpmMap[sourcePackageName], packageName)
		rpmCompOf[packageName] = sourcePackageName
	}

	return srpmMap, rpmCompOf, nil
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
			Type:        PackageTypeRPM,
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
			Type:        PackageTypeRPM,
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
