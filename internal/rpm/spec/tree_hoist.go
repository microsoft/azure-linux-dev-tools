// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// ErrUnsafeMacroHoist is returned when moving a macro declaration out of a
// removed section could change RPM macro evaluation.
var ErrUnsafeMacroHoist = errors.New("unsafe macro hoist")

var undefineDirective = regexp.MustCompile(`(?i)^\s*%undefine\s+([[:alnum:]_.-]+)\b`)

const macroDirectiveSubmatches = 2

type macroDefinition struct {
	block       *block
	global      bool
	conditional bool
	parametered bool
	lua         bool
	removed     bool
	order       int
	refs        map[string]bool
	dynamicRefs []string
}

type macroReference struct {
	name  string
	order int
}

type macroDynamicReference struct {
	pattern string
	order   int
}

type macroDependencyTraversal struct {
	definition *macroDefinition
	useOrder   int
}

type selectedMacroClosure struct {
	definitions map[*macroDefinition]bool
}

type macroFacts struct {
	removed     []*macroDefinition
	all         map[string][]*macroDefinition
	references  []macroReference
	dynamicRefs []macroDynamicReference
	undefined   map[string]bool
	rootStarts  map[*block]int
}

// hoistReferencedMacros preserves only the exact declarations used by surviving
// references. RPM macro evaluation is contextual, so any ambiguous relocation
// is rejected rather than guessed.
func (t *specTree) hoistReferencedMacros(removeSet map[*block]bool) error {
	insertAt := -1

	for index, child := range t.root.Children {
		if containsRemovedSection(child, removeSet) {
			insertAt = index

			break
		}
	}

	if insertAt < 0 {
		return fmt.Errorf("finding removed root ancestor for macro hoist:\n%w", ErrUnsafeMacroHoist)
	}

	facts := collectMacroFacts(t.root, removeSet)
	if len(facts.removed) == 0 {
		return nil
	}

	closure, err := selectMacroClosure(facts)
	if err != nil {
		return err
	}

	if len(closure.definitions) == 0 {
		return validateDynamicMacroReferences(closure.definitions, facts)
	}

	if err := validateMacroHoist(closure, facts, facts.rootStarts[t.root.Children[insertAt]]); err != nil {
		return err
	}

	hoisted := make([]*block, 0, len(closure.definitions))
	for _, definition := range facts.removed {
		if !closure.definitions[definition] {
			continue
		}

		hoisted = append(hoisted, &block{
			Kind:   macroDefBlock,
			Header: definition.block.Header,
			Name:   definition.block.Name,
			Lines:  slices.Clone(definition.block.Lines),
		})
		slog.Debug("Hoisting macro definition from removed section", "macro", definition.block.Name)
	}

	t.root.Children = append(t.root.Children[:insertAt], append(hoisted, t.root.Children[insertAt:]...)...)

	return nil
}

func selectMacroClosure(facts *macroFacts) (*selectedMacroClosure, error) {
	closure := &selectedMacroClosure{
		definitions: make(map[*macroDefinition]bool),
	}
	selectedByName := make(map[string]*macroDefinition)
	traversed := make(map[macroDependencyTraversal]bool)

	var visit func(string, int) error

	visit = func(name string, useOrder int) error {
		definition := effectiveMacroDefinition(facts.all[name], useOrder)
		if definition == nil {
			return nil
		}

		if definition.removed {
			closure.definitions[definition] = true
		}

		traversal := macroDependencyTraversal{definition: definition, useOrder: useOrder}
		if traversed[traversal] {
			return nil
		}

		traversed[traversal] = true

		// A %global is expanded at its definition, while a %define remains
		// lazy. Follow lazy dependencies at each surviving use so a removed
		// definition they resolve to can be preserved. Traversal includes the
		// use order because separate invocations can resolve dependencies to
		// different declarations.
		dependencyOrder := useOrder
		if definition.global {
			dependencyOrder = definition.order - 1
		}

		for dependency := range definition.refs {
			if err := visit(dependency, dependencyOrder); err != nil {
				return err
			}
		}

		return nil
	}

	for _, reference := range facts.references {
		definition := effectiveMacroDefinition(facts.all[reference.name], reference.order)
		if definition != nil && definition.removed {
			if existing := selectedByName[reference.name]; existing != nil && existing != definition {
				return nil, fmt.Errorf("multiple removed declarations of macro %#q are effective at surviving references:\n%w",
					reference.name, ErrUnsafeMacroHoist)
			}

			selectedByName[reference.name] = definition
		}

		if err := visit(reference.name, reference.order); err != nil {
			return nil, err
		}
	}

	return closure, nil
}

