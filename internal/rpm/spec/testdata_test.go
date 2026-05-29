// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec_test

import (
	"bytes"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdataSpecsDir is the directory containing curated hand-crafted spec
// fixtures harvested from real-world Fedora / Azure Linux patterns.
const testdataSpecsDir = "testdata/specs"

// loadFixture reads a fixture file and returns its raw bytes.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()

	path := filepath.Join(testdataSpecsDir, name)

	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading fixture %s", path)

	return raw
}

// openFixture parses a fixture into a *Spec.
func openFixture(t *testing.T, name string) *spec.Spec {
	t.Helper()

	raw := loadFixture(t, name)

	specObj, err := spec.OpenSpec(bytes.NewReader(raw))
	require.NoError(t, err, "parsing fixture %s", name)

	return specObj
}

// serializeSpec serializes a Spec to a string for assertion.
func serializeSpec(t *testing.T, s *spec.Spec) string {
	t.Helper()

	var buf bytes.Buffer

	require.NoError(t, s.Serialize(&buf))

	return buf.String()
}

// listFixtures returns the names (basename only) of all *.spec files in
// the curated testdata directory.
func listFixtures(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(testdataSpecsDir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".spec") {
			continue
		}

		names = append(names, entry.Name())
	}

	require.NotEmpty(t, names, "expected at least one fixture in %s", testdataSpecsDir)

	return names
}

// --- Tier 1: round-trip preservation for every curated fixture. ---

// TestTestdataRoundTrip parses every fixture in testdata/specs/, serializes
// it back to bytes, and asserts byte-for-byte equality with the source.
// Failures here indicate the parser/serializer is dropping or mutating lines
// for some real-world spec pattern.
func TestTestdataRoundTrip(t *testing.T) {
	for _, name := range listFixtures(t) {
		t.Run(name, func(t *testing.T) {
			raw := loadFixture(t, name)

			s, err := spec.OpenSpec(bytes.NewReader(raw))
			require.NoError(t, err, "parsing %s", name)

			got := serializeSpec(t, s)
			assert.Equal(t, string(raw), got, "round-trip mismatch for %s", name)
		})
	}
}

// --- Tier 3: targeted edit-operation tests per fixture pattern. ---

// TestTestdataAddTagPreservesStructure exercises AddTag against every fixture
// and asserts that:
//  1. The operation succeeds (no parse-state corruption from quirky inputs).
//  2. Serialized output still round-trips through OpenSpec (idempotent parser).
//  3. The injected tag is observable in the rendered output.
func TestTestdataAddTagPreservesStructure(t *testing.T) {
	for _, name := range listFixtures(t) {
		t.Run(name, func(t *testing.T) {
			s := openFixture(t, name)

			require.NoError(t, s.AddTag("", "BuildRequires", "regression-marker"))

			out := serializeSpec(t, s)
			assert.Contains(t, out, "BuildRequires: regression-marker",
				"injected tag should appear in serialized output")

			// Re-parse the result. The parser should accept its own output.
			_, err := spec.OpenSpec(bytes.NewReader([]byte(out)))
			require.NoError(t, err, "serialized output should re-parse cleanly")
		})
	}
}

