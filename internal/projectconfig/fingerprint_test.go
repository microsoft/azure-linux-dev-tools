// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"reflect"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
)

// TestAllFingerprintedFieldsHaveDecision verifies that every field in every
// fingerprinted struct has been consciously categorized as either included
// (no fingerprint tag) or excluded (`fingerprint:"-"`).
//
// This test serves two purposes:
//  1. It ensures that newly added fields default to **included** in the fingerprint
//     (the safe default — you get a false positive, never a false negative).
//  2. It catches accidental removal of `fingerprint:"-"` tags from excluded fields,
//     since all exclusions are tracked in expectedExclusions.
func TestAllFingerprintedFieldsHaveDecision(t *testing.T) {
	// All struct types whose fields participate in component fingerprinting.
	// When adding a new struct that feeds into the fingerprint, add it here.
	fingerprintedStructs := []reflect.Type{
		reflect.TypeFor[projectconfig.ComponentConfig](),
		reflect.TypeFor[projectconfig.ComponentBuildConfig](),
		reflect.TypeFor[projectconfig.CheckConfig](),
		reflect.TypeFor[projectconfig.PackageConfig](),
		reflect.TypeFor[projectconfig.ComponentOverlay](),
		reflect.TypeFor[projectconfig.SpecSource](),
		reflect.TypeFor[projectconfig.DistroReference](),
		reflect.TypeFor[projectconfig.SourceFileReference](),
		reflect.TypeFor[projectconfig.ReleaseConfig](),
		reflect.TypeFor[projectconfig.ComponentRenderConfig](),
	}

	// Maps "StructName.FieldName" for every field that should carry a
	// `fingerprint:"-"` tag. Catches accidental tag removal.
	//
	// Each entry documents WHY the field is excluded from the fingerprint:
	expectedExclusions := map[string]bool{
		// ComponentConfig.Name — metadata, already the map key in project config.
		"ComponentConfig.Name": true,
		// ComponentConfig.SourceConfigFile — internal bookkeeping reference, not a build input.
		"ComponentConfig.SourceConfigFile": true,

		// ComponentBuildConfig.Failure — CI policy (expected failure tracking), not a build input.
		"ComponentBuildConfig.Failure": true,
		// ComponentBuildConfig.Hints — scheduling hints (e.g. expensive), not a build input.
		"ComponentBuildConfig.Hints": true,

		// CheckConfig.SkipReason — human documentation for why check is skipped, not a build input.
		"CheckConfig.SkipReason": true,

		// PackageConfig.Publish — post-build routing (where to publish), not a build input.
		"PackageConfig.Publish": true,

		// ComponentOverlay.Description — human-readable documentation for the overlay.
		"ComponentOverlay.Description": true,

		// SourceFileReference.Component — back-reference to parent, not a build input.
		"SourceFileReference.Component": true,

		// DistroReference.Snapshot — snapshot timestamp is not a build input; the resolved
		// upstream commit hash (captured separately via SourceIdentity) is what matters.
		// Excluding this prevents a snapshot bump from marking all upstream components as changed.
		"DistroReference.Snapshot": true,

		// SourceFileReference.Origin — download location metadata (URI, type), not a build input.
		// The file content is already captured by Filename + Hash; changing a CDN URL should not
		// trigger a rebuild.
		"SourceFileReference.Origin": true,
	}

	// Collect all actual exclusions found via reflection, and flag invalid tag values.
	actualExclusions := make(map[string]bool)

	for _, st := range fingerprintedStructs {
		for i := range st.NumField() {
			field := st.Field(i)
			key := st.Name() + "." + field.Name

			tag := field.Tag.Get("fingerprint")

			switch tag {
			case "":
				// No tag — included by default (the safe default).
			case "-":
				actualExclusions[key] = true
			default:
				// hashstructure only recognises "" (include) and "-" (exclude).
				// Any other value is silently treated as included, which is
				// almost certainly a typo.
				assert.Failf(t, "invalid fingerprint tag",
					"field %q has unrecognised fingerprint tag value %q — "+
						"only `fingerprint:\"-\"` (exclude) is valid; "+
						"remove the tag to include the field", key, tag)
			}
		}
	}

	// Verify every expected exclusion is actually present.
	for key := range expectedExclusions {
		assert.Truef(t, actualExclusions[key],
			"expected field %q to have `fingerprint:\"-\"` tag, but it does not — "+
				"was the tag accidentally removed?", key)
	}

	// Verify no unexpected exclusions exist.
	for key := range actualExclusions {
		assert.Truef(t, expectedExclusions[key],
			"field %q has `fingerprint:\"-\"` tag but is not in expectedExclusions — "+
				"add it to expectedExclusions if the exclusion is intentional", key)
	}
}
