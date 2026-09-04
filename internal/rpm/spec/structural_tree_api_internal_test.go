// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVisitAllLinesTracksPhysicalLineNumbersThroughConditionals(t *testing.T) {
	tests := []struct {
		name     string
		lines    []string
		expected []int
	}{
		{
			name: "one elif",
			lines: []string{
				"%if 1",
				"then",
				"%elif 0",
				"elif",
				"%endif",
				"after",
			},
			expected: []int{1, 3, 5},
		},
		{
			name: "multiple elif",
			lines: []string{
				"%if 1",
				"then",
				"%elif 0",
				"first elif",
				"%elif 0",
				"second elif",
				"%endif",
				"after",
			},
			expected: []int{1, 3, 5, 7},
		},
		{
			name: "elif and else",
			lines: []string{
				"%if 1",
				"then",
				"%elif 0",
				"elif",
				"%else",
				"else",
				"%endif",
				"after",
			},
			expected: []int{1, 3, 5, 7},
		},
		{
			name: "nested if within elif",
			lines: []string{
				"%if 1",
				"then",
				"%elif 0",
				"%if 1",
				"nested",
				"%endif",
				"elif",
				"%endif",
				"after",
			},
			expected: []int{1, 4, 6, 8},
		},
		{
			name: "ordinary if and else",
			lines: []string{
				"%if 1",
				"then",
				"%else",
				"else",
				"%endif",
				"after",
			},
			expected: []int{1, 3, 5},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specification := newTreeAPISpec(test.lines)

			var lineNumbers []int

			err := specification.inspectTree(func(tree *specTree) error {
				return tree.VisitAllLines(func(_, _ string, line *lineHandle) error {
					lineNumbers = append(lineNumbers, line.lineNumber)

					return nil
				})
			})

			require.NoError(t, err)
			assert.Equal(t, test.expected, lineNumbers)
		})
	}
}

func TestInspectTreeQueriesSectionsInDocumentOrder(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"Name: example",
		"%package -n example-devel",
		"%description -n example-devel",
		"%ifarch x86_64",
		"%files -n example-devel",
		"%else",
		"%files -n example-devel",
		"%endif",
	})

	err := specification.inspectTree(func(tree *specTree) error {
		section := tree.Section("%description", "example-devel")
		require.NotNil(t, section)
		assert.Equal(t, "%description", section.Name())
		assert.Equal(t, "example-devel", section.Package())
		assert.True(t, tree.HasSection("%files"))
		assert.False(t, tree.HasSection("%check"))
		assert.Len(t, tree.Sections("%files", "example-devel"), 2)
		assert.Len(t, tree.SectionsByPackage("example-devel"), 4)
		assert.Empty(t, tree.Sections("%build", ""))

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"Name: example",
		"%package -n example-devel",
		"%description -n example-devel",
		"%ifarch x86_64",
		"%files -n example-devel",
		"%else",
		"%files -n example-devel",
		"%endif",
	}, specification.rawLines)
}

func TestHasSectionRemainsFoundAfterLaterNonMatchingBlocks(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%build",
		"make",
		"%check",
		"make check",
	})

	err := specification.inspectTree(func(tree *specTree) error {
		assert.True(t, tree.HasSection("%build"))

		return nil
	})

	require.NoError(t, err)
}

func TestTreeWrappersAreTransactional(t *testing.T) {
	t.Run("callback error", func(t *testing.T) {
		specification := newTreeAPISpec([]string{"%build", "make"})
		before := append([]string(nil), specification.rawLines...)

		err := specification.mutateTree(func(tree *specTree) error {
			tree.Section("%build", "").AppendLines([]string{"make install"})

			return errors.New("stop")
		})

		require.EqualError(t, err, "stop")
		assert.Equal(t, before, specification.rawLines)
	})

	t.Run("validation error", func(t *testing.T) {
		specification := newTreeAPISpec([]string{"%if 1", "%build", "make", "%endif"})
		before := append([]string(nil), specification.rawLines...)

		err := specification.mutateTree(func(tree *specTree) error {
			tree.root.Children[1].Endif = ""

			return nil
		})

		require.Error(t, err)
		assert.Equal(t, before, specification.rawLines)
	})

	t.Run("malformed source", func(t *testing.T) {
		specification := newTreeAPISpec([]string{"%if 1", "%build"})
		before := append([]string(nil), specification.rawLines...)

		err := specification.inspectTree(func(*specTree) error {
			t.Fatal("inspect callback must not run for malformed input")

			return nil
		})

		require.Error(t, err)
		assert.Equal(t, before, specification.rawLines)
	})
}

func TestSectionLinePrimitivesPreserveOrder(t *testing.T) {
	specification := newTreeAPISpec([]string{"%build", "make"})

	err := specification.mutateTree(func(tree *specTree) error {
		section := tree.Section("%build", "")
		require.NotNil(t, section)
		section.PrependLines([]string{"setup"})
		section.AppendLines([]string{"make install"})

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"%build", "setup", "make", "make install"}, specification.rawLines)
}