func effectiveMacroDefinition(definitions []*macroDefinition, order int) *macroDefinition {
	var effective *macroDefinition
	for _, definition := range definitions {
		if definition.order <= order && (effective == nil || effective.order < definition.order) {
			effective = definition
		}
	}

	return effective
}

func validateMacroHoist(closure *selectedMacroClosure, facts *macroFacts, insertionOrder int) error {
	if err := validateSelectedMacroHoists(closure.definitions, facts, insertionOrder); err != nil {
		return err
	}

	if err := validateConditionalMacroSelections(closure.definitions, facts); err != nil {
		return err
	}

	if err := validateLazyDependencyBindings(closure.definitions, facts, insertionOrder); err != nil {
		return err
	}

	if err := validateDynamicMacroReferences(closure.definitions, facts); err != nil {
		return err
	}

	return nil
}

func validateSelectedMacroHoists(selected map[*macroDefinition]bool, facts *macroFacts, insertionOrder int) error {
	for definition := range selected {
		if definition.conditional || definition.parametered || definition.lua {
			return fmt.Errorf("cannot safely hoist macro %#q from conditional, parameterized, or Lua scope:\n%w",
				definition.block.Name, ErrUnsafeMacroHoist)
		}

		if facts.undefined[definition.block.Name] {
			return fmt.Errorf("cannot safely hoist macro %#q across '%%undefine':\n%w",
				definition.block.Name, ErrUnsafeMacroHoist)
		}

		for _, candidate := range facts.all[definition.block.Name] {
			if !candidate.removed &&
				insertionOrder < candidate.order && candidate.order < definition.order {
				return fmt.Errorf("cannot safely hoist macro %#q across surviving declaration:\n%w",
					definition.block.Name, ErrUnsafeMacroHoist)
			}
		}

		if err := validateRelocation(definition, facts.references, insertionOrder); err != nil {
			return err
		}

		if err := validateGlobalDependency(definition, facts); err != nil {
			return err
		}

		if err := validateSelectedGlobalDynamicBindings(definition, facts); err != nil {
			return err
		}
	}

	return nil
}

func validateSelectedGlobalDynamicBindings(definition *macroDefinition, facts *macroFacts) error {
	if !definition.global {
		return nil
	}

	for _, pattern := range definition.dynamicRefs {
		for _, candidate := range facts.all {
			for _, binding := range candidate {
				if dynamicMacroNameMayReferTo(pattern, binding.block.Name) {
					return fmt.Errorf("cannot safely hoist eager '%%global' macro %#q with dynamic name %#q:\n%w",
						definition.block.Name, pattern, ErrUnsafeMacroHoist)
				}
			}
		}
	}

	return nil
}

//nolint:cyclop,funlen // Selected closure validation follows both reachability and dependency bindings.
func validateConditionalMacroSelections(selected map[*macroDefinition]bool, facts *macroFacts) error {
	containsSelected := make(map[macroDependencyTraversal]bool)
	searching := make(map[macroDependencyTraversal]bool)
	relevant := make(map[macroDependencyTraversal]bool)

	var reachesSelected func(*macroDefinition, int) bool

	reachesSelected = func(definition *macroDefinition, useOrder int) bool {
		if definition == nil {
			return false
		}

		traversal := macroDependencyTraversal{definition: definition, useOrder: useOrder}
		if known, ok := containsSelected[traversal]; ok {
			return known
		}

		if searching[traversal] {
			return selected[definition]
		}

		searching[traversal] = true
		found := selected[definition]

		dependencyOrder := useOrder
		if definition.global {
			dependencyOrder = definition.order - 1
		}

		for dependency := range definition.refs {
			dependencyDefinition := effectiveMacroDefinition(facts.all[dependency], dependencyOrder)
			found = found || reachesSelected(dependencyDefinition, dependencyOrder)
		}

		delete(searching, traversal)
		containsSelected[traversal] = found

		return found
	}

	var markRelevant func(*macroDefinition, int)

	markRelevant = func(definition *macroDefinition, useOrder int) {
		if definition == nil {
			return
		}

		traversal := macroDependencyTraversal{definition: definition, useOrder: useOrder}
		if relevant[traversal] {
			return
		}

		relevant[traversal] = true

		dependencyOrder := useOrder
		if definition.global {
			dependencyOrder = definition.order - 1
		}

		for dependency := range definition.refs {
			markRelevant(effectiveMacroDefinition(facts.all[dependency], dependencyOrder), dependencyOrder)
		}
	}

	for _, reference := range facts.references {
		definition := effectiveMacroDefinition(facts.all[reference.name], reference.order)
		if reachesSelected(definition, reference.order) {
			markRelevant(definition, reference.order)
		}
	}

	for binding := range relevant {
		for _, candidate := range facts.all[binding.definition.block.Name] {
			if candidate.conditional && candidate.order <= binding.useOrder {
				return fmt.Errorf("cannot safely choose macro %#q around conditional declarations:\n%w",
					binding.definition.block.Name, ErrUnsafeMacroHoist)
			}
		}
	}

	return nil
}

