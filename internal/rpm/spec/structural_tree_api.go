// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"errors"
	"fmt"
	"strings"
)

// specTree is an opaque handle for a parsed spec structure.
type specTree struct {
	root *block
}

// sectionHandle refers to one section in a [specTree].
type sectionHandle struct {
	block *block
	tree  *specTree
}

// mutateTree parses the spec, applies mutate, and validates the resulting tree
// before replacing [structuralSpec.rawLines]. Errors leave the spec unchanged.
func (s *structuralSpec) mutateTree(mutate func(*specTree) error) error {
	root, err := parseTree(s.rawLines)
	if err != nil {
		return fmt.Errorf("parsing spec tree:\n%w", err)
	}

	tree := &specTree{root: root}
	if err := mutate(tree); err != nil {
		return err
	}

	lines := serializeTree(root)
	if _, err := parseTree(lines); err != nil {
		return fmt.Errorf("validating mutated spec tree:\n%w", err)
	}

	s.rawLines = lines

	return nil
}

// inspectTree parses the spec and passes its structure to inspect without
// modifying [structuralSpec.rawLines].
func (s *structuralSpec) inspectTree(inspect func(*specTree) error) error {
	root, err := parseTree(s.rawLines)
	if err != nil {
		return fmt.Errorf("parsing spec tree:\n%w", err)
	}

	return inspect(&specTree{root: root})
}

// Section returns the first section with name and pkg, or nil if it is absent.
func (t *specTree) Section(name, pkg string) *sectionHandle {
	for _, section := range t.Sections(name, pkg) {
		return section
	}

	return nil
}

// HasSection reports whether a section with name is present for any package.
func (t *specTree) HasSection(name string) bool {
	found := false

	walkBlocks(t.root, func(blk *block) bool {
		if blk.Kind == sectionBlock && blk.Name == name {
			found = true
		}

		return !found
	})

	return found
}

// Sections returns all matching sections in document order.
func (t *specTree) Sections(name, pkg string) []*sectionHandle {
	var matches []*sectionHandle

	walkBlocks(t.root, func(blk *block) bool {
		if blk.Kind == sectionBlock && blk.Name == name && blk.Package == pkg {
			matches = append(matches, &sectionHandle{block: blk, tree: t})
		}

		return true
	})

	return matches
}

// SectionsByPackage returns every section associated with pkg in document order.
func (t *specTree) SectionsByPackage(pkg string) []*sectionHandle {
	var matches []*sectionHandle

	walkBlocks(t.root, func(blk *block) bool {
		if blk.Kind == sectionBlock && blk.Package == pkg {
			matches = append(matches, &sectionHandle{block: blk, tree: t})
		}

		return true
	})

	return matches
}

// RemoveSections removes sections as one transaction.
func (t *specTree) RemoveSections(handles []*sectionHandle) error {
	sections := make(map[*block]bool, len(handles))
	for _, handle := range handles {
		if handle == nil || handle.tree != t || handle.block == nil {
			return errors.New("section handle does not belong to this spec tree")
		}

		if handle.block.Name == "" && handle.block.Package == "" {
			return errors.New("cannot remove the global/preamble section")
		}

		sections[handle.block] = true
	}

	if err := validateSectionRemoval(t.root, sections); err != nil {
		return err
	}

	removeSections(t.root, sections)

	return nil
}

// Name returns the section keyword. The preamble has an empty name.
func (h *sectionHandle) Name() string {
	return h.block.Name
}

// Package returns the section package qualifier.
func (h *sectionHandle) Package() string {
	return h.block.Package
}

// AppendLines appends lines to the section's content.
func (h *sectionHandle) AppendLines(lines []string) {
	if len(lines) == 0 {
		return
	}

	h.block.Children = append(h.block.Children, &block{Kind: textBlock, Lines: lines})
}

// PrependLines inserts lines immediately after the section header.
func (h *sectionHandle) PrependLines(lines []string) {
	if len(lines) == 0 {
		return
	}

	child := &block{Kind: textBlock, Lines: lines}
	h.block.Children = append([]*block{child}, h.block.Children...)
}

func walkBlocks(blk *block, visit func(*block) bool) bool {
	if !visit(blk) {
		return false
	}

	for _, child := range blk.Children {
		if !walkBlocks(child, visit) {
			return false
		}
	}

	if blk.Kind == conditionalBlock {
		for _, child := range blk.Else {
			if !walkBlocks(child, visit) {
				return false
			}
		}
	}

	return true
}

func removeSections(blk *block, removeSet map[*block]bool) {
	blk.Children = removeSectionBlocks(blk.Children, removeSet)
	if blk.Kind == conditionalBlock {
		blk.Else = removeSectionBlocks(blk.Else, removeSet)
	}

	for _, child := range blk.Children {
		removeSections(child, removeSet)
	}

	if blk.Kind == conditionalBlock {
		for _, child := range blk.Else {
			removeSections(child, removeSet)
		}
	}
}

func removeSectionBlocks(blocks []*block, removeSet map[*block]bool) []*block {
	result := make([]*block, 0, len(blocks))
	for _, blk := range blocks {
		if !removeSet[blk] {
			result = append(result, blk)
		}
	}

	return result
}

func validateSectionRemoval(root *block, removeSet map[*block]bool) error {
	_, err := validatePostRemovalOwnership(root.Children, removeSet, false)

	return err
}

// validatePostRemovalOwnership follows every reachable structural path after
// removal. needsSection is true when a removed section could still be the
// active preceding section on that path.
func validatePostRemovalOwnership(blocks []*block, removeSet map[*block]bool, needsSection bool) (bool, error) {
	for _, blk := range blocks {
		switch blk.Kind {
		case sectionBlock:
			needsSection = removeSet[blk]
		case rootBlock:
			var err error

			needsSection, err = validatePostRemovalOwnership(blk.Children, removeSet, needsSection)
			if err != nil {
				return false, err
			}
		case conditionalBlock:
			var err error

			needsSection, err = validateConditionalPostRemovalOwnership(blk, removeSet, needsSection)
			if err != nil {
				return false, err
			}
		case macroDefBlock:
			if needsSection {
				return false, fmt.Errorf("macro definition at %#q may follow a removed section without a section boundary:\n%w",
					blk.Header, ErrConditionalSpansSections)
			}
		case textBlock:
			if needsSection && hasMeaningfulText(blk.Lines) {
				return false, fmt.Errorf("meaningful text may follow a removed section without a surviving section boundary:\n%w",
					ErrConditionalSpansSections)
			}
		}
	}

	return needsSection, nil
}

func validateConditionalPostRemovalOwnership(
	conditional *block,
	removeSet map[*block]bool,
	needsSection bool,
) (bool, error) {
	thenNeedsSection, err := validatePostRemovalOwnership(conditional.Children, removeSet, needsSection)
	if err != nil {
		return false, err
	}

	if conditional.ElseDirective == "" && len(conditional.Else) == 0 {
		// The condition can be false, leaving the prior ownership unchanged.
		return thenNeedsSection || needsSection, nil
	}

	elseNeedsSection, err := validatePostRemovalOwnership(conditional.Else, removeSet, needsSection)
	if err != nil {
		return false, err
	}

	return thenNeedsSection || elseNeedsSection, nil
}

func hasMeaningfulText(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return true
		}
	}

	return false
}
