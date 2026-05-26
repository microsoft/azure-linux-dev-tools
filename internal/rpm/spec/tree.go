// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"fmt"
	"strings"
)

// blockKind classifies what a [block] represents in the spec tree.
type blockKind int

const (
	// rootBlock is the top-level container for the entire spec.
	rootBlock blockKind = iota
	// sectionBlock is a named section (e.g., %build, %package -n foo).
	// The implicit preamble (before any section header) is also a [sectionBlock]
	// with an empty [block.Name].
	sectionBlock
	// conditionalBlock is a %if/%endif block. May wrap sections (at top level)
	// or appear as content inside a section.
	conditionalBlock
	// textBlock is a contiguous run of raw text lines (leaf node).
	textBlock
	// macroDefBlock is a %define/%global directive, optionally spanning
	// multiple lines via backslash continuation.
	macroDefBlock
)

// block is a recursive node in the spec's structural tree.
//
// The tree is built by [parseTree] and serialized back to lines by [serializeTree].
// Operations find and manipulate blocks, then serialize to update [Spec.rawLines].
type block struct {
	// Kind classifies this block.
	Kind blockKind
	// Header is the opening line: section header, conditional directive, or macro
	// definition line. Empty for [rootBlock] and [textBlock].
	Header string
	// Name is the section keyword (e.g., "%build") or macro name (e.g., "buildflags").
	// Empty for [rootBlock], [conditionalBlock], and [textBlock].
	Name string
	// Package is the sub-package name for section blocks (e.g., "devel", "foo").
	// Empty for sections that target the main package.
	Package string
	// Endif is the %endif line text for [conditionalBlock] nodes.
	Endif string
	// Lines holds raw text for [textBlock] and [macroDefBlock] leaf nodes
	// (including continuation lines for multi-line macros).
	Lines []string
	// Children holds nested blocks. For [sectionBlock], these are the section's
	// content. For [conditionalBlock], these are the "then" branch. For [rootBlock],
	// these are top-level sections and conditional wrappers.
	Children []*block
	// Else holds the "else" branch blocks for [conditionalBlock] nodes.
	// nil when there is no %else/%elif branch.
	Else []*block
	// ElseDirective is the %else/%elif directive line, if present.
	ElseDirective string
}

// parseTree parses raw spec lines into a [block] tree.
//
// The parser runs in two passes:
//  1. Collect conditional pairs (%if/%endif) and section header positions.
//  2. Build the tree, classifying each conditional as a wrapper (spans sections)
//     or content block (fully inside a section) based on whether its body contains
//     section headers.
//
// Line continuations (backslash at end of line) are respected: continuation bodies
// are never interpreted as section headers or conditional directives.
func parseTree(rawLines []string) (*block, error) {
	pairs, err := collectConditionalPairs(rawLines)
	if err != nil {
		return nil, fmt.Errorf("parsing conditional structure:\n%w", err)
	}

	pairByIf := make(map[int]conditionalPair, len(pairs))
	for _, p := range pairs {
		pairByIf[p.ifLine] = p
	}

	sectionHeaders := findSectionHeaderLines(rawLines)

	sectionHeaderSet := make(map[int]bool, len(sectionHeaders))
	for _, h := range sectionHeaders {
		sectionHeaderSet[h] = true
	}

	root := &block{Kind: rootBlock}

	err = buildBlockChildren(rawLines, 0, len(rawLines), pairByIf, sectionHeaderSet, root, true)
	if err != nil {
		return nil, fmt.Errorf("building spec tree:\n%w", err)
	}

	// Wrap leading non-section children (preamble content) into an implicit
	// preamble sectionBlock with empty Name, matching how Visit treats lines
	// before the first section header. This allows findSectionBlock(root, "", "")
	// to locate the preamble.
	wrapPreamble(root)

	return root, nil
}

