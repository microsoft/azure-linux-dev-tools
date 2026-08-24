// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec //nolint:testpackage // Tests access unexported parser tree types.

import (
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTree(splitLines(tt.input))
			require.Error(t, err)
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
		"%%{literal}",
		"%if 0",
		"ignored",
		"}",
		"%build",
		"echo %{helper}",
	}

	tree, err := parseTree(lines)
	require.NoError(t, err)
	assert.Equal(t, lines, serializeTree(tree))
}

func splitLines(input string) []string {
	return strings.Split(input, "\n")
}
