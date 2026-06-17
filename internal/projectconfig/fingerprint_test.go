// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"reflect"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/fingerprint"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllFingerprintedFieldsHaveDecision verifies that every field in every
// fingerprinted struct carries an explicit, valid fingerprint decision: either a
// version-set tag (e.g. `fingerprint:"v1..*"`) declaring when it is measured, or
// `fingerprint:"-"` excluding it. An absent tag is now a failure - the
// mandatory-decision rule the projection substrate relies on (a forgotten field
// must never silently drop out of the hash).
//
// This test serves three purposes:
//  1. It enforces the mandatory tag via [fingerprint.ValidateFieldTag], which also
//     rejects malformed / future-referencing tags and the unimplemented
//     composite-'!' placeholder.
//  2. It ensures version-set tags parse and resolve an emit-key.
//  3. It catches accidental removal of `fingerprint:"-"` tags from excluded fields,
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
		// ComponentConfig.RenderedSpecDir — derived output path that varies by checkout location.
		"ComponentConfig.RenderedSpecDir": true,
		// ComponentConfig.Locked — runtime lock state populated by resolver, not a build input.
		// Lock data is an output of the update command, not a config-level input.
		"ComponentConfig.Locked": true,

		// ComponentBuildConfig.Failure — CI policy (expected failure tracking), not a build input.
		"ComponentBuildConfig.Failure": true,
		// ComponentBuildConfig.Hints — scheduling hints (e.g. expensive), not a build input.
		"ComponentBuildConfig.Hints": true,

		// CheckConfig.SkipReason — human documentation for why check is skipped, not a build input.
		"CheckConfig.SkipReason": true,

		// PackageConfig.Publish — post-build routing (where to publish), not a build input.
		"PackageConfig.Publish": true,

		// ComponentConfig.Publish — post-build routing (where to publish), not a build input.
		"ComponentConfig.Publish": true,

		// ComponentOverlay.Description — human-readable documentation for the overlay.
		"ComponentOverlay.Description": true,
		// ComponentOverlay.Source — absolute path that varies by checkout location.
		// Overlay content is hashed separately by ComputeIdentity.
		"ComponentOverlay.Source": true,

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

		// SourceFileReference.ReplaceReason — human documentation for why an upstream entry is
		// being replaced. ReplaceUpstream itself remains in the fingerprint because flipping it
		// changes the resulting 'sources' file content.
		"SourceFileReference.ReplaceReason": true,

		// SpecSource.Path — absolute path that varies by checkout location.
		// Spec content identity is captured separately via SourceIdentity.
		"SpecSource.Path": true,
	}

	// Collect all actual exclusions found via reflection, and validate every
	// field's tag through the production decision gate.
	actualExclusions := make(map[string]bool)

	for _, st := range fingerprintedStructs {
		for i := range st.NumField() {
			field := st.Field(i)
			key := st.Name() + "." + field.Name

			// ValidateFieldTag enforces the mandatory decision: an absent tag,
			// a malformed/future-referencing version set, or an unimplemented
			// composite-'!' all fail here.
			require.NoErrorf(t, fingerprint.ValidateFieldTag(field),
				"field %q has no valid fingerprint decision", key)

			if field.Tag.Get("fingerprint") == "-" {
				actualExclusions[key] = true
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
