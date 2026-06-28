// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cutoverFieldSet is the frozen v1-cutover field set: every field measured at the
// cutover, each set to a distinct non-zero value, golden-vectored as "maximal".
//
// Do NOT add fields here. It is effectively frozen - it is a golden corpus config,
// so changing what it returns moves its pinned digest and trips the append-only
// guard. A field added after the cutover gets its own new golden vector and is set
// in [emissionProbeConfig], never here.
func cutoverFieldSet() projectconfig.ComponentConfig {
	return projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType:     projectconfig.SpecSourceTypeUpstream,
			UpstreamCommit: "abc1234",
			UpstreamName:   "upstream-name",
			UpstreamDistro: projectconfig.DistroReference{
				Name:    "distro-name",
				Version: "distro-version",
			},
		},
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationStatic,
		},
		Build: projectconfig.ComponentBuildConfig{
			With:      []string{"feature-a"},
			Without:   []string{"feature-b"},
			Defines:   map[string]string{"macro": "value"},
			Undefines: []string{"macro-c"},
			Check:     projectconfig.CheckConfig{Skip: true},
		},
		Render: projectconfig.ComponentRenderConfig{SkipFileFilter: true},
		Overlays: []projectconfig.ComponentOverlay{{
			Type:        projectconfig.ComponentOverlaySetSpecTag,
			Filename:    "file.txt",
			SectionName: "%build",
			PackageName: "subpkg",
			Tag:         "Release",
			Value:       "match-value",
			Regex:       "match-regex",
			Replacement: "replacement",
			Lines:       []string{"line-1"},
		}},
		SourceFiles: []projectconfig.SourceFileReference{{
			Filename:        "source.tar.gz",
			Hash:            "deadbeef",
			HashType:        projectconfig.HashTypeSHA256,
			ReplaceUpstream: true,
		}},
		Packages: map[string]projectconfig.PackageConfig{"pkg": {}},
	}
}

// emissionProbeConfig sets every measured field non-zero so the emission probe can
// assert each measured emit-key appears in the projection. Unlike [cutoverFieldSet]
// (frozen), this GROWS: every omit-if-zero field added after the cutover must be
// set here, or the probe correctly fails that the new measured key is missing.
//
// Additive-field workflow (no version bump):
//  1. add the field + `fingerprint:"v1..*"` tag and its projectV1 emit line;
//  2. set it non-zero below (the probe fails until you do);
//  3. append a new named golden vector that sets it and run -update-golden;
//  4. confirm every pre-existing digest (maximal, minimal, ...) is unchanged.
func emissionProbeConfig() projectconfig.ComponentConfig {
	cfg := cutoverFieldSet() // the frozen v1-cutover field set.
	// Post-cutover additive fields are set here as they are introduced.
	return cfg
}

func mustProject(t *testing.T, cfg projectconfig.ComponentConfig) []byte {
	t.Helper()

	tree, err := projectV1(cfg)
	require.NoError(t, err)

	if tree == nil {
		tree = map[string]any{}
	}

	raw, err := json.Marshal(tree)
	require.NoError(t, err)

	canonical, err := jcs.Transform(raw)
	require.NoError(t, err)

	return canonical
}

// TestProjectV1NilEmptySliceEquivalent confirms the omit predicate treats a nil
// and a non-nil empty scalar slice identically (resolution's WithAppendSlice merge
// yields either for the same intent), so neither emits and both project the same
// canonical JSON - the property that lets projectV1 read the resolved config with
// no config-normalization pre-pass.
func TestProjectV1NilEmptySliceEquivalent(t *testing.T) {
	withNil := projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{With: nil},
	}
	withEmpty := projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{With: []string{}},
	}

	assert.Equal(t, mustProject(t, withNil), mustProject(t, withEmpty),
		"nil and non-nil empty scalar slice must project identically")
	assert.Equal(t, "{}", string(mustProject(t, withEmpty)), "an empty scalar slice omits (empty projection)")

	withVals := projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{With: []string{"x"}},
	}
	assert.NotEqual(t, "{}", string(mustProject(t, withVals)), "a set scalar slice emits")
}

// TestProjEmitAlwaysEmitsZero confirms the treeBuilder's emitAlways method emits a
// scalar at its zero value (the '!' always-emit path) while emit omits it. Without
// it, a future `fingerprint:"!v1..*"` field would validate and pass the probe but
// be silently dropped at zero, since projectV1* could only reach emit.
func TestProjEmitAlwaysEmitsZero(t *testing.T) {
	var always treeBuilder
	always.emitAlways("strip-debug", false)

	treeAlways, err := always.result()
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"strip-debug": false}, treeAlways, "emitAlways emits a build-meaningful zero")

	var omit treeBuilder
	omit.emit("strip-debug", false)

	treeOmit, err := omit.result()
	require.NoError(t, err)
	assert.Nil(t, treeOmit, "emit omits the zero")
}

