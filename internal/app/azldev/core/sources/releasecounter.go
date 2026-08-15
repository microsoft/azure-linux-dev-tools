// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
)

type releaseCounterChange struct {
	OldValue string
	NewValue string
}

type releaseTagCounterCandidate struct {
	lineNumber int
	rawLine    string
	value      string
	match      []int
}

type specMacroCounterDefinition struct {
	directive    string
	name         string
	lineNumber   int
	rawLine      string
	counterStart int
	counterEnd   int
	counterValue string
}

// DefaultReleaseCounter returns the built-in counter used for standard static
// Release tags: a leading integer optionally followed by a dist macro.
func DefaultReleaseCounter() projectconfig.ReleaseCounter {
	return projectconfig.ReleaseCounter{
		Source: projectconfig.ReleaseCounterSourceReleaseTag,
		Regex:  staticReleasePattern.String(),
	}
}

// ValidateReleaseCounterInSpec verifies that a release counter resolves to exactly one physical
// target in a rendered spec. It only parses and mutates an in-memory representation, so it is
// safe for CI drift checks. The component's build configuration is required because it decides
// which macros the build environment supplies; pass the same configuration a render would use.
func ValidateReleaseCounterInSpec(
	fs opctx.FS,
	counter projectconfig.ReleaseCounter,
	specPath string,
	build projectconfig.ComponentBuildConfig,
) error {
	if err := counter.Validate(); err != nil {
		return fmt.Errorf("validating release counter:\n%w", err)
	}

	specFile, err := fs.Open(specPath)
	if err != nil {
		return fmt.Errorf("opening spec %#q for reading:\n%w", specPath, err)
	}
	defer specFile.Close()

	openedSpec, err := spec.OpenSpec(specFile)
	if err != nil {
		return fmt.Errorf("loading spec %#q:\n%w", specPath, err)
	}

	switch counter.Source {
	case projectconfig.ReleaseCounterSourceReleaseTag:
		_, err = bumpReleaseTagCounter(fs, openedSpec, specPath, build, counter.Regex, 0)
	case projectconfig.ReleaseCounterSourceSpecMacro:
		_, err = bumpSpecMacroCounter(openedSpec, counter.Directive, counter.Name, 0)
	default:
		return fmt.Errorf("unknown release counter source %#q", counter.Source)
	}

	if err != nil {
		return fmt.Errorf("validating release counter in spec %#q:\n%w", specPath, err)
	}

	return nil
}

// BumpReleaseCounterInSpecFile increments a component's release counter in place, exactly as a
// render would, and reports the counter value before and after the bump. Callers that only
// want to reason about a hypothetical bump must pass a copy of the rendered output directory:
// the counter's backing file is rewritten in place. Depending on the configured source that
// file is either the spec itself or the sibling '.azl.macros' file.
func BumpReleaseCounterInSpecFile(
	fs opctx.FS,
	counter projectconfig.ReleaseCounter,
	specPath string,
	build projectconfig.ComponentBuildConfig,
	increment int,
) (oldValue string, newValue string, err error) {
	change, err := applyReleaseCounterToFileInPlace(fs, counter, specPath, build, increment)
	if err != nil {
		return "", "", err
	}

	return change.OldValue, change.NewValue, nil
}

func applyReleaseCounterToFileInPlace(
	fs opctx.FS,
	counter projectconfig.ReleaseCounter,
	specPath string,
	build projectconfig.ComponentBuildConfig,
	commitCount int,
) (change releaseCounterChange, err error) {
	if err := counter.Validate(); err != nil {
		return releaseCounterChange{}, fmt.Errorf("validating release counter:\n%w", err)
	}

	specFile, err := fs.Open(specPath)
	if err != nil {
		return releaseCounterChange{}, fmt.Errorf("opening spec %#q for reading:\n%w", specPath, err)
	}

	openedSpec, err := spec.OpenSpec(specFile)
	closeErr := specFile.Close()

	if err != nil {
		return releaseCounterChange{}, fmt.Errorf("loading spec %#q:\n%w", specPath, err)
	}

	if closeErr != nil {
		return releaseCounterChange{}, fmt.Errorf("closing spec %#q after reading:\n%w", specPath, closeErr)
	}

	switch counter.Source {
	case projectconfig.ReleaseCounterSourceReleaseTag:
		change, err = bumpReleaseTagCounter(fs, openedSpec, specPath, build, counter.Regex, commitCount)
	case projectconfig.ReleaseCounterSourceSpecMacro:
		change, err = bumpSpecMacroCounter(openedSpec, counter.Directive, counter.Name, commitCount)
	default:
		return releaseCounterChange{}, fmt.Errorf("unknown release counter source %#q", counter.Source)
	}

	if err != nil {
		return releaseCounterChange{}, err
	}

	specFile, err = fs.OpenFile(specPath, os.O_RDWR|os.O_TRUNC, fileperms.PrivateFile)
	if err != nil {
		return releaseCounterChange{}, fmt.Errorf("opening spec %#q for writing:\n%w", specPath, err)
	}
	defer defers.HandleDeferError(specFile.Close, &err)

	if err := openedSpec.Serialize(specFile); err != nil {
		return releaseCounterChange{}, fmt.Errorf("serializing updated spec %#q:\n%w", specPath, err)
	}

	return change, nil
}

