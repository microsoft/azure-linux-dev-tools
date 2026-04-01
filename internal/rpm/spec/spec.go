// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// sectionTypesByName is a table of known sections, mapping them to their types. This table must
// be kept in sync with new section types as they are added to the RPM spec format.
//
//nolint:gochecknoglobals // This is effectively a constant, but Go doesn't have const maps.
var sectionTypesByName = map[string]SectionType{
	"%package":                PackageSection,
	"%prep":                   ScriptSection,
	"%conf":                   ScriptSection,
	"%build":                  ScriptSection,
	"%install":                ScriptSection,
	"%check":                  ScriptSection,
	"%clean":                  ScriptSection,
	"%generate_buildrequires": ScriptSection,
	"%pre":                    ScriptSection,
	"%post":                   ScriptSection,
	"%preun":                  ScriptSection,
	"%postun":                 ScriptSection,
	"%pretrans":               ScriptSection,
	"%posttrans":              ScriptSection,
	"%preuntrans":             ScriptSection,
	"%postuntrans":            ScriptSection,
	"%verify":                 ScriptSection,
	"%triggerin":              ScriptSection,
	"%triggerun":              ScriptSection,
	"%triggerprein":           ScriptSection,
	"%triggerpostun":          ScriptSection,
	"%filetriggerin":          ScriptSection,
	"%filetriggerun":          ScriptSection,
	"%filetriggerpostun":      ScriptSection,
	"%transfiletriggerin":     ScriptSection,
	"%transfiletriggerun":     ScriptSection,
	"%transfiletriggerpostun": ScriptSection,
	"%description":            RawSection,
	"%files":                  FilesSection,
	"%changelog":              ChangelogSection,
	"%patchlist":              SourceFileListSection,
	"%sourcelist":             SourceFileListSection,
}

// Spec encapsulates the contents of an RPM spec file.
type Spec struct {
	rawLines []string
}

// Line represents a single line in an RPM spec file.
type Line struct {
	// Text is the original physical text of the line.
	Text string
	// Parsed is the parsed representation of the line's contents.
	Parsed ParsedLine
}

// ParsedLineType represents the type of a parsed line.
type ParsedLineType string

const (
	// SectionStart applies to lines that start a new section, e.g. "%description".
	SectionStart ParsedLineType = "SectionStart"
	// Tag applies to lines that define a tag, e.g. "Name: foo".
	Tag ParsedLineType = "Tag"
	// Raw applies to lines that are raw text, e.g. a line in a script section.
	Raw ParsedLineType = "Raw"
)

// ParsedLine is the interface that all parsed line types implement.
type ParsedLine interface {
	// GetType returns the type of the parsed line.
	GetType() ParsedLineType
}

// SectionType represents the type of a section in an RPM spec file.
type SectionType string

const (
	// PackageSection applies to sections that define a package, e.g. "%package -n foo".
	PackageSection SectionType = "Package"
	// ScriptSection applies to sections that contain scripts, e.g. "%build".
	ScriptSection SectionType = "Script"
	// RawSection applies to sections that contain raw content, e.g.: "%description".
	RawSection SectionType = "Raw"
	// ChangelogSection applies to the "%changelog" section.
	ChangelogSection SectionType = "Changelog"
	// FilesSection applies to a "%files" section.
	FilesSection SectionType = "Files"
	// SourceFileListSection applies to a section that lists source files, e.g.: "%sourcelist".
	SourceFileListSection SectionType = "SourceFileList"
)

// SectionStartLine represents a line that starts a new section in the spec, e.g.: "%build".
type SectionStartLine struct {
	SectType SectionType
	SectName string
	Tokens   []string
}

// GetType returns the type of the parsed line.
func (*SectionStartLine) GetType() ParsedLineType {
	return SectionStart
}

// TagLine encapsulates the definition of a tag.
type TagLine struct {
	// Tag is the name of the tag being defined.
	Tag string
	// Value is the value assigned to the tag.
	Value string
}

// GetType returns the type of the parsed line.
func (*TagLine) GetType() ParsedLineType {
	return Tag
}

// RawLine represents a line that is raw text.
type RawLine struct {
	// Content is the raw line text.
	Content string
}

// GetType returns the type of the parsed line.
func (*RawLine) GetType() ParsedLineType {
	return Raw
}

type parseState struct {
	currentSect SectionTarget
}

func newParseState() parseState {
	return parseState{
		currentSect: SectionTarget{
			SectType: PackageSection,
			SectName: "",
			Package:  "",
		},
	}
}

