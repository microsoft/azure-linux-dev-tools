// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// hoistReferencedMacros moves [macroDefBlock] children of soon-to-be-removed
// sections to the preamble when those macros are referenced by content that
// will survive removal.
//
// Motivation (issue #203): spec authors sometimes place `%define` inside a
// `%package` subpackage block (e.g. a `%define testsdir` under `%package tests`)
// even though the macro is referenced by an unconditional section like
// `%install`. Naively removing the subpackage drops the macro and leaves
// dangling `%{testsdir}` references in survivors. Hoisting preserves the
// definition at the end of the preamble (before any section) so survivors
// still resolve regardless of where in the file they sit.
//
// Hoisting is deliberately narrow because RPM macro definitions are ordered
// and scope-sensitive. It moves only one unconditional, unique definition
// with no dependency on another removed definition. Redefinitions, undefines,
// conditionals, and eager forward dependencies are rejected rather than
// guessing at a different evaluation order.
//
// This function mutates root in place. It must be called BEFORE removed
// blocks are detached from the tree so that the "referenced outside the
// removed subtrees" check can compute the survivor set correctly.
func hoistReferencedMacros(root *block, removed []*block) error {
	if len(removed) == 0 {
		return nil
	}

	removedSet := blockSet(removed)

	macros := collectMacrosInSections(removed)
	if len(macros) == 0 {
		return nil
	}

	var referenced []*block

	for _, macro := range macros {
		if isMacroReferencedOutside(root, macro.Name, removedSet) {
			referenced = append(referenced, macro)
		}
	}

	if len(referenced) == 0 {
		return nil
	}

	if len(referenced) != 1 {
		return fmt.Errorf("cannot safely hoist multiple referenced macro definitions:\n%w", ErrUnsafeMacroHoist)
	}

	macro := referenced[0]
	if macroHasUnsafeHoistSemantics(root, macro, removedSet) {
		return fmt.Errorf("cannot safely hoist macro %#q because its RPM scope or evaluation order is ambiguous:\n%w",
			macro.Name, ErrUnsafeMacroHoist)
	}

	// Hoisting moves a definition the caller didn't explicitly touch, so make
	// it visible at the default log level rather than silently relocating it.
	slog.Info("Hoisted referenced macro to preamble during section removal",
		"macro", macro.Name, "definition", strings.TrimSpace(macro.Header))

	hoistIntoPreamble(root, []*block{macro})

	return nil
}

//nolint:cyclop // The checks enumerate the independent RPM scope hazards.
func macroHasUnsafeHoistSemantics(root *block, candidate *block, removedSet map[*block]bool) bool {
	definitions := 0
	conditional := false
	undefined := false

	var visit func(*block, bool)

	visit = func(blk *block, inConditional bool) {
		if blk.Kind == conditionalBlock {
			inConditional = true
		}

		if blk.Kind == macroDefBlock && blk.Name == candidate.Name {
			definitions++
			conditional = conditional || inConditional
		}

		if blk.Kind == textBlock || blk.Kind == macroDefBlock {
			for _, line := range blk.Lines {
				if name, ok := isUndefineLine(line); ok && name == candidate.Name {
					undefined = true
				}
			}
		}

		for _, child := range blk.Children {
			visit(child, inConditional)
		}

		for _, child := range blk.Else {
			visit(child, inConditional)
		}
	}
	visit(root, false)

	if definitions != 1 || conditional || undefined {
		return true
	}

	if hasSurvivingReferenceBeforeCandidate(root, candidate, removedSet) {
		return true
	}

	for _, name := range macroNamesReferencedIn(candidate.Lines) {
		if name == candidate.Name {
			return true
		}

		if hasDefinitionInRemovedSections(root, name, removedSet) ||
			hasDefinitionOutsidePreamble(root, name, candidate) ||
			hasDependencyStateChangeBeforeCandidate(root, name, candidate) {
			return true
		}
	}

	return false
}

