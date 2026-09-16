// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec //nolint:testpackage // Tests access unexported parser tree types.

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTreeRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "whitespace", input: " \t \n\t"},
		{name: "comment-only conditional", input: "%if 1\n# then\n%else\n# else\n%endif"},
		{name: "empty else", input: "%if 1\n%else\n%endif"},
		{name: "terminal elif", input: "%if 1\n%elif 0\n%endif"},
		{name: "nested wrappers", input: strings.Join([]string{
			"%ifarch x86_64", "%package x", "%ifnos linux", "%description x", "ignored",
			"%endif", "%else", "%package y", "%endif",
		}, "\n")},
		{name: "elif with sections", input: strings.Join([]string{
			"%if 1", "%package first", "%elifarch x86_64", "%package second", "%else",
			"%package third", "%endif",
		}, "\n")},
		{name: "macro continuation with directives", input: "%if 1\n%define flags \\\n%else \\\n%if 0 \\\nbody\n%endif"},
		{name: "ordinary continuation followed by structure", input: strings.Join([]string{
			"%build", `configure \`, "%if 1", "make", "%endif", "%files", "/bin/example",
		}, "\n")},
		{name: "parameterized macro", input: strings.Join([]string{
			`%define configure(name:) %{name} \`, "  --enabled", "%build", "echo %{configure test}",
		}, "\n")},
		{name: "lua raw braces strings and expansions", input: `%global helper %{lua:
local value = { nested = %{version}, literal = "}", escaped = "\}" }
print(value.nested)
}
%build
echo %{helper}`},
		{name: "macro expand body with shell parameter expansion", input: strings.Join([]string{
			"%define gobuild(o:) %{expand:",
			"  %if 0",
			`  go build -tags="${BUILDTAGS:-}" %{?**}`,
			"  %else",
			"  go build %{?**}",
			"  %endif",
			"}",
			"Release: 7%{?dist}",
		}, "\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := splitLines(tt.input)
			tree, err := parseTree(lines)
			require.NoError(t, err)
			assert.Equal(t, lines, serializeTree(tree))
		})
	}
}

func TestParseTreeRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "unterminated conditional", input: "%if 1\n%build"},
		{name: "unterminated macro continuation", input: "%global flags \\\nbody \\"},
		{name: "unterminated lua macro", input: "%global helper %{lua:\nlocal value = {}\n%build"},
		{name: "else outside conditional", input: "%else"},
		{name: "elif outside conditional", input: "%elif 0"},
		{name: "elifarch outside conditional", input: "%elifarch x86_64"},
		{name: "elifnarch outside conditional", input: "%elifnarch x86_64"},
		{name: "elifos outside conditional", input: "%elifos linux"},
		{name: "elifnos outside conditional", input: "%elifnos linux"},
		{name: "duplicate else", input: "%if 1\n%else\n%else\n%endif"},
		{name: "elif after else", input: "%if 1\n%else\n%elif 0\n%endif"},
		{name: "elifarch after else", input: "%if 1\n%else\n%elifarch x86_64\n%endif"},
		{name: "elifnarch after else", input: "%if 1\n%else\n%elifnarch x86_64\n%endif"},
		{name: "elifos after else", input: "%if 1\n%else\n%elifos linux\n%endif"},
		{name: "elifnos after else", input: "%if 1\n%else\n%elifnos linux\n%endif"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTree(splitLines(tt.input))
			require.Error(t, err)
		})
	}
}

func TestParseTreeAcceptsElifChainBeforeElse(t *testing.T) {
	lines := []string{
		"%if 1",
		"then",
		"%elif 0",
		"elif",
		"%elifarch x86_64",
		"elifarch",
		"%elifnarch aarch64",
		"elifnarch",
		"%elifos linux",
		"elifos",
		"%elifnos linux",
		"elifnos",
		"%else",
		"else",
		"%endif",
	}

	tree, err := parseTree(lines)

	require.NoError(t, err)
	assert.Equal(t, lines, serializeTree(tree))
}

func TestParseTreeAcceptsNestedConditionalBranches(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "outer elif after completed inner else",
			lines: []string{
				"%if 1",
				"%if 0",
				"%else",
				"%endif",
				"%elif 0",
				"%endif",
			},
		},
		{
			name: "nested elif inside outer else",
			lines: []string{
				"%if 1",
				"%else",
				"%if 0",
				"%elif 1",
				"%endif",
				"%endif",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, err := parseTree(tt.lines)

			require.NoError(t, err)
			assert.Equal(t, tt.lines, serializeTree(tree))
		})
	}
}

func TestParseTreeKeepsBranchLinesOpaqueInMultilineMacroBodies(t *testing.T) {
	for _, macroHeader := range []string{"%define helper \\", "%global helper \\"} {
		t.Run(macroHeader, func(t *testing.T) {
			lines := []string{
				macroHeader,
				"%else \\",
				"%elif 0 \\",
				"%elifarch x86_64 \\",
				"%elifnarch aarch64 \\",
				"%elifos linux \\",
				"%elifnos linux \\",
				"body",
				"%build",
				"echo %{helper}",
			}

			tree, err := parseTree(lines)

			require.NoError(t, err)
			assert.Equal(t, lines, serializeTree(tree))
		})
	}
}

func TestIsElifDirectiveIgnoresWhitespace(t *testing.T) {
	assert.False(t, isElifDirective(" \t "))
	assert.True(t, isElifDirective("%elif 0"))
}

func TestPercentRunOpensBracedMacro(t *testing.T) {
	tests := []struct {
		run   string
		opens bool
	}{
		{run: "%%", opens: false},
		{run: "%%%", opens: true},
		{run: "%%%%", opens: false},
		{run: "%%%%%", opens: true},
	}

	for _, test := range tests {
		t.Run(test.run, func(t *testing.T) {
			assert.Equal(t, test.opens, percentRunOpensBracedMacro(test.run+"{macro}", 0))
		})
	}
}

func TestParseTreeTreatsLivePercentRunMacroBodiesAsAtomic(t *testing.T) {
	lines := []string{
		"%global helper %%%{",
		"%endif",
		"}",
		"%build",
		"echo %{helper}",
	}

	tree, err := parseTree(lines)

	require.NoError(t, err)
	assert.Equal(t, lines, serializeTree(tree))
}

func TestParseTreeDoesNotContinueMacroForStandaloneEscapedBracedOpeners(t *testing.T) {
	for _, macro := range []string{"%define helper %%{", "%global helper %%{"} {
		t.Run(macro, func(t *testing.T) {
			lines := []string{
				macro,
				"%build",
				"make",
				"%check",
				"make check",
			}

			tree, err := parseTree(lines)

			require.NoError(t, err)
			assert.Equal(t, []int{1, 3}, findSectionHeaderLines(lines))
			assert.Equal(t, lines, serializeTree(tree))
			require.Len(t, tree.Children, 3)
			assert.Equal(t, "%build", tree.Children[1].Name)
			assert.Equal(t, "%check", tree.Children[2].Name)
		})
	}
}

func TestParseTreeTracksEvenAndOddPercentRunsAtMacroDefinitionBoundary(t *testing.T) {
	tests := []struct {
		name          string
		macroLines    []string
		sectionHeader []int
	}{
		{
			name:          "four percent escaped opener",
			macroLines:    []string{"%global helper %%%%{"},
			sectionHeader: []int{1, 3},
		},
		{
			name:          "five percent live opener",
			macroLines:    []string{"%global helper %%%%%{", "}"},
			sectionHeader: []int{2, 4},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lines := append(slices.Clone(test.macroLines), []string{
				"%build",
				"make",
				"%check",
				"make check",
			}...)

			tree, err := parseTree(lines)

			require.NoError(t, err)
			assert.Equal(t, test.sectionHeader, findSectionHeaderLines(lines))
			assert.True(t, treeHasSection(tree, "%build"))
			assert.True(t, treeHasSection(tree, "%check"))
			require.Len(t, tree.Children, 3)
			require.Len(t, tree.Children[0].Children, 1)
			assert.Equal(t, test.macroLines, tree.Children[0].Children[0].Lines)
		})
	}
}

func TestParseTreeKeepsEvenAndOddPercentRunBodiesOpaqueInsideOuterMacro(t *testing.T) {
	for _, percentRun := range []string{"%%%%{", "%%%%%{"} {
		t.Run(percentRun, func(t *testing.T) {
			lines := []string{
				"%global helper %{expand:",
				percentRun,
				"}",
				"%build",
				"make",
				"}",
				"%check",
				"make check",
			}

			tree, err := parseTree(lines)

			require.NoError(t, err)
			assert.Equal(t, []int{6}, findSectionHeaderLines(lines))
			assert.False(t, treeHasSection(tree, "%build"))
			assert.True(t, treeHasSection(tree, "%check"))
			require.Len(t, tree.Children, 2)
			require.Len(t, tree.Children[0].Children, 1)
			assert.Equal(t, lines[:6], tree.Children[0].Children[0].Lines)
		})
	}
}

func TestParseTreeTreatsTrailingPercentRunsAsLiteralMacroContent(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name:  "define single trailing percent",
			lines: []string{"%define helper %", "%build", "echo %{helper}"},
		},
		{
			name:  "global even trailing percent run",
			lines: []string{"%global helper %%", "%build", "echo %{helper}"},
		},
		{
			name:  "define odd trailing percent run",
			lines: []string{"%define helper %%%", "%build", "echo %{helper}"},
		},
		{
			name:  "global even multiple trailing percent run",
			lines: []string{"%global helper %%%%", "%build", "echo %{helper}"},
		},
		{
			name: "continued intermediate line",
			lines: []string{
				"%define helper \\",
				"value %\\",
				"final",
				"%build",
				"echo %{helper}",
			},
		},
		{
			name: "continued final line",
			lines: []string{
				"%global helper \\",
				"value %%%%",
				"%build",
				"echo %{helper}",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tree, err := parseTree(test.lines)
			require.NoError(t, err)
			assert.Equal(t, test.lines, serializeTree(tree))
		})
	}
}

func TestParseTreeKeepsEscapedBracedMacrosOpaqueInsideExpandBody(t *testing.T) {
	lines := []string{
		"%global helper %{expand:",
		"%%{",
		"}",
		"%if 0",
		"ignored",
		"%endif",
		"}",
		"%build",
		"echo %{helper}",
	}

	tree, err := parseTree(lines)
	require.NoError(t, err)
	assert.Equal(t, lines, serializeTree(tree))
	assert.Equal(t, []int{7}, findSectionHeaderLines(lines))
	require.Len(t, tree.Children, 2)
	require.Len(t, tree.Children[0].Children, 1)
	assert.Equal(t, lines[:7], tree.Children[0].Children[0].Lines)
}

func splitLines(input string) []string {
	return strings.Split(input, "\n")
}

func treeHasSection(root *block, name string) bool {
	if root.Kind == sectionBlock && root.Name == name {
		return true
	}

	for _, child := range root.Children {
		if treeHasSection(child, name) {
			return true
		}
	}

	for _, child := range root.Else {
		if treeHasSection(child, name) {
			return true
		}
	}

	return false
}