// bumpReleaseTagCounter rewrites the counter captured by pattern in the main-package Release
// tag. It is the single entry point for release-tag counters on both the render and the check
// path, so the two cannot disagree about which macros count as defined: specPath locates the
// sibling macros file and build supplies the macros rpmbuild defines from component config.
func bumpReleaseTagCounter(
	fs opctx.FS,
	openedSpec *spec.Spec,
	specPath string,
	build projectconfig.ComponentBuildConfig,
	pattern string,
	commitCount int,
) (releaseCounterChange, error) {
	candidate, err := resolveReleaseTagCandidate(openedSpec, pattern)
	if err != nil {
		return releaseCounterChange{}, err
	}

	definedMacros, err := collectVisibleMacroNames(fs, openedSpec, specPath)
	if err != nil {
		return releaseCounterChange{}, err
	}

	if err := validateReleaseTagCaptureTarget(
		candidate.value, candidate.match[2], candidate.match[3], definedMacros, build,
	); err != nil {
		return releaseCounterChange{}, fmt.Errorf(
			"release counter regex %#q targets an unbumpable position:\n%w", pattern, err)
	}

	newValue, oldCounter, newCounter, err := incrementCapturedCounter(
		candidate.value,
		candidate.match[2],
		candidate.match[3],
		commitCount,
	)
	if err != nil {
		return releaseCounterChange{}, fmt.Errorf("bumping Release value %#q:\n%w", candidate.value, err)
	}

	newLine, err := replaceTagValue(candidate.rawLine, candidate.value, newValue)
	if err != nil {
		return releaseCounterChange{}, fmt.Errorf("updating Release tag at line %d:\n%w", candidate.lineNumber+1, err)
	}

	openedSpec.ReplaceLine(candidate.lineNumber, newLine)

	return releaseCounterChange{OldValue: oldCounter, NewValue: newCounter}, nil
}

// resolveReleaseTagCandidate finds the single main-package Release tag fully matched by the
// counter regex and confirms the regex captured exactly one group.
func resolveReleaseTagCandidate(
	openedSpec *spec.Spec,
	pattern string,
) (releaseTagCounterCandidate, error) {
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return releaseTagCounterCandidate{},
			fmt.Errorf("compiling release counter regex %#q:\n%w", pattern, err)
	}

	var candidates []releaseTagCounterCandidate

	err = openedSpec.VisitTagsPackage("", func(tagLine *spec.TagLine, ctx *spec.Context) error {
		if !strings.EqualFold(tagLine.Tag, "Release") {
			return nil
		}

		match := compiled.FindStringSubmatchIndex(tagLine.Value)
		if match == nil || match[0] != 0 || match[1] != len(tagLine.Value) {
			return nil
		}

		if ctx.RawLine == nil {
			return fmt.Errorf("matched Release tag at line %d has no raw line", ctx.CurrentLineNum+1)
		}

		candidates = append(candidates, releaseTagCounterCandidate{
			lineNumber: ctx.CurrentLineNum,
			rawLine:    *ctx.RawLine,
			value:      tagLine.Value,
			match:      match,
		})

		return nil
	})
	if err != nil {
		return releaseTagCounterCandidate{}, fmt.Errorf("visiting main-package Release tags:\n%w", err)
	}

	if len(candidates) != 1 {
		return releaseTagCounterCandidate{}, fmt.Errorf(
			"release counter regex %#q matched %d main-package Release tags; expected exactly one",
			pattern, len(candidates))
	}

	candidate := candidates[0]
	if len(candidate.match) != 4 || candidate.match[2] < 0 || candidate.match[3] < 0 {
		return releaseTagCounterCandidate{}, fmt.Errorf(
			"release counter regex %#q did not capture an integer in Release value %#q",
			pattern, candidate.value)
	}

	return candidate, nil
}