// TestTestdataHasSectionWalksWrappers verifies HasSection finds sections that
// live inside conditional wrappers (straddling, nested, elif-with-sections).
func TestTestdataHasSectionWalksWrappers(t *testing.T) {
	cases := []struct {
		fixture string
		section string
		want    bool
	}{
		// straddling-wrapper: %install and %check live inside %if 0%{?with_tests}.
		{"straddling-wrapper.spec", "%install", true},
		{"straddling-wrapper.spec", "%check", true},
		{"straddling-wrapper.spec", "%files", true},
		{"straddling-wrapper.spec", "%post", false},

		// nested-wrappers: %package devel lives inside %if 0%{?with_devel}.
		{"nested-wrappers.spec", "%package", true},
		{"nested-wrappers.spec", "%description", true},
		{"nested-wrappers.spec", "%files", true},
		{"nested-wrappers.spec", "%check", false},

		// elif-with-sections: each branch contributes a different %package, but
		// from the spec's perspective at least one %package header exists.
		{"elif-with-sections.spec", "%package", true},
		{"elif-with-sections.spec", "%files", true},

		// multi-package-mixed: %if-wrapped %package doc.
		{"multi-package-mixed.spec", "%package", true},
		{"multi-package-mixed.spec", "%changelog", true},
	}

	for _, testCase := range cases {
		t.Run(testCase.fixture+"/"+testCase.section, func(t *testing.T) {
			specObj := openFixture(t, testCase.fixture)

			got, err := specObj.HasSection(testCase.section)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

// TestTestdataAppendLinesToSection verifies AppendLinesToSection works through
// straddling and nested conditional wrappers — the lines must land inside the
// targeted section even when the section itself is inside an %if block.
func TestTestdataAppendLinesToSection(t *testing.T) {
	cases := []struct {
		fixture string
		section string
		pkg     string
		marker  string
	}{
		{"straddling-wrapper.spec", "%install", "", "echo straddling-marker"},
		{"nested-wrappers.spec", "%files", "devel", "/usr/share/nested-marker"},
		{"multi-package-mixed.spec", "%files", "devel", "/usr/share/multi-marker"},
		{"elif-with-sections.spec", "%files", "", "/usr/share/elif-marker"},
	}

	for _, testCase := range cases {
		t.Run(testCase.fixture+"/"+testCase.section+"/"+testCase.pkg, func(t *testing.T) {
			specObj := openFixture(t, testCase.fixture)

			require.NoError(t, specObj.AppendLinesToSection(testCase.section, testCase.pkg, []string{testCase.marker}))

			out := serializeSpec(t, specObj)
			assert.Contains(t, out, testCase.marker, "marker should appear in serialized output")

			// Sanity: parser should accept its own output.
			_, err := spec.OpenSpec(bytes.NewReader([]byte(out)))
			require.NoError(t, err)
		})
	}
}

// TestTestdataScriptSectionTagShapedSafety asserts that tag-walking operations
// targeting script sections leave shell lines that look like tags untouched.
// This guards against regressions in the isTagBearingSection filter.
func TestTestdataScriptSectionTagShapedSafety(t *testing.T) {
	raw := loadFixture(t, "script-section-tag-shaped.spec")

	// Capture the script-section shell lines that look like tags.
	scriptyLines := []string{
		`echo "Name: not-a-tag-write"`,
		`printf "Version: still-not-a-tag\n"`,
		`echo "Requires: bash" >> .build-manifest`,
		`echo "License: MIT" | tee -a check.log`,
		`echo "Conflicts: previous-version" >&2`,
		`echo "Provides: %{name}-runtime" > /var/log/%{name}-post.log`,
	}

	for _, line := range scriptyLines {
		require.Contains(t, string(raw), line, "fixture must contain %q for the test to be meaningful", line)
	}

	specObj, err := spec.OpenSpec(bytes.NewReader(raw))
	require.NoError(t, err)

	// Try to remove every tag named "Name", "Version", "Requires", "License",
	// "Conflicts", "Provides" in the main package. Only real preamble tags
	// (Name, Version, Release, Summary, License at the top of the file)
	// should be considered; the shell lines must be left alone.
	for _, tag := range []string{"Name", "Version", "Requires", "License", "Conflicts", "Provides"} {
		_, err := specObj.RemoveTagsMatching("", func(t, _ string) bool {
			return strings.EqualFold(t, tag)
		})
		require.NoError(t, err, "RemoveTagsMatching(%q) should not error", tag)
	}

	out := serializeSpec(t, specObj)

	// Every shell-shaped line must still be present.
	for _, line := range scriptyLines {
		assert.Contains(t, out, line,
			"script-section shell line %q must NOT be removed by tag operations", line)
	}
}

// TestTestdataSearchAndReplaceSectionScope verifies that SearchAndReplace
// confined to a section only touches that section. We replace a string that
// appears in both %install and %files of multi-package-mixed.spec, restricted
// to %install, and confirm %files is untouched.
func TestTestdataSearchAndReplaceSectionScope(t *testing.T) {
	specObj := openFixture(t, "multi-package-mixed.spec")

	// %make_install appears only in %install for this fixture — replace with a
	// marker, then confirm the marker shows up once.
	require.NoError(t, specObj.SearchAndReplace("%install", "", "%make_install", "%make_install # PATCHED"))

	out := serializeSpec(t, specObj)
	assert.Contains(t, out, "%make_install # PATCHED")
	assert.Equal(t, 1, strings.Count(out, "# PATCHED"),
		"section-scoped SearchAndReplace must apply exactly once")
}

// --- Synthetic generator stress test. ---

// syntheticBuilder composes the same primitive patterns the curated fixtures
// exercise (preamble tags, conditionals, macros, sections) into random spec
// inputs. The test asserts every generated input round-trips through the
// parser/serializer.
type syntheticBuilder struct {
	rng     *rand.Rand
	out     strings.Builder
	pkgIdx  int
	condIdx int
}

func (b *syntheticBuilder) line(s string) {
	b.out.WriteString(s)
	b.out.WriteByte('\n')
}

func (b *syntheticBuilder) writePreamble(name string) {
	b.line("Name:    " + name)
	b.line("Version: 1.0")
	b.line("Release: 1")
	b.line("Summary: Synthetic test fixture")
	b.line("License: MIT")
	b.line("")
}

func (b *syntheticBuilder) writeMacroContinuation() {
	b.line("%global synth_flags \\")
	b.line("    --enable-foo \\")
	b.line("    --enable-bar \\")
	b.line("    --enable-baz")
	b.line("")
}

func (b *syntheticBuilder) writeIfWrapper(body func()) {
	b.condIdx++
	b.line("%if 0%{?with_synth_" + strconv.Itoa(b.condIdx) + "}")
	body()
	b.line("%endif")
	b.line("")
}

func (b *syntheticBuilder) writeIfElseContent(then, els string) {
	b.condIdx++
	b.line("%if 0%{?fedora}")
	b.line(then)
	b.line("%else")
	b.line(els)
	b.line("%endif")
	b.line("")
}

func (b *syntheticBuilder) writeElifChain() {
	b.condIdx++
	b.line("%if 0%{?rhel}")
	b.line("Requires: rhel-thing")
	b.line("%elif 0%{?fedora}")
	b.line("Requires: fedora-thing")
	b.line("%elif 0%{?suse_version}")
	b.line("Requires: suse-thing")
	b.line("%else")
	b.line("Requires: generic-thing")
	b.line("%endif")
	b.line("")
}

func (b *syntheticBuilder) writeSubpackage() {
	b.pkgIdx++

	name := "sub" + strconv.Itoa(b.pkgIdx)

	b.line("%package " + name)
	b.line("Summary: Sub-package " + name)
	b.line("")
	b.line("%description " + name)
	b.line("Synthetic sub-package " + name + ".")
	b.line("")
	b.line("%files " + name)
	b.line("/usr/share/synth/" + name)
	b.line("")
}

func (b *syntheticBuilder) writeScriptSection(name string) {
	b.line(name)
	b.line(`echo "Name: not-a-tag"`)
	b.line(`printf "Version: still-not-a-tag\n"`)
	b.line("make")
	b.line("")
}

func (b *syntheticBuilder) writeFooter() {
	b.line("%changelog")
	b.line("* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1")
	b.line("- Synthetic.")
}

// generateSyntheticSpec composes a random spec from the primitive patterns
// above. The output ends with a newline so byte-for-byte round-trip checks
// match Spec.Serialize behavior.
func generateSyntheticSpec(seed1, seed2 uint64) string {
	//nolint:gosec // deterministic synthetic test data, not security-sensitive
	builder := &syntheticBuilder{rng: rand.New(rand.NewPCG(seed1, seed2))}

	builder.writePreamble("synthetic")

	// Insert a randomized 0..4 mix of preamble-level primitives.
	for range 4 {
		switch builder.rng.IntN(5) {
		case 0:
			builder.writeMacroContinuation()
		case 1:
			builder.writeElifChain()
		case 2:
			builder.writeIfElseContent("BuildRequires: fedora-only", "BuildRequires: other")
		case 3:
			builder.writeIfWrapper(func() {
				builder.writeSubpackage()
			})
		case 4:
			builder.writeSubpackage()
		}
	}

	builder.line("%description")
	builder.line("Synthetic top-level description.")
	builder.line("")

	for _, sect := range []string{"%prep", "%build", "%install", "%check"} {
		builder.writeScriptSection(sect)
	}

	builder.line("%files")
	builder.line("/usr/bin/synthetic")
	builder.line("")

	builder.writeFooter()

	return builder.out.String()
}

// TestSyntheticSpecsRoundTrip generates 64 random spec bodies from primitive
// patterns and asserts every one round-trips byte-for-byte. Failures expose a
// composition the parser handles incorrectly even though the individual
// patterns work in isolation.
func TestSyntheticSpecsRoundTrip(t *testing.T) {
	const iterations = 64

	for iteration := range iterations {
		//nolint:gosec // iteration is bounded by iterations, no overflow risk
		seed1 := uint64(iteration + 1)
		//nolint:gosec // iteration is bounded by iterations, no overflow risk
		seed2 := uint64(iteration)*1099511628211 + 14695981039346656037

		t.Run("seed_"+strconv.Itoa(iteration), func(t *testing.T) {
			input := generateSyntheticSpec(seed1, seed2)

			s, err := spec.OpenSpec(bytes.NewReader([]byte(input)))
			require.NoError(t, err, "parsing synthetic spec (seed1=%d, seed2=%d)", seed1, seed2)

			out := serializeSpec(t, s)
			assert.Equal(t, input, out,
				"synthetic spec must round-trip (seed1=%d, seed2=%d)", seed1, seed2)
		})
	}
}

// TestSyntheticSpecsAddTag generates random specs and asserts AddTag preserves
// re-parseability. This is the random-composition analogue of
// TestTestdataAddTagPreservesStructure.
func TestSyntheticSpecsAddTag(t *testing.T) {
	const iterations = 32

	for iteration := range iterations {
		//nolint:gosec // iteration is bounded by iterations, no overflow risk
		seed1 := uint64(iteration + 1000)
		//nolint:gosec // iteration is bounded by iterations, no overflow risk
		seed2 := uint64(iteration)*0x9E3779B97F4A7C15 + 0xBF58476D1CE4E5B9

		t.Run("seed_"+strconv.Itoa(iteration), func(t *testing.T) {
			input := generateSyntheticSpec(seed1, seed2)

			s, err := spec.OpenSpec(bytes.NewReader([]byte(input)))
			require.NoError(t, err)

			require.NoError(t, s.AddTag("", "BuildRequires", "synth-marker"))

			out := serializeSpec(t, s)
			assert.Contains(t, out, "BuildRequires: synth-marker")

			_, err = spec.OpenSpec(bytes.NewReader([]byte(out)))
			require.NoError(t, err, "spec must re-parse after AddTag")
		})
	}
}

// --- Issue #203: macro hoisting on subpackage removal. ---

// lineIndex returns the 0-based line index where target appears as an exact
// trimmed-line match in lines, or -1 if no such line exists.
func lineIndex(lines []string, target string) int {
	for i, line := range lines {
		if strings.TrimSpace(line) == target {
			return i
		}
	}

	return -1
}

// hasLine reports whether any trimmed line in lines equals target.
func hasLine(lines []string, target string) bool {
	return lineIndex(lines, target) >= 0
}

// hasLineWithPrefix reports whether any trimmed line in lines starts with
// prefix. Useful for header lines like `%package tests` where the trailing
// whitespace may vary.
func hasLineWithPrefix(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}

	return false
}

// TestTestdataRemoveSubpackageHoistsReferencedMacro is the issue #203 repro
// turned into a regression test. The fixture has `%define testsdir` inside
// `%package tests` and references `%{testsdir}` from `%install` (which is
// unconditional and survives subpackage removal).
//
// Required behavior: removing the `tests` subpackage must hoist the macro
// definition to the root level (before the first removed section) so the
// surviving references in `%install` still resolve. All sections targeting
// the `tests` subpackage must still be removed.
func TestTestdataRemoveSubpackageHoistsReferencedMacro(t *testing.T) {
	specObj := openFixture(t, "subpackage-define-referenced.spec")

	require.NoError(t, specObj.RemoveSubpackage("tests"))

	out := serializeSpec(t, specObj)
	outLines := strings.Split(out, "\n")

	// The macro definition must survive as its own header line.
	macroLine := "%define testsdir %{_libdir}/%{name}/tests-src"
	assert.True(t, hasLine(outLines, macroLine),
		"referenced macro must be hoisted, not dropped with the subpackage")

	// All subpackage section headers must be gone (line-exact, ignoring the
	// description text which legitimately mentions `%%package tests`).
	assert.False(t, hasLineWithPrefix(outLines, "%package tests"),
		"subpackage header must be removed")
	assert.False(t, hasLineWithPrefix(outLines, "%description tests"),
		"subpackage description must be removed")
	assert.False(t, hasLineWithPrefix(outLines, "%files tests"),
		"subpackage files must be removed")

	// The hoisted macro must appear before %install so the surviving
	// `%{testsdir}` references resolve.
	macroIdx := lineIndex(outLines, macroLine)
	installIdx := lineIndex(outLines, "%install")

	require.GreaterOrEqual(t, macroIdx, 0, "hoisted macro must be in output")
	require.GreaterOrEqual(t, installIdx, 0, "%install section must remain")
	assert.Less(t, macroIdx, installIdx,
		"hoisted macro must appear before %%install so the reference resolves")

	// The reference itself must still exist in %install.
	assert.Contains(t, out, "%{buildroot}%{testsdir}/python",
		"surviving %%install must still reference the hoisted macro")

	// Output must re-parse cleanly.
	_, err := spec.OpenSpec(bytes.NewReader([]byte(out)))
	require.NoError(t, err, "spec must re-parse after subpackage removal")
}

// TestTestdataRemoveSubpackageDoesNotHoistUnreferencedMacro verifies the
// negative case: when a `%define` inside a subpackage is only referenced from
// within that same subpackage, removal drops it cleanly (no hoisting needed,
// no noise added to the result).
func TestTestdataRemoveSubpackageDoesNotHoistUnreferencedMacro(t *testing.T) {
	specObj := openFixture(t, "subpackage-define-unreferenced.spec")

	require.NoError(t, specObj.RemoveSubpackage("tools"))

	out := serializeSpec(t, specObj)
	outLines := strings.Split(out, "\n")

	// The macro must be gone -- no `%define toolsdir ...` line anywhere.
	assert.False(t, hasLineWithPrefix(outLines, "%define toolsdir"),
		"unreferenced macro must be dropped along with the subpackage")

	// All subpackage section headers must be gone.
	assert.False(t, hasLineWithPrefix(outLines, "%package tools"),
		"subpackage header must be removed")
	assert.False(t, hasLineWithPrefix(outLines, "%description tools"),
		"subpackage description must be removed")
	assert.False(t, hasLineWithPrefix(outLines, "%files tools"),
		"subpackage files must be removed")

	// Output must re-parse cleanly.
	_, err := spec.OpenSpec(bytes.NewReader([]byte(out)))
	require.NoError(t, err, "spec must re-parse after subpackage removal")
}
