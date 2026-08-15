// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// capturePlacement describes where a release counter's capture group lands once the Release
// tag value is parsed as a sequence of RPM macro references.
type capturePlacement int

const (
	// capturePlacementLiteral means the capture lands on literal text that always renders.
	capturePlacementLiteral capturePlacement = iota
	// capturePlacementMacroName means the capture lands inside a macro reference's identifier.
	capturePlacementMacroName
	// capturePlacementConditionalBody means the capture lands inside the body of a
	// '%{?name:body}' conditional expansion.
	capturePlacementConditionalBody
)

// macroConstruct is a single RPM macro reference found inside a Release tag value, expressed
// as character spans into that value. [macroConstruct.bodyStart] is negative for constructs
// that have no ':' body.
type macroConstruct struct {
	start     int
	end       int
	nameStart int
	nameEnd   int
	bodyStart int
	bodyEnd   int
	children  []macroConstruct
}

func (construct macroConstruct) hasBody() bool {
	return construct.bodyStart >= 0
}

var (
	bareMacroReferencePattern = regexp.MustCompile(`^%[A-Za-z_][A-Za-z0-9_]*`)
	undefineDirectivePattern  = regexp.MustCompile(`^\s*%undefine\s+([A-Za-z_][A-Za-z0-9_]*)\s*$`)
)

// isDistributionMacro reports whether a macro is supplied by the target distribution's own
// macro files regardless of the component: 'dist' comes from the mock configuration, 'rhel' and
// 'fedora' from the distribution release macros.
func isDistributionMacro(name string) bool {
	switch name {
	case "dist", "fedora", "rhel":
		return true
	default:
		return false
	}
}

// isBuildEnvironmentMacro reports whether a macro is supplied by the RPM build environment
// rather than by the spec itself. Such macros are treated as defined even though no '%global'
// or '%define' declares them, so a capture inside '%{?name:...}' does render and is safe to
// bump. Besides the distribution macros, a component's own [projectconfig.ComponentBuildConfig]
// reaches rpmbuild as '--with' / '--without' / '-D' flags; [buildMacrosMap] resolves exactly
// that set (including the '_with_'/'_without_' name mangling and the 'build.undefines'
// removals), so the rule stays tied to the macros this component's build really defines
// instead of blanket-matching a '_with_' prefix.
func isBuildEnvironmentMacro(name string, build projectconfig.ComponentBuildConfig) bool {
	if isDistributionMacro(name) {
		return true
	}

	_, defined := buildMacrosMap(build)[name]

	return defined
}

// validateReleaseTagCaptureTarget rejects a release counter whose capture group cannot
// deterministically change the rendered Release tag. Three placements are rejected: a capture
// inside the body of a conditional whose macro is never defined (renders to nothing), a capture
// inside a macro name (rewrites the reference into a different, undefined macro), and a capture
// on the leading '0' of a '0.' pre-release marker (would sort above the final release).
func validateReleaseTagCaptureTarget(
	value string,
	captureStart int,
	captureEnd int,
	definedMacros map[string]bool,
	build projectconfig.ComponentBuildConfig,
) error {
	captured := value[captureStart:captureEnd]

	if isPreReleaseLeadingZero(value, captureStart, captured) {
		return preReleaseLeadingZeroError(value)
	}

	placement, construct := classifyCapture(captureStart, parseMacroConstructs(value, 0))

	switch placement {
	case capturePlacementMacroName:
		return macroNameCaptureError(value, captured, value[construct.nameStart:construct.nameEnd])
	case capturePlacementConditionalBody:
		macroName := value[construct.nameStart:construct.nameEnd]
		if definedMacros[macroName] || isBuildEnvironmentMacro(macroName, build) {
			return nil
		}

		return undefinedConditionalCaptureError(value, captured, macroName)
	case capturePlacementLiteral:
	}

	return nil
}

// isPreReleaseLeadingZero reports whether the capture is the leading '0' of a '0.'-prefixed
// Release value, the Fedora convention for marking a pre-release.
func isPreReleaseLeadingZero(value string, captureStart int, captured string) bool {
	return captureStart == 0 && captured == "0" && strings.HasPrefix(value[len(captured):], ".")
}

// collectVisibleMacroNames returns every macro name that is defined by the time a rendered
// spec's Release tag is evaluated. That is more than the spec's own declarations: the sibling
// macros file is pulled in by a '%{load:}' directive, so its definitions — whether generated
// from 'build.defines' or checked in next to a local spec — are visible to the Release tag too.
// The macros file seeds the walk rather than being merged afterwards because the '%{load:}'
// runs inside the spec, so a later '%undefine' in the spec still wins.
func collectVisibleMacroNames(
	fs opctx.FS,
	openedSpec *spec.Spec,
	specPath string,
) (map[string]bool, error) {
	definedMacros, err := collectMacrosFileMacroNames(fs, filepath.Dir(specPath))
	if err != nil {
		return nil, err
	}

	if err := addSpecDefinedMacroNames(openedSpec, definedMacros); err != nil {
		return nil, err
	}

	return definedMacros, nil
}

