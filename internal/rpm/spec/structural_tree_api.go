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

func walkBlocks(blk *block, visit func(*block) bool) {
	if !visit(blk) {
		return
	}

	for _, child := range blk.Children {
		walkBlocks(child, visit)
	}

	if blk.Kind == conditionalBlock {
		for _, child := range blk.Else {
			walkBlocks(child, visit)
		}
	}
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
	return validateRemovalChildren(root.Children, removeSet, nil)
}

func validateRemovalChildren(children []*block, removeSet map[*block]bool, preceding *block) error {
	for index, child := range children {
		if child.Kind == sectionBlock {
			preceding = child

			continue
		}

		if child.Kind != conditionalBlock {
			continue
		}

		if conditionalHasTextOrMacroContent(child) && containsSectionBlocks(child) {
			if preceding != nil && removeSet[preceding] {
				return fmt.Errorf("%%if block at %#q contains content belonging to the preceding section:\n%w",
					child.Header, ErrConditionalSpansSections)
			}
		}

		if wouldEmptySectionWrapper(child, removeSet) && index+1 < len(children) {
			next := children[index+1]
			if next.Kind == conditionalBlock && !containsSectionBlocks(next) && conditionalHasTextOrMacroContent(next) {
				return fmt.Errorf("content in %%if block at %#q would be orphaned after removing the preceding section:\n%w",
					next.Header, ErrConditionalSpansSections)
			}
		}

		if err := validateRemovalChildren(child.Children, removeSet, preceding); err != nil {
			return err
		}

		if err := validateRemovalChildren(child.Else, removeSet, preceding); err != nil {
			return err
		}
	}

	return nil
}

func conditionalHasTextOrMacroContent(conditional *block) bool {
	return hasTextOrMacroContent(conditional.Children) || hasTextOrMacroContent(conditional.Else)
}

func hasTextOrMacroContent(blocks []*block) bool {
	for _, blk := range blocks {
		if blk.Kind == macroDefBlock {
			return true
		}

		if blk.Kind == textBlock {
			for _, line := range blk.Lines {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
					return true
				}
			}
		}

		if blk.Kind == conditionalBlock &&
			(hasTextOrMacroContent(blk.Children) || hasTextOrMacroContent(blk.Else)) {
			return true
		}
	}

	return false
}

func wouldEmptySectionWrapper(conditional *block, removeSet map[*block]bool) bool {
	if !containsSectionBlocks(conditional) {
		return false
	}

	return !hasRemainingSection(conditional.Children, removeSet) &&
		!hasRemainingSection(conditional.Else, removeSet)
}

func hasRemainingSection(blocks []*block, removeSet map[*block]bool) bool {
	for _, blk := range blocks {
		if blk.Kind == sectionBlock && !removeSet[blk] {
			return true
		}

		if hasRemainingSection(blk.Children, removeSet) {
			return true
		}

		if blk.Kind == conditionalBlock && hasRemainingSection(blk.Else, removeSet) {
			return true
		}
	}

	return false
}
