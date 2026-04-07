// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"slices"

	"dario.cat/mergo"
)

// PackagePublishConfig holds publish settings for a single binary package.
// The zero value means the channel is inherited from a higher-priority config layer.
type PackagePublishConfig struct {
	// Channel identifies the publish channel for this package.
	// The special value "none" is a convention meaning the package should not be published;
	// azldev records this value in build results but enforcement is left to downstream tooling.
	// When empty, the value is inherited from the next layer in the resolution order.
	Channel string `toml:"channel,omitempty" json:"channel,omitempty" validate:"omitempty,ne=.,ne=..,excludesall=/\\" jsonschema:"title=Channel,description=Publish channel for this package; use 'none' to signal to downstream tooling that this package should not be published"`
}

// PackageConfig holds all configuration applied to a single binary package.
// Currently only publish settings are supported; additional fields may be added in the future.
type PackageConfig struct {
	// Publish holds the publish settings for this package.
	Publish PackagePublishConfig `toml:"publish,omitempty" json:"publish,omitempty" jsonschema:"title=Publish settings,description=Publishing settings for this binary package"`
}

// MergeUpdatesFrom updates the package config with non-zero values from other.
func (p *PackageConfig) MergeUpdatesFrom(other *PackageConfig) error {
	err := mergo.Merge(p, other, mergo.WithOverride)
	if err != nil {
		return fmt.Errorf("failed to merge package config:\n%w", err)
	}

	return nil
}

// PackageGroupConfig defines a named group of binary packages with shared configuration.
// It is analogous to [ComponentGroupConfig] for components.
//
// If a binary package name appears in a group's [PackageGroupConfig.Packages] list, that group's
// [PackageGroupConfig.DefaultPackageConfig] is applied when resolving the package's [PackageConfig].
// A package may belong to at most one group.
type PackageGroupConfig struct {
	// Description is an optional human-readable description of this group.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human-readable description of this package group"`

	// Packages is an explicit list of binary package names that belong to this group.
	Packages []string `toml:"packages,omitempty" json:"packages,omitempty" jsonschema:"title=Packages,description=Explicit list of binary package names that are members of this group"`

	// DefaultPackageConfig is the configuration applied to all packages listed in Packages.
	DefaultPackageConfig PackageConfig `toml:"default-package-config,omitempty" json:"defaultPackageConfig,omitempty" jsonschema:"title=Default package config,description=Configuration inherited by all packages in this group"`
}

// Validate checks that all package names in the group are non-empty and unique within the group.
func (g *PackageGroupConfig) Validate() error {
	seen := make(map[string]struct{}, len(g.Packages))

	for i, pkg := range g.Packages {
		if pkg == "" {
			return fmt.Errorf("packages[%d] must not be empty", i)
		}

		if _, duplicate := seen[pkg]; duplicate {
			return fmt.Errorf("package %#q appears more than once in the packages list", pkg)
		}

		seen[pkg] = struct{}{}
	}

	return nil
}

// ResolvePackageConfig returns the effective [PackageConfig] for a binary package produced
// by a component, merging contributions from all applicable config layers.
//
// Resolution order (each layer overrides the previous — later wins):
//  1. The project's DefaultPackageConfig (lowest priority)
//  2. The [PackageGroupConfig] whose Packages list contains pkgName, if any
//  3. The component's DefaultPackageConfig
//  4. The component's explicit Packages entry for the exact package name (highest priority)
func ResolvePackageConfig(pkgName string, comp *ComponentConfig, proj *ProjectConfig) (PackageConfig, error) {
	// 1. Start from the project-level default (lowest priority).
	result := proj.DefaultPackageConfig

	// 2. Apply the package group that contains this package, if any.
	// A package belongs to at most one group, so we stop at the first match.
	for groupName, group := range proj.PackageGroups {
		if slices.Contains(group.Packages, pkgName) {
			if err := result.MergeUpdatesFrom(&group.DefaultPackageConfig); err != nil {
				return PackageConfig{}, fmt.Errorf(
					"failed to apply defaults from package group %#q to package %#q:\n%w",
					groupName, pkgName, err,
				)
			}

			break
		}
	}

	// 3. Apply the component-level default (overrides group defaults).
	if err := result.MergeUpdatesFrom(&comp.DefaultPackageConfig); err != nil {
		return PackageConfig{}, fmt.Errorf(
			"failed to apply component defaults to package %#q:\n%w", pkgName, err,
		)
	}

	// 4. Apply the explicit per-package override (exact name, highest priority).
	if pkgConfig, ok := comp.Packages[pkgName]; ok {
		if err := result.MergeUpdatesFrom(&pkgConfig); err != nil {
			return PackageConfig{}, fmt.Errorf(
				"failed to apply package override for %#q:\n%w", pkgName, err,
			)
		}
	}

	return result, nil
}
