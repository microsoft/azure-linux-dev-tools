// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
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
// The set of macros to hoist is computed as a transitive closure: starting
// from macros referenced by surviving content, any macro those definitions
// themselves reference (and which also lives in the removed set) is hoisted
// too, to a fixed point. This prevents a hoisted macro from dangling on an
// inner macro that would otherwise be dropped (e.g. `%define testsdir
// %{testroot}/tests` where `testroot` is also defined in the subpackage).
//
// The closure is conservative:
//   - A name explicitly `%undefine`d within the removed set is never hoisted
//     (the author tore it down in-scope on purpose).
//   - A name that already has a surviving definition outside the removed set
//     is never hoisted (the survivor reference resolves to the existing one).
//   - Duplicate definitions of the same name across removed sections collapse
//     to the first declaration.
//
// Hoisted macros are emitted in their original declaration order, which keeps
// inter-macro dependencies well-formed for both lazy `%define` and eager
// `%global` expansion.
//
// This function mutates root in place. It must be called BEFORE removed
// blocks are detached from the tree so that the "referenced outside the
// removed subtrees" check can compute the survivor set correctly.
func hoistReferencedMacros(root *block, removed []*block) {
	if len(removed) == 0 {
		return
	}

	removedSet := blockSet(removed)

	macros := collectMacrosInSections(removed)
	if len(macros) == 0 {
		return
	}

	// First declaration wins for each name; declaration order is preserved by
	// iterating macros (which collectMacrosInSections returns in order).
	macroByName := firstDeclarations(macros)

	toHoist := computeHoistClosure(root, macros, macroByName, removedSet, collectUndefinedNames(removed))
	if len(toHoist) == 0 {
		return
	}

	ordered := orderedHoistBlocks(macros, toHoist)

	// Hoisting moves a definition the caller didn't explicitly touch, so make
	// it visible at the default log level rather than silently relocating it.
	for _, macro := range ordered {
		slog.Info("Hoisted referenced macro to preamble during section removal",
			"macro", macro.Name, "definition", strings.TrimSpace(macro.Header))
	}

	hoistIntoPreamble(root, ordered)
}

// firstDeclarations indexes macros by name, keeping the first declaration of
// each name. Declaration order is the order collectMacrosInSections returns.
func firstDeclarations(macros []*block) map[string]*block {
	byName := make(map[string]*block, len(macros))

	for _, macro := range macros {
		if _, dup := byName[macro.Name]; !dup {
			byName[macro.Name] = macro
		}
	}

	return byName
}

// computeHoistClosure returns the set of macro names that must be hoisted: the
// transitive closure of macros referenced by surviving content, following
// references between removed macros to a fixed point. Names that are
// %undefine'd in-scope or already defined by a survivor are excluded.
func computeHoistClosure(
	root *block,
	macros []*block,
	macroByName map[string]*block,
	removedSet map[*block]bool,
	undefined map[string]bool,
) map[string]bool {
	toHoist := make(map[string]bool, len(macros))
	enqueued := make(map[string]bool, len(macros))
	queue := make([]string, 0, len(macros))

	enqueue := func(name string) {
		if !enqueued[name] {
			enqueued[name] = true
			queue = append(queue, name)
		}
	}

	// Seed with macros referenced by surviving (non-removed) content.
	for _, macro := range macros {
		if isMacroReferencedOutside(root, macro.Name, removedSet) {
			enqueue(macro.Name)
		}
	}

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]

		// Skip names torn down in-scope (%undefine) or already defined by a
		// surviving definition (the reference resolves to the existing one).
		if undefined[name] || hasDefinitionOutside(root, name, removedSet) {
			continue
		}

		def, ok := macroByName[name]
		if !ok {
			// Referenced name isn't one of the removed macros; nothing to hoist.
			continue
		}

		toHoist[name] = true

		// Follow this definition's own references into other removed macros so
		// transitive dependencies are hoisted alongside it.
		for _, ref := range macroNamesReferencedIn(def.Lines) {
			if _, isRemovedMacro := macroByName[ref]; isRemovedMacro {
				enqueue(ref)
			}
		}
	}

	return toHoist
}

// orderedHoistBlocks returns the first declaration of each hoisted name, in
// original declaration order. Eager `%global` and lazy `%define` are both kept
// well-formed because dependencies precede their dependents in this order.
func orderedHoistBlocks(macros []*block, toHoist map[string]bool) []*block {
	ordered := make([]*block, 0, len(toHoist))
	emitted := make(map[string]bool, len(toHoist))

	for _, macro := range macros {
		if toHoist[macro.Name] && !emitted[macro.Name] {
			emitted[macro.Name] = true
			ordered = append(ordered, macro)
		}
	}

	return ordered
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

// collectUndefinedNames returns the set of macro names that are %undefine'd
// anywhere within the removed sections. Such names must not be hoisted.
func collectUndefinedNames(removed []*block) map[string]bool {
	undefined := make(map[string]bool)

	for _, sec := range removed {
		walk(sec, func(blk *block) bool {
			if blk.Kind == textBlock || blk.Kind == macroDefBlock {
				for _, line := range blk.Lines {
					if name, ok := isUndefineLine(line); ok {
						undefined[name] = true
					}
				}
			}

			return true
		})
	}

	return undefined
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

// hasDefinitionOutside reports whether a macro named name is defined by a
// [macroDefBlock] that lives outside every removed section subtree.
func hasDefinitionOutside(root *block, name string, removedSet map[*block]bool) bool {
	found := false

	walk(root, func(blk *block) bool {
		// Don't count definitions inside the removed subtrees.
		if blk.Kind == sectionBlock && removedSet[blk] {
			return false
		}

		if blk.Kind == macroDefBlock && blk.Name == name {
			found = true

			return false
		}

		return true
	})

	return found
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
		case rootBlock, sectionBlock:
			// Containers: references live in their descendant leaves/headers.
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
	// Braced: %{ optional ! optional ? NAME ( } | : ... )
	// Bare:   %NAME terminated by \b
	pattern := `%(?:\{!?\??` + quoted + `[}:]|` + quoted + `\b)`

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
