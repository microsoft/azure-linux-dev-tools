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
	// OverlayCategoryUpstreamBackport applies a fix backported from an upstream source
	// (Fedora dist-git or the component's OSS project) that AZL will inherit once its
	// upstream pin bumps past the fix.
	OverlayCategoryUpstreamBackport OverlayCategory = "upstream-backport"
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
	OverlayCategoryUpstreamBackport,
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

// OverlayUpstreamStatus classifies an overlay's relationship to its upstream project.
// It distinguishes "already upstream", "could be upstreamed", "needs an upstream
// mechanism first", "permanent AZL-only deviation", and "not yet assessed".
type OverlayUpstreamStatus string

const (
	// OverlayUpstreamStatusUpstreamed indicates the change is already in Fedora / the
	// upstream project. The overlay is carried only until AZL bumps past the fix.
	OverlayUpstreamStatusUpstreamed OverlayUpstreamStatus = "upstreamed"
	// OverlayUpstreamStatusUpstreamable indicates the change could be sent upstream
	// as-is. Reviewers should ask the author to link the upstream PR.
	OverlayUpstreamStatusUpstreamable OverlayUpstreamStatus = "upstreamable"
	// OverlayUpstreamStatusNeedsUpstreamHook indicates an AZL specialization that today
	// requires invasive spec edits; upstream could add a bcond / ifdef / config knob
	// that would let us drop the overlay. Reviewers should ask whether the hook can be
	// upstreamed instead of the change itself.
	OverlayUpstreamStatusNeedsUpstreamHook OverlayUpstreamStatus = "needs-upstream-hook"
	// OverlayUpstreamStatusInapplicable indicates a permanent AZL-only deviation.
	// Reviewers should push back on why we have to fork upstream forever.
	OverlayUpstreamStatusInapplicable OverlayUpstreamStatus = "inapplicable"
	// OverlayUpstreamStatusUnknown indicates the author has not yet assessed the
	// overlay's upstream story. Reviewers should push for a definite status before
	// approving.
	OverlayUpstreamStatusUnknown OverlayUpstreamStatus = "unknown"
)

// allOverlayUpstreamStatuses lists every recognized [OverlayUpstreamStatus] value.
//
//nolint:gochecknoglobals // effectively a constant; Go doesn't allow const slices.
var allOverlayUpstreamStatuses = []OverlayUpstreamStatus{
	OverlayUpstreamStatusUpstreamed,
	OverlayUpstreamStatusUpstreamable,
	OverlayUpstreamStatusNeedsUpstreamHook,
	OverlayUpstreamStatusInapplicable,
	OverlayUpstreamStatusUnknown,
}

// IsValid reports whether s is one of the recognized [OverlayUpstreamStatus] values.
func (s OverlayUpstreamStatus) IsValid() bool {
	return slices.Contains(allOverlayUpstreamStatuses, s)
}

// URLRef is a typed reference to an external resource (an upstream commit, issue-
// tracker entry, or similar). Today it carries only a URL; the struct form leaves
// room for source-specific metadata to be added later without breaking the on-disk
// schema.
type URLRef struct {
	// URL is the http(s) link to the referenced resource. Required.
	URL string `toml:"url" json:"url" validate:"required,http_url" jsonschema:"required,format=uri,pattern=^https?://,title=URL,description=HTTP(S) link to the referenced resource"`
}

// OverlayMetadata describes the intent and provenance of an overlay. It is documentation
// only — it does not affect how the overlay is applied and is excluded from component
// fingerprints. When present, it must declare a [OverlayMetadata.Category]; other fields
// are optional but constrained by category-specific rules (see [OverlayMetadata.Validate]).
type OverlayMetadata struct {
	// Category classifies the overlay's intent. Required.
	Category OverlayCategory `toml:"category" json:"category" jsonschema:"required,enum=upstream-backport,enum=azl-pruning,enum=azl-compatibility,enum=azl-dep-missing-workaround,enum=azl-branding-policy,enum=azl-disable-flaky-tests,enum=azl-disable-unsupported-tests,enum=azl-security-compliance,enum=azl-release-management,enum=azl-platform-adaptation,title=Category,description=Classification of the overlay's intent"`

	// Commits references upstream commits (typically Fedora dist-git or upstream-project
	// commits) that this overlay backports or references. Each entry must carry an
	// http(s) URL (see [URLRef]).
	Commits []URLRef `toml:"commits,omitempty" json:"commits,omitempty" validate:"omitempty,dive" jsonschema:"title=Commits,description=Upstream commits this overlay backports or references"`

	// Bugs holds references to issue-tracker entries related to this overlay. Each entry
	// must carry an http(s) URL (see [URLRef]).
	Bugs []URLRef `toml:"bugs,omitempty" json:"bugs,omitempty" validate:"omitempty,dive" jsonschema:"title=Bug references,description=References to issue-tracker entries related to this overlay"`

	// UpstreamStatus classifies the overlay's relationship to its upstream project.
	// Required. Use [OverlayUpstreamStatusUnknown] when the assessment has not been
	// made yet; reviewers should push for a definite status before approving.
	UpstreamStatus OverlayUpstreamStatus `toml:"upstream-status" json:"upstreamStatus" jsonschema:"required,title=Upstream status,description=Classifies the overlay's relationship to its upstream project,enum=upstreamed,enum=upstreamable,enum=needs-upstream-hook,enum=inapplicable,enum=unknown"`
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

	// 'upstream-backport' is the only category that imposes an extra required field; all
	// other categories require nothing beyond a valid 'category'.
	if m.Category == OverlayCategoryUpstreamBackport && len(m.Commits) == 0 {
		return fmt.Errorf(
			"overlay category %#q requires at least one entry in 'commits'",
			string(m.Category),
		)
	}

	if m.UpstreamStatus == "" {
		return errors.New("'metadata' requires 'upstream-status'")
	}

	if !m.UpstreamStatus.IsValid() {
		return fmt.Errorf("unknown overlay upstream-status %#q", string(m.UpstreamStatus))
	}

	// 'upstream-backport' asserts the change is already in upstream or could be
	// (either already-merged Fedora / OSS commits, or a change the author intends to
	// send upstream next). Any [OverlayUpstreamStatus] other than
	// [OverlayUpstreamStatusUpstreamed] or [OverlayUpstreamStatusUpstreamable]
	// contradicts the category.
	if m.Category == OverlayCategoryUpstreamBackport &&
		m.UpstreamStatus != OverlayUpstreamStatusUpstreamed &&
		m.UpstreamStatus != OverlayUpstreamStatusUpstreamable {
		return fmt.Errorf(
			"overlay category %#q implies the change is already upstream or upstreamable; "+
				"'upstream-status' value %#q is contradictory (allowed: %#q or %#q)",
			string(m.Category), string(m.UpstreamStatus),
			string(OverlayUpstreamStatusUpstreamed), string(OverlayUpstreamStatusUpstreamable),
		)
	}

	if err := metadataValidator.Struct(m); err != nil {
		return fmt.Errorf("invalid overlay metadata:\n%w", err)
	}

	return nil
}