// wrapPreamble wraps the leading non-section children of root into a preamble
// [sectionBlock] with empty Name and Package. If the root already starts with
// a [sectionBlock], no wrapping is needed.
func wrapPreamble(root *block) {
	// Find the index of the first sectionBlock or section-wrapping conditionalBlock.
	firstSectionIdx := -1

	for childIdx, child := range root.Children {
		if child.Kind == sectionBlock {
			firstSectionIdx = childIdx

			break
		}

		if child.Kind == conditionalBlock && containsSectionBlocks(child) {
			firstSectionIdx = childIdx

			break
		}
	}

	// If everything is preamble (no sections) or nothing precedes the first section,
	// still wrap in a preamble block for uniform access.
	preambleEnd := firstSectionIdx
	if preambleEnd < 0 {
		preambleEnd = len(root.Children)
	}

	if preambleEnd == 0 {
		// Nothing to wrap, but insert an empty preamble for uniform lookup.
		preamble := &block{Kind: sectionBlock, Name: "", Package: ""}
		root.Children = append([]*block{preamble}, root.Children...)

		return
	}

	preamble := &block{
		Kind:     sectionBlock,
		Name:     "",
		Package:  "",
		Children: root.Children[:preambleEnd],
	}

	root.Children = append([]*block{preamble}, root.Children[preambleEnd:]...)
}

// containsSectionBlocks checks if a block (typically a conditionalBlock) contains
// any sectionBlock children in any branch, recursing through %elif chains.
func containsSectionBlocks(block *block) bool {
	for _, child := range block.Children {
		if child.Kind == sectionBlock {
			return true
		}

		if child.Kind == conditionalBlock && containsSectionBlocks(child) {
			return true
		}
	}

	for _, child := range block.Else {
		if child.Kind == sectionBlock {
			return true
		}

		if child.Kind == conditionalBlock && containsSectionBlocks(child) {
			return true
		}
	}

	return false
}

// findSectionHeaderLines returns the 0-indexed line numbers of all section headers,
// respecting line continuations (backslash-terminated lines suppress the next line).
func findSectionHeaderLines(rawLines []string) []int {
	var headers []int

	inCont := false

	for lineIdx, line := range rawLines {
		if inCont {
			inCont = strings.HasSuffix(line, "\\")

			continue
		}

		if isSectionHeaderLine(line) {
			headers = append(headers, lineIdx)
		}

		inCont = strings.HasSuffix(line, "\\")
	}

	return headers
}

// isSectionHeaderLine returns true if the line starts a new RPM spec section.
func isSectionHeaderLine(rawLine string) bool {
	tokens := strings.Fields(strings.TrimSpace(rawLine))
	if len(tokens) == 0 {
		return false
	}

	_, known := sectionTypesByName[strings.ToLower(tokens[0])]

	return known
}

// hasSectionHeaderInRange checks whether any line in [start, end) is a section header.
func hasSectionHeaderInRange(start, end int, sectionHeaderSet map[int]bool) bool {
	for lineNum := start; lineNum < end; lineNum++ {
		if sectionHeaderSet[lineNum] {
			return true
		}
	}

	return false
}

