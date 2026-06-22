// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"slices"

	"github.com/go-playground/validator/v10"
)

// OverlayCategory is a classification label for an overlay's intent. Categories help
// reviewers and tooling reason about why an overlay exists and when it can be removed.
type OverlayCategory string

const (
	// OverlayCategoryBackportDistGit applies a fix backported from (or being upstreamed to)
	// a dist-git or upstream project. The overlay can be dropped once AZL bumps its upstream
	// pin past the fix.
	OverlayCategoryBackportDistGit OverlayCategory = "backport-dist-git"
	// OverlayCategoryAZLDependencyPruning removes dependencies that are not shipped in AZL.
	OverlayCategoryAZLDependencyPruning OverlayCategory = "azl-dependency-pruning"
	// OverlayCategoryAZLFeatureDisablement disables unneeded features or subpackages.
	OverlayCategoryAZLFeatureDisablement OverlayCategory = "azl-feature-disablement"
	// OverlayCategoryAZLBrandingPolicy applies Fedora→AzureLinux name/path changes or
	// RHEL/enterprise convention alignment.
	OverlayCategoryAZLBrandingPolicy OverlayCategory = "azl-branding-policy"
	// OverlayCategoryAZLBuild adjusts toolchain, mock, or CI environment specifics.
	OverlayCategoryAZLBuild OverlayCategory = "azl-build"
	// OverlayCategoryAZLTestDisablement skips failing tests.
	OverlayCategoryAZLTestDisablement OverlayCategory = "azl-test-disablement"
	// OverlayCategoryAZLSecurityCompliance applies FIPS or crypto policy changes.
	OverlayCategoryAZLSecurityCompliance OverlayCategory = "azl-security-compliance"
	// OverlayCategoryAZLReleaseManagement adjusts release tag and changelog mechanics.
	OverlayCategoryAZLReleaseManagement OverlayCategory = "azl-release-management"
	// OverlayCategoryAZLMissingDependencyWorkaround is a temporary workaround for packages
	// not yet imported into AZL.
	OverlayCategoryAZLMissingDependencyWorkaround OverlayCategory = "azl-missing-dependency-workaround"
	// OverlayCategoryAZLPlatformAdaptation applies architecture-specific adjustments.
	OverlayCategoryAZLPlatformAdaptation OverlayCategory = "azl-platform-adaptation"
)

// allOverlayCategories lists every recognized [OverlayCategory] value.
//
//nolint:gochecknoglobals // effectively a constant; Go doesn't allow const slices.
var allOverlayCategories = []OverlayCategory{
	OverlayCategoryBackportDistGit,
	OverlayCategoryAZLDependencyPruning,
	OverlayCategoryAZLFeatureDisablement,
	OverlayCategoryAZLBrandingPolicy,
	OverlayCategoryAZLBuild,
	OverlayCategoryAZLTestDisablement,
	OverlayCategoryAZLSecurityCompliance,
	OverlayCategoryAZLReleaseManagement,
	OverlayCategoryAZLMissingDependencyWorkaround,
	OverlayCategoryAZLPlatformAdaptation,
}

// IsValid reports whether c is one of the recognized [OverlayCategory] values.
func (c OverlayCategory) IsValid() bool {
	return slices.Contains(allOverlayCategories, c)
}

// OverlayMetadata describes the intent and provenance of an overlay. It is documentation
// only — it does not affect how the overlay is applied and is excluded from component
// fingerprints. When present, it must declare a [OverlayMetadata.Category]; other fields
// are optional but constrained by category-specific rules (see [OverlayMetadata.Validate]).
type OverlayMetadata struct {
	// Category classifies the overlay's intent. Required.
	Category OverlayCategory `toml:"category" json:"category" jsonschema:"required,enum=backport-dist-git,enum=azl-dependency-pruning,enum=azl-feature-disablement,enum=azl-branding-policy,enum=azl-build,enum=azl-test-disablement,enum=azl-security-compliance,enum=azl-release-management,enum=azl-missing-dependency-workaround,enum=azl-platform-adaptation,title=Category,description=Classification of the overlay's intent"`

	// Commits lists URLs of upstream commits (typically Fedora dist-git or upstream-project
	// commits) that this overlay backports or references.
	Commits []string `toml:"commits,omitempty" json:"commits,omitempty" validate:"omitempty,dive,http_url" jsonschema:"title=Commits,description=URLs of upstream commits this overlay backports or references"`

	// PR is a link to the upstream pull request that carries (or proposes) the fix.
	PR string `toml:"pr,omitempty" json:"pr,omitempty" validate:"omitempty,http_url" jsonschema:"title=Upstream PR,description=URL of the upstream pull request"`

	// BugLinks holds URLs of issue-tracker entries related to this overlay.
	BugLinks []string `toml:"bug,omitempty" json:"bug,omitempty" validate:"omitempty,dive,http_url" jsonschema:"title=Issue-tracker links,description=URLs of related issue-tracker entries"`

	// Upstreamable records whether this overlay's change can be upstreamed. It is omitted
	// (nil) when upstreamability has not yet been assessed.
	Upstreamable *bool `toml:"upstreamable,omitempty" json:"upstreamable,omitempty" jsonschema:"title=Upstreamable,description=Whether this overlay's change can be upstreamed; omit if not yet assessed"`
}

// clone returns a deep copy of the metadata. It is used to stamp a single file-level
// [OverlayMetadata] onto every overlay in a `.overlay.toml` file without aliasing the
// slice fields. A manual copy is preferred over a reflection-based deep copy so that a
// future struct change can never panic during config load.
func (m *OverlayMetadata) clone() *OverlayMetadata {
	if m == nil {
		return nil
	}

	cloned := *m
	cloned.Commits = slices.Clone(m.Commits)
	cloned.BugLinks = slices.Clone(m.BugLinks)

	if m.Upstreamable != nil {
		upstreamable := *m.Upstreamable
		cloned.Upstreamable = &upstreamable
	}

	return &cloned
}

// metadataValidator is a shared, concurrency-safe validator instance reused across all
// [OverlayMetadata.Validate] calls to avoid per-call allocation.
//
//nolint:gochecknoglobals // validator instances are safe for concurrent use once configured.
var metadataValidator = validator.New()

// Validate checks that the metadata is internally consistent: the category is recognized,
// category-specific required fields are present, and URL-shaped fields parse as URLs.
func (m *OverlayMetadata) Validate() error {
	if m.Category == "" {
		return errors.New("'metadata' requires 'category'")
	}

	if !m.Category.IsValid() {
		return fmt.Errorf("unknown overlay category %#q", string(m.Category))
	}

	// 'backport-dist-git' is the only category that imposes an extra required field; all
	// other categories require nothing beyond a valid 'category'.
	if m.Category == OverlayCategoryBackportDistGit && len(m.Commits) == 0 {
		return fmt.Errorf(
			"overlay category %#q requires at least one entry in 'commits'",
			string(m.Category),
		)
	}

	// Field-level constraints (for example 'pr' and 'bug' must be http(s) URLs) are
	// expressed as 'validate' struct tags and enforced here.
	if err := metadataValidator.Struct(m); err != nil {
		return fmt.Errorf("invalid overlay metadata:\n%w", err)
	}

	return nil
}
