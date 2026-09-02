// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage,nolintlint // Tests access unexported structural tree internals.
package spec

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestHasSectionStopsAfterFirstMatch(t *testing.T) {
	tree := &specTree{root: &block{
		Kind: rootBlock,
		Children: []*block{
			{Kind: sectionBlock, Name: "%build"},
			nil,
		},
	}}

	assert.NotPanics(t, func() {
		assert.True(t, tree.HasSection("%build"))
	})
}

func TestWalkBlocksStopsBeforeLaterSiblings(t *testing.T) {
	first := &block{Kind: sectionBlock, Name: "%build"}
	later := &block{Kind: sectionBlock, Name: "%check"}
	root := &block{Kind: rootBlock, Children: []*block{first, later}}

	var visited []string

	walkBlocks(root, func(blk *block) bool {
		visited = append(visited, blk.Name)

		return blk != first
	})

	assert.Equal(t, []string{"", "%build"}, visited)
}

func TestWalkBlocksStopsInsideElseBeforeAncestorSiblings(t *testing.T) {
	stop := &block{Kind: sectionBlock, Name: "%stop"}
	innerElseLater := &block{Kind: sectionBlock, Name: "%inner-else-later"}
	outerElseLater := &block{Kind: sectionBlock, Name: "%outer-else-later"}
	rootLater := &block{Kind: sectionBlock, Name: "%root-later"}
	inner := &block{
		Kind:   conditionalBlock,
		Header: "%if inner",
		Else:   []*block{stop, innerElseLater},
	}
	outer := &block{
		Kind:   conditionalBlock,
		Header: "%if outer",
		Else:   []*block{inner, outerElseLater},
	}
	root := &block{Kind: rootBlock, Children: []*block{outer, rootLater}}

	var visited []string

	walkBlocks(root, func(blk *block) bool {
		switch {
		case blk.Header != "":
			visited = append(visited, blk.Header)
		case blk.Kind == rootBlock:
			visited = append(visited, "root")
		default:
			visited = append(visited, blk.Name)
		}

		return blk != stop
	})

	assert.Equal(t, []string{"root", "%if outer", "%if inner", "%stop"}, visited)
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

func TestRemoveSectionsRejectsPreambleTransactionally(t *testing.T) {
	tests := []struct {
		name       string
		removeFunc func(*specTree) []*sectionHandle
		wantLines  []string
	}{
		{
			name: "preamble",
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("", "")
			},
			wantLines: []string{
				"Name: example",
				"%package example-devel",
				"%description example-devel",
				"Development files",
			},
		},
		{
			name: "preamble and section",
			removeFunc: func(tree *specTree) []*sectionHandle {
				return append(tree.Sections("", ""), tree.Sections("%package", "example-devel")...)
			},
			wantLines: []string{
				"Name: example",
				"%package example-devel",
				"%description example-devel",
				"Development files",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specification := newTreeAPISpec(test.wantLines)
			before := slices.Clone(specification.rawLines)

			err := specification.mutateTree(func(tree *specTree) error {
				before := serializeTree(tree.root)

				err := tree.RemoveSections(test.removeFunc(tree))

				require.EqualError(t, err, "cannot remove the global/preamble section")
				assert.Equal(t, before, serializeTree(tree.root))

				return err
			})

			require.EqualError(t, err, "cannot remove the global/preamble section")
			assert.Equal(t, before, specification.rawLines)
		})
	}
}

func TestRemoveSectionsAllowsMainPackageSection(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"Name: example",
		"%build",
		"make",
	})

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.Sections("%build", ""))
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"Name: example"}, specification.rawLines)
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

func TestRemoveSectionsRejectsDirectTailAfterEmptyingWrapper(t *testing.T) {
	tests := []struct {
		name       string
		lines      []string
		removeFunc func(*specTree) []*sectionHandle
	}{
		{
			name: "tag-shaped tail",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"Summary: one",
				"%description",
				"main",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
		},
		{
			name: "description body tail",
			lines: []string{
				"%if 1",
				"%description one",
				"%endif",
				"one description",
				"%files",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%description", "one")
			},
		},
		{
			name: "files body tail",
			lines: []string{
				"%if 1",
				"%files one",
				"%endif",
				"/usr/bin/one",
				"%changelog",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%files", "one")
			},
		},
		{
			name: "script command tail",
			lines: []string{
				"%if 1",
				"%build",
				"%endif",
				"make install",
				"%check",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%build", "")
			},
		},
		{
			name: "global tail",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%global helper value",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
		},
		{
			name: "multiline define tail",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%define helper \\",
				"value",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
		},
		{
			name: "section-free conditional tail",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 0",
				"orphan",
				"%endif",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
		},
		{
			name: "comment then section-free conditional tail",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"# not content",
				"%if 0",
				"orphan",
				"%endif",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
		},
		{
			name: "empty section-free conditional then content",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 0",
				"%endif",
				"%if 0",
				"orphan",
				"%endif",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
		},
		{
			name: "comment-only section-free conditional then content",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 0",
				"# not content",
				"%endif",
				"%if 0",
				"orphan",
				"%endif",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
		},
		{
			name: "conditional section with implicit no-section path then direct content",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 0",
				"%package two",
				"%endif",
				"Summary: one",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
		},
		{
			name: "conditional section in then with meaningful else content",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 0",
				"%package two",
				"%else",
				"Summary: one",
				"%endif",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
		},
		{
			name: "nested elif chain with implicit no-section path then direct content",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 0",
				"%if 0",
				"%package two",
				"%else",
				"%package three",
				"%endif",
				"%elif 0",
				"%package four",
				"%endif",
				"Summary: one",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
		},
		{
			name: "joint removal",
			lines: []string{
				"%if 1",
				"%package one",
				"%description one",
				"%endif",
				"Summary: one",
				"%files",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return append(tree.Sections("%package", "one"), tree.Sections("%description", "one")...)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertSectionRemovalRejectedTransactionally(t, test.lines, test.removeFunc)
		})
	}
}