// buildBlockChildren parses lines in [start, end) and appends resulting blocks
// to parent.Children. topLevel indicates whether sections can appear (true at
// root level and inside conditional wrappers).
//
//nolint:funlen,gocognit // Recursive parser with multiple block types.
func buildBlockChildren(
	rawLines []string,
	start, end int,
	pairByIf map[int]conditionalPair,
	sectionHeaderSet map[int]bool,
	parent *block,
	topLevel bool,
) error {
	lineIdx := start
	inCont := false

	var textBuf []string

	flushText := func() {
		if len(textBuf) > 0 {
			parent.Children = append(parent.Children, &block{
				Kind:  textBlock,
				Lines: textBuf,
			})

			textBuf = nil
		}
	}

	for lineIdx < end {
		line := rawLines[lineIdx]

		if inCont {
			textBuf = append(textBuf, line)
			inCont = strings.HasSuffix(line, "\\")
			lineIdx++

			continue
		}

		// Section headers (only at top level).
		if topLevel && sectionHeaderSet[lineIdx] {
			flushText()

			name, pkg := getSectionNameAndPackageFromHeader(line)
			sectionBlock := &block{
				Kind:    sectionBlock,
				Header:  line,
				Name:    name,
				Package: pkg,
			}

			sectionEnd := findTreeSectionEnd(lineIdx+1, end, pairByIf, sectionHeaderSet)

			err := buildBlockChildren(rawLines, lineIdx+1, sectionEnd, pairByIf, sectionHeaderSet, sectionBlock, false)
			if err != nil {
				return err
			}

			parent.Children = append(parent.Children, sectionBlock)
			lineIdx = sectionEnd

			continue
		}

		// Conditional directives.
		if conditionalDepthChange(line) == 1 {
			flushText()

			pair, ok := pairByIf[lineIdx]
			if !ok {
				return fmt.Errorf("%%if at line %d has no matching pair", lineIdx+1)
			}

			condBlock := &block{
				Kind:   conditionalBlock,
				Header: line,
				Endif:  rawLines[pair.endifLine],
			}

			bodyStart := lineIdx + 1
			bodyEnd := pair.endifLine

			elseLine := findElseDirectiveLine(rawLines, bodyStart, bodyEnd)

			thenEnd := bodyEnd
			if elseLine >= 0 {
				thenEnd = elseLine
			}

			isWrapper := hasSectionHeaderInRange(bodyStart, bodyEnd, sectionHeaderSet)

			if err := buildConditionalBranches(
				rawLines, bodyStart, thenEnd, elseLine, bodyEnd,
				pairByIf, sectionHeaderSet, condBlock, isWrapper,
			); err != nil {
				return err
			}

			parent.Children = append(parent.Children, condBlock)
			lineIdx = pair.endifLine + 1

			continue
		}

		// Macro definitions.
		if name, ok := isMacroDefLine(line); ok {
			flushText()

			macroBlock := &block{
				Kind:   macroDefBlock,
				Header: line,
				Name:   name,
				Lines:  []string{line},
			}

			if strings.HasSuffix(line, "\\") {
				inCont = true
				lineIdx++

				for lineIdx < end {
					macroBlock.Lines = append(macroBlock.Lines, rawLines[lineIdx])

					if !strings.HasSuffix(rawLines[lineIdx], "\\") {
						inCont = false
						lineIdx++

						break
					}

					lineIdx++
				}
			} else {
				lineIdx++
			}

			parent.Children = append(parent.Children, macroBlock)

			continue
		}

		// Plain text line.
		textBuf = append(textBuf, line)
		inCont = strings.HasSuffix(line, "\\")
		lineIdx++
	}

	flushText()

	return nil
}

// buildConditionalBranches parses the then and optional else/elif branches of a
// conditional block. For %elif chains, the else branch contains a single nested
// [conditionalBlock] whose Header is the %elif directive, forming a linked list.
func buildConditionalBranches(
	rawLines []string,
	bodyStart, thenEnd, elseLine, bodyEnd int,
	pairByIf map[int]conditionalPair,
	sectionHeaderSet map[int]bool,
	condBlock *block,
	isWrapper bool,
) error {
	err := buildBlockChildren(rawLines, bodyStart, thenEnd, pairByIf, sectionHeaderSet, condBlock, isWrapper)
	if err != nil {
		return err
	}

	if elseLine < 0 {
		return nil
	}

	if isElifDirective(rawLines[elseLine]) {
		// %elif: create a nested conditionalBlock forming a linked list.
		// The inner block has no Endif — only the outermost block owns %endif.
		inner := &block{
			Kind:   conditionalBlock,
			Header: rawLines[elseLine],
		}

		// Find the next branch directive (%elif/%else) within the remaining body.
		nextElse := findElseDirectiveLine(rawLines, elseLine+1, bodyEnd)

		nextThenEnd := bodyEnd
		if nextElse >= 0 {
			nextThenEnd = nextElse
		}

		if err := buildConditionalBranches(
			rawLines, elseLine+1, nextThenEnd, nextElse, bodyEnd,
			pairByIf, sectionHeaderSet, inner, isWrapper,
		); err != nil {
			return err
		}

		condBlock.Else = []*block{inner}
	} else {
		// %else: terminal branch — store directive and parse content directly.
		condBlock.ElseDirective = rawLines[elseLine]
		elseContainer := &block{Kind: rootBlock}

		err := buildBlockChildren(rawLines, elseLine+1, bodyEnd, pairByIf, sectionHeaderSet, elseContainer, isWrapper)
		if err != nil {
			return err
		}

		condBlock.Else = elseContainer.Children
	}

	return nil
}

