// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"fmt"
	"io"
	"time"
)

// EditorMode identifies the implementation that edits an RPM spec.
type EditorMode string

const (
	elseDirective = "%else"

	// EditorLegacy uses the established line-oriented editor.
	EditorLegacy EditorMode = "legacy"
	// EditorStructural uses the lossless structural editor.
	EditorStructural EditorMode = "structural"
)

type editorOptions struct {
	mode EditorMode
}

// OpenOption configures [OpenSpec].
type OpenOption func(*editorOptions)

// WithEditor selects the editor implementation used by [OpenSpec].
func WithEditor(mode EditorMode) OpenOption {
	return func(options *editorOptions) {
		options.mode = mode
	}
}

//nolint:interfacebloat,inamedparam // The facade must cover the established public Spec API.
type specEditor interface {
	Serialize(io.Writer) error
	ReplaceLine(int, string)
	RemoveLine(int)
	RemoveLines(int, int)
	InsertLinesAt([]string, int)
	Visit(Visitor) error
	VisitTags(func(*TagLine, *Context) error) error
	VisitTagsPackage(string, func(*TagLine, *Context) error) error
	SetTag(string, string, string) error
	UpdateExistingTag(string, string, string) error
	RemoveTag(string, string, string) error
	RemoveTagsMatching(string, func(string, string) bool) (int, error)
	AddTag(string, string, string) error
	InsertTag(string, string, string) error
	PrependLines([]string)
	AppendLines([]string)
	PrependLinesToSection(string, string, []string) error
	AppendLinesToSection(string, string, []string) error
	SearchAndReplace(string, string, string, string) error
	AddChangelogEntry(string, string, string, string, time.Time, []string) error
	HasSection(string) (bool, error)
	AddPatchEntry(string, string) error
	RemovePatchEntry(string) error
	GetHighestPatchTagNumber() (int, error)
	RemoveSection(string, string) error
	RemoveSubpackage(string) error
	GetTag(string, string) (string, error)
	GetLastTag(string, string) (string, error)
}

// Spec is the public facade for a configuration-selected RPM spec editor.
type Spec struct {
	editor specEditor
}

// OpenSpec reads a spec and selects its editor once. With no option, it preserves
// the established legacy behavior.
func OpenSpec(reader io.Reader, options ...OpenOption) (*Spec, error) {
	config := editorOptions{mode: EditorLegacy}
	for _, option := range options {
		option(&config)
	}

	var (
		editor specEditor
		err    error
	)

	switch config.mode {
	case EditorLegacy, "":
		editor, err = openLegacySpec(reader)
	case EditorStructural:
		editor, err = openStructuralSpec(reader)
	default:
		return nil, fmt.Errorf("unknown spec editor %#q", config.mode)
	}

	if err != nil {
		return nil, err
	}

	return &Spec{editor: editor}, nil
}

// Serialize writes the spec contents to writer.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) Serialize(writer io.Writer) error {
	return s.editor.Serialize(writer)
}

// ReplaceLine replaces the line at the specified 0-indexed line number.
func (s *Spec) ReplaceLine(lineNumber int, replacement string) {
	s.editor.ReplaceLine(lineNumber, replacement)
}

// RemoveLine removes the line at the specified 0-indexed line number.
func (s *Spec) RemoveLine(lineNumber int) { s.editor.RemoveLine(lineNumber) }

// RemoveLines removes the lines in the specified 0-indexed range.
func (s *Spec) RemoveLines(start, end int) { s.editor.RemoveLines(start, end) }

// InsertLinesAt inserts lines before the specified 0-indexed line number.
func (s *Spec) InsertLinesAt(lines []string, lineNumber int) {
	s.editor.InsertLinesAt(lines, lineNumber)
}

//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) Visit(visitor Visitor) error {
	return s.editor.Visit(visitor)
}

// VisitTags iterates over all tag lines across all packages, calling the visitor function
// for each one. The visitor receives the parsed [TagLine] and the mutation [Context].
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) VisitTags(visitor func(tagLine *TagLine, ctx *Context) error) error {
	return s.editor.VisitTags(visitor)
}