// TestProjectV1OmitsExcludedAndZeroFields confirms a fingerprint:"-" field never
// reaches the projection and an omit-if-zero field is absent at its zero value.
func TestProjectV1OmitsExcludedAndZeroFields(t *testing.T) {
	t.Run("an empty config projects to an empty object", func(t *testing.T) {
		assert.Equal(t, "{}", string(mustProject(t, projectconfig.ComponentConfig{})))
	})

	t.Run("an excluded field does not move the projection", func(t *testing.T) {
		withExcluded := cutoverFieldSet()
		withExcluded.Build.Failure.Expected = true
		withExcluded.Build.Hints.Expensive = true

		assert.Equal(t, mustProject(t, cutoverFieldSet()), mustProject(t, withExcluded),
			"varying a fingerprint:\"-\" field alone must not move the digest")
	})
}

// TestProjectV1EmitKeyIsTomlKeyNotGoName pins the frozen-emit-key rule: the
// projection emits the field's toml key, never its Go identifier, so a Go-only
// rename is byte-neutral.
func TestProjectV1EmitKeyIsTomlKeyNotGoName(t *testing.T) {
	cfg := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{UpstreamName: "x"},
	}

	encoded := mustProject(t, cfg)

	assert.True(t, bytes.Contains(encoded, []byte(`"upstream-name":`)),
		"must emit the frozen toml key")
	assert.False(t, bytes.Contains(encoded, []byte("UpstreamName")),
		"must not emit the Go field name")
}

// TestProjectV1EmissionProbe runs projectV1 on the emission-probe config (every
// measured field set) and asserts that every measured scalar-leaf / map field's
// emit-key appears in the output - the guard against a hand-written projector that
// measures a field but forgets to emit it.
func TestProjectV1EmissionProbe(t *testing.T) {
	encoded := mustProject(t, emissionProbeConfig())

	for _, key := range measuredLeafEmitKeys(t) {
		slot := fmt.Sprintf("%q:", key)
		assert.Truef(t, bytes.Contains(encoded, []byte(slot)),
			"emit-key %q is measured but not emitted by projectV1. Wiring a fingerprinted "+
				"field (full contract in goldenConfigs's doc comment): add its projectV1 emit "+
				"line, set it in emissionProbeConfig, then append a <toml-key>-set golden vector "+
				"via -update-golden.", key)
	}
}

// TestFingerprintedStructTypesIsComplete walks the measured config graph outward
// from ComponentConfig and asserts the set of struct types it reaches is exactly
// the set registered in FingerprintedStructTypes(). A measured nested struct type
// wired into projectV1 but forgotten from the list is reachable-but-undeclared -
// both the decision test and the emission probe would silently skip its fields.
// A declared-but-unreachable type is a stale entry. Either direction fails here.
func TestFingerprintedStructTypesIsComplete(t *testing.T) {
	declared := make(map[reflect.Type]bool)
	for _, st := range FingerprintedStructTypes() {
		declared[st] = true
	}

	reachable := make(map[reflect.Type]bool)

	var visit func(reflect.Type)

	visit = func(structType reflect.Type) {
		if reachable[structType] {
			return
		}

		reachable[structType] = true

		for i := range structType.NumField() {
			field := structType.Field(i)
			if field.PkgPath != "" {
				continue // unexported: not measured.
			}

			if field.Tag.Get(fingerprintTagName) == excludeTag {
				continue // pruned subtree (matches projectV1's descent).
			}

			if elem := structElem(field.Type); elem != nil {
				visit(elem)
			}
		}
	}

	visit(reflect.TypeFor[projectconfig.ComponentConfig]())

	for st := range reachable {
		assert.Truef(t, declared[st],
			"struct type %s is reachable through a measured field but is missing from "+
				"FingerprintedStructTypes() - add it there so the decision test and emission "+
				"probe police its fields", st.Name())
	}

	for st := range declared {
		assert.Truef(t, reachable[st],
			"struct type %s is in FingerprintedStructTypes() but is no longer reachable "+
				"through a measured field (stale entry?)", st.Name())
	}
}

