// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec_test

import (
	"bytes"
	"embed"
	"io/fs"
	"math/rand/v2"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/specs/*.spec
var fixtureFS embed.FS

const fixtureDirectory = "testdata/specs"

func fixtureNames(t *testing.T) []string {
	t.Helper()

	entries, err := fs.ReadDir(fixtureFS, fixtureDirectory)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}

	slices.Sort(names)

	return names
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	contents, err := fixtureFS.ReadFile(path.Join(fixtureDirectory, name))
	require.NoError(t, err)

	return contents
}

func openFixture(t *testing.T, name string) *spec.Spec {
	t.Helper()

	specification, err := spec.OpenSpec(bytes.NewReader(fixture(t, name)), spec.WithEditor(spec.EditorStructural))
	require.NoError(t, err)

	return specification
}

func serializeFixture(t *testing.T, specification *spec.Spec) string {
	t.Helper()

	var contents bytes.Buffer
	require.NoError(t, specification.Serialize(&contents))

	return contents.String()
}

func assertReparseable(t *testing.T, contents string) {
	t.Helper()

	_, err := spec.OpenSpec(strings.NewReader(contents), spec.WithEditor(spec.EditorStructural))
	require.NoError(t, err)
}

func TestStructuralParserFixturesRoundTripByteForByte(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			contents := fixture(t, name)
			specification, err := spec.OpenSpec(bytes.NewReader(contents), spec.WithEditor(spec.EditorStructural))
			require.NoError(t, err)

			assert.Equal(t, string(contents), serializeFixture(t, specification))
		})
	}
}

func TestStructuralParserFixtureEditsReparse(t *testing.T) {
	tests := []struct {
		name        string
		fixtureName string
		edit        func(*testing.T, *spec.Spec)
	}{
		{
			name:        "insert tag after conditional source",
			fixtureName: "macro-continuation.spec",
			edit: func(t *testing.T, specification *spec.Spec) {
				t.Helper()

				require.NoError(t, specification.InsertTag("", "Source9999", "fixture-marker"))
			},
		},
		{
			name:        "append through nested wrapper",
			fixtureName: "nested-wrappers.spec",
			edit: func(t *testing.T, specification *spec.Spec) {
				t.Helper()

				require.NoError(t, specification.AppendLinesToSection(
					"%files", "devel", []string{"/usr/share/fixture-marker"},
				))
			},
		},
		{
			name:        "remove unreferenced subpackage macro",
			fixtureName: "subpackage-define-unreferenced.spec",
			edit: func(t *testing.T, specification *spec.Spec) {
				t.Helper()

				require.NoError(t, specification.RemoveSubpackage("tools"))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specification := openFixture(t, test.fixtureName)

			test.edit(t, specification)
			assertReparseable(t, serializeFixture(t, specification))
		})
	}
}

func TestStructuralParserFixturesHasSectionThroughWrappers(t *testing.T) {
	tests := []struct {
		fixture string
		section string
		want    bool
	}{
		{fixture: "straddling-wrapper.spec", section: "%install", want: true},
		{fixture: "straddling-wrapper.spec", section: "%check", want: true},
		{fixture: "nested-wrappers.spec", section: "%package", want: true},
		{fixture: "nested-wrappers.spec", section: "%files", want: true},
		{fixture: "elif-with-sections.spec", section: "%files", want: true},
		{fixture: "nested-wrappers.spec", section: "%post", want: false},
	}

	for _, test := range tests {
		t.Run(test.fixture+"/"+test.section, func(t *testing.T) {
			got, err := openFixture(t, test.fixture).HasSection(test.section)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestStructuralParserFixtureAppendRespectsWrapperBoundary(t *testing.T) {
	specification := openFixture(t, "straddling-wrapper.spec")
	require.NoError(t, specification.AppendLinesToSection("%build", "", []string{"echo fixture-marker"}))

	contents := serializeFixture(t, specification)
	assert.Less(t, strings.Index(contents, "echo fixture-marker"), strings.Index(contents, "%if 0%{?with_tests}"))
	assertReparseable(t, contents)
}

func TestStructuralParserFixtureScriptTagShapedLinesAreNotTags(t *testing.T) {
	specification := openFixture(t, "script-section-tag-shaped.spec")

	_, err := specification.RemoveTagsMatching("", func(tag, _ string) bool {
		return strings.EqualFold(tag, "Name") || strings.EqualFold(tag, "Version")
	})
	require.NoError(t, err)

	contents := serializeFixture(t, specification)
	for _, line := range []string{
		`echo "Name: not-a-tag-write"`,
		`printf "Version: still-not-a-tag\n"`,
	} {
		assert.Contains(t, contents, line)
	}
}

func TestStructuralParserFixtureSearchAndReplaceCoversLineTypes(t *testing.T) {
	specification := openFixture(t, "macro-conditional.spec")
	require.NoError(t, specification.SearchAndReplace("", "", "kernel", "fixture-kernel"))
	require.NoError(t, specification.SearchAndReplace("", "", "0%\\{\\?fedora\\}", "1"))

	contents := serializeFixture(t, specification)
	assert.Contains(t, contents, "%define fixture-kernel_reqprovconf")
	assert.Contains(t, contents, "Provides: fixture-kernel")
	assert.Contains(t, contents, "%if 1")
	assertReparseable(t, contents)
}

func TestStructuralParserGDBShapedMacroBodyIsOpaqueKnownLimitation(t *testing.T) {
	input := `%define gdb_python_configure \
%if 0%{?with_python}\
--with-python\
%endif\
%{nil}
`

	specification, err := spec.OpenSpec(strings.NewReader(input), spec.WithEditor(spec.EditorStructural))
	require.NoError(t, err)

	// Parser-only coverage: structural edits cannot target directives inside a
	// macro body individually. Macro hoisting and symbolic macro edits follow
	// in the issue #203 implementation.
	assert.Equal(t, input, serializeFixture(t, specification))
}

func TestStructuralParserSyntheticRoundTripsAreDeterministic(t *testing.T) {
	for seed := uint64(1); seed <= 32; seed++ {
		t.Run("seed-"+strconv.FormatUint(seed, 10), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, seed+1)) //nolint:gosec // Fixed test seeds.
			input := syntheticSpec(rng, rng.IntN(4)+1)

			specification, err := spec.OpenSpec(strings.NewReader(input), spec.WithEditor(spec.EditorStructural))
			require.NoError(t, err)
			assert.Equal(t, input, serializeFixture(t, specification))
		})
	}
}

func syntheticSpec(rng *rand.Rand, branches int) string {
	var output strings.Builder
	output.WriteString("Name: synthetic\n%global flags \\\n  --seed=")
	output.WriteRune(rune('0' + rng.IntN(10)))
	output.WriteString("\n")

	for branch := range branches {
		output.WriteString("%if ")
		output.WriteRune(rune('0' + branch%2))
		output.WriteString("\n%package package")
		output.WriteRune(rune('0' + branch))
		output.WriteString("\n%description package")
		output.WriteRune(rune('0' + branch))
		output.WriteString("\nsynthetic\n%endif\n")
	}

	output.WriteString("%build\necho synthetic\n%files\n/usr/bin/synthetic\n")

	return output.String()
}
