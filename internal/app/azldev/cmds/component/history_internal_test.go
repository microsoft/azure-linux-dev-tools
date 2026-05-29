// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"reflect"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
)

// TestCustomizationCollectorsCoverEveryFingerprintableField pins the
// "customization vs upstream" split to the existing fingerprint:"-"
// taxonomy: a field counts as a customization iff it contributes to the
// component's input fingerprint (i.e., it changes what we ship).
//
// The collector layer in [collectCustomizations] is hand-written so it can
// produce nice human-readable Kind/Value/Description entries per field.
// This test enforces that every fingerprint-relevant field of
// [projectconfig.ComponentConfig] (and its directly walked sub-structs)
// has been consciously categorized in [expectedCovered]. When a new field
// is added to one of these structs, this test forces a choice:
//
//   - Tag it `fingerprint:"-"` (declaring it operational metadata such as
//     publish channels, build hints, or maintenance markers). The
//     fingerprint test in projectconfig already enforces tag presence.
//   - Add it here AND wire up a collector in [collectCustomizations].
//
// Structs whose fingerprintable fields are surfaced *wholesale* by a
// single collector ([projectconfig.ComponentOverlay],
// [projectconfig.SourceFileReference], [projectconfig.PackageConfig],
// [projectconfig.DistroReference]) are intentionally NOT walked here --
// only their parent ComponentConfig field appears in expectedCovered.
// Field-level drift inside those opaque units is still caught by the
// fingerprint exhaustiveness test in projectconfig, at which point a
// human reviewer decides whether richer per-field surfacing belongs in
// history too.
func TestCustomizationCollectorsCoverEveryFingerprintableField(t *testing.T) {
	t.Parallel()

	// Structs walked here are those whose individual fingerprintable
	// fields each map to distinct collector logic in
	// [collectCustomizations]. Adding a new sub-struct that should be
	// walked per-field (rather than treated as an opaque unit) means
	// adding it here too.
	walkedStructs := []reflect.Type{
		reflect.TypeFor[projectconfig.ComponentConfig](),
		reflect.TypeFor[projectconfig.ComponentBuildConfig](),
		reflect.TypeFor[projectconfig.CheckConfig](),
		reflect.TypeFor[projectconfig.SpecSource](),
		reflect.TypeFor[projectconfig.ReleaseConfig](),
		reflect.TypeFor[projectconfig.ComponentRenderConfig](),
	}

	// Maps "StructName.FieldName" -> short note describing how the field
	// surfaces in `component history` output. Every fingerprint-relevant
	// (i.e., NOT `fingerprint:"-"`) field in walkedStructs must appear here.
	expectedCovered := map[string]string{
		// ComponentConfig -- top-level fields dispatch to sub-collectors
		// or are treated as opaque-unit collections.
		"ComponentConfig.Spec":        "appendSpecItems (per-field via SpecSource walk)",
		"ComponentConfig.Release":     "appendReleaseItems (per-field via ReleaseConfig walk)",
		"ComponentConfig.Overlays":    "appendOverlayItems (opaque unit per overlay)",
		"ComponentConfig.Build":       "appendBuildItems (per-field via ComponentBuildConfig walk)",
		"ComponentConfig.Render":      "appendRenderItems (per-field via ComponentRenderConfig walk)",
		"ComponentConfig.SourceFiles": "appendSourceFileItems (opaque unit per source file)",
		"ComponentConfig.Packages":    "appendPackageItems (opaque unit per package override)",

		// ComponentBuildConfig.
		"ComponentBuildConfig.With":      "build.with",
		"ComponentBuildConfig.Without":   "build.without",
		"ComponentBuildConfig.Defines":   "build.defines",
		"ComponentBuildConfig.Undefines": "build.undefines",
		"ComponentBuildConfig.Check":     "delegates to CheckConfig walk",

		// CheckConfig.
		"CheckConfig.Skip": "build.check.skip",

		// SpecSource.
		"SpecSource.SourceType":     "spec.source-type",
		"SpecSource.UpstreamDistro": "spec.upstream-distro",
		"SpecSource.UpstreamName":   "spec.upstream-name (only when distinct from component name)",
		"SpecSource.UpstreamCommit": "spec.upstream-commit",

		// ReleaseConfig.
		"ReleaseConfig.Calculation": "release.calculation (only when non-auto)",

		// ComponentRenderConfig.
		"ComponentRenderConfig.SkipFileFilter": "render.skip-file-filter",
	}

	actualFields := make(map[string]bool)

	for _, st := range walkedStructs {
		for i := range st.NumField() {
			field := st.Field(i)
			key := st.Name() + "." + field.Name

			// Fields excluded from the fingerprint are operational
			// metadata (publish channels, build hints, maintenance
			// markers, etc.), not modifications to upstream. Skip them.
			if field.Tag.Get("fingerprint") == "-" {
				continue
			}

			actualFields[key] = true

			_, ok := expectedCovered[key]
			assert.Truef(t, ok,
				"field %q is fingerprint-relevant but has no entry in expectedCovered. "+
					"Either tag it `fingerprint:\"-\"` (operational metadata) or add it "+
					"to expectedCovered AND wire a collector in collectCustomizations.", key)
		}
	}

	// Reverse: no stale entries left after a field was removed or
	// re-tagged `fingerprint:"-"`.
	for key := range expectedCovered {
		assert.Truef(t, actualFields[key],
			"expectedCovered entry %q does not correspond to a fingerprint-relevant "+
				"field. Was the field removed, renamed, or tagged `fingerprint:\"-\"`?", key)
	}
}