// OpenSpec reads in the contents of an RPM spec file from the provided reader, returning a [Spec] object.
// An error is returned if the reader cannot be fully read (e.g., I/O error or line exceeds buffer size).
func OpenSpec(reader io.Reader) (*Spec, error) {
	scanner := bufio.NewScanner(reader)
	spec := &Spec{}

	// Read each line from the reader, parsing as we go. Store all parsed lines in the spec object.
	for scanner.Scan() {
		spec.rawLines = append(spec.rawLines, scanner.Text())
	}

	// Check for scanner errors (e.g., I/O error or line too long for buffer).
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read spec:\n%w", err)
	}

	return spec, nil
}

// Serialize writes the spec's contents to the provided writer.
func (s *Spec) Serialize(writer io.Writer) error {
	bufWriter := bufio.NewWriter(writer)
	for _, line := range s.rawLines {
		_, err := bufWriter.WriteString(line + "\n")
		if err != nil {
			return fmt.Errorf("failed to write spec line: %w", err)
		}
	}

	err := bufWriter.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush spec: %w", err)
	}

	return nil
}

// ReplaceLine replaces the line at the specified (0-indexed) line number with the provided replacement line.
func (s *Spec) ReplaceLine(lineNumber int, replacement string) {
	s.rawLines[lineNumber] = replacement
}

// RemoveLine removes the line at the specified (0-indexed) line number.
func (s *Spec) RemoveLine(lineNumber int) {
	s.rawLines = slices.Delete(s.rawLines, lineNumber, lineNumber+1)
}

// RemoveLines removes the lines in the specified (0-indexed) line number range [startLineNumber, endLineNumber).
func (s *Spec) RemoveLines(startLineNumber int, endLineNumber int) {
	s.rawLines = slices.Delete(s.rawLines, startLineNumber, endLineNumber)
}

// InsertLinesAt inserts the provided lines just before the specified (0-indexed) line number.
func (s *Spec) InsertLinesAt(insertedLines []string, lineNumber int) {
	s.rawLines = slices.Insert(s.rawLines, lineNumber, insertedLines...)
}

// Context provides context information to a visitor function when visiting a spec.
type Context struct {
	// Target is the current visit target.
	Target VisitTarget
	// RawLine is the raw text of the current line, if applicable (nil otherwise).
	RawLine *string
	// CurrentSection is the current section being visited.
	CurrentSection SectionTarget
	// CurrentLineNum is the current (0-indexed) line number being visited.
	CurrentLineNum int

	// parseStateBeforeCurrentLine represents the parse state for this spec as it was
	// before parsing the current line.
	parseStateBeforeCurrentLine parseState
	// parseState represents the parse state for this spec after having parsed the
	// current line.
	parseState parseState
	// nextLineNumToParse is the next (0-indexed) line number that will be parsed
	// during visiting; note that not all parsed lines will be send to the visitor,
	// though.
	nextLineNumToParse int
	// nextLineNumToVisit is the next (0-indexed) line number that will be visited.
	nextLineNumToVisit int
	// spec is the spec being visited.
	spec *Spec
}

// InsertLinesBefore inserts the provided lines just before the line currently being visited,
// updating the context accordingly. The next line to be visited will be the line following
// the current one being visited.
func (ctx *Context) InsertLinesBefore(lines []string) {
	ctx.spec.InsertLinesAt(lines, ctx.CurrentLineNum)

	// Account for the displacement from the inserted lines. We will parse the
	// new lines but not visit them, nor will we visit the current line again.
	// This will require rollback back to the previous parse state first.
	ctx.parseState = ctx.parseStateBeforeCurrentLine
	ctx.nextLineNumToParse = ctx.CurrentLineNum
	ctx.nextLineNumToVisit = ctx.CurrentLineNum + len(lines) + 1
}

// InsertLinesAfter inserts the provided lines just after the line currently being visited,
// updating the context accordingly. The next line to be visited will be the line following
// the newly inserted lines.
func (ctx *Context) InsertLinesAfter(lines []string) {
	ctx.spec.InsertLinesAt(lines, ctx.CurrentLineNum+1)

	// Skip ahead past the newly inserted lines.
	ctx.nextLineNumToParse = ctx.CurrentLineNum + 1
	ctx.nextLineNumToVisit += len(lines)
}

// RemoveLine removes the line currently being visited, updating the context accordingly.
// The next line to be visited will be the line that followed the removed line.
func (ctx *Context) RemoveLine() {
	ctx.spec.RemoveLine(ctx.CurrentLineNum)

	// Account for the removed line. We will reparse the new current line and revisit it.
	// This will require rolling back to the previous parse state first.
	ctx.parseState = ctx.parseStateBeforeCurrentLine
	ctx.nextLineNumToParse = ctx.CurrentLineNum
	ctx.nextLineNumToVisit = ctx.CurrentLineNum
}