func TestRemoveSectionsRejectsPostRemovalPathWithoutSectionBoundary(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "direct preceding section then conditional with implicit no-section path",
			lines: []string{
				"%package one",
				"%if 0",
				"%package two",
				"%endif",
				"Summary: common",
				"%description",
			},
		},
		{
			name: "removed then branch and retained else branch",
			lines: []string{
				"%if 0",
				"%package one",
				"%else",
				"%package two",
				"%endif",
				"Summary: common",
				"%description",
			},
		},
		{
			name: "removed elif branch",
			lines: []string{
				"%if 0",
				"%package two",
				"%elif 0",
				"%package one",
				"%else",
				"%package three",
				"%endif",
				"Summary: common",
				"%description",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertSectionRemovalRejectedTransactionally(t, test.lines, func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			})
		})
	}
}

func TestRemoveSectionsAllowsSafeTailsAfterEmptyingWrapper(t *testing.T) {
	tests := []struct {
		name       string
		lines      []string
		removeFunc func(*specTree) []*sectionHandle
		want       []string
	}{
		{
			name: "blank and comment only",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"",
				"  # comment",
				"%description",
				"main",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
			want: []string{
				"%if 1",
				"%endif",
				"",
				"  # comment",
				"%description",
				"main",
			},
		},
		{
			name: "direct section boundary",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%description",
				"main",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
			want: []string{
				"%if 1",
				"%endif",
				"%description",
				"main",
			},
		},
		{
			name: "no following sibling",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
			want: []string{
				"%if 1",
				"%endif",
			},
		},
		{
			name: "section-wrapping conditional boundary",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 0",
				"%package two",
				"%endif",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
			want: []string{
				"%if 1",
				"%endif",
				"%if 0",
				"%package two",
				"%endif",
			},
		},
		{
			name: "all conditional paths establish a section boundary",
			lines: []string{
				"%if 1",
				"%package one",
				"%endif",
				"%if 0",
				"%if 0",
				"%package two",
				"%else",
				"%package three",
				"%endif",
				"%elif 0",
				"%package four",
				"%else",
				"%package five",
				"%endif",
				"Summary: after conditional",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
			want: []string{
				"%if 1",
				"%endif",
				"%if 0",
				"%if 0",
				"%package two",
				"%else",
				"%package three",
				"%endif",
				"%elif 0",
				"%package four",
				"%else",
				"%package five",
				"%endif",
				"Summary: after conditional",
				"%description",
			},
		},
		{
			name: "direct preceding removal with explicit section boundaries",
			lines: []string{
				"%package one",
				"%if 0",
				"%package two",
				"%else",
				"%package three",
				"%endif",
				"Summary: common",
				"%description",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
			want: []string{
				"%if 0",
				"%package two",
				"%else",
				"%package three",
				"%endif",
				"Summary: common",
				"%description",
			},
		},
		{
			name: "partial removal leaves section",
			lines: []string{
				"%if 1",
				"%package one",
				"%description one",
				"%endif",
				"Summary: one",
				"%files",
			},
			removeFunc: func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			},
			want: []string{
				"%if 1",
				"%description one",
				"%endif",
				"Summary: one",
				"%files",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specification := newTreeAPISpec(test.lines)

			err := specification.mutateTree(func(tree *specTree) error {
				return tree.RemoveSections(test.removeFunc(tree))
			})

			require.NoError(t, err)
			assert.Equal(t, test.want, specification.rawLines)
		})
	}
}

func TestRemoveSectionsDoesNotEvaluateConditionalValues(t *testing.T) {
	for _, condition := range []string{"0", "1"} {
		t.Run(condition, func(t *testing.T) {
			assertSectionRemovalRejectedTransactionally(t, []string{
				"%if " + condition,
				"%package one",
				"%endif",
				"Summary: one",
				"%description",
			}, func(tree *specTree) []*sectionHandle {
				return tree.Sections("%package", "one")
			})
		})
	}
}

func TestTreeEditKeepsEscapedBracedMacrosOpaqueInsideExpandBody(t *testing.T) {
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
	specification := newTreeAPISpec(lines)

	err := specification.mutateTree(func(tree *specTree) error {
		section := tree.Section("%build", "")
		require.NotNil(t, section)
		require.Len(t, tree.root.Children, 2)
		require.Len(t, tree.root.Children[0].Children, 1)
		assert.Equal(t, lines[:7], tree.root.Children[0].Children[0].Lines)
		section.AppendLines([]string{"make"})

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, append(lines, "make"), specification.rawLines)
}

func assertSectionRemovalRejectedTransactionally(
	t *testing.T,
	lines []string,
	removeFunc func(*specTree) []*sectionHandle,
) {
	t.Helper()

	specification := newTreeAPISpec(lines)
	before := slices.Clone(specification.rawLines)

	err := specification.mutateTree(func(tree *specTree) error {
		beforeTree := serializeTree(tree.root)

		err := tree.RemoveSections(removeFunc(tree))
		require.ErrorIs(t, err, ErrConditionalSpansSections)
		assert.Equal(t, beforeTree, serializeTree(tree.root))

		return err
	})

	require.ErrorIs(t, err, ErrConditionalSpansSections)
	assert.Equal(t, before, specification.rawLines)
}

func newTreeAPISpec(lines []string) *structuralSpec {
	return &structuralSpec{rawLines: slices.Clone(lines)}
}