// addSpecDefinedMacroNames records every macro name declared by a '%global' or '%define' in the
// spec into definedMacros, honoring later '%undefine' directives.
func addSpecDefinedMacroNames(openedSpec *spec.Spec, definedMacros map[string]bool) error {
	err := openedSpec.Visit(func(ctx *spec.Context) error {
		if ctx.RawLine == nil {
			return nil
		}

		if match := undefineDirectivePattern.FindStringSubmatch(*ctx.RawLine); match != nil {
			delete(definedMacros, match[1])

			return nil
		}

		if definition, matches := parseSpecMacroDefinition(*ctx.RawLine); matches {
			definedMacros[definition.name] = true
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("visiting spec macro definitions:\n%w", err)
	}

	return nil
}

// parseMacroConstructs scans a Release tag value for RPM macro references. The offset is added
// to every recorded span so nested bodies report positions in the original value.
func parseMacroConstructs(value string, offset int) []macroConstruct {
	var constructs []macroConstruct

	position := 0
	for position < len(value) {
		if value[position] != '%' {
			position++

			continue
		}

		if position+1 < len(value) && value[position+1] == '{' {
			closing := matchingBraceIndex(value, position+1)
			if closing < 0 {
				position++

				continue
			}

			constructs = append(constructs, parseBracedMacroConstruct(value, offset, position, closing))
			position = closing + 1

			continue
		}

		reference := bareMacroReferencePattern.FindString(value[position:])
		if reference == "" {
			position++

			continue
		}

		constructs = append(constructs, macroConstruct{
			start:     offset + position,
			end:       offset + position + len(reference),
			nameStart: offset + position + 1,
			nameEnd:   offset + position + len(reference),
			bodyStart: -1,
			bodyEnd:   -1,
		})
		position += len(reference)
	}

	return constructs
}

// parseBracedMacroConstruct builds the construct for a '%{...}' reference whose opening '%' is
// at start and whose matching '}' is at closing.
func parseBracedMacroConstruct(value string, offset int, start int, closing int) macroConstruct {
	innerStart := start + len("%{")
	inner := value[innerStart:closing]

	flagLength := 0
	for flagLength < len(inner) && (inner[flagLength] == '?' || inner[flagLength] == '!') {
		flagLength++
	}

	construct := macroConstruct{
		start:     offset + start,
		end:       offset + closing + 1,
		nameStart: offset + innerStart + flagLength,
		nameEnd:   offset + closing,
		bodyStart: -1,
		bodyEnd:   -1,
	}

	colon := strings.IndexByte(inner, ':')
	if colon < 0 {
		return construct
	}

	bodyStart := innerStart + colon + 1
	construct.nameEnd = offset + innerStart + colon
	construct.bodyStart = offset + bodyStart
	construct.bodyEnd = offset + closing
	construct.children = parseMacroConstructs(value[bodyStart:closing], offset+bodyStart)

	return construct
}

// matchingBraceIndex returns the index of the '}' closing the '{' at openIndex, or -1 when the
// braces are unbalanced.
func matchingBraceIndex(value string, openIndex int) int {
	depth := 0

	for index := openIndex; index < len(value); index++ {
		switch value[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index
			}
		}
	}

	return -1
}

// classifyCapture locates the innermost construct covering position and reports how the capture
// relates to it. The returned construct is meaningless when the placement is literal.
func classifyCapture(position int, constructs []macroConstruct) (capturePlacement, macroConstruct) {
	for _, construct := range constructs {
		if position < construct.start || position >= construct.end {
			continue
		}

		if position >= construct.nameStart && position < construct.nameEnd {
			return capturePlacementMacroName, construct
		}

		if construct.hasBody() && position >= construct.bodyStart && position < construct.bodyEnd {
			nestedPlacement, nested := classifyCapture(position, construct.children)
			if nestedPlacement != capturePlacementLiteral {
				return nestedPlacement, nested
			}

			return capturePlacementConditionalBody, construct
		}

		// The capture landed on the construct's punctuation ('%', '{', '}' or a '?'/'!' flag),
		// which is part of the reference itself and never safe to rewrite.
		return capturePlacementMacroName, construct
	}

	return capturePlacementLiteral, macroConstruct{}
}

func undefinedConditionalCaptureError(value string, captured string, macroName string) error {
	return fmt.Errorf(
		"capture %#q in Release value %#q sits inside the conditional expansion %%{?%s:...}, but "+
			"%#q is never defined by a %%global or %%define in this spec, by the component's macros "+
			"file, or by the build environment, so the captured text never renders and bumping it "+
			"would rebuild an identical NEVRA; capture a field that always renders, or set "+
			"'source = \"spec-macro\"' to target a bare-integer %%global/%%define instead",
		captured, value, macroName, macroName)
}

func macroNameCaptureError(value string, captured string, macroName string) error {
	return fmt.Errorf(
		"capture %#q in Release value %#q sits inside the name of macro reference %%{%s}; bumping it "+
			"would rewrite the reference into a different, undefined macro and silently delete its "+
			"expansion; capture a literal digit field instead, or set 'source = \"spec-macro\"' to "+
			"target a bare-integer %%global/%%define",
		captured, value, macroName)
}

func preReleaseLeadingZeroError(value string) error {
	return fmt.Errorf(
		"capture \"0\" in Release value %#q is the leading pre-release marker; a Release starting with "+
			"'0.' denotes a pre-release and must never be incremented because the bumped value would "+
			"sort above the final release; capture the counter field after the leading '0.' instead",
		value)
}

// collectMacrosFileMacroNames returns every macro name defined by the macros files next to a
// rendered spec. A component without a macros file simply contributes no names, so an empty
// directory listing is not an error here.
func collectMacrosFileMacroNames(fs opctx.FS, specDir string) (map[string]bool, error) {
	names := make(map[string]bool)

	macrosPaths, err := listMacrosFiles(fs, specDir)
	if err != nil {
		return nil, err
	}

	for _, macrosPath := range macrosPaths {
		contents, err := fileutils.ReadFile(fs, macrosPath)
		if err != nil {
			return nil, fmt.Errorf("reading macros file %#q:\n%w", macrosPath, err)
		}

		for _, rawLine := range strings.Split(string(contents), "\n") {
			if definition, matches := parseMacrosFileDefinition(rawLine); matches {
				names[definition.name] = true
			}
		}
	}

	return names, nil
}

// listMacrosFiles returns every '.azl.macros' file next to a rendered spec.
func listMacrosFiles(fs opctx.FS, specDir string) ([]string, error) {
	entries, err := fileutils.ReadDir(fs, specDir)
	if err != nil {
		return nil, fmt.Errorf("reading rendered spec directory %#q:\n%w", specDir, err)
	}

	var matches []string

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), MacrosFileExtension) {
			matches = append(matches, filepath.Join(specDir, entry.Name()))
		}
	}

	return matches, nil
}

