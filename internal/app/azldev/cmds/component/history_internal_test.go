// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
)

// TestHasExplicitComponentSelection pins the NEW-1 fix: only an exact name or
// spec path is "explicit". A glob pattern selects broadly and must not defeat
// --include-bare / --shared=omit (it carries no more intent than -a).
func TestHasExplicitComponentSelection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		filter components.ComponentFilter
		want   bool
	}{
		{"exact name", components.ComponentFilter{ComponentNamePatterns: []string{"curl"}}, true},
		{"spec path", components.ComponentFilter{SpecPaths: []string{"specs/curl/curl.spec"}}, true},
		{"star glob", components.ComponentFilter{ComponentNamePatterns: []string{"*"}}, false},
		{"prefix glob", components.ComponentFilter{ComponentNamePatterns: []string{"lib*"}}, false},
		{"char-class glob", components.ComponentFilter{ComponentNamePatterns: []string{"cur[lp]"}}, false},
		{"question glob", components.ComponentFilter{ComponentNamePatterns: []string{"cur?"}}, false},
		{"glob plus exact", components.ComponentFilter{ComponentNamePatterns: []string{"*", "curl"}}, true},
		{"nothing", components.ComponentFilter{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, hasExplicitComponentSelection(&tc.filter))
		})
	}
}

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
// [projectconfig.PackageConfig]) are intentionally NOT walked here -- only
// their parent ComponentConfig field appears in expectedCovered.
// Field-level drift inside those opaque units is still caught by the
// fingerprint exhaustiveness test in projectconfig, at which point a
// human reviewer decides whether richer per-field surfacing belongs in
// history too.
func TestCustomizationCollectorsCoverEveryFingerprintableField(t *testing.T) {
	t.Parallel()

	// Structs walked here are those we want field-level drift detection on,
	// so adding a fingerprintable field forces a conscious decision about
	// surfacing it. A walked field need not map to a *distinct* Kind:
	// DistroReference's two fields both fold into spec.upstream-distro, and
	// SourceFileReference's Hash/HashType fold into the file's entry. Adding
	// a new sub-struct that should get this scrutiny means adding it here.
	walkedStructs := []reflect.Type{
		reflect.TypeFor[projectconfig.ComponentConfig](),
		reflect.TypeFor[projectconfig.ComponentBuildConfig](),
		reflect.TypeFor[projectconfig.CheckConfig](),
		reflect.TypeFor[projectconfig.SpecSource](),
		reflect.TypeFor[projectconfig.DistroReference](),
		reflect.TypeFor[projectconfig.ReleaseConfig](),
		reflect.TypeFor[projectconfig.ComponentRenderConfig](),
		reflect.TypeFor[projectconfig.SourceFileReference](),
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

		// DistroReference -- both fields fold into the single spec.upstream-distro
		// item emitted by appendSpecItems (DistroReference.String()).
		"DistroReference.Name":    "spec.upstream-distro",
		"DistroReference.Version": "spec.upstream-distro",

		// ReleaseConfig.
		"ReleaseConfig.Calculation": "release.calculation (only when non-auto)",

		// ComponentRenderConfig.
		"ComponentRenderConfig.SkipFileFilter": "render.skip-file-filter",

		// SourceFileReference -- Filename and the ReplaceUpstream toggle each get
		// their own Kind. Hash/HashType are deliberately NOT emitted as output:
		// the file's *presence* is the customization signal, and a checksum-only
		// change is still caught by toml-commits / fingerprint-changes.
		// Script and MockPackages are emitted when set (custom-origin source files).
		"SourceFileReference.Filename":        "source-files",
		"SourceFileReference.Hash":            "not emitted (checksum change caught via toml-commits/fingerprint)",
		"SourceFileReference.HashType":        "not emitted (ditto Hash)",
		"SourceFileReference.ReplaceUpstream": "source-files.replace-upstream",
		"SourceFileReference.Script":          "source-files.script",
		"SourceFileReference.MockPackages":    "source-files.mock-packages",
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

// TestCollectCustomizationsEmitsEveryKind complements the reflection-based
// coverage test above: that test proves every fingerprintable field is
// *categorized*, this one proves the collectors are actually *wired* by
// invoking collectCustomizations on a config with every customizable field
// populated and asserting each expected Kind appears. Deleting a collector
// call or emptying a collector body turns this red (the reflection test
// alone would stay green).
func TestCollectCustomizationsEmitsEveryKind(t *testing.T) {
	t.Parallel()

	config := projectconfig.ComponentConfig{
		Overlays: []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlayAddSpecTag, Tag: "Release", Value: "1"},
		},
		Build: projectconfig.ComponentBuildConfig{
			With:      []string{"feature"},
			Without:   []string{"docs"},
			Defines:   map[string]string{"macro": "value"},
			Undefines: []string{"othermacro"},
			Check:     projectconfig.CheckConfig{Skip: true, SkipReason: "flaky"},
		},
		Spec: projectconfig.SpecSource{
			SourceType:     projectconfig.SpecSourceTypeUpstream,
			UpstreamName:   "different-name",
			UpstreamCommit: "abc1234",
			UpstreamDistro: projectconfig.DistroReference{Name: "fedora", Version: "43"},
		},
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAutorelease,
		},
		Render: projectconfig.ComponentRenderConfig{SkipFileFilter: true},
		Packages: map[string]projectconfig.PackageConfig{
			"libfoo": {},
		},
		SourceFiles: []projectconfig.SourceFileReference{
			{Filename: "extra.tar.gz", ReplaceUpstream: true, ReplaceReason: "vendored fix"},
		},
	}

	wantKinds := []string{
		"spec-add-tag",
		"build.with",
		"build.without",
		"build.defines",
		"build.undefines",
		"build.check.skip",
		"spec.source-type",
		"spec.upstream-commit",
		"spec.upstream-name",
		"spec.upstream-distro",
		"release.calculation",
		"render.skip-file-filter",
		"packages",
		"source-files",
		"source-files.replace-upstream",
	}

	items := collectCustomizations("comp", &config)

	gotKinds := make(map[string]bool, len(items))
	for _, item := range items {
		gotKinds[item.Kind] = true
	}

	for _, kind := range wantKinds {
		assert.Truef(t, gotKinds[kind],
			"collectCustomizations did not emit an item of Kind %q; "+
				"a collector for it may be unwired or its trigger condition wrong", kind)
	}
}