func validateLazyDependencyBindings(selected map[*macroDefinition]bool, facts *macroFacts, insertionOrder int) error {
	visited := make(map[macroDependencyTraversal]bool)

	var visit func(*macroDefinition, int) error

	visit = func(definition *macroDefinition, useOrder int) error {
		if definition == nil || definition.global {
			return nil
		}

		traversal := macroDependencyTraversal{definition: definition, useOrder: useOrder}
		if visited[traversal] {
			return nil
		}

		visited[traversal] = true

		for dependency := range definition.refs {
			before := effectiveMacroDefinition(facts.all[dependency], useOrder)

			after := effectiveHoistedMacroDefinition(facts.all[dependency], selected, insertionOrder, useOrder)
			if before != after {
				return fmt.Errorf("cannot safely hoist dependency %#q used by lazy macro %#q:\n%w",
					dependency, definition.block.Name, ErrUnsafeMacroHoist)
			}

			if err := visit(before, useOrder); err != nil {
				return err
			}
		}

		return nil
	}

	for _, reference := range facts.references {
		definition := effectiveMacroDefinition(facts.all[reference.name], reference.order)
		if definition == nil || definition.global {
			continue
		}

		if err := visit(definition, reference.order); err != nil {
			return err
		}
	}

	return nil
}

func effectiveHoistedMacroDefinition(
	definitions []*macroDefinition,
	selected map[*macroDefinition]bool,
	insertionOrder int,
	useOrder int,
) *macroDefinition {
	var effective *macroDefinition

	effectiveOrder := -1

	for _, definition := range definitions {
		order := definition.order
		if definition.removed {
			if !selected[definition] {
				continue
			}

			order = insertionOrder
		}

		if order <= useOrder && order > effectiveOrder {
			effective = definition
			effectiveOrder = order
		}
	}

	return effective
}

func validateDynamicMacroReferences(selected map[*macroDefinition]bool, facts *macroFacts) error {
	dynamics := slices.Clone(facts.dynamicRefs)
	visited := make(map[macroDependencyTraversal]bool)

	for definition := range selected {
		for _, pattern := range definition.dynamicRefs {
			dynamics = append(dynamics, macroDynamicReference{pattern: pattern, order: definition.order})
		}
	}

	var visit func(*macroDefinition, int)

	visit = func(definition *macroDefinition, useOrder int) {
		if definition == nil || definition.global {
			return
		}

		traversal := macroDependencyTraversal{definition: definition, useOrder: useOrder}
		if visited[traversal] {
			return
		}

		visited[traversal] = true

		for _, pattern := range definition.dynamicRefs {
			dynamics = append(dynamics, macroDynamicReference{pattern: pattern, order: useOrder})
		}

		for dependency := range definition.refs {
			visit(effectiveMacroDefinition(facts.all[dependency], useOrder), useOrder)
		}
	}

	for _, reference := range facts.references {
		visit(effectiveMacroDefinition(facts.all[reference.name], reference.order), reference.order)
	}

	for _, dynamic := range dynamics {
		for _, definition := range facts.removed {
			if (definition.order <= dynamic.order || selected[definition]) &&
				dynamicMacroNameMayReferTo(dynamic.pattern, definition.block.Name) {
				return fmt.Errorf("cannot safely resolve dynamic macro name %#q:\n%w",
					dynamic.pattern, ErrUnsafeMacroHoist)
			}
		}
	}

	return nil
}

func dynamicMacroNameMayReferTo(pattern, name string) bool {
	prefixEnd := strings.IndexByte(pattern, '%')
	if prefixEnd < 0 {
		return false
	}

	suffixStart := strings.LastIndexByte(pattern, '}') + 1
	prefix, suffix := pattern[:prefixEnd], pattern[suffixStart:]

	return strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix)
}