// isElifDirective returns true if the line is a %elif/%elifarch/%elifnarch/%elifos/%elifnos
// directive (as opposed to a plain %else which is a terminal branch).
func isElifDirective(rawLine string) bool {
	lower := strings.ToLower(strings.Fields(strings.TrimSpace(rawLine))[0])

	return lower != "%else" && isConditionalBranchDirective(rawLine)
}

// findTreeSectionEnd finds where a section ends: at the next section header at the
// same nesting level, or at a conditional that wraps sections.
func findTreeSectionEnd(start, end int, pairByIf map[int]conditionalPair, sectionHeaderSet map[int]bool) int {
	lineIdx := start

	for lineIdx < end {
		if sectionHeaderSet[lineIdx] {
			return lineIdx
		}

		if pair, ok := pairByIf[lineIdx]; ok {
			if hasSectionHeaderInRange(lineIdx+1, pair.endifLine, sectionHeaderSet) {
				return lineIdx
			}

			lineIdx = pair.endifLine + 1

			continue
		}

		lineIdx++
	}

	return end
}

// findElseDirectiveLine finds the %else/%elif line within [start, end) at
// conditional depth 0.
func findElseDirectiveLine(rawLines []string, start, end int) int {
	depth := 0

	for lineIdx := start; lineIdx < end; lineIdx++ {
		d := conditionalDepthChange(rawLines[lineIdx])

		switch {
		case d == 1:
			depth++
		case d == -1:
			depth--
		case depth == 0 && isConditionalBranchDirective(rawLines[lineIdx]):
			return lineIdx
		}
	}

	return -1
}

// isMacroDefLine returns the macro name if the line is a %define or %global directive.
func isMacroDefLine(rawLine string) (string, bool) {
	trimmed := strings.TrimSpace(rawLine)
	tokens := strings.Fields(trimmed)

	const minMacroDefTokens = 2

	if len(tokens) < minMacroDefTokens {
		return "", false
	}

	lower := strings.ToLower(tokens[0])
	if lower == "%define" || lower == "%global" {
		// Strip trailing parentheses from macro names with parameters,
		// e.g. "%define foo(x)" → "foo".
		name := tokens[1]
		if idx := strings.IndexByte(name, '('); idx >= 0 {
			name = name[:idx]
		}

		return name, true
	}

	return "", false
}

// getSectionNameAndPackageFromHeader extracts the section keyword and package name
// from a section header line. Uses the existing [GetPackageNameFromSectionHeader]
// for package name extraction.
func getSectionNameAndPackageFromHeader(rawLine string) (string, string) {
	tokens := strings.Fields(strings.TrimSpace(rawLine))
	if len(tokens) == 0 {
		return "", ""
	}

	sectName := tokens[0]

	sectType, ok := sectionTypesByName[strings.ToLower(sectName)]
	if !ok {
		return sectName, ""
	}

	pkg := getPackageNameForSection(sectType, tokens)

	return sectName, pkg
}