// TestFingerprintChangeDTOMirrorsSource guards the direction the explicit
// field-by-field copy in [toFingerprintChanges] cannot: a NEW field added to
// [sources.FingerprintChange] / [sources.CommitMetadata] would compile fine
// but silently never reach JSON consumers. This asserts the local DTO carries
// a field of the same type for every exported source field (matched by name),
// so a field addition OR a type change (e.g. int64->int32) trips the test.
func TestFingerprintChangeDTOMirrorsSource(t *testing.T) {
	t.Parallel()

	dtoFields := exportedFieldTypes(reflect.TypeFor[FingerprintChange]())

	for name, srcType := range exportedFieldTypes(reflect.TypeFor[sources.FingerprintChange]()) {
		dtoType, ok := dtoFields[name]
		if !assert.Truef(t, ok,
			"sources.FingerprintChange field %q has no counterpart in the local "+
				"FingerprintChange DTO; add it (and to toFingerprintChanges) so it "+
				"reaches JSON consumers, or it is silently dropped.", name) {
			continue
		}

		assert.Equalf(t, srcType, dtoType,
			"FingerprintChange DTO field %q has type %s but sources.FingerprintChange "+
				"has %s; the explicit copy in toFingerprintChanges would silently "+
				"narrow or mistype the value.", name, dtoType, srcType)
	}
}

// TestRenderCardViewFingerprintHint pins the N6 fix: the single-component
// card omits the per-commit FingerprintChangeDetails (to stay scannable) but
// must point the user at -O json whenever fingerprint changes exist, so the
// details aren't a silent dead end.
func TestRenderCardViewFingerprintHint(t *testing.T) {
	t.Parallel()

	var withChanges strings.Builder

	renderCardView(&withChanges, HistoryResult{
		Name:               "curl",
		TomlPath:           "azldev.toml",
		TomlCommits:        3,
		Customizations:     2,
		FingerprintChanges: 2,
	})

	out := withChanges.String()
	assert.Contains(t, out, "Component: curl")
	assert.Contains(t, out, "FP changes:     2")
	assert.Contains(t, out, "-O json",
		"card should point at -O json when fingerprint changes exist")

	var noChanges strings.Builder

	renderCardView(&noChanges, HistoryResult{Name: "bash"})

	assert.NotContains(t, noChanges.String(), "-O json",
		"no fingerprint changes means no -O json hint")
}

// exportedFieldTypes returns the exported fields of a struct type keyed by
// name -> type, flattening anonymously-embedded structs (e.g. CommitMetadata)
// into the parent's namespace.
func exportedFieldTypes(t reflect.Type) map[string]reflect.Type {
	types := make(map[string]reflect.Type)

	for i := range t.NumField() {
		field := t.Field(i)

		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			for name, typ := range exportedFieldTypes(field.Type) {
				types[name] = typ
			}

			continue
		}

		if field.IsExported() {
			types[field.Name] = field.Type
		}
	}

	return types
}