// ReplaceLine replaces the line currently being visited with the provided replacement line,
// updating the context accordingly.
func (ctx *Context) ReplaceLine(replacement string) {
	ctx.spec.ReplaceLine(ctx.CurrentLineNum, replacement)

	// Account for the replaced line. We will reparse the current line, but not revisit it.
	// This will require rolling back to the previous parse state.
	ctx.parseState = ctx.parseStateBeforeCurrentLine
	ctx.nextLineNumToParse = ctx.CurrentLineNum
	ctx.nextLineNumToVisit = ctx.CurrentLineNum + 1
}

// VisitTarget encapsulates the current target of a visit operation.
type VisitTarget struct {
	// TargetType is the type of the current visit target.
	TargetType VisitTargetType
	// Optionally, provides more detail about the target's section.
	// Left nil when the target is not part of a section.
	Section *SectionTarget
	// Optionally, provides more detail about the target's line. Left nil
	// when the target is not a line.
	Line *Line
}

// VisitTargetType indicates the type of a visit target.
type VisitTargetType string

const (
	// SpecStartTarget indicates the start of the spec.
	SpecStartTarget VisitTargetType = "SpecStart"
	// SectionStartTarget indicates the start of a section.
	SectionStartTarget VisitTargetType = "SectionStart"
	// SectionLineTarget indicates a line within a section.
	SectionLineTarget VisitTargetType = "SectionLine"
	// SectionEndTarget indicates the end of a section.
	SectionEndTarget VisitTargetType = "SectionEnd"
	// SpecEndTarget indicates the end of the spec.
	SpecEndTarget VisitTargetType = "SpecEnd"
)

// SectionTarget encapsulates information about the current section context.
type SectionTarget struct {
	// SectName is the name of the section, e.g. "%description".
	SectName string
	// SectType is the type of the section.
	SectType SectionType
	// Package is the package this section applies to, if any. Left empty for
	// the default package or sections that aren't package-specific.
	Package string
}

// Visitor is the type of a visitor function that can be passed to [Spec.Visit].
type Visitor = func(ctx *Context) error

// Visit walks through the spec, invoking the provided visitor function for each relevant target.
//
// State Management Invariants:
//   - nextLineNumToParse: The next 0-indexed line number to parse. May be less than or equal to
//     CurrentLineNum when context mutations (InsertLinesBefore, RemoveLine, ReplaceLine) require
//     re-parsing the current position.
//   - nextLineNumToVisit: The next 0-indexed line number to send to the visitor. A line is visited
//     only if CurrentLineNum >= nextLineNumToVisit, allowing mutations to skip visiting newly
//     inserted lines or re-visit lines after removal.
//   - Context mutation methods update these values to maintain correct traversal after modifications.
//
//nolint:funlen
func (s *Spec) Visit(visitor Visitor) error {
	ctx := Context{
		Target:                      VisitTarget{TargetType: SpecStartTarget},
		CurrentSection:              newParseState().currentSect,
		CurrentLineNum:              0,
		parseState:                  newParseState(),
		parseStateBeforeCurrentLine: newParseState(),
		nextLineNumToVisit:          0,
		nextLineNumToParse:          1,
		spec:                        s,
	}

	// Visit the spec start.
	err := visitor(&ctx)
	if err != nil {
		return err
	}

	// Visit the preamble start.
	ctx.Target = VisitTarget{
		TargetType: SectionStartTarget,
		Section:    &ctx.CurrentSection,
	}

	err = visitor(&ctx)
	if err != nil {
		return err
	}

	// Go through the lines.
	for ctx.CurrentLineNum < len(ctx.spec.rawLines) {
		var parsedLine ParsedLine

		rawLine := ctx.spec.rawLines[ctx.CurrentLineNum]

		ctx.parseStateBeforeCurrentLine = ctx.parseState
		parsedLine, ctx.parseState = parseSpecLine(rawLine, ctx.parseStateBeforeCurrentLine)

		if _, ok := parsedLine.(*SectionStartLine); ok {
			// Visit the end of the preceding section.
			ctx.Target = VisitTarget{
				TargetType: SectionEndTarget,
				Section:    &ctx.CurrentSection,
			}

			ctx.RawLine = nil

			// Skip visiting if this line was inserted or we're re-parsing after a mutation.
			if ctx.CurrentLineNum >= ctx.nextLineNumToVisit {
				err = visitor(&ctx)
				if err != nil {
					return err
				}
			}

			// Visit the start of the new section.
			ctx.CurrentSection = ctx.parseState.currentSect
			ctx.RawLine = &rawLine
			ctx.Target = VisitTarget{
				TargetType: SectionStartTarget,
				Section:    &ctx.CurrentSection,
			}
		} else {
			ctx.RawLine = &rawLine
			ctx.Target = VisitTarget{
				TargetType: SectionLineTarget,
				Line: &Line{
					Text:   rawLine,
					Parsed: parsedLine,
				},
			}
		}

		// Visit the line (if so requested).
		if ctx.CurrentLineNum >= ctx.nextLineNumToVisit {
			err = visitor(&ctx)
			if err != nil {
				return err
			}
		}

		// Move to whatever is the next line that we need to parse; note that it
		// may be the same as the previous line number in case that line got removed.
		// It's also possible that we won't send the line to the visitor.
		ctx.CurrentLineNum = ctx.nextLineNumToParse

		// Update next *next* lines to visit/parse.
		ctx.nextLineNumToParse++
		ctx.nextLineNumToVisit = max(ctx.CurrentLineNum, ctx.nextLineNumToVisit)
	}

	// Visit the end of the last section.
	ctx.Target = VisitTarget{
		TargetType: SectionEndTarget,
		Section:    &ctx.CurrentSection,
	}

	ctx.RawLine = nil

	err = visitor(&ctx)
	if err != nil {
		return err
	}

	// Visit the spec end.
	ctx.Target = VisitTarget{TargetType: SpecEndTarget}
	ctx.RawLine = nil

	err = visitor(&ctx)
	if err != nil {
		return err
	}

	return nil
}