func TestSectionLinePrimitivesHandleEmptySections(t *testing.T) {
	specification := newTreeAPISpec([]string{"%build", "%check"})

	err := specification.mutateTree(func(tree *specTree) error {
		section := tree.Section("%build", "")
		require.NotNil(t, section)
		section.AppendLines([]string{"make"})

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"%build", "make", "%check"}, specification.rawLines)
}

func TestRemoveSectionsPreservesConditionalBalance(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%if 1",
		"%package one",
		"%description one",
		"one",
		"%else",
		"%package two",
		"%description two",
		"two",
		"%endif",
	})

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("one"))
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"%if 1",
		"%else",
		"%package two",
		"%description two",
		"two",
		"%endif",
	}, specification.rawLines)
	_, err = parseTree(specification.rawLines)
	require.NoError(t, err)
}

func TestRemoveSectionsRejectsOrphanedConditionalContent(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package one",
		"%if 1",
		"shared",
		"%package two",
		"%endif",
	})

	err := specification.inspectTree(func(tree *specTree) error {
		before := serializeTree(tree.root)
		err := tree.RemoveSections(tree.Sections("%package", "one"))
		require.ErrorIs(t, err, ErrConditionalSpansSections)
		assert.Equal(t, before, serializeTree(tree.root))

		return nil
	})

	require.NoError(t, err)
}

func TestRemoveSectionsRejectsOrphanedConditionalBranchContent(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "then",
			lines: []string{
				"%package one",
				"%if 1",
				"orphan",
				"%package two",
				"%endif",
			},
		},
		{
			name: "else",
			lines: []string{
				"%package one",
				"%if 1",
				"%package two",
				"%else",
				"orphan",
				"%package three",
				"%endif",
			},
		},
		{
			name: "elif",
			lines: []string{
				"%package one",
				"%if 1",
				"%package two",
				"%elif 0",
				"orphan",
				"%package three",
				"%endif",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specification := newTreeAPISpec(test.lines)
			before := append([]string(nil), specification.rawLines...)

			err := specification.mutateTree(func(tree *specTree) error {
				return tree.RemoveSections(tree.Sections("%package", "one"))
			})

			require.ErrorIs(t, err, ErrConditionalSpansSections)
			assert.Equal(t, before, specification.rawLines)
		})
	}
}

func TestRemoveSectionsRejectsOrphanedAdjacentConditionalBranchContent(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "else",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 1",
				"%else",
				"orphan",
				"%endif",
			},
		},
		{
			name: "elif",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 1",
				"%elif 0",
				"orphan",
				"%endif",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specification := newTreeAPISpec(test.lines)
			before := append([]string(nil), specification.rawLines...)

			err := specification.mutateTree(func(tree *specTree) error {
				return tree.RemoveSections(tree.Sections("%package", "one"))
			})

			require.ErrorIs(t, err, ErrConditionalSpansSections)
			assert.Equal(t, before, specification.rawLines)
		})
	}
}

func TestRemoveSectionsAllowsIndependentConditionalBranchRemoval(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%if 1",
		"%package one",
		"%else",
		"%package two",
		"%endif",
	})

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.Sections("%package", "one"))
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"%if 1",
		"%else",
		"%package two",
		"%endif",
	}, specification.rawLines)
}

func TestRemoveSectionsAllowsEmptyOrCommentOnlyAdjacentElse(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "empty",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 1",
				"%else",
				"%endif",
			},
		},
		{
			name: "comment only",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 1",
				"%else",
				"# not content",
				"%endif",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specification := newTreeAPISpec(test.lines)

			err := specification.mutateTree(func(tree *specTree) error {
				return tree.RemoveSections(tree.Sections("%package", "one"))
			})

			require.NoError(t, err)
		})
	}
}

func TestHasTextOrMacroContentRecursesThroughConditionalBranches(t *testing.T) {
	directivesOnly := []*block{{
		Kind:   conditionalBlock,
		Header: "%if 1",
		Else: []*block{{
			Kind:   conditionalBlock,
			Header: "%else",
		}},
	}}
	assert.False(t, hasTextOrMacroContent(directivesOnly))

	nestedContent := []*block{{
		Kind:   conditionalBlock,
		Header: "%if 1",
		Else: []*block{{
			Kind:   conditionalBlock,
			Header: "%else",
			Children: []*block{{
				Kind:  macroDefBlock,
				Lines: []string{"%global helper value"},
			}},
		}},
	}}
	assert.True(t, hasTextOrMacroContent(nestedContent))

	assert.False(t, hasTextOrMacroContent([]*block{{
		Kind:  textBlock,
		Lines: []string{"", "  ", "# comment"},
	}}))
}

func TestTreeEditKeepsEscapedBracedMacrosOpaqueInsideExpandBody(t *testing.T) {
	lines := []string{
		"%global helper %{expand:",
		"%%{literal}",
		"%if 0",
		"ignored",
		"}",
		"%build",
		"echo %{helper}",
	}
	specification := newTreeAPISpec(lines)

	err := specification.mutateTree(func(tree *specTree) error {
		section := tree.Section("%build", "")
		require.NotNil(t, section)
		section.AppendLines([]string{"make"})

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, append(lines, "make"), specification.rawLines)
}

func newTreeAPISpec(lines []string) *structuralSpec {
	return &structuralSpec{rawLines: slices.Clone(lines)}
}
