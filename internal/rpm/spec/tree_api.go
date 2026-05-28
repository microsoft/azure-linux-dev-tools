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

// --- Line-level iteration & mutation ---

// lineHandle is an opaque reference to a single content line within a tree.
// Mutations (Replace, Remove) are queued during iteration and applied when the
// enclosing [specTree.VisitAllLines] / [sectionHandle.VisitLines] call returns,
// so callers can mutate freely during the walk without invalidating indices.
type lineHandle struct {
	// Text is the original line text. Mutations made via Replace do not update
	// this field; callers should treat the visited handle as a single snapshot.
	Text string

	block    *block
	idx      int
	replaced bool
	removed  bool
	newText  string
}

// Replace marks the line for replacement with newText. A subsequent Remove
// overrides any prior Replace; subsequent Replace overrides any prior Remove.
func (lh *lineHandle) Replace(newText string) {
	lh.replaced = true
	lh.removed = false
	lh.newText = newText
}

// Remove marks the line for deletion.
func (lh *lineHandle) Remove() {
	lh.removed = true
	lh.replaced = false
}

// VisitAllLines walks every content line in the spec (text-block lines only;
// macro definitions and section/conditional headers are skipped). The visitor
// receives the enclosing section name and package qualifier plus a handle that
// can buffer Replace/Remove mutations. Mutations are flushed after the walk.
// Returning a non-nil error stops iteration; buffered mutations made prior to
// the error are still flushed.
func (t *specTree) VisitAllLines(visit func(secName, secPkg string, lh *lineHandle) error) error {
	var handles []*lineHandle

	visitErr := collectAndVisitLines(t.root, "", "", visit, &handles)

	flushLineMutations(handles)

	return visitErr
}

// VisitLines walks every content line inside this section, including lines
// nested inside conditional branches. Macro definitions and section/conditional
// headers are skipped. See [specTree.VisitAllLines] for mutation semantics.
func (h *sectionHandle) VisitLines(visit func(lh *lineHandle) error) error {
	var handles []*lineHandle

	wrap := func(_, _ string, lh *lineHandle) error { return visit(lh) }

	visitErr := collectAndVisitLines(h.block, h.block.Name, h.block.Package, wrap, &handles)

	flushLineMutations(handles)

	return visitErr
}

// collectAndVisitLines walks b, calls visit on every text-line, and records
// each handle for later mutation flushing.
func collectAndVisitLines(
	b *block,
	secName, secPkg string,
	visit func(string, string, *lineHandle) error,
	handles *[]*lineHandle,
) error {
	switch b.Kind {
	case rootBlock:
		for _, child := range b.Children {
			if err := collectAndVisitLines(child, secName, secPkg, visit, handles); err != nil {
				return err
			}
		}

	case sectionBlock:
		for _, child := range b.Children {
			if err := collectAndVisitLines(child, b.Name, b.Package, visit, handles); err != nil {
				return err
			}
		}

	case conditionalBlock:
		for _, child := range b.Children {
			if err := collectAndVisitLines(child, secName, secPkg, visit, handles); err != nil {
				return err
			}
		}

		for _, child := range b.Else {
			if err := collectAndVisitLines(child, secName, secPkg, visit, handles); err != nil {
				return err
			}
		}

	case textBlock:
		for i, line := range b.Lines {
			lh := &lineHandle{Text: line, block: b, idx: i}
			*handles = append(*handles, lh)

			if err := visit(secName, secPkg, lh); err != nil {
				return err
			}
		}

	case macroDefBlock:
		// Macro definitions are not visited as content lines.
	}

	return nil
}

// flushLineMutations applies buffered Replace/Remove operations.
// Iterates handles in reverse insertion order so per-block removals don't
// invalidate the indices of yet-to-be-applied operations.
func flushLineMutations(handles []*lineHandle) {
	for i := len(handles) - 1; i >= 0; i-- {
		h := handles[i]

		switch {
		case h.removed:
			h.block.Lines = append(h.block.Lines[:h.idx], h.block.Lines[h.idx+1:]...)
		case h.replaced:
			h.block.Lines[h.idx] = h.newText
		}
	}
}

// --- Tag-aware insertion ---

// InsertTag inserts a tag-style line into this section, placing it after the
// last existing tag from the same family (e.g., "Source9999" lands after the
// last Source* tag). If no same-family tag exists, the new tag goes after the
// last tag of any kind. If the section has no tags at all, the new line is
// appended to the section's end.
//
// If the chosen anchor tag lives inside a [conditionalBlock], the new tag is
// inserted after the entire conditional block instead, so it remains
// unconditional.
func (h *sectionHandle) InsertTag(tag, value, family string) {
	newLine := fmt.Sprintf("%s: %s", tag, value)

	anchor := h.findTagInsertAnchor(family)
	if anchor == nil {
		h.AppendLines([]string{newLine})

		return
	}

	anchor.insertAfter(h.block, newLine)
}