func validateRelocation(definition *macroDefinition, references []macroReference, insertionOrder int) error {
	if definition.order <= insertionOrder {
		return nil
	}

	for _, reference := range references {
		if reference.name == definition.block.Name &&
			insertionOrder <= reference.order && reference.order < definition.order {
			return fmt.Errorf("cannot safely hoist macro %#q across an earlier surviving reference:\n%w",
				definition.block.Name, ErrUnsafeMacroHoist)
		}
	}

	return nil
}

func validateGlobalDependency(definition *macroDefinition, facts *macroFacts) error {
	if !definition.global {
		return nil
	}

	for dependency := range definition.refs {
		if effectiveMacroDefinition(facts.all[dependency], definition.order-1) == nil {
			for _, candidate := range facts.all[dependency] {
				if candidate == definition {
					return fmt.Errorf("cannot safely hoist eager '%%global' macro %#q before dependency %#q:\n%w",
						definition.block.Name, dependency, ErrUnsafeMacroHoist)
				}
			}
		}

		for _, candidate := range facts.all[dependency] {
			if candidate == definition {
				continue
			}

			if !candidate.removed || candidate.order >= definition.order {
				return fmt.Errorf("cannot safely hoist eager '%%global' macro %#q before dependency %#q:\n%w",
					definition.block.Name, dependency, ErrUnsafeMacroHoist)
			}
		}

		if facts.undefined[dependency] {
			return fmt.Errorf("cannot safely hoist eager '%%global' macro %#q across '%%undefine' of %#q:\n%w",
				definition.block.Name, dependency, ErrUnsafeMacroHoist)
		}
	}

	return nil
}

//nolint:cyclop,funlen,gocognit // Macro facts require one pass over structural block types.
func collectMacroFacts(root *block, removeSet map[*block]bool) *macroFacts {
	facts := &macroFacts{
		all:        make(map[string][]*macroDefinition),
		undefined:  make(map[string]bool),
		rootStarts: make(map[*block]int),
	}
	order := 0

	addReferences := func(content string, referenceOrder int) {
		references, dynamics := macroReferenceDetails(content)
		for name := range references {
			facts.references = append(facts.references, macroReference{name: name, order: referenceOrder})
		}

		for _, pattern := range dynamics {
			facts.dynamicRefs = append(facts.dynamicRefs, macroDynamicReference{pattern: pattern, order: referenceOrder})
		}
	}

	var walk func(*block, bool, bool)

	walk = func(current *block, removed, conditional bool) {
		removed = removed || removeSet[current]
		conditional = conditional || current.Kind == conditionalBlock

		switch current.Kind {
		case rootBlock:
			for _, child := range current.Children {
				facts.rootStarts[child] = order
				walk(child, false, false)
			}

			return
		case sectionBlock:
			if !removed {
				addReferences(sectionHeaderArguments(current.Header, current.Name), order)
			}

			order++
		case macroDefBlock:
			references, dynamics := macroReferenceDetails(strings.Join(current.Lines, "\n"))
			definition := &macroDefinition{
				block:       current,
				global:      strings.HasPrefix(strings.ToLower(strings.TrimSpace(current.Header)), "%global"),
				conditional: conditional,
				parametered: strings.Contains(strings.Fields(strings.TrimSpace(current.Header))[1], "("),
				lua:         strings.Contains(strings.Join(current.Lines, "\n"), "%{lua:"),
				removed:     removed,
				order:       order,
				refs:        references,
				dynamicRefs: dynamics,
			}

			facts.all[current.Name] = append(facts.all[current.Name], definition)
			if removed {
				facts.removed = append(facts.removed, definition)
			} else if definition.global {
				// A %define body is expanded only at an invocation, so recording
				// its references here would treat them as real earlier uses.
				addReferences(strings.Join(current.Lines, "\n"), order-1)
			}

			order++
		case textBlock:
			for _, line := range current.Lines {
				if matches := undefineDirective.FindStringSubmatch(line); len(matches) == macroDirectiveSubmatches {
					facts.undefined[matches[1]] = true
				}

				if !removed {
					addReferences(line, order)
				}

				order++
			}
		case conditionalBlock:
			if !removed {
				addReferences(current.Header, order)
				addReferences(current.ElseDirective, order)
			}

			order++
		}

		for _, child := range current.Children {
			walk(child, removed, conditional)
		}

		for _, child := range current.Else {
			walk(child, removed, conditional)
		}
	}

	walk(root, false, false)

	return facts
}