// parseMacrosFileDefinition parses one '%name body' line of an RPM macros file. Unlike a spec,
// a macros file has no '%global'/'%define' directive: the macro name follows '%' directly.
func parseMacrosFileDefinition(rawLine string) (specMacroCounterDefinition, bool) {
	linePos := skipHorizontalWhitespace(rawLine, 0)
	if linePos == len(rawLine) || rawLine[linePos] != '%' {
		return specMacroCounterDefinition{}, false
	}

	linePos++
	nameStart := linePos

	for linePos < len(rawLine) && isMacroNameByte(rawLine[linePos]) {
		linePos++
	}

	if nameStart == linePos {
		return specMacroCounterDefinition{}, false
	}

	// Anything other than whitespace or end-of-line after the name means this is a macro
	// *reference* (e.g. '%{load:...}'), not a definition.
	if linePos < len(rawLine) && !isHorizontalWhitespace(rawLine[linePos]) {
		return specMacroCounterDefinition{}, false
	}

	if isRPMConditionalDirective(rawLine[nameStart:linePos]) {
		return specMacroCounterDefinition{}, false
	}

	counterStart := skipHorizontalWhitespace(rawLine, linePos)

	counterEnd := len(rawLine)
	for counterEnd > counterStart && isHorizontalWhitespace(rawLine[counterEnd-1]) {
		counterEnd--
	}

	return specMacroCounterDefinition{
		name:         rawLine[nameStart:linePos],
		rawLine:      rawLine,
		counterStart: counterStart,
		counterEnd:   counterEnd,
		counterValue: rawLine[counterStart:counterEnd],
	}, true
}

// isRPMConditionalDirective reports whether a macros-file line starting with '%name' is one of
// the '%if'-family directives rather than a definition. Their argument is separated from the
// directive by whitespace exactly like a macro body, so without this check '%if 0' would parse
// as a definition of a macro named 'if'.
func isRPMConditionalDirective(name string) bool {
	switch name {
	case "if", "ifarch", "ifnarch", "ifos", "ifnos", "else", "elif", "endif":
		return true
	default:
		return false
	}
}

func isMacroNameByte(value byte) bool {
	return value == '_' ||
		(value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}
