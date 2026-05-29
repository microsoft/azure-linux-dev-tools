// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"bufio"
	"fmt"
	"io"
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
		case token == "--":
			// Trigger terminator: in %trigger* sections, `--` separates the
			// owning sub-package from the trigger condition. Everything after
			// `--` is the trigger condition, not the package name.
			index = len(tokens)
		case token == "-n":
			// Absolute package name form: the next token is the full package name.
			index++
			if index < len(tokens) {
				fullName = tokens[index]
				index++
			}
		case token == "-f", token == "-p", token == "-l", token == "-P":
			// Flags that consume the next token as their argument.
			index += 2
		case strings.HasPrefix(token, "-"):
			// Other flags (e.g. -q, -e, or unknown): skip the flag itself.
			index++
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
