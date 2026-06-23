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
	// OverlayCategoryAZLPruning removes content from a component for AZL: dependencies that
	// are not shipped, unneeded features, subpackages, or files. Replaces the more granular
	// historical 'azl-dependency-pruning' and 'azl-feature-disablement' categories.
	OverlayCategoryAZLPruning OverlayCategory = "azl-pruning"
	// OverlayCategoryAZLCompatibility makes a component work in the AZL build/runtime
	// environment: toolchain and mock adjustments and similar compatibility fixes that
	// are not themselves backports. See [OverlayCategoryAZLDepMissingWorkaround] for
	// workarounds for dependencies that have not yet been imported into AZL.
	OverlayCategoryAZLCompatibility OverlayCategory = "azl-compatibility"
	// OverlayCategoryAZLDepMissingWorkaround works around a runtime or build dependency
	// that has not yet been imported into AZL (or is unavailable on a given target). Drop
	// the overlay once the dependency lands.
	OverlayCategoryAZLDepMissingWorkaround OverlayCategory = "azl-dep-missing-workaround"
	// OverlayCategoryAZLBrandingPolicy applies Fedora→AzureLinux name/path changes or
	// RHEL/enterprise convention alignment.
	OverlayCategoryAZLBrandingPolicy OverlayCategory = "azl-branding-policy"
	// OverlayCategoryAZLDisableFlakyTests skips tests that fail intermittently or due to
	// environmental flakiness rather than a real problem with the component,
	// may require retry logic / improve the tests.
	OverlayCategoryAZLDisableFlakyTests OverlayCategory = "azl-disable-flaky-tests"
	// OverlayCategoryAZLDisableUnsupportedTests skips tests that cannot meaningfully run
	// in AZL's build/runtime environment (e.g. tests that require network access, root,
	// or hardware that is unavailable in mock), may require figuring out how to extend support.
	OverlayCategoryAZLDisableUnsupportedTests OverlayCategory = "azl-disable-unsupported-tests"
	// OverlayCategoryAZLSecurityCompliance applies FIPS or crypto policy changes.
	OverlayCategoryAZLSecurityCompliance OverlayCategory = "azl-security-compliance"
	// OverlayCategoryAZLReleaseManagement adjusts release tag and changelog mechanics.
	OverlayCategoryAZLReleaseManagement OverlayCategory = "azl-release-management"
	// OverlayCategoryAZLPlatformAdaptation applies architecture-specific adjustments.
	OverlayCategoryAZLPlatformAdaptation OverlayCategory = "azl-platform-adaptation"
)

// allOverlayCategories lists every recognized [OverlayCategory] value.
//
//nolint:gochecknoglobals // effectively a constant; Go doesn't allow const slices.
var allOverlayCategories = []OverlayCategory{
	OverlayCategoryBackportDistGit,
	OverlayCategoryAZLPruning,
	OverlayCategoryAZLCompatibility,
	OverlayCategoryAZLDepMissingWorkaround,
	OverlayCategoryAZLBrandingPolicy,
	OverlayCategoryAZLDisableFlakyTests,
	OverlayCategoryAZLDisableUnsupportedTests,
	OverlayCategoryAZLSecurityCompliance,
	OverlayCategoryAZLReleaseManagement,
	OverlayCategoryAZLPlatformAdaptation,
}

// IsValid reports whether c is one of the recognized [OverlayCategory] values.
func (c OverlayCategory) IsValid() bool {
	return slices.Contains(allOverlayCategories, c)
}

// BugRef is a typed reference to an issue-tracker entry related to an overlay. Today
// it carries only a URL; the struct form leaves room for tracker-specific metadata to
// be added later without breaking the on-disk schema.
type BugRef struct {
	// URL is the http(s) link to the bug entry. Required.
	URL string `toml:"url" json:"url" validate:"required,http_url" jsonschema:"required,format=uri,pattern=^https?://,title=URL,description=HTTP(S) link to the bug entry"`
}

// OverlayMetadata describes the intent and provenance of an overlay. It is documentation
// only — it does not affect how the overlay is applied and is excluded from component
// fingerprints. When present, it must declare a [OverlayMetadata.Category]; other fields
// are optional but constrained by category-specific rules (see [OverlayMetadata.Validate]).
type OverlayMetadata struct {
	// Category classifies the overlay's intent. Required.
	Category OverlayCategory `toml:"category" json:"category" jsonschema:"required,enum=backport-dist-git,enum=azl-pruning,enum=azl-compatibility,enum=azl-dep-missing-workaround,enum=azl-branding-policy,enum=azl-disable-flaky-tests,enum=azl-disable-unsupported-tests,enum=azl-security-compliance,enum=azl-release-management,enum=azl-platform-adaptation,title=Category,description=Classification of the overlay's intent"`

	// Commits lists URLs of upstream commits (typically Fedora dist-git or upstream-project
	// commits) that this overlay backports or references.
	Commits []string `toml:"commits,omitempty" json:"commits,omitempty" validate:"omitempty,dive,http_url" jsonschema:"title=Commits,description=URLs of upstream commits this overlay backports or references"`

	// Bugs holds references to issue-tracker entries related to this overlay. Each entry
	// must carry an http(s) URL (see [BugRef]).
	Bugs []BugRef `toml:"bugs,omitempty" json:"bugs,omitempty" validate:"omitempty,dive" jsonschema:"title=Bug references,description=References to issue-tracker entries related to this overlay"`

	// Upstreamable records whether this overlay's change can be upstreamed. It is omitted
	// (nil) when upstreamability has not yet been assessed.
	Upstreamable *bool `toml:"upstreamable,omitempty" json:"upstreamable,omitempty" jsonschema:"title=Upstreamable,description=Whether this overlay's change can be upstreamed; omit if not yet assessed"`
}

// clone returns a deep copy of the metadata. It is used to stamp a single file-level
// [OverlayMetadata] onto every overlay in an overlay file without aliasing the
// slice fields. A manual copy is preferred over a reflection-based deep copy so that a
// future struct change can never panic during config load.
func (m *OverlayMetadata) clone() *OverlayMetadata {
	if m == nil {
		return nil
	}

	cloned := *m
	cloned.Commits = slices.Clone(m.Commits)
	cloned.Bugs = slices.Clone(m.Bugs)

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
// category-specific required fields are present, and URL-shaped fields parse as http(s)
// URLs.
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

	if err := metadataValidator.Struct(m); err != nil {
		return fmt.Errorf("invalid overlay metadata:\n%w", err)
	}

	return nil
}