// VisitTagsPackage iterates over all tag lines in the given package, calling the visitor
// function for each one. The visitor receives the parsed [TagLine] and the mutation [Context].
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) VisitTagsPackage(packageName string, visitor func(tagLine *TagLine, ctx *Context) error) error {
	return s.editor.VisitTagsPackage(packageName, visitor)
}

// SetTag sets the value of a tag in the specified package.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) SetTag(pkg, tag, value string) error {
	return s.editor.SetTag(pkg, tag, value)
}

// UpdateExistingTag updates every matching tag in the specified package.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) UpdateExistingTag(pkg, tag, value string) error {
	return s.editor.UpdateExistingTag(pkg, tag, value)
}

// RemoveTag removes matching tags from the specified package.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) RemoveTag(pkg, tag, value string) error {
	return s.editor.RemoveTag(pkg, tag, value)
}

// RemoveTagsMatching removes tags matched by matcher from the specified package.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) RemoveTagsMatching(pkg string, matcher func(string, string) bool) (int, error) {
	return s.editor.RemoveTagsMatching(pkg, matcher)
}

// AddTag adds a tag to the specified package.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) AddTag(pkg, tag, value string) error {
	return s.editor.AddTag(pkg, tag, value)
}

// InsertTag inserts a tag in the specified package.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) InsertTag(pkg, tag, value string) error {
	return s.editor.InsertTag(pkg, tag, value)
}

// PrependLines adds lines at the start of the spec.
func (s *Spec) PrependLines(lines []string) { s.editor.PrependLines(lines) }

// AppendLines adds lines at the end of the spec.
func (s *Spec) AppendLines(lines []string) { s.editor.AppendLines(lines) }

// PrependLinesToSection adds lines at the start of a section.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) PrependLinesToSection(section, pkg string, lines []string) error {
	return s.editor.PrependLinesToSection(section, pkg, lines)
}

// AppendLinesToSection adds lines at the end of a section.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) AppendLinesToSection(section, pkg string, lines []string) error {
	return s.editor.AppendLinesToSection(section, pkg, lines)
}

// SearchAndReplace replaces matching content in a section.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) SearchAndReplace(section, pkg, regex, replacement string) error {
	return s.editor.SearchAndReplace(section, pkg, regex, replacement)
}

// AddChangelogEntry adds an entry to the changelog section.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) AddChangelogEntry(user, email, version, release string, at time.Time, details []string) error {
	return s.editor.AddChangelogEntry(user, email, version, release, at, details)
}

// HasSection reports whether a section exists.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) HasSection(section string) (bool, error) {
	return s.editor.HasSection(section)
}

// AddPatchEntry registers a patch in the spec.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) AddPatchEntry(pkg, filename string) error {
	return s.editor.AddPatchEntry(pkg, filename)
}

// RemovePatchEntry removes patch references matching pattern.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) RemovePatchEntry(pattern string) error {
	return s.editor.RemovePatchEntry(pattern)
}

// GetHighestPatchTagNumber returns the highest PatchN tag number.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) GetHighestPatchTagNumber() (int, error) {
	return s.editor.GetHighestPatchTagNumber()
}

// RemoveSection removes a section from the specified package.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) RemoveSection(section, pkg string) error {
	return s.editor.RemoveSection(section, pkg)
}

// RemoveSubpackage removes all sections for a sub-package.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) RemoveSubpackage(pkg string) error {
	return s.editor.RemoveSubpackage(pkg)
}

// GetTag returns the first matching tag in the specified package.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) GetTag(pkg, tag string) (string, error) {
	return s.editor.GetTag(pkg, tag)
}

// GetLastTag returns the final matching tag in the specified package.
//
//nolint:wrapcheck // Preserve errors returned by the selected editor.
func (s *Spec) GetLastTag(pkg, tag string) (string, error) {
	return s.editor.GetLastTag(pkg, tag)
}