// hasDependencyStateChangeBeforeCandidate reports a define, redefine, or
// undefine event for name that relocation would cross while moving candidate
// to the end of the preamble. Eager macro definitions expand at definition
// time, so crossing any such event can change the candidate's value.
func hasDependencyStateChangeBeforeCandidate(root *block, name string, candidate *block) bool {
	preamble := findSectionBlock(root, "", "")
	found := false
	seenCandidate := false

	var visit func(*block)

	visit = func(blk *block) {
		if found || seenCandidate || blk == preamble {
			return
		}

		if blk == candidate {
			seenCandidate = true

			return
		}

		if blk.Kind == macroDefBlock && blk.Name == name {
			found = true

			return
		}

		if blk.Kind == textBlock || blk.Kind == macroDefBlock {
			for _, line := range blk.Lines {
				if undefined, ok := isUndefineLine(line); ok && undefined == name {
					found = true

					return
				}
			}
		}

		for _, child := range blk.Children {
			visit(child)
		}

		for _, child := range blk.Else {
			visit(child)
		}
	}
	visit(root)

	return found
}

// hasSurvivingReferenceBeforeCandidate rejects relocation when it would make a
// macro visible to content that originally preceded its definition.
//
//nolint:cyclop // The switch covers every block kind while preserving lexical order.
func hasSurvivingReferenceBeforeCandidate(root, candidate *block, removedSet map[*block]bool) bool {
	found := false
	seenCandidate := false

	var visit func(*block, bool)

	visit = func(blk *block, inRemovedSection bool) {
		if found {
			return
		}

		if blk.Kind == sectionBlock {
			inRemovedSection = inRemovedSection || removedSet[blk]
		}

		if blk == candidate {
			seenCandidate = true

			return
		}

		if seenCandidate {
			return
		}

		if !inRemovedSection {
			pattern := macroReferencePattern(candidate.Name)

			switch blk.Kind {
			case textBlock, macroDefBlock:
				found = anyLineMatches(blk.Lines, pattern)
			case conditionalBlock:
				found = pattern.MatchString(blk.Header) ||
					(blk.ElseDirective != "" && pattern.MatchString(blk.ElseDirective))
			case sectionBlock:
				found = pattern.MatchString(blk.Header)
			case rootBlock:
			}
		}

		if found {
			return
		}

		for _, child := range blk.Children {
			visit(child, inRemovedSection)
		}

		for _, child := range blk.Else {
			visit(child, inRemovedSection)
		}
	}

	visit(root, false)

	return found
}

// hasDefinitionOutsidePreamble reports a dependency definition that would no
// longer be available when candidate is moved to the preamble.
func hasDefinitionOutsidePreamble(root *block, name string, candidate *block) bool {
	preamble := findSectionBlock(root, "", "")
	found := false

	walk(root, func(blk *block) bool {
		if blk.Kind == macroDefBlock && blk != candidate && blk.Name == name && !isDescendant(preamble, blk) {
			found = true

			return false
		}

		return true
	})

	return found
}

func isDescendant(root, target *block) bool {
	if root == nil {
		return false
	}

	if root == target {
		return true
	}

	for _, child := range root.Children {
		if isDescendant(child, target) {
			return true
		}
	}

	for _, child := range root.Else {
		if isDescendant(child, target) {
			return true
		}
	}

	return false
}

func hasDefinitionInRemovedSections(root *block, name string, removedSet map[*block]bool) bool {
	found := false

	walk(root, func(blk *block) bool {
		if blk.Kind == sectionBlock && removedSet[blk] {
			walk(blk, func(descendant *block) bool {
				if descendant.Kind == macroDefBlock && descendant.Name == name {
					found = true
				}

				return !found
			})

			return false
		}

		return !found
	})

	return found
}

// hoistIntoPreamble appends the given macro blocks to the end of the preamble
// section (the implicit section before the first section header), so they are
// defined before any section that might reference them. If no preamble section
// exists, the macros are prepended at the root as a fallback.
func hoistIntoPreamble(root *block, macros []*block) {
	if preamble := findSectionBlock(root, "", ""); preamble != nil {
		preamble.Children = append(preamble.Children, macros...)

		return
	}

	root.Children = append(append([]*block{}, macros...), root.Children...)
}