// collectSpecMacroDefinitions indexes every '%global' and '%define' in a spec by macro name.
// Names may map to several definitions; callers that intend to bump one reject that case.
func collectSpecMacroDefinitions(openedSpec *spec.Spec) (map[string][]specMacroCounterDefinition, error) {
	definitionsByName := make(map[string][]specMacroCounterDefinition)

	err := openedSpec.Visit(func(ctx *spec.Context) error {
		if ctx.RawLine == nil {
			return nil
		}

		definition, matches := parseSpecMacroDefinition(*ctx.RawLine)
		if !matches {
			return nil
		}

		definition.lineNumber = ctx.CurrentLineNum
		definitionsByName[definition.name] = append(definitionsByName[definition.name], definition)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("visiting spec macros:\n%w", err)
	}

	return definitionsByName, nil
}

func bumpSpecMacroCounter(
	openedSpec *spec.Spec,
	directive string,
	name string,
	commitCount int,
) (releaseCounterChange, error) {
	definitionsByName, err := collectSpecMacroDefinitions(openedSpec)
	if err != nil {
		return releaseCounterChange{}, err
	}

	var definitions []specMacroCounterDefinition

	for _, definition := range definitionsByName[name] {
		if definition.directive == directive {
			definitions = append(definitions, definition)
		}
	}

	if len(definitions) != 1 {
		return releaseCounterChange{}, fmt.Errorf(
			"spec macro %%%s %s has %d physical definitions; expected exactly one",
			directive, name, len(definitions))
	}

	if allDefinitions := definitionsByName[name]; len(allDefinitions) != 1 {
		return releaseCounterChange{}, fmt.Errorf(
			"spec macro %s has %d physical definitions across %%global/%%define directives; expected exactly one",
			name, len(allDefinitions))
	}

	definition := definitions[0]
	if !isDecimalInteger(definition.counterValue) {
		return releaseCounterChange{}, fmt.Errorf(
			"spec macro %%%s %s must define only a bare integer, got %#q",
			directive, name, definition.counterValue)
	}

	if err := verifyMainPackageReleaseReferencesMacro(openedSpec, name, definitionsByName); err != nil {
		return releaseCounterChange{}, err
	}

	newCounter, err := incrementDecimalInteger(definition.counterValue, commitCount)
	if err != nil {
		return releaseCounterChange{}, fmt.Errorf(
			"bumping spec macro %%%s %s:\n%w", directive, name, err)
	}

	newLine := definition.rawLine[:definition.counterStart] + newCounter + definition.rawLine[definition.counterEnd:]
	openedSpec.ReplaceLine(definition.lineNumber, newLine)

	return releaseCounterChange{OldValue: definition.counterValue, NewValue: newCounter}, nil
}

func parseSpecMacroCounterDefinition(
	rawLine string,
	directive string,
	name string,
) (specMacroCounterDefinition, bool) {
	definition, matches := parseSpecMacroDefinition(rawLine)
	if !matches || definition.directive != directive || definition.name != name {
		return specMacroCounterDefinition{}, false
	}

	return definition, true
}

func parseSpecMacroDefinition(rawLine string) (specMacroCounterDefinition, bool) {
	linePos := skipHorizontalWhitespace(rawLine, 0)

	var directive string
	for _, candidate := range []string{"global", "define"} {
		directiveToken := "%" + candidate
		if strings.HasPrefix(rawLine[linePos:], directiveToken) {
			directive = candidate
			linePos += len(directiveToken)

			break
		}
	}

	if directive == "" {
		return specMacroCounterDefinition{}, false
	}

	if linePos == len(rawLine) || !isHorizontalWhitespace(rawLine[linePos]) {
		return specMacroCounterDefinition{}, false
	}

	linePos = skipHorizontalWhitespace(rawLine, linePos)

	nameStart := linePos
	for linePos < len(rawLine) && !isHorizontalWhitespace(rawLine[linePos]) {
		linePos++
	}

	if nameStart == linePos {
		return specMacroCounterDefinition{}, false
	}

	counterStart := skipHorizontalWhitespace(rawLine, linePos)

	counterEnd := len(rawLine)
	for counterEnd > counterStart && isHorizontalWhitespace(rawLine[counterEnd-1]) {
		counterEnd--
	}

	return specMacroCounterDefinition{
		directive:    directive,
		name:         rawLine[nameStart:linePos],
		rawLine:      rawLine,
		counterStart: counterStart,
		counterEnd:   counterEnd,
		counterValue: rawLine[counterStart:counterEnd],
	}, true
}

// verifyMainPackageReleaseReferencesMacro proves that every main-package Release tag value
// transitively expands the named macro. definitionsByName must contain every macro definition
// visible to the rendered spec. Without this proof a counter bump can leave the rendered
// NEVRA unchanged.
func verifyMainPackageReleaseReferencesMacro(
	openedSpec *spec.Spec,
	macroName string,
	definitionsByName map[string][]specMacroCounterDefinition,
) error {
	var releaseValues []string

	err := openedSpec.VisitTagsPackage("", func(tagLine *spec.TagLine, _ *spec.Context) error {
		if strings.EqualFold(tagLine.Tag, "Release") {
			releaseValues = append(releaseValues, tagLine.Value)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("visiting main-package Release tags:\n%w", err)
	}

	if len(releaseValues) == 0 {
		return fmt.Errorf("no main-package Release tag references macro %%%s", macroName)
	}

	for _, releaseValue := range releaseValues {
		if !valueTransitivelyReferencesMacro(releaseValue, macroName, definitionsByName, make(map[string]bool)) {
			return fmt.Errorf(
				"main-package Release value %#q does not transitively reference macro %%%s",
				releaseValue, macroName)
		}
	}

	return nil
}

var macroReferencePattern = regexp.MustCompile(`%(?:\{[?!]?([A-Za-z_][A-Za-z0-9_]*)|([A-Za-z_][A-Za-z0-9_]*))`)

func valueTransitivelyReferencesMacro(
	value string,
	target string,
	definitionsByName map[string][]specMacroCounterDefinition,
	visiting map[string]bool,
) bool {
	for _, match := range macroReferencePattern.FindAllStringSubmatch(value, -1) {
		referencedName := match[1]
		if referencedName == "" {
			referencedName = match[2]
		}

		if referencedName == target {
			return true
		}

		if visiting[referencedName] {
			continue
		}

		definitions := definitionsByName[referencedName]
		if len(definitions) == 0 {
			continue
		}

		visiting[referencedName] = true
		allDefinitionsReferenceTarget := true

		for _, definition := range definitions {
			if !valueTransitivelyReferencesMacro(
				definition.counterValue, target, definitionsByName, visiting) {
				allDefinitionsReferenceTarget = false

				break
			}
		}

		delete(visiting, referencedName)

		if allDefinitionsReferenceTarget {
			return true
		}
	}

	return false
}

func replaceTagValue(rawLine string, oldValue string, newValue string) (string, error) {
	colonIndex := strings.IndexByte(rawLine, ':')
	if colonIndex < 0 {
		return "", fmt.Errorf("release tag line %#q does not contain ':'", rawLine)
	}

	valueOffset := strings.Index(rawLine[colonIndex+1:], oldValue)
	if valueOffset < 0 {
		return "", fmt.Errorf("release tag line %#q does not contain parsed value %#q", rawLine, oldValue)
	}

	valueOffset += colonIndex + 1

	return rawLine[:valueOffset] + newValue + rawLine[valueOffset+len(oldValue):], nil
}

func incrementCapturedCounter(value string, start int, end int, commitCount int) (string, string, string, error) {
	if start < 0 || end < start || end > len(value) {
		return "", "", "", fmt.Errorf("invalid counter capture span [%d:%d]", start, end)
	}

	oldCounter := value[start:end]

	newCounter, err := incrementDecimalInteger(oldCounter, commitCount)
	if err != nil {
		return "", "", "", err
	}

	return value[:start] + newCounter + value[end:], oldCounter, newCounter, nil
}

func incrementDecimalInteger(counter string, commitCount int) (string, error) {
	if !isDecimalInteger(counter) {
		return "", fmt.Errorf("counter value %#q must contain only digits", counter)
	}

	if commitCount < 0 {
		return "", fmt.Errorf("commit count %d must not be negative", commitCount)
	}

	current, err := strconv.ParseUint(counter, 10, 64)
	if err != nil {
		return "", fmt.Errorf("parsing counter value %#q:\n%w", counter, err)
	}

	increment := uint64(commitCount)
	if increment > ^uint64(0)-current {
		return "", fmt.Errorf("incrementing counter value %#q by %d overflows", counter, commitCount)
	}

	updated := strconv.FormatUint(current+increment, 10)
	if len(updated) < len(counter) {
		updated = strings.Repeat("0", len(counter)-len(updated)) + updated
	}

	return updated, nil
}

func isDecimalInteger(value string) bool {
	if value == "" {
		return false
	}

	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}

	return true
}

func skipHorizontalWhitespace(value string, start int) int {
	for start < len(value) && isHorizontalWhitespace(value[start]) {
		start++
	}

	return start
}

func isHorizontalWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r'
}
