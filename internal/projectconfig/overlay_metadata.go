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
	// OverlayCategoryBackportFedora applies a fix that already exists in some Fedora branch.
	// The overlay can be dropped once AZL bumps its upstream pin past the fix.
	OverlayCategoryBackportFedora OverlayCategory = "backport-fedora"
	// OverlayCategoryUpstreamFix applies a fix that is NOT yet in any Fedora branch and is
	// a candidate for upstreaming.
	OverlayCategoryUpstreamFix OverlayCategory = "upstream-fix"
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
	OverlayCategoryBackportFedora,
	OverlayCategoryUpstreamFix,
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

// OverlayUpstreamability records whether an overlay's change can be sent upstream. It
// defaults to 'unknown' (not yet assessed) when the field is omitted.
type OverlayUpstreamability string

const (
	// OverlayUpstreamabilityUnknown means upstreamability has not been assessed. It is the
	// default when the field is omitted (an empty value is treated as 'unknown').
	OverlayUpstreamabilityUnknown OverlayUpstreamability = "unknown"
	// OverlayUpstreamabilityYes means the change is a candidate for upstreaming.
	OverlayUpstreamabilityYes OverlayUpstreamability = "yes"
	// OverlayUpstreamabilityNo means the change is intentionally AZL-specific and will
	// never be upstreamed.
	OverlayUpstreamabilityNo OverlayUpstreamability = "no"
)

// IsValid reports whether u is one of the recognized [OverlayUpstreamability] values. An
// empty value is accepted and means the field was omitted (defaults to 'unknown').
func (u OverlayUpstreamability) IsValid() bool {
	switch u {
	case "", OverlayUpstreamabilityUnknown, OverlayUpstreamabilityYes, OverlayUpstreamabilityNo:
		return true
	default:
		return false
	}
}

// OverlayMetadata describes the intent and provenance of an overlay. It is documentation
// only — it does not affect how the overlay is applied and is excluded from component
// fingerprints. When present, it must declare a [OverlayMetadata.Category]; other fields
// are optional but constrained by category-specific rules (see [OverlayMetadata.Validate]).
type OverlayMetadata struct {
	// Category classifies the overlay's intent. Required.
	Category OverlayCategory `toml:"category" json:"category" jsonschema:"required,enum=backport-fedora,enum=upstream-fix,enum=azl-dependency-pruning,enum=azl-feature-disablement,enum=azl-branding-policy,enum=azl-build,enum=azl-test-disablement,enum=azl-security-compliance,enum=azl-release-management,enum=azl-missing-dependency-workaround,enum=azl-platform-adaptation,title=Category,description=Classification of the overlay's intent"`

	// Commits lists URLs of upstream commits (typically Fedora dist-git or upstream-project
	// commits) that this overlay backports or references.
	Commits []string `toml:"commits,omitempty" json:"commits,omitempty" validate:"omitempty,dive,http_url" jsonschema:"title=Commits,description=URLs of upstream commits this overlay backports or references"`

	// FixedIn names the upstream version or Fedora NVR where the fix lands (for example
	// 'xclock-1.1.1-11.fc44' or '4.11.2').
	FixedIn string `toml:"fixed-in,omitempty" json:"fixedIn,omitempty" jsonschema:"title=Fixed in,description=Upstream version or Fedora NVR where the fix lands"`

	// RemovableAfter names a Fedora branch (for example 'f44'); the overlay may be dropped
	// once AZL bumps past this branch. Only meaningful for the 'backport-fedora' category.
	RemovableAfter string `toml:"removable-after,omitempty" json:"removableAfter,omitempty" jsonschema:"title=Removable after,description=Fedora branch after which this overlay can be dropped. Only valid for the 'backport-fedora' category."`

	// PR is a link to the upstream pull request that carries (or proposes) the fix.
	PR string `toml:"pr,omitempty" json:"pr,omitempty" validate:"omitempty,http_url" jsonschema:"title=Upstream PR,description=URL of the upstream pull request"`

	// BugLinks holds URLs of issue-tracker entries related to this overlay.
	BugLinks []string `toml:"bug,omitempty" json:"bug,omitempty" validate:"omitempty,dive,http_url" jsonschema:"title=Issue-tracker links,description=URLs of related issue-tracker entries"`

	// Upstreamability records whether this overlay's change can be upstreamed: 'yes', 'no',
	// or 'unknown'. Omitting the field defaults to 'unknown' (not yet assessed).
	Upstreamability OverlayUpstreamability `toml:"upstreamability,omitempty" json:"upstreamability,omitempty" jsonschema:"enum=yes,enum=no,enum=unknown,default=unknown,title=Upstreamability,description=Whether this overlay's change can be upstreamed: 'yes', 'no', or 'unknown' (the default)"`
}

// Validate checks that the metadata is internally consistent: the category is recognized,
// category-specific required fields are present, and URL-shaped fields parse as URLs.
func (m *OverlayMetadata) Validate() error {
	if m.Category == "" {
		return errors.New("'metadata' requires 'category'")
	}

	if !m.Category.IsValid() {
		return fmt.Errorf("unknown overlay category %#q", string(m.Category))
	}

	// 'backport-fedora' is the only category that imposes an extra required field; all
	// other categories require nothing beyond a valid 'category'.
	if m.Category == OverlayCategoryBackportFedora && len(m.Commits) == 0 && m.FixedIn == "" {
		return fmt.Errorf(
			"overlay category %#q requires at least one of 'commits' or 'fixed-in'",
			string(m.Category),
		)
	}

	if m.RemovableAfter != "" && m.Category != OverlayCategoryBackportFedora {
		return fmt.Errorf(
			"'removable-after' is only valid for overlay category %#q; found category %#q",
			string(OverlayCategoryBackportFedora), string(m.Category),
		)
	}

	if !m.Upstreamability.IsValid() {
		return fmt.Errorf(
			"unknown upstreamability %#q; want %#q, %#q, or %#q",
			string(m.Upstreamability), string(OverlayUpstreamabilityYes),
			string(OverlayUpstreamabilityNo), string(OverlayUpstreamabilityUnknown),
		)
	}

	// Field-level constraints (for example 'pr' and 'bug' must be http(s) URLs) are
	// expressed as 'validate' struct tags and enforced here.
	if err := validator.New().Struct(m); err != nil {
		return fmt.Errorf("invalid overlay metadata:\n%w", err)
	}

	return nil
}
