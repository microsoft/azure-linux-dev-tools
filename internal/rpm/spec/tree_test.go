// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec //nolint:testpackage // Tests access unexported tree types (block, parseTree, etc.).

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Round-trip tests: parse → serialize must equal original ---

func TestParseTreeRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name: "simple spec",
			input: `Name: simple
Version: 1.0
Release: 1
Summary: A simple package

%description
A simple package.

%build
make

%install
make install

%files
/usr/bin/simple`,
		},
		{
			name: "conditional inside section",
			input: `Name: test
Version: 1.0

%build
%if 0%{?with_debug}
CFLAGS="-g" make
%else
CFLAGS="-O2" make
%endif`,
		},
		{
			name: "conditional wrapping sections",
			input: `Name: test
Version: 1.0

%if 0%{?with_docs}
%package docs
Summary: Documentation

%description docs
Full docs.
%endif

%build
make`,
		},
		{
			name: "else branch with different sections",
			input: `Name: test
Version: 1.0

%if 0%{?with_docs}
%package docs
Summary: Documentation
%description docs
Full docs.
%else
%package minimal-docs
Summary: Minimal documentation
%description minimal-docs
Minimal docs.
%endif

%build
make`,
		},
		{
			name: "straddling conditional",
			input: `Name: test
Version: 1.0

%build
make

%if 0%{?with_extra}
%install
make install EXTRA=1
%endif

%files
/usr/bin/test`,
		},
		{
			name: "macro definitions",
			input: `Name: test
Version: 1.0

%global debug_package %{nil}
%define _builddir %{_topdir}/BUILD

%build
%define buildflags -O2 -Wall
CFLAGS="%{buildflags}" make`,
		},
		{
			name: "multi-line macro definition",
			input: `Name: test
Version: 1.0

%define common_flags \
  -DENABLE_FEATURE=ON \
  -DCMAKE_BUILD_TYPE=Release

%build
cmake %{common_flags} .
make`,
		},
		{
			name: "continuation with section keyword",
			input: `Name: test
Version: 1.0

%global extra_config \
  %files \
  something

%build
make

%files
/usr/bin/test`,
		},
		{
			name: "nested conditionals",
			input: `Name: test
Version: 1.0

%build
%if 0%{?with_feature}
%if 0%{?with_debug}
make debug
%else
make feature
%endif
%endif`,
		},
		{
			name: "complex real-world pattern",
			input: `Name: complex
Version: 2.0
Release: 1
Summary: Complex real-world test
License: MIT

%global debug_package %{nil}
%define _builddir %{_topdir}/BUILD

%description
A complex package.

%package devel
Summary: Development files
Requires: %{name} = %{version}-%{release}

%description devel
Development files for complex.

%if 0%{?with_docs}
%package docs
Summary: Documentation subpackage

%description docs
Full documentation.
%endif

%prep
%autosetup

%build
%define buildroot_flags --prefix=%{_prefix}
%if 0%{?with_debug}
CFLAGS="-g" ./configure %{buildroot_flags}
%else
./configure %{buildroot_flags}
%endif
make %{?_smp_mflags}

%install
make install DESTDIR=%{buildroot}

%if 0%{?with_docs}
%files docs
%doc README.md
%endif

%files
%license LICENSE
/usr/bin/complex

%files devel
/usr/include/complex.h

%changelog`,
		},
		{
			name:  "empty spec",
			input: ``,
		},
		{
			name:  "preamble only",
			input: `Name: preamble-only`,
		},
		{
			name: "comments and blank lines",
			input: `# This is a comment
Name: test

# Another comment

%build
# Build comment
make

%install
make install`,
		},
		{
			name: "multiple conditionals at top level",
			input: `Name: test

%if 0%{?with_a}
%package a
Summary: Package A
%endif

%if 0%{?with_b}
%package b
Summary: Package B
%endif

%build
make`,
		},
		{
			name: "elif chain",
			input: `Name: test

%if 0%{?rhel}
Requires: rhel-thing
%elif 0%{?fedora}
Requires: fedora-thing
%elif 0%{?suse}
Requires: suse-thing
%else
Requires: generic-thing
%endif

%build
make`,
		},
		{
			name: "elif with sections in branches",
			input: `Name: test

%if 0%{?rhel}
%package rhel-extras
Summary: RHEL extras
%elif 0%{?fedora}
%package fedora-extras
Summary: Fedora extras
%else
%package generic-extras
Summary: Generic extras
%endif

%build
make`,
		},
		{
			name: "elif without terminal else",
			input: `Name: test

%if 0%{?rhel}
Requires: rhel-thing
%elif 0%{?fedora}
Requires: fedora-thing
%endif

%build
make`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := splitLines(tt.input)

			root, err := parseTree(lines)
			require.NoError(t, err)

			serialized := serializeTree(root)
			assert.Equal(t, lines, serialized, "round-trip should preserve all lines exactly")
		})
	}
}