// serializeTree flattens a [block] tree back into raw spec lines.
// The result preserves all original whitespace, comments, and blank lines.
func serializeTree(block *block) []string {
	var lines []string

	switch block.Kind {
	case rootBlock:
		for _, child := range block.Children {
			lines = append(lines, serializeTree(child)...)
		}

	case sectionBlock:
		if block.Header != "" {
			lines = append(lines, block.Header)
		}

		for _, child := range block.Children {
			lines = append(lines, serializeTree(child)...)
		}

	case conditionalBlock:
		lines = append(lines, block.Header)

		for _, child := range block.Children {
			lines = append(lines, serializeTree(child)...)
		}

		if block.Else != nil {
			if block.ElseDirective != "" {
				lines = append(lines, block.ElseDirective)
			}

			for _, child := range block.Else {
				lines = append(lines, serializeTree(child)...)
			}
		}

		if block.Endif != "" {
			lines = append(lines, block.Endif)
		}

	case textBlock:
		lines = append(lines, block.Lines...)

	case macroDefBlock:
		lines = append(lines, block.Lines...)
	}

	return lines
}

// --- Tree query helpers ---

// walk performs a depth-first, pre-order traversal of the tree rooted at b,
// invoking visit on every block (starting with b itself). Traversal descends
// into Children and, for [conditionalBlock] nodes, the Else branch as well —
// mirroring the structural nesting used throughout the tree.
//
// If visit returns false, the subtree rooted at the current block is pruned:
// its descendants are skipped, but siblings are still visited. Returning true
// continues the traversal. walk is read-only; visitors must not add or remove
// blocks during traversal.
func walk(blk *block, visit func(*block) bool) {
	if !visit(blk) {
		return
	}

	for _, child := range blk.Children {
		walk(child, visit)
	}

	if blk.Kind == conditionalBlock {
		for _, child := range blk.Else {
			walk(child, visit)
		}
	}
}

// findSectionBlock finds the first section block (in document order) matching
// name and package, searching recursively through conditional wrappers.
func findSectionBlock(root *block, name, pkg string) *block {
	var found *block

	walk(root, func(blk *block) bool {
		if found != nil {
			return false
		}

		if blk.Kind == sectionBlock && blk.Name == name && blk.Package == pkg {
			found = blk

			return false
		}

		return true
	})

	return found
}

// findAllSectionBlocks returns all section blocks matching name and package.
func findAllSectionBlocks(root *block, name, pkg string) []*block {
	var results []*block

	walk(root, func(blk *block) bool {
		if blk.Kind == sectionBlock && blk.Name == name && blk.Package == pkg {
			results = append(results, blk)
		}

		return true
	})

	return results
}

// findAllSectionBlocksByPackage returns all section blocks matching a package name
// (any section name).
func findAllSectionBlocksByPackage(root *block, pkg string) []*block {
	var results []*block

	walk(root, func(blk *block) bool {
		if blk.Kind == sectionBlock && blk.Package == pkg {
			results = append(results, blk)
		}

		return true
	})

	return results
}

// removeBlockFromParent removes a target block from any parent in the tree.
// It searches recursively through all [conditionalBlock] nesting levels.
func removeBlockFromParent(root *block, target *block) {
	root.Children = filterBlocks(root.Children, target)

	for _, child := range root.Children {
		if child.Kind == conditionalBlock {
			removeFromConditional(child, target)
		}
	}
}

func removeFromConditional(cond *block, target *block) {
	cond.Children = filterBlocks(cond.Children, target)

	if cond.Else != nil {
		cond.Else = filterBlocks(cond.Else, target)
	}

	for _, child := range cond.Children {
		if child.Kind == conditionalBlock {
			removeFromConditional(child, target)
		}
	}

	for _, child := range cond.Else {
		if child.Kind == conditionalBlock {
			removeFromConditional(child, target)
		}
	}
}

func filterBlocks(blocks []*block, exclude *block) []*block {
	result := make([]*block, 0, len(blocks))

	for _, b := range blocks {
		if b != exclude {
			result = append(result, b)
		}
	}

	return result
}