// sectionHeaderArguments excludes the section marker, which RPM does not expand,
// while retaining the raw argument text where macro references are meaningful.
func sectionHeaderArguments(header, name string) string {
	header = strings.TrimLeftFunc(header, unicode.IsSpace)
	if !strings.HasPrefix(strings.ToLower(header), strings.ToLower(name)) {
		return ""
	}

	return header[len(name):]
}

func macroReferences(content string) map[string]bool {
	refs := make(map[string]bool)
	scanMacroReferences(content, refs, nil)

	return refs
}

func macroReferenceDetails(content string) (map[string]bool, []string) {
	refs := make(map[string]bool)

	var dynamics []string
	scanMacroReferences(content, refs, &dynamics)

	return refs, dynamics
}

func scanMacroReferences(content string, refs map[string]bool, dynamics *[]string) {
	for index := 0; index < len(content); {
		if content[index] != '%' {
			index++

			continue
		}

		runEnd := index
		for runEnd < len(content) && content[runEnd] == '%' {
			runEnd++
		}

		if runEnd == len(content) {
			index = runEnd

			continue
		}

		if (runEnd-index)%2 == 0 {
			index = runEnd

			continue
		}

		if content[runEnd] == '{' && percentRunOpensBracedMacro(content, index) {
			end, ok := bracedMacroEnd(content, runEnd+1)
			if !ok {
				index = runEnd + 1

				continue
			}

			addBracedMacroReference(content[runEnd+1:end], refs, dynamics)
			index = end + 1

			continue
		}

		if macroNameStart(content[runEnd]) {
			end := runEnd + 1
			for end < len(content) && macroNameCharacter(content[end]) {
				end++
			}

			if !macroDirectiveName(content[runEnd:end]) {
				refs[content[runEnd:end]] = true
			}

			index = end

			continue
		}

		index = runEnd + 1
	}
}

func addBracedMacroReference(body string, refs map[string]bool, dynamics *[]string) {
	name := strings.TrimLeft(body, "!?")

	fields := strings.Fields(name)
	if len(fields) == 0 {
		return
	}

	addBracedMacroName(fields, refs, dynamics)
	scanMacroReferences(body, refs, dynamics)
}

func addBracedMacroName(fields []string, refs map[string]bool, dynamics *[]string) {
	if macroTestDirectiveName(fields[0]) {
		if len(fields) > 1 {
			refs[fields[1]] = true
		}

		return
	}

	macroName := strings.Split(fields[0], ":")[0]
	if strings.Contains(macroName, "%") {
		if dynamics != nil {
			*dynamics = append(*dynamics, macroName)
		}

		return
	}

	if !macroDirectiveName(macroName) {
		refs[macroName] = true
	}
}

func bracedMacroEnd(content string, start int) (int, bool) {
	depth := 1

	for index := start; index < len(content); index++ {
		if content[index] == '%' {
			nextIndex, nested, ok := bracedPercentIndex(content, index)
			if !ok {
				return 0, false
			}

			if nested {
				depth++
			}

			index = nextIndex

			continue
		}

		if content[index] == '}' {
			depth--
			if depth == 0 {
				return index, true
			}
		}
	}

	return 0, false
}

func bracedPercentIndex(content string, index int) (nextIndex int, nested bool, ok bool) {
	runEnd := index
	for runEnd < len(content) && content[runEnd] == '%' {
		runEnd++
	}

	if runEnd == len(content) || content[runEnd] != '{' {
		return runEnd - 1, false, true
	}

	if percentRunOpensBracedMacro(content, index) {
		return runEnd, true, true
	}

	escapedEnd, ok := escapedBracedMacroEnd(content, runEnd+1)

	return escapedEnd, false, ok
}

func escapedBracedMacroEnd(content string, start int) (int, bool) {
	depth := 1

	for index := start; index < len(content); index++ {
		switch content[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index, true
			}
		}
	}

	return 0, false
}

func macroNameStart(character byte) bool {
	return character == '_' || (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

func macroNameCharacter(character byte) bool {
	return macroNameStart(character) ||
		(character >= '0' && character <= '9') ||
		character == '.' || character == '-'
}

func macroDirectiveName(name string) bool {
	switch strings.ToLower(name) {
	case "define", "defined", "global", "undefine", "undefined",
		"if", "else", "elif", "endif", "ifarch", "ifnarch", "ifos", "ifnos":
		return true
	}

	return false
}

func macroTestDirectiveName(name string) bool {
	switch strings.ToLower(name) {
	case "defined", "undefined":
		return true
	}

	return false
}