// structElem unwraps pointer/slice/array/map layers and returns the struct type at
// the bottom, or nil when the type bottoms out in a non-struct (scalar) leaf.
func structElem(typ reflect.Type) reflect.Type {
	for {
		//exhaustive:ignore // only struct-bearing kinds matter; the rest are scalar leaves.
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			typ = typ.Elem()
		case reflect.Struct:
			return typ
		default:
			return nil
		}
	}
}

// measuredLeafEmitKeys enumerates, via reflection, the emit-keys of every
// measured non-composite field (scalar leaf or map) across the fingerprinted
// graph - the keys that must emit a direct slot when set non-zero. Composite
// container keys are intentionally excluded (they omit on projected emptiness,
// so e.g. an all-publish-only "packages" legitimately produces no slot).
//
// The struct list mirrors the decision test's; the reflective auto-discovery
// walk that would remove this hand list is deferred with the generator.
func measuredLeafEmitKeys(t *testing.T) []string {
	t.Helper()

	structs := FingerprintedStructTypes()

	var keys []string

	for _, st := range structs {
		for i := range st.NumField() {
			field := st.Field(i)

			set, err := parseVersionSet(field.Tag.Get(fingerprintTagName), currentContentVersion)
			require.NoError(t, err)

			if set.excluded {
				continue
			}

			// Only fields that emit a direct slot under their own key are probed:
			// scalar leaves and scalar-valued maps. Composites (nested structs,
			// struct slices, and struct-valued maps like Packages) omit on
			// projected emptiness, so they need not appear.
			zero := reflect.New(field.Type).Elem()
			directSlot := isScalarLeaf(zero) ||
				(field.Type.Kind() == reflect.Map && isScalarKind(field.Type.Elem().Kind()))

			if !directSlot {
				continue
			}

			key, err := set.resolveEmitKey(tomlKeyOf(field))
			require.NoError(t, err)

			keys = append(keys, key)
		}
	}

	return keys
}

// TestValidateFieldTagCompositeBangPlaceholder confirms the documented
// composite-'!' placeholder: an always-emit nested struct is rejected with the
// placeholder error, while an always-emit scalar and a plain version set pass.
func TestValidateFieldTagCompositeBangPlaceholder(t *testing.T) {
	type composite struct {
		Nested struct {
			X string `toml:"x" fingerprint:"v1..*"`
		} `toml:"nested" fingerprint:"!v1..*"`
	}

	type scalar struct {
		Flag bool `toml:"flag" fingerprint:"!v1..*"`
	}

	type untagged struct {
		X string `toml:"x"`
	}

	compositeField, found := reflect.TypeFor[composite]().FieldByName("Nested")
	require.True(t, found)

	err := ValidateFieldTag(compositeField)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "composite-'!'")

	scalarField, found := reflect.TypeFor[scalar]().FieldByName("Flag")
	require.True(t, found)
	require.NoError(t, ValidateFieldTag(scalarField))

	untaggedField, found := reflect.TypeFor[untagged]().FieldByName("X")
	require.True(t, found)
	require.Error(t, ValidateFieldTag(untaggedField), "an absent tag must fail the mandatory decision")
}

// TestProjectV1MapAndDiscriminationVectors covers the named edge cases a single
// all-set maximal config misses.
func TestProjectV1MapAndDiscriminationVectors(t *testing.T) {
	t.Run("map key membership is measured", func(t *testing.T) {
		withEmptyValue := projectconfig.ComponentConfig{
			Build: projectconfig.ComponentBuildConfig{Defines: map[string]string{"k": ""}},
		}
		withoutKey := projectconfig.ComponentConfig{
			Build: projectconfig.ComponentBuildConfig{Defines: map[string]string{}},
		}

		assert.NotEqual(t, mustProject(t, withEmptyValue), mustProject(t, withoutKey),
			`{"k":""} must differ from {}`)
	})

	t.Run("a projected-empty package entry adds no bytes", func(t *testing.T) {
		withEmptyEntry := projectconfig.ComponentConfig{
			Packages: map[string]projectconfig.PackageConfig{"pkg": {}},
		}

		assert.Equal(t, mustProject(t, projectconfig.ComponentConfig{}), mustProject(t, withEmptyEntry),
			"a package whose value projects empty must not move the digest")
	})

	t.Run("overlay slice order is significant", func(t *testing.T) {
		first := projectconfig.ComponentConfig{Overlays: []projectconfig.ComponentOverlay{
			{Tag: "a"}, {Tag: "b"},
		}}
		swapped := projectconfig.ComponentConfig{Overlays: []projectconfig.ComponentOverlay{
			{Tag: "b"}, {Tag: "a"},
		}}

		assert.NotEqual(t, mustProject(t, first), mustProject(t, swapped),
			"reordering overlays is a different encoding")
	})
}