//nolint:maintidx // Comprehensive table-driven structural test covering all block kinds.
func TestParseTreeStructure(t *testing.T) {
	t.Run("simple spec sections", func(t *testing.T) {
		input := `Name: test
Version: 1.0

%description
A test.

%build
make

%install
make install

%files
/usr/bin/test`
		lines := splitLines(input)

		root, err := parseTree(lines)
		require.NoError(t, err)

		// Preamble is now wrapped in a sectionBlock with empty name.
		// Named sections are sectionBlock children.
		var sectionNames []string

		for _, child := range root.Children {
			if child.Kind == sectionBlock {
				sectionNames = append(sectionNames, child.Name)
			}
		}

		assert.Equal(t, []string{"", "%description", "%build", "%install", "%files"}, sectionNames)
	})

	t.Run("straddling conditional is wrapper not content", func(t *testing.T) {
		input := `Name: test

%build
make

%if 0%{?with_extra}
%install
make install
%endif

%files
/usr/bin/test`
		lines := splitLines(input)

		root, err := parseTree(lines)
		require.NoError(t, err)

		// %build should NOT contain the %if.
		buildSect := findSectionBlock(root, "%build", "")
		require.NotNil(t, buildSect)

		for _, child := range buildSect.Children {
			assert.NotEqual(t, conditionalBlock, child.Kind,
				"straddling %%if should be a sibling wrapper, not %%build content")
		}

		// %install should be findable inside the conditional wrapper.
		installSect := findSectionBlock(root, "%install", "")
		assert.NotNil(t, installSect, "%%install should be findable inside conditional wrapper")
	})

	t.Run("conditional inside section is content", func(t *testing.T) {
		input := `Name: test

%build
%if 0%{?with_debug}
make debug
%else
make release
%endif`
		lines := splitLines(input)

		root, err := parseTree(lines)
		require.NoError(t, err)

		buildSect := findSectionBlock(root, "%build", "")
		require.NotNil(t, buildSect)

		hasConditional := false

		for _, child := range buildSect.Children {
			if child.Kind == conditionalBlock {
				hasConditional = true

				break
			}
		}

		assert.True(t, hasConditional, "%%if inside section should be a content conditionalBlock")
	})

	t.Run("else branch with sections is wrapper", func(t *testing.T) {
		input := `Name: test

%if 0%{?with_docs}
%package docs
Summary: Docs
%else
%package minimal
Summary: Minimal
%endif

%build
make`
		lines := splitLines(input)

		root, err := parseTree(lines)
		require.NoError(t, err)

		docsSect := findSectionBlock(root, "%package", "docs")
		assert.NotNil(t, docsSect, "%%package docs in then branch")

		minSect := findSectionBlock(root, "%package", "minimal")
		assert.NotNil(t, minSect, "%%package minimal in else branch")
	})

	t.Run("macro def recognized", func(t *testing.T) {
		input := `Name: test

%build
%define buildflags -O2
CFLAGS="%{buildflags}" make`
		lines := splitLines(input)

		root, err := parseTree(lines)
		require.NoError(t, err)

		buildSect := findSectionBlock(root, "%build", "")
		require.NotNil(t, buildSect)

		hasMacroDef := false

		for _, child := range buildSect.Children {
			if child.Kind == macroDefBlock && child.Name == "buildflags" {
				hasMacroDef = true

				break
			}
		}

		assert.True(t, hasMacroDef, "%%define should be recognized as macroDefBlock")
	})

	t.Run("multi-line macro continuation", func(t *testing.T) {
		input := `Name: test

%define flags \
  -DFOO=ON \
  -DBAR=OFF

%build
make`
		lines := splitLines(input)

		root, err := parseTree(lines)
		require.NoError(t, err)

		// The macro should have 3 lines (header + 2 continuation).
		var macroBlock *block

		for _, child := range root.Children {
			if child.Kind == sectionBlock && child.Name == "" {
				// Preamble — look for macro.
				for _, pChild := range child.Children {
					if pChild.Kind == macroDefBlock && pChild.Name == "flags" {
						macroBlock = pChild

						break
					}
				}
			}

			if child.Kind == macroDefBlock && child.Name == "flags" {
				macroBlock = child

				break
			}
		}

		require.NotNil(t, macroBlock, "should find macroDefBlock for 'flags'")
		assert.Len(t, macroBlock.Lines, 3, "multi-line macro should have 3 lines")
	})

	t.Run("continuation with section keyword not a section", func(t *testing.T) {
		input := `Name: test

%global extra \
  %files \
  stuff

%build
make

%files
/usr/bin/test`
		lines := splitLines(input)

		root, err := parseTree(lines)
		require.NoError(t, err)

		// Only one real %files section.
		allFiles := findAllSectionBlocks(root, "%files", "")
		assert.Len(t, allFiles, 1, "continuation body should not create phantom %%files section")
	})

	t.Run("find sections by package", func(t *testing.T) {
		input := `Name: test

%package devel
Summary: Dev

%description devel
Dev files.

%files devel
/usr/include/*

%build
make`
		lines := splitLines(input)

		root, err := parseTree(lines)
		require.NoError(t, err)

		develSections := findAllSectionBlocksByPackage(root, "devel")
		assert.Len(t, develSections, 3, "should find 3 sections for 'devel' package")

		var names []string
		for _, s := range develSections {
			names = append(names, s.Name)
		}

		assert.Contains(t, names, "%package")
		assert.Contains(t, names, "%description")
		assert.Contains(t, names, "%files")
	})

	t.Run("elif chain structure", func(t *testing.T) {
		input := `Name: test

%if 0%{?rhel}
Requires: rhel-thing
%elif 0%{?fedora}
Requires: fedora-thing
%elif 0%{?suse}
Requires: suse-thing
%else
Requires: generic-thing
%endif

%build
make`
		lines := splitLines(input)

		root, err := parseTree(lines)
		require.NoError(t, err)

		// Find the conditional block (inside preamble section as content).
		var cond *block

		for _, child := range root.Children {
			if child.Kind == sectionBlock && child.Name == "" {
				for _, pc := range child.Children {
					if pc.Kind == conditionalBlock {
						cond = pc

						break
					}
				}
			}
		}

		require.NotNil(t, cond, "should find conditional in preamble")
		assert.Equal(t, "%if 0%{?rhel}", cond.Header)
		assert.Equal(t, "%endif", cond.Endif)

		// Then-branch: "Requires: rhel-thing"
		require.Len(t, cond.Children, 1)
		assert.Equal(t, textBlock, cond.Children[0].Kind)
		assert.Equal(t, []string{"Requires: rhel-thing"}, cond.Children[0].Lines)

		// Else is a single conditionalBlock for %elif fedora.
		require.Len(t, cond.Else, 1)
		elif1 := cond.Else[0]
		assert.Equal(t, conditionalBlock, elif1.Kind)
		assert.Equal(t, "%elif 0%{?fedora}", elif1.Header)
		assert.Empty(t, elif1.Endif, "inner elif should not own %%endif")

		// elif1 then-branch: "Requires: fedora-thing"
		require.Len(t, elif1.Children, 1)
		assert.Equal(t, []string{"Requires: fedora-thing"}, elif1.Children[0].Lines)

		// elif1 else is another conditionalBlock for %elif suse.
		require.Len(t, elif1.Else, 1)
		elif2 := elif1.Else[0]
		assert.Equal(t, conditionalBlock, elif2.Kind)
		assert.Equal(t, "%elif 0%{?suse}", elif2.Header)
		assert.Empty(t, elif2.Endif)

		// elif2 then-branch: "Requires: suse-thing"
		require.Len(t, elif2.Children, 1)
		assert.Equal(t, []string{"Requires: suse-thing"}, elif2.Children[0].Lines)

		// elif2 else is a terminal %else with content blocks.
		assert.Equal(t, "%else", elif2.ElseDirective)
		require.Len(t, elif2.Else, 1)
		assert.Equal(t, []string{"Requires: generic-thing"}, elif2.Else[0].Lines)
	})

	t.Run("elif with sections finds all packages", func(t *testing.T) {
		input := `Name: test

%if 0%{?rhel}
%package rhel-extras
Summary: RHEL extras
%elif 0%{?fedora}
%package fedora-extras
Summary: Fedora extras
%else
%package generic-extras
Summary: Generic extras
%endif

%build
make`
		lines := splitLines(input)

		root, err := parseTree(lines)
		require.NoError(t, err)

		for _, pkg := range []string{"rhel-extras", "fedora-extras", "generic-extras"} {
			sect := findSectionBlock(root, "%package", pkg)
			assert.NotNil(t, sect, "should find %%package %s", pkg)
		}
	})
}

func TestParseTreeErrors(t *testing.T) {
	t.Run("unmatched endif", func(t *testing.T) {
		input := `Name: test
%endif`
		lines := splitLines(input)

		_, err := parseTree(lines)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmatched %endif")
	})

	t.Run("unmatched if", func(t *testing.T) {
		input := `Name: test
%if 0%{?foo}`
		lines := splitLines(input)

		_, err := parseTree(lines)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmatched %if")
	})
}

// splitLines splits input into lines. For an empty string, returns a slice with
// one empty element (matching strings.Split behavior).
func splitLines(input string) []string {
	return strings.Split(input, "\n")
}