// tagInsertAnchor records where a new tag should be placed relative to an
// existing tag. Exactly one of inTextBlock or afterChild is set:
//   - inTextBlock != nil: the anchor tag is a direct line inside a textBlock;
//     the new line is spliced into that block right after lineIdx.
//   - afterChild != nil: the anchor tag lives inside a top-level conditionalBlock
//     of the section; the new line goes into a new sibling textBlock immediately
//     after afterChild in the section's Children.
type tagInsertAnchor struct {
	inTextBlock *block
	lineIdx     int
	afterChild  *block
}

func (a *tagInsertAnchor) insertAfter(parent *block, newLine string) {
	if a.inTextBlock != nil {
		lines := a.inTextBlock.Lines
		spliced := make([]string, 0, len(lines)+1)
		spliced = append(spliced, lines[:a.lineIdx+1]...)
		spliced = append(spliced, newLine)
		spliced = append(spliced, lines[a.lineIdx+1:]...)
		a.inTextBlock.Lines = spliced

		return
	}

	childIdx := -1

	for i, child := range parent.Children {
		if child == a.afterChild {
			childIdx = i

			break
		}
	}

	newChild := &block{Kind: textBlock, Lines: []string{newLine}}

	if childIdx < 0 {
		// Defensive: anchor not found in parent.Children — append.
		parent.Children = append(parent.Children, newChild)

		return
	}

	spliced := make([]*block, 0, len(parent.Children)+1)
	spliced = append(spliced, parent.Children[:childIdx+1]...)
	spliced = append(spliced, newChild)
	spliced = append(spliced, parent.Children[childIdx+1:]...)
	parent.Children = spliced
}

// findTagInsertAnchor walks the section's top-level children to find the last
// tag matching family (preferred) or the last tag of any kind (fallback).
// Returns nil if the section contains no tags.
func (h *sectionHandle) findTagInsertAnchor(family string) *tagInsertAnchor {
	var lastAny, lastFamily *tagInsertAnchor

	for _, child := range h.block.Children {
		switch child.Kind {
		case textBlock:
			for i, line := range child.Lines {
				tag, _, isTag := parseTagLine(line)
				if !isTag {
					continue
				}

				anchor := &tagInsertAnchor{inTextBlock: child, lineIdx: i}
				lastAny = anchor

				if tagFamily(tag) == family {
					lastFamily = anchor
				}
			}

		case conditionalBlock:
			hasAny, hasFamily := scanConditionalForTags(child, family)
			if hasAny {
				anchor := &tagInsertAnchor{afterChild: child}
				lastAny = anchor

				if hasFamily {
					lastFamily = anchor
				}
			}

		case rootBlock, sectionBlock, macroDefBlock:
			// Not encountered as a section child (or carry no tag lines).
		}
	}

	if lastFamily != nil {
		return lastFamily
	}

	return lastAny
}

// scanConditionalForTags reports whether the conditional block (any branch,
// any nesting depth) contains at least one tag line, and whether any of those
// tags belongs to the given family.
func scanConditionalForTags(cond *block, family string) (hasAny, hasFamily bool) {
	scan := func(blocks []*block) {
		for _, b := range blocks {
			a, f := scanForTags(b, family)
			hasAny = hasAny || a
			hasFamily = hasFamily || f
		}
	}

	scan(cond.Children)
	scan(cond.Else)

	return hasAny, hasFamily
}

func scanForTags(b *block, family string) (hasAny, hasFamily bool) {
	switch b.Kind {
	case textBlock:
		for _, line := range b.Lines {
			tag, _, isTag := parseTagLine(line)
			if !isTag {
				continue
			}

			hasAny = true

			if tagFamily(tag) == family {
				hasFamily = true
			}
		}

	case conditionalBlock:
		return scanConditionalForTags(b, family)

	case rootBlock, sectionBlock, macroDefBlock:
		// Not searched for tags here.
	}

	return hasAny, hasFamily
}

// parseTagLine attempts to parse line as an RPM tag line ("Name: value").
// Returns the tag name and value, or ok=false if line is not a tag.
func parseTagLine(line string) (tag, value string, ok bool) {
	const reSubmatchCount = 3

	matches := tagRegex.FindStringSubmatch(line)
	if len(matches) != reSubmatchCount {
		return "", "", false
	}

	return matches[1], matches[2], true
}

// isTagBearingSection reports whether a section keyword can legally hold RPM
// tag declarations (e.g. "Name:", "Source0:"). Only the preamble (empty name)
// and "%package" sections qualify. Script-style sections like "%build" may
// contain shell that happens to match the "word: word" pattern; we must avoid
// treating those as tags.
func isTagBearingSection(secName string) bool {
	return secName == "" || secName == "%package"
}

