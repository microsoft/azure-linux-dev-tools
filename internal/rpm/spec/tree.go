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

// treeConditionalPair represents a matched `%if`/`%endif` pair by their line numbers.
type treeConditionalPair struct {
	ifLine    int
	endifLine int
}

// parseTree parses raw spec lines into a [block] tree.
//
// The parser runs in two passes:
//  1. Collect conditional pairs (%if/%endif) and section header positions.
//  2. Build the tree, classifying each conditional as a wrapper (spans sections)
//     or content block (fully inside a section) based on whether its body contains
//     section headers.
//
// Only '%define' and '%global' continuation bodies are opaque. Ordinary script
// backslashes do not suppress RPM section headers or conditional directives.
func parseTree(rawLines []string) (*block, error) {
	pairs, err := collectTreeConditionalPairs(rawLines)
	if err != nil {
		return nil, fmt.Errorf("parsing conditional structure:\n%w", err)
	}

	pairByIf := make(map[int]treeConditionalPair, len(pairs))
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

// collectTreeConditionalPairs matches conditionals while treating macro bodies as
// opaque. Unlike the line-oriented editor helper, the structural parser must not
// interpret directive-shaped macro content.
func collectTreeConditionalPairs(rawLines []string) ([]treeConditionalPair, error) {
	var (
		pairs       []treeConditionalPair
		stack       []int
		inMacroBody bool
		parseState  macroState
	)

	for lineNum, line := range rawLines {
		if inMacroBody {
			parseState, inMacroBody = macroBodyStateAfter(line, parseState)

			continue
		}

		if _, isMacro := isMacroDefLine(line); isMacro {
			parseState, inMacroBody = macroBodyStateAfter(line, macroState{})

			continue
		}

		switch conditionalDepthChange(line) {
		case 1:
			stack = append(stack, lineNum)
		case -1:
			if len(stack) == 0 {
				return nil, fmt.Errorf("unmatched %%endif at line %d", lineNum+1)
			}

			ifLine := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			pairs = append(pairs, treeConditionalPair{ifLine: ifLine, endifLine: lineNum})
		}
	}

	if len(stack) > 0 {
		return nil, fmt.Errorf("unmatched %%if at line %d", stack[0]+1)
	}

	return pairs, nil
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

	inMacroBody := false
	macroParseState := macroState{}

	for lineIdx, line := range rawLines {
		if inMacroBody {
			macroParseState, inMacroBody = macroBodyStateAfter(line, macroParseState)

			continue
		}

		if isSectionHeaderLine(line) {
			headers = append(headers, lineIdx)
		}

		if _, isMacro := isMacroDefLine(line); isMacro {
			macroParseState, inMacroBody = macroBodyStateAfter(line, macroState{})
		}
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
//nolint:funlen // Recursive parser with multiple block types.
func buildBlockChildren(
	rawLines []string,
	start, end int,
	pairByIf map[int]treeConditionalPair,
	sectionHeaderSet map[int]bool,
	parent *block,
	topLevel bool,
) error {
	lineIdx := start

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

			macroBlock, nextLineIdx, err := parseMacroDefBlock(rawLines, lineIdx, end, name)
			if err != nil {
				return err
			}

			parent.Children = append(parent.Children, macroBlock)
			lineIdx = nextLineIdx

			continue
		}

		// Plain text line.
		textBuf = append(textBuf, line)
		lineIdx++
	}

	flushText()

	return nil
}

func parseMacroDefBlock(rawLines []string, start, end int, name string) (*block, int, error) {
	macroBlock := &block{
		Kind:   macroDefBlock,
		Header: rawLines[start],
		Name:   name,
		Lines:  []string{rawLines[start]},
	}

	state, continues := macroBodyStateAfter(rawLines[start], macroState{})

	lineIdx := start + 1
	if !continues {
		return macroBlock, lineIdx, nil
	}

	for lineIdx < end {
		line := rawLines[lineIdx]
		macroBlock.Lines = append(macroBlock.Lines, line)
		state, continues = macroBodyStateAfter(line, state)
		lineIdx++

		if !continues {
			return macroBlock, lineIdx, nil
		}
	}

	return nil, 0, fmt.Errorf("unterminated macro construct at line %d", start+1)
}

// buildConditionalBranches parses the then and optional else/elif branches of a
// conditional block. For %elif chains, the else branch contains a single nested
// [conditionalBlock] whose Header is the %elif directive, forming a linked list.
func buildConditionalBranches(
	rawLines []string,
	bodyStart, thenEnd, elseLine, bodyEnd int,
	pairByIf map[int]treeConditionalPair,
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
	tokens := strings.Fields(strings.TrimSpace(rawLine))
	if len(tokens) == 0 {
		return false
	}

	lower := strings.ToLower(tokens[0])

	return lower != "%else" && isConditionalBranchDirective(rawLine)
}

// findTreeSectionEnd finds where a section ends: at the next section header at the
// same nesting level, or at a conditional that wraps sections.
func findTreeSectionEnd(start, end int, pairByIf map[int]treeConditionalPair, sectionHeaderSet map[int]bool) int {
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
	inMacroBody := false
	macroParseState := macroState{}

	for lineIdx := start; lineIdx < end; lineIdx++ {
		line := rawLines[lineIdx]
		if inMacroBody {
			macroParseState, inMacroBody = macroBodyStateAfter(line, macroParseState)

			continue
		}

		if _, isMacro := isMacroDefLine(line); isMacro {
			macroParseState, inMacroBody = macroBodyStateAfter(line, macroState{})

			continue
		}

		d := conditionalDepthChange(line)

		switch {
		case d == 1:
			depth++
		case d == -1:
			depth--
		case depth == 0 && isConditionalBranchDirective(line):
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

type macroState struct {
	depth         int
	escapedBraces int
	shellBraces   int
	rawBraces     int
	lua           *luaState
}

type luaState struct {
	braces    int
	nestedRPM int
	quote     byte
	escaped   bool
	longClose string
}

func (state macroState) open() bool {
	return state.depth > 0 || state.escapedBraces > 0 || state.lua != nil
}

// percentRunOpensBracedMacro reports whether the percent run at start ends in
// an active '%{' opener. RPM escapes percent pairs, leaving only odd runs live.
func percentRunOpensBracedMacro(content string, start int) bool {
	if start >= len(content) || content[start] != '%' {
		return false
	}

	end := start
	for end < len(content) && content[end] == '%' {
		end++
	}

	return end < len(content) && content[end] == '{' && (end-start)%2 != 0
}

// macroBodyStateAfter advances the parser state for one physical macro-body
// line and reports whether the body continues onto another line.
func macroBodyStateAfter(line string, state macroState) (macroState, bool) {
	state = macroStateAfter(line, state)

	return state, strings.HasSuffix(line, "\\") || state.open()
}

// macroStateAfter tracks RPM macro constructs in a '%define'/'%global' body.
// Lua has its own syntax, so raw Lua braces, strings, comments, and nested RPM
// expansions are accounted for before deciding that the outer '%{lua:...}'
// expansion has ended.
//
//nolint:cyclop // Macro and shell delimiters require independent lexical states.
func macroStateAfter(line string, state macroState) macroState {
	for idx := 0; idx < len(line); {
		if state.lua != nil {
			consumed, closed := state.lua.consume(line[idx:])
			idx += consumed

			if closed {
				state.lua = nil
				state.depth--
			}

			continue
		}

		if state.escapedBraces > 0 {
			state, idx = consumeEscapedBracedMacro(line, idx, state)

			continue
		}

		switch {
		case line[idx] == '%' && (idx == 0 || line[idx-1] != '%'):
			state, idx = macroStateAfterPercentRun(line, idx, state)
		case line[idx] == '$' && idx+1 < len(line) && line[idx+1] == '{':
			state.shellBraces++
			idx += 2
		case line[idx] == '}' && state.shellBraces > 0:
			state.shellBraces--
			idx++
		case line[idx] == '{' && state.depth > 0:
			state.rawBraces++
			idx++
		case line[idx] == '}' && state.rawBraces > 0:
			state.rawBraces--
			idx++
		case line[idx] == '}' && state.depth > 0:
			state.depth--
			idx++
		default:
			idx++
		}
	}

	// Lua treats a backslash followed by a physical newline as one escaped
	// newline. The next line starts with a fresh escape state.
	if state.lua != nil {
		state.lua.escaped = false
	}

	return state
}

func consumeEscapedBracedMacro(line string, idx int, state macroState) (macroState, int) {
	switch line[idx] {
	case '{':
		state.escapedBraces++
	case '}':
		state.escapedBraces--
	}

	return state, idx + 1
}

func macroStateAfterPercentRun(line string, idx int, state macroState) (macroState, int) {
	runEnd := idx
	for runEnd < len(line) && line[runEnd] == '%' {
		runEnd++
	}

	if runEnd >= len(line) || line[runEnd] != '{' {
		return state, runEnd
	}

	if (runEnd-idx)%2 == 0 {
		state.escapedBraces++

		return state, runEnd + 1
	}

	state.depth++
	if strings.HasPrefix(line[runEnd-1:], "%{lua:") {
		state.lua = &luaState{}

		return state, runEnd - 1 + len("%{lua:")
	}

	return state, runEnd + 1
}

// consume scans one Lua body fragment. It returns whether the outer RPM Lua
// expansion closes. Lua line comments naturally end at the next physical line.
//
//nolint:cyclop,funlen // Lua lexical states must be recognized before structural braces.
func (state *luaState) consume(text string) (int, bool) {
	const (
		longOpenLength    = 2
		longCommentLength = 4
	)

	for idx := 0; idx < len(text); {
		if state.longClose != "" {
			if strings.HasPrefix(text[idx:], state.longClose) {
				idx += len(state.longClose)
				state.longClose = ""
			} else {
				idx++
			}

			continue
		}

		if state.quote != 0 {
			switch {
			case state.escaped:
				state.escaped = false
			case text[idx] == '\\':
				state.escaped = true
			case text[idx] == state.quote:
				state.quote = 0
			}

			idx++

			continue
		}

		if delimiter, ok := luaLongDelimiter(text[idx:]); ok {
			state.longClose = "]" + delimiter + "]"
			idx += len(delimiter) + longOpenLength

			continue
		}

		if strings.HasPrefix(text[idx:], "--") {
			if delimiter, ok := luaLongDelimiter(text[idx+2:]); ok {
				state.longClose = "]" + delimiter + "]"
				idx += len(delimiter) + longCommentLength

				continue
			}

			return len(text), false
		}

		switch {
		case text[idx] == '\'', text[idx] == '"':
			state.quote = text[idx]
		case text[idx] == '%' && idx+1 < len(text) && text[idx+1] == '{':
			state.nestedRPM++
			idx++
		case text[idx] == '}':
			switch {
			case state.nestedRPM > 0:
				state.nestedRPM--
			case state.braces > 0:
				state.braces--
			default:
				return idx + 1, true
			}
		case text[idx] == '{':
			state.braces++
		}

		idx++
	}

	return len(text), false
}

// luaLongDelimiter recognizes the '=' run in a Lua long-bracket opener.
func luaLongDelimiter(text string) (string, bool) {
	if len(text) == 0 || text[0] != '[' {
		return "", false
	}

	idx := 1
	for idx < len(text) && text[idx] == '=' {
		idx++
	}

	if idx >= len(text) || text[idx] != '[' {
		return "", false
	}

	return text[1:idx], true
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

		if block.ElseDirective != "" {
			lines = append(lines, block.ElseDirective)
		}

		if block.Else != nil {
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
