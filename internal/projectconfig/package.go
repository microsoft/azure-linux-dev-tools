// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"path"
	"sort"

	"dario.cat/mergo"
)

// PackagePublishConfig holds publish settings for a single binary package.
// The zero value means the channel is inherited from a higher-priority config layer.
type PackagePublishConfig struct {
	// Channel identifies the publish channel for this package.
	// The special value "none" means the package should not be published.
	// When empty, the value is inherited from the next layer in the resolution order.
	Channel string `toml:"channel,omitempty" json:"channel,omitempty" jsonschema:"title=Channel,description=Publish channel for this package; 'none' skips publishing entirely"`
}

// PackageConfig holds all configuration applied to a single binary package.
// Currently only publish settings are supported; additional fields may be added in the future.
type PackageConfig struct {
	// Description is an optional human-readable note about this package's configuration
	// (e.g., "user-facing API — ships in base repo").
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human-readable note about this package's configuration"`

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

// PackageGroupConfig defines a named group of binary packages matched by name globs.
// It is analogous to [ComponentGroupConfig] for components, but operates at publish time
// rather than config-load time.
//
// All package-groups whose [PackageGroupConfig.PackagePatterns] match a given binary package
// name contribute their [PackageGroupConfig.DefaultPackageConfig] to the resolved [PackageConfig]
// for that package. Groups are applied in alphabetical name order; later-named groups override
// earlier-named ones.
type PackageGroupConfig struct {
	// Description is an optional human-readable description of this group.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human-readable description of this package group"`

	// PackagePatterns is a list of binary package name globs.
	// A package belongs to this group if its name matches any one of these patterns.
	// Pattern syntax follows [path.Match] rules (e.g., "*-devel", "python3-*", "curl").
	PackagePatterns []string `toml:"package-patterns,omitempty" json:"packagePatterns,omitempty" jsonschema:"title=Package patterns,description=Glob patterns matched against binary package names to determine group membership"`

	// DefaultPackageConfig is the configuration applied to all packages whose name matches
	// any pattern in PackagePatterns.
	DefaultPackageConfig PackageConfig `toml:"default-package-config,omitempty" json:"defaultPackageConfig,omitempty" jsonschema:"title=Default package config,description=Configuration inherited by all packages matched by this group"`
}

// Validate checks that all package patterns in the group are non-empty and well-formed globs.
func (g *PackageGroupConfig) Validate() error {
	for patternIdx, pattern := range g.PackagePatterns {
		if pattern == "" {
			return fmt.Errorf("package-patterns[%d] must not be empty", patternIdx)
		}

		// Verify the pattern is a valid glob by doing a trial match.
		// path.Match returns ErrBadPattern for malformed globs.
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("package-patterns[%d] %#q is not a valid glob:\n%w", patternIdx, pattern, err)
		}
	}

	return nil
}

// ResolvePackageConfig returns the effective [PackageConfig] for a binary package produced
// by a component, merging contributions from all applicable config layers.
//
// Resolution order (each layer overrides the previous — later wins):
//  1. The project's DefaultPackageConfig (lowest priority)
//  2. All [PackageGroupConfig] whose patterns match pkgName, applied in alphabetical group name order
//  3. The component's DefaultPackageConfig
//  4. The component's explicit Packages entry for the exact package name (highest priority)
func ResolvePackageConfig(pkgName string, comp *ComponentConfig, proj *ProjectConfig) (PackageConfig, error) {
	// 1. Start from the project-level default (lowest priority).
	result := proj.DefaultPackageConfig

	// 2. Apply all matching package-groups in sorted name order for deterministic behavior.
	groupNames := make([]string, 0, len(proj.PackageGroups))
	for name := range proj.PackageGroups {
		groupNames = append(groupNames, name)
	}

	sort.Strings(groupNames)

	for _, groupName := range groupNames {
		group := proj.PackageGroups[groupName]
		for _, pattern := range group.PackagePatterns {
			if matchGlob(pattern, pkgName) {
				if err := result.MergeUpdatesFrom(&group.DefaultPackageConfig); err != nil {
					return PackageConfig{}, fmt.Errorf(
						"failed to apply defaults from package group %#q to package %#q:\n%w",
						groupName, pkgName, err,
					)
				}

				break // one pattern match per group is sufficient
			}
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

// matchGlob reports whether pkgName matches the given glob pattern.
// Pattern syntax follows [path.Match] rules. A malformed pattern is treated as a non-match
// to avoid panicking at resolution time; patterns should be validated at config-load time
// via [PackageGroupConfig.Validate].
func matchGlob(pattern, pkgName string) bool {
	matched, err := path.Match(pattern, pkgName)

	return err == nil && matched
}