func parseSpecLine(physicalText string, state parseState) (ParsedLine, parseState) {
	parsedLine := newParsedLine(physicalText, state)

	if sectionStartLine, ok := parsedLine.(*SectionStartLine); ok {
		state.currentSect.SectType = sectionStartLine.SectType
		state.currentSect.SectName = sectionStartLine.SectName
		state.currentSect.Package = getPackageNameForSection(sectionStartLine.SectType, sectionStartLine.Tokens)
	}

	return parsedLine, state
}

func newParsedLine(physicalText string, state parseState) ParsedLine {
	logicalLine := physicalText
	if strings.HasPrefix(physicalText, "#") {
		logicalLine = ""
	}

	logicalLine = strings.TrimSpace(logicalLine)

	return parseLogicalLine(logicalLine, state)
}

var tagRegex = regexp.MustCompile(`^\s*([^\s:]+):\s*(.*?)\s*$`)

func parseLogicalLine(logicalLine string, state parseState) ParsedLine {
	tokens := strings.Fields(logicalLine)
	if len(tokens) == 0 {
		return &RawLine{}
	}

	// See if this appears to be the start of a new section.
	if strings.HasPrefix(tokens[0], "%") {
		if newSectionType, known := sectionTypesByName[strings.ToLower(tokens[0])]; known {
			return &SectionStartLine{
				SectType: newSectionType,
				SectName: tokens[0],
				Tokens:   tokens,
			}
		}
	}

	// Otherwise, if we're currently in a package section, see if this looks like a line that defines
	// one or more tags.
	if state.currentSect.SectType == PackageSection {
		const reSubmatchCount = 3

		matches := tagRegex.FindStringSubmatch(logicalLine)
		if len(matches) == reSubmatchCount {
			return &TagLine{
				Tag:   matches[1],
				Value: matches[2],
			}
		}
	}

	// This doesn't appear to be the start of a section, nor a tag definition; treat it as a raw line.
	return &RawLine{
		Content: logicalLine,
	}
}

func getPackageNameForSection(sectionType SectionType, headerTokens []string) string {
	switch sectionType {
	case SourceFileListSection:
		fallthrough
	case ChangelogSection:
		return ""
	case PackageSection:
		fallthrough
	case RawSection:
		fallthrough
	case FilesSection:
		fallthrough
	case ScriptSection:
		return GetPackageNameFromSectionHeader(headerTokens)
	default:
		return ""
	}
}

// GetPackageNameFromSectionHeader extracts the package name from the tokens of a section
// header line. For example, for a line like "%package -n foo", it would return "foo".
// For a line like "%package foo", it would return "foo" as well. Because this function
// does not know the base name of the spec, it cannot take a suffix-only name and resolve
// it to a full name.
func GetPackageNameFromSectionHeader(tokens []string) string {
	fullName := ""
	nameSuffix := ""
	index := 1 // Skip the first token

	for index < len(tokens) {
		token := tokens[index]

		switch {
		case strings.HasPrefix(token, "-"):
			switch token {
			case "-n":
				index++
				if index < len(tokens) {
					fullName = tokens[index]
					index++
				}
			case "-f":
				index += 2
			case "-p":
				index += 2
			case "-q":
				index++
			default:
				index++
			}
		case nameSuffix == "":
			nameSuffix = token
			index++
		default:
			index++
		}
	}

	switch {
	case fullName != "":
		return fullName
	case nameSuffix != "":
		return nameSuffix
	default:
		return ""
	}
}
