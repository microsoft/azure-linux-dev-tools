// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import "fmt"

// specTree is an opaque handle wrapping the parsed structural tree of a spec.
// Operations on the tree are exposed via methods so callers in edit.go do not
// depend on the internal [block] representation. Obtain one via [Spec.mutateTree]
// or [Spec.inspectTree].
type specTree struct {
	root *block
}

// sectionHandle is an opaque reference to a single section within a [specTree].
// Returned by [specTree.Section] / [specTree.Sections] and used to apply edits
// to that section's content.
type sectionHandle struct {
	block *block
	tree  *specTree
}

// mutateTree parses the spec into a tree, runs mutate against it, and serializes
// the tree back into [Spec.rawLines]. If mutate returns an error, [Spec.rawLines]
// is left unchanged.
func (s *Spec) mutateTree(mutate func(*specTree) error) error {
	root, err := parseTree(s.rawLines)
	if err != nil {
		return fmt.Errorf("parsing spec tree:\n%w", err)
	}

	tree := &specTree{root: root}
	if err := mutate(tree); err != nil {
		return err
	}

	s.rawLines = serializeTree(root)

	return nil
}

// inspectTree parses the spec into a tree and passes it to inspect for read-only
// inspection. The tree is discarded after inspect returns; [Spec.rawLines] is
// never modified.
func (s *Spec) inspectTree(inspect func(*specTree) error) error {
	root, err := parseTree(s.rawLines)
	if err != nil {
		return fmt.Errorf("parsing spec tree:\n%w", err)
	}

	return inspect(&specTree{root: root})
}

// --- specTree query API ---

// Section returns a handle to the first section matching name and pkg, or nil
// if no such section exists. Searches recursively into conditional wrappers.
func (t *specTree) Section(name, pkg string) *sectionHandle {
	b := findSectionBlock(t.root, name, pkg)
	if b == nil {
		return nil
	}

	return &sectionHandle{block: b, tree: t}
}

// HasSection reports whether the tree contains any section with the given name
// (regardless of package qualifier), including sections inside conditional
// wrappers.
func (t *specTree) HasSection(name string) bool {
	return hasSectionWithName(t.root, name)
}

func hasSectionWithName(blk *block, name string) bool {
	found := false

	walk(blk, func(b *block) bool {
		if b.Kind == sectionBlock && b.Name == name {
			found = true

			return false
		}

		return true
	})

	return found
}

// Sections returns handles for every section matching name and pkg, including
// sections inside conditional branches (both %if and %else).
func (t *specTree) Sections(name, pkg string) []*sectionHandle {
	return t.handles(findAllSectionBlocks(t.root, name, pkg))
}

// SectionsByPackage returns handles for every section associated with the given
// package name (regardless of section keyword).
func (t *specTree) SectionsByPackage(pkg string) []*sectionHandle {
	return t.handles(findAllSectionBlocksByPackage(t.root, pkg))
}

func (t *specTree) handles(blocks []*block) []*sectionHandle {
	hs := make([]*sectionHandle, len(blocks))
	for i, b := range blocks {
		hs[i] = &sectionHandle{block: b, tree: t}
	}

	return hs
}

// --- specTree mutation API ---

// RemoveSections removes the given sections from the tree. Removal is validated
// as a set: if any one removal would orphan content or break a conditional's
// semantics, the entire operation fails and the tree is left unmodified.
func (t *specTree) RemoveSections(handles []*sectionHandle) error {
	blocks := make([]*block, len(handles))
	for i, h := range handles {
		blocks[i] = h.block
	}

	if err := validateSectionRemoval(t.root, blocks); err != nil {
		return err
	}

	for _, b := range blocks {
		removeBlockFromParent(t.root, b)
	}

	return nil
}

// --- sectionHandle accessors and mutations ---

// Name returns the section's keyword (e.g. "%build"). Empty for the preamble.
func (h *sectionHandle) Name() string { return h.block.Name }

// Package returns the section's package qualifier (e.g. "devel"). Empty for
// sections that target the main package.
func (h *sectionHandle) Package() string { return h.block.Package }

// AppendLines appends the given lines as a new text block at the end of the
// section's content.
func (h *sectionHandle) AppendLines(lines []string) {
	h.block.Children = append(h.block.Children, &block{
		Kind:  textBlock,
		Lines: lines,
	})
}

// PrependLines inserts the given lines as a new text block at the start of the
// section's content (right after the section header).
func (h *sectionHandle) PrependLines(lines []string) {
	newChild := &block{Kind: textBlock, Lines: lines}
	h.block.Children = append([]*block{newChild}, h.block.Children...)
}