// isUndefineLine returns the macro name if the line is a %undefine directive.
func isUndefineLine(rawLine string) (string, bool) {
	tokens := strings.Fields(strings.TrimSpace(rawLine))

	const minUndefineTokens = 2

	if len(tokens) < minUndefineTokens {
		return "", false
	}

	if strings.ToLower(tokens[0]) == "%undefine" {
		return tokens[1], true
	}

	return "", false
}

// blockSet builds an identity-set of block pointers for O(1) lookup.
func blockSet(blocks []*block) map[*block]bool {
	set := make(map[*block]bool, len(blocks))
	for _, b := range blocks {
		set[b] = true
	}

	return set
}

// collectMacrosInSections gathers every [macroDefBlock] reachable from any of
// the given section blocks, preserving declaration order across sections.
func collectMacrosInSections(sections []*block) []*block {
	var macros []*block

	for _, sec := range sections {
		walk(sec, func(b *block) bool {
			if b.Kind == macroDefBlock {
				macros = append(macros, b)
			}

			return true
		})
	}

	return macros
}

// isMacroReferencedOutside walks the tree looking for references to name in
// any block whose enclosing section is NOT in removedSet. References include
// the standard RPM forms: %{name}, %{?name}, %{!?name}, %{name:...}, and bare
// %name terminated by a non-word character.
func isMacroReferencedOutside(root *block, name string, removedSet map[*block]bool) bool {
	pattern := macroReferencePattern(name)
	found := false

	walk(root, func(blk *block) bool {
		// Skip entire subtrees rooted at a removed section — references that
		// live inside the removed content are going away too.
		if blk.Kind == sectionBlock && removedSet[blk] {
			return false
		}

		if found {
			return false
		}

		switch blk.Kind {
		case textBlock, macroDefBlock:
			// A macro definition outside the removed set may itself reference
			// the hoisted macro (e.g. `%define foo %{name}-suffix`).
			if anyLineMatches(blk.Lines, pattern) {
				found = true
			}
		case conditionalBlock:
			// The %if / %else directives themselves can reference macros
			// (e.g. `%if 0%{?with_foo}`).
			if pattern.MatchString(blk.Header) ||
				(blk.ElseDirective != "" && pattern.MatchString(blk.ElseDirective)) {
				found = true
			}
		case sectionBlock:
			if pattern.MatchString(blk.Header) {
				found = true
			}
		case rootBlock:
			// Container: references live in descendants.
		}

		return !found
	})

	return found
}

// anyLineMatches reports whether any line in lines matches pattern.
func anyLineMatches(lines []string, pattern *regexp.Regexp) bool {
	for _, line := range lines {
		if pattern.MatchString(line) {
			return true
		}
	}

	return false
}

// macroReferencePattern builds a regexp that matches references to a named
// RPM macro. Supported forms:
//   - %{name}, %{?name}, %{!?name}
//   - %{name:default} (parameterized expansion)
//   - bare %name terminated by a non-word character or end of string
//
// The bare form requires a word boundary so we don't match %nameOther.
func macroReferencePattern(name string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(name)
	// Braced: %{ optional ! optional ? NAME ( } | : ... | whitespace args )
	// Bare:   %NAME terminated by \b
	pattern := `%(?:\{!?\??` + quoted + `(?:[}:]|\s)|` + quoted + `\b)`

	return regexp.MustCompile(pattern)
}

// macroReferenceNamePattern captures the macro name from any reference form:
// braced (`%{name}`, `%{?name}`, `%{!?name}`, `%{name:...}`) or bare (`%name`).
// It is intentionally permissive — callers filter the captured names against
// the known macro set, so matching directives like `%if` is harmless.
var macroReferenceNamePattern = regexp.MustCompile(`%\{!?\??(\w+)|%(\w+)`)

// macroNamesReferencedIn returns the names of all macros referenced anywhere in
// the given lines, in order of appearance (with duplicates). Used to discover a
// hoisted definition's dependencies on other removed macros.
func macroNamesReferencedIn(lines []string) []string {
	var names []string

	for _, line := range lines {
		for _, match := range macroReferenceNamePattern.FindAllStringSubmatch(line, -1) {
			name := match[1]
			if name == "" {
				name = match[2]
			}

			if name != "" {
				names = append(names, name)
			}
		}
	}

	return names
}