// validateSectionRemoval checks that removing the given sections is safe.
// It detects patterns where the tree's structural section boundaries don't
// align with RPM's linear section ownership, which would produce incorrect
// output if sections were naively removed.
func validateSectionRemoval(root *block, toRemove []*block) error {
	removeSet := make(map[*block]bool, len(toRemove))
	for _, b := range toRemove {
		removeSet[b] = true
	}

	// Check each level of the tree for unsafe patterns.
	return validateRemovalInChildren(root.Children, removeSet)
}

func validateRemovalInChildren(children []*block, removeSet map[*block]bool) error {
	for childIdx, child := range children {
		if child.Kind != conditionalBlock {
			continue
		}

		// Check if a wrapper conditional has orphaned text that semantically belongs
		// to the section immediately preceding it. If that preceding section is being
		// removed, the text would be orphaned.
		//
		// Note: cross-branch asymmetry (removing sections from %if but not %else,
		// or vice versa) is intentionally allowed — a valid use case is removing
		// a subpackage from one branch while keeping the alternative in the other.
		// Text/macro content alongside surviving sections is also fine — it belongs
		// to the section preceding the wrapper, not to the removed section.
		if hasTextOrMacroContent(child.Children) && containsSectionBlocks(child) {
			preceding := findPrecedingSection(children, childIdx)
			if preceding != nil && removeSet[preceding] {
				return fmt.Errorf("%%if block at %q "+
					"contains content belonging to the preceding section:\n%w",
					child.Header, ErrConditionalSpansSections)
			}
		}

		// Check if removing sections from a wrapper would leave orphaned content
		// in an adjacent non-wrapper conditional (case: adjacent content conditional
		// after a wrapper whose only sections are being removed).
		if wouldEmptyWrapper(child, removeSet) && childIdx+1 < len(children) {
			next := children[childIdx+1]
			if next.Kind == conditionalBlock && !containsSectionBlocks(next) && hasTextOrMacroContent(next.Children) {
				return fmt.Errorf("content in %%if block at %q "+
					"would be orphaned after removing the preceding section:\n%w",
					next.Header, ErrConditionalSpansSections)
			}
		}

		// Recurse into wrapper conditional's branches.
		if err := validateRemovalInChildren(child.Children, removeSet); err != nil {
			return err
		}

		if child.Else != nil {
			if err := validateRemovalInChildren(child.Else, removeSet); err != nil {
				return err
			}
		}
	}

	return nil
}

// findPrecedingSection walks backwards from index i in children to find
// the most recent sectionBlock, skipping over text and other blocks.
func findPrecedingSection(children []*block, i int) *block {
	for j := i - 1; j >= 0; j-- {
		if children[j].Kind == sectionBlock {
			return children[j]
		}
	}

	return nil
}

func hasTextOrMacroContent(blocks []*block) bool {
	for _, b := range blocks {
		if b.Kind == textBlock || b.Kind == macroDefBlock {
			return true
		}
	}

	return false
}

// wouldEmptyWrapper checks if removing the targeted sections would leave
// a wrapper conditional with no section content in either branch.
func wouldEmptyWrapper(cond *block, removeSet map[*block]bool) bool {
	if !containsSectionBlocks(cond) {
		return false
	}

	for _, child := range cond.Children {
		if child.Kind == sectionBlock && !removeSet[child] {
			return false
		}

		if child.Kind == conditionalBlock && hasNonRemovedSectionsDeep(child, removeSet) {
			return false
		}
	}

	for _, child := range cond.Else {
		if child.Kind == sectionBlock && !removeSet[child] {
			return false
		}

		if child.Kind == conditionalBlock && hasNonRemovedSectionsDeep(child, removeSet) {
			return false
		}
	}

	return true
}

func hasNonRemovedSectionsDeep(block *block, removeSet map[*block]bool) bool {
	if block.Kind == sectionBlock && !removeSet[block] {
		return true
	}

	for _, child := range block.Children {
		if hasNonRemovedSectionsDeep(child, removeSet) {
			return true
		}
	}

	if block.Kind == conditionalBlock {
		for _, child := range block.Else {
			if hasNonRemovedSectionsDeep(child, removeSet) {
				return true
			}
		}
	}

	return false
}
