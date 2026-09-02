// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

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

// inspectTree parses the spec into a tree and passes it to inspect for read-only
// inspection. The tree is discarded after inspect returns; [Spec.rawLines] is
// never modified.
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

// GetTag returns the first tag matching name in the requested package.
func (t *specTree) GetTag(pkg, name string) (string, bool) {
	var (
		value string
		found bool
	)

	_ = t.VisitAllLines(func(secName, secPkg string, line *lineHandle) error {
		if found || secPkg != pkg || !isTagBearingSection(secName) {
			return nil
		}

		tag, tagValue, isTag := parseTagLine(line.Text)
		if isTag && strings.EqualFold(tag, name) {
			value = tagValue
			found = true
		}

		return nil
	})

	return value, found
}

// GetLastTag returns the last lexical tag matching name in the requested package.
func (t *specTree) GetLastTag(pkg, name string) (string, bool) {
	var (
		value string
		found bool
	)

	_ = t.VisitAllLines(func(secName, secPkg string, line *lineHandle) error {
		if secPkg != pkg || !isTagBearingSection(secName) {
			return nil
		}

		tag, tagValue, isTag := parseTagLine(line.Text)
		if isTag && strings.EqualFold(tag, name) {
			value = tagValue
			found = true
		}

		return nil
	})

	return value, found
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

	if err := t.hoistReferencedMacros(sections); err != nil {
		return err
	}

	removeSections(t.root, sections)

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

	block      *block
	idx        int
	replaced   bool
	removed    bool
	newText    string
	lineNumber int
	before     []string
	after      []string
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

// InsertBefore queues lines immediately before this line.
func (lh *lineHandle) InsertBefore(lines []string) {
	lh.before = append(lh.before, lines...)
}

// InsertAfter queues lines immediately after this line.
func (lh *lineHandle) InsertAfter(lines []string) {
	lh.after = append(lh.after, lines...)
}

// VisitAllLines walks every content line in the spec (text-block lines only;
// macro definitions and section/conditional headers are skipped). The visitor
// receives the enclosing section name and package qualifier plus a handle that
// can buffer Replace/Remove mutations. Mutations are flushed after the walk.
// Returning a non-nil error stops iteration; buffered mutations made prior to
// the error are still flushed.
func (t *specTree) VisitAllLines(visit func(secName, secPkg string, lh *lineHandle) error) error {
	var handles []*lineHandle

	lineNumber := 0

	visitErr := collectAndVisitLines(t.root, "", "", visit, &handles, &lineNumber)

	flushLineMutations(handles)

	return visitErr
}

// VisitLines walks every content line inside this section, including lines
// nested inside conditional branches. Macro definitions and section/conditional
// headers are skipped. See [specTree.VisitAllLines] for mutation semantics.
func (h *sectionHandle) VisitLines(visit func(lh *lineHandle) error) error {
	var handles []*lineHandle

	lineNumber := 0

	wrap := func(_, _ string, lh *lineHandle) error { return visit(lh) }

	visitErr := collectAndVisitLines(h.block, h.block.Name, h.block.Package, wrap, &handles, &lineNumber)

	flushLineMutations(handles)

	return visitErr
}

// collectAndVisitLines walks blk, calls visit on every text-line, and records
// each handle for later mutation flushing.
//
//nolint:cyclop,gocognit // Switch over blockKind with a small recursive call per kind; splitting hurts readability.
func collectAndVisitLines(
	blk *block,
	secName, secPkg string,
	visit func(string, string, *lineHandle) error,
	handles *[]*lineHandle,
	lineNumber *int,
) error {
	switch blk.Kind {
	case rootBlock:
		for _, child := range blk.Children {
			if err := collectAndVisitLines(child, secName, secPkg, visit, handles, lineNumber); err != nil {
				return err
			}
		}

	case sectionBlock:
		if blk.Name != "" {
			*lineNumber++
		}

		for _, child := range blk.Children {
			if err := collectAndVisitLines(child, blk.Name, blk.Package, visit, handles, lineNumber); err != nil {
				return err
			}
		}

	case conditionalBlock:
		*lineNumber++
		for _, child := range blk.Children {
			if err := collectAndVisitLines(child, secName, secPkg, visit, handles, lineNumber); err != nil {
				return err
			}
		}

		if blk.ElseDirective != "" {
			*lineNumber++
		}

		for _, child := range blk.Else {
			if err := collectAndVisitLines(child, secName, secPkg, visit, handles, lineNumber); err != nil {
				return err
			}
		}

		if blk.Endif != "" {
			*lineNumber++
		}

	case textBlock:
		for i, line := range blk.Lines {
			handle := &lineHandle{Text: line, block: blk, idx: i, lineNumber: *lineNumber}
			*lineNumber++

			*handles = append(*handles, handle)

			if err := visit(secName, secPkg, handle); err != nil {
				return err
			}
		}

	case macroDefBlock:
		// Macro definitions are not visited as content lines.
		*lineNumber += len(blk.Lines)
	}

	return nil
}

// flushLineMutations applies buffered Replace/Remove operations.
// Iterates handles in reverse insertion order so per-block removals don't
// invalidate the indices of yet-to-be-applied operations.
func flushLineMutations(handles []*lineHandle) {
	for i := len(handles) - 1; i >= 0; i-- {
		handle := handles[i]

		line := handle.block.Lines[handle.idx]
		if handle.replaced {
			line = handle.newText
		}

		replacement := append([]string{}, handle.before...)
		if !handle.removed {
			replacement = append(replacement, line)
		}

		replacement = append(replacement, handle.after...)
		handle.block.Lines = append(handle.block.Lines[:handle.idx],
			append(replacement, handle.block.Lines[handle.idx+1:]...)...)
	}
}

// tagRegex matches RPM tag lines in the form "Name: value".
var tagRegex = regexp.MustCompile(`^\s*([^\s:]+):\s*(.*?)\s*$`)

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

// packageSectionName is the canonical section name for sub-package definitions
// (the `%package <name>` directive). The preamble (empty section name) and these
// sections are the only places where tag-style lines (`Foo: bar`) carry semantic
// meaning; script-style sections such as `%build` may contain lines that match
// the tag regex but are not actually tags.
const packageSectionName = "%package"

// isTagBearingSection reports whether a section keyword can legally hold RPM
// tag declarations (e.g. "Name:", "Source0:"). Only the preamble (empty name)
// and "%package" sections qualify. Script-style sections like "%build" may
// contain shell that happens to match the "word: word" pattern; we must avoid
// treating those as tags.
func isTagBearingSection(secName string) bool {
	return secName == "" || secName == packageSectionName
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

		if err := validateConditionalRemoval(child, children, index, removeSet, preceding); err != nil {
			return err
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

//nolint:cyclop // Conditional wrapper validation must examine each independent unsafe shape.
func validateConditionalRemoval(
	child *block,
	children []*block,
	index int,
	removeSet map[*block]bool,
	preceding *block,
) error {
	if preceding != nil && preceding.Name != "" &&
		containsBranchDirective(child) && containsRemovedSection(child, removeSet) {
		return fmt.Errorf("%%if block at %#q contains a branch directive across a removed section:\n%w",
			child.Header, ErrConditionalSpansSections)
	}

	if conditionalHasTextOrMacroContent(child) && containsSectionBlocks(child) {
		if preceding != nil && removeSet[preceding] {
			return fmt.Errorf("%%if block at %#q contains content belonging to the preceding section:\n%w",
				child.Header, ErrConditionalSpansSections)
		}
	}

	if wouldEmptySectionWrapper(child, removeSet) && containsBranchDirective(child) {
		return fmt.Errorf("%%if block at %#q contains branches that would be removed with its sections:\n%w",
			child.Header, ErrConditionalSpansSections)
	}

	if wouldEmptySectionWrapper(child, removeSet) && index+1 < len(children) {
		next := children[index+1]
		if next.Kind == conditionalBlock && !containsSectionBlocks(next) && conditionalHasTextOrMacroContent(next) {
			return fmt.Errorf("content in %%if block at %#q would be orphaned after removing the preceding section:\n%w",
				next.Header, ErrConditionalSpansSections)
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

func containsBranchDirective(blk *block) bool {
	if blk.ElseDirective != "" || isConditionalBranchDirective(blk.Header) {
		return true
	}

	for _, child := range blk.Children {
		if containsBranchDirective(child) {
			return true
		}
	}

	for _, child := range blk.Else {
		if containsBranchDirective(child) {
			return true
		}
	}

	return false
}

func containsRemovedSection(blk *block, removeSet map[*block]bool) bool {
	if blk.Kind == sectionBlock && removeSet[blk] {
		return true
	}

	for _, child := range blk.Children {
		if containsRemovedSection(child, removeSet) {
			return true
		}
	}

	for _, child := range blk.Else {
		if containsRemovedSection(child, removeSet) {
			return true
		}
	}

	return false
}
