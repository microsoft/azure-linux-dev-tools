// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// ErrNoSuchTag is returned when a requested tag does not exist in the spec.
var ErrNoSuchTag = errors.New("no such tag")

// ErrSectionNotFound is returned when a requested section does not exist in the spec.
var ErrSectionNotFound = errors.New("section not found")

// ErrConditionalSpansSections is returned when a conditional block (%if/%endif) spans
// across section boundaries, making it impossible to safely remove the section without
// breaking the conditional nesting structure.
var ErrConditionalSpansSections = errors.New("conditional block spans across section boundaries")

// ErrUnsafeMacroHoist is returned when removing a section would require moving
// a macro definition whose RPM scope or evaluation order cannot be preserved.
var ErrUnsafeMacroHoist = errors.New("unsafe macro hoist")

// ErrPatternNotFound is returned when a search pattern does not match any content in the spec.
var ErrPatternNotFound = errors.New("pattern not found")

// visitLinearSpecLines reports each non-section-header line with the section
// that RPM assigns to it. Section ownership is lexical: a section declared
// inside a conditional remains active after its %endif, so this deliberately
// does not follow the structural tree's branch nesting.
func visitLinearSpecLines(lines []string, visit func(lineIdx int, secName, secPkg string)) {
	secName, secPkg := "", ""

	for lineIdx, line := range lines {
		if isSectionHeaderLine(line) {
			secName, secPkg = getSectionNameAndPackageFromHeader(line)

			continue
		}

		visit(lineIdx, secName, secPkg)
	}
}

// SetTag sets the value of the given tag in the spec, under the specified
// package. It updates matching instances, or adds a tag when none exists.
func (s *Spec) SetTag(packageName string, tag string, value string) (err error) {
	err = s.UpdateExistingTag(packageName, tag, value)
	if err == nil {
		return nil
	}

	if errors.Is(err, ErrNoSuchTag) {
		err = s.AddTag(packageName, tag, value)
	}

	return err
}

// UpdateExistingTag updates every instance of the named tag in the given
// package. If no such tag exists, it returns an error.
func (s *Spec) UpdateExistingTag(packageName string, tag string, value string) (err error) {
	slog.Debug("Updating tag in spec", "package", packageName, "tag", tag, "newValue", value)

	tagToCompareAgainst := strings.ToLower(tag)

	updated := false

	rawLines := append([]string(nil), s.rawLines...)

	visitLinearSpecLines(rawLines, func(lineIdx int, secName, secPkg string) {
		if secPkg != packageName || !isTagBearingSection(secName) {
			return
		}

		parsedTag, _, isTag := parseTagLine(rawLines[lineIdx])
		if !isTag || strings.ToLower(parsedTag) != tagToCompareAgainst {
			return
		}

		rawLines[lineIdx] = fmt.Sprintf("%s: %s", tag, value)
		updated = true
	})

	if !updated {
		return fmt.Errorf("tag %#q not found in spec:\n%w", tag, ErrNoSuchTag)
	}

	s.rawLines = rawLines

	return nil
}

// RemoveTag removes all instances of the given tag from the spec, under the specified
// package (or globally if `packageName` is empty). If the provided `value` is non-empty,
// then only tag instances whose values are as specified will be removed. This function
// returns an error if a tag matching those criteria did not exist in the given package.
func (s *Spec) RemoveTag(packageName string, tag string, value string) (err error) {
	slog.Debug("Removing tag from spec", "package", packageName, "tag", tag, "value", value)

	tagToCompareAgainst := strings.ToLower(tag)

	removed, err := s.RemoveTagsMatching(packageName, func(t, v string) bool {
		if strings.ToLower(t) != tagToCompareAgainst {
			return false
		}

		if value != "" && !strings.EqualFold(v, value) {
			return false
		}

		return true
	})
	if err != nil {
		return err
	}

	if removed == 0 {
		return fmt.Errorf("tag %#q with value %#q not found in spec:\n%w", tag, value, ErrNoSuchTag)
	}

	return nil
}

// GetTag returns the value of the first instance of the named tag in the given package.
// Returns [ErrNoSuchTag] if the tag does not exist.
func (s *Spec) GetTag(packageName string, tag string) (string, error) {
	return s.getTag(packageName, tag, true)
}

// GetLastTag returns the value of the last lexical instance of the named tag
// in the given package. It preserves the historical Release-tag selection
// behavior for callers that need it. Returns [ErrNoSuchTag] if the tag does
// not exist.
func (s *Spec) GetLastTag(packageName string, tag string) (string, error) {
	return s.getTag(packageName, tag, false)
}

// GetFirstNonEmptyTag returns the first lexical instance of tag in packageName
// whose value is not empty. Returns [ErrNoSuchTag] when no such tag exists.
func (s *Spec) GetFirstNonEmptyTag(packageName string, tag string) (string, error) {
	tagToCompareAgainst := strings.ToLower(tag)

	var foundValue string

	visitLinearSpecLines(s.rawLines, func(lineIdx int, secName, secPkg string) {
		if foundValue != "" || secPkg != packageName || !isTagBearingSection(secName) {
			return
		}

		parsedTag, parsedValue, isTag := parseTagLine(s.rawLines[lineIdx])
		if isTag && strings.ToLower(parsedTag) == tagToCompareAgainst && strings.TrimSpace(parsedValue) != "" {
			foundValue = parsedValue
		}
	})

	if foundValue == "" {
		return "", fmt.Errorf("non-empty tag %#q not found in package %#q:\n%w", tag, packageName, ErrNoSuchTag)
	}

	return foundValue, nil
}

func (s *Spec) getTag(packageName string, tag string, first bool) (string, error) {
	tagToCompareAgainst := strings.ToLower(tag)

	var (
		foundValue string
		found      bool
	)

	visitLinearSpecLines(s.rawLines, func(lineIdx int, secName, secPkg string) {
		if (first && found) || secPkg != packageName || !isTagBearingSection(secName) {
			return
		}

		parsedTag, parsedValue, isTag := parseTagLine(s.rawLines[lineIdx])
		if !isTag || strings.ToLower(parsedTag) != tagToCompareAgainst {
			return
		}

		foundValue = parsedValue
		found = true
	})

	if !found {
		return "", fmt.Errorf("tag %#q not found in package %#q:\n%w", tag, packageName, ErrNoSuchTag)
	}

	return foundValue, nil
}

// RemoveTagsMatching removes all tags in the given package for which the provided matcher
// function returns true. The matcher receives the tag name and value as arguments. Returns
// the number of tags removed. If no matching tags were found, returns 0 and no error.
func (s *Spec) RemoveTagsMatching(packageName string, matcher func(tag, value string) bool) (int, error) {
	removed := 0

	remove := make([]bool, len(s.rawLines))
	visitLinearSpecLines(s.rawLines, func(lineIdx int, secName, secPkg string) {
		if secPkg != packageName || !isTagBearingSection(secName) {
			return
		}

		parsedTag, parsedValue, isTag := parseTagLine(s.rawLines[lineIdx])
		if isTag && matcher(parsedTag, parsedValue) {
			remove[lineIdx] = true
			removed++
		}
	})

	if removed == 0 {
		return 0, nil
	}

	rawLines := make([]string, 0, len(s.rawLines)-removed)
	for lineIdx, line := range s.rawLines {
		if !remove[lineIdx] {
			rawLines = append(rawLines, line)
		}
	}

	s.rawLines = rawLines

	return removed, nil
}

// AddTag adds the given tag to the spec, under the specified package (or globally if
// `packageName` is empty). This function will indiscriminately add the tag and does not
// first check to see if any instances of this tag already exist in the indicated
// package. This is useful for tags that can appear multiple times, or in cases in which
// a determination has already been made that a singleton tag in question doesn't already exist.
//
// Note: When adding to a sub-package (non-empty packageName), the corresponding %package
// section must already exist in the spec; otherwise, an [ErrSectionNotFound] error is returned.
func (s *Spec) AddTag(packageName string, tag string, value string) (err error) {
	slog.Debug("Adding tag to spec", "package", packageName, "tag", tag, "value", value)

	return s.insertLinearTag(packageName, tag, value, false)
}

// tagFamily returns the "family" prefix of a tag name by stripping any trailing digits.
// For example, "Source9999" returns "source", "Patch100" returns "patch", and
// "BuildRequires" returns "buildrequires". The result is always lowercased.
func tagFamily(tag string) string {
	lower := strings.ToLower(tag)

	// Strip trailing digits.
	end := len(lower)
	for end > 0 && lower[end-1] >= '0' && lower[end-1] <= '9' {
		end--
	}

	// If the entire tag is digits, return the full lowered tag.
	if end == 0 {
		return lower
	}

	return lower[:end]
}

// conditionalDepthChange returns +1 for lines that open a conditional block,
// -1 for %endif, and 0 for everything else. Comments are ignored.
//
// The recognized conditional openers are: %if, %ifarch, %ifnarch, %ifos, %ifnos.
func conditionalDepthChange(rawLine string) int {
	trimmed := strings.TrimSpace(rawLine)
	if strings.HasPrefix(trimmed, "#") {
		return 0
	}

	token := strings.Fields(trimmed)
	if len(token) == 0 {
		return 0
	}

	lower := strings.ToLower(token[0])

	switch lower {
	case "%endif":
		return -1
	case "%if", "%ifarch", "%ifnarch", "%ifos", "%ifnos":
		return 1
	default:
		return 0
	}
}

// isConditionalBranchDirective returns true for lines that are branch directives
// within a conditional block. These do not change nesting depth but mark branch
// boundaries within an enclosing %if/%endif pair. Comments are ignored.
//
// The recognized branch directives are: %else, %elif, %elifarch, %elifnarch, %elifos, %elifnos.
func isConditionalBranchDirective(rawLine string) bool {
	trimmed := strings.TrimSpace(rawLine)
	if strings.HasPrefix(trimmed, "#") {
		return false
	}

	tokens := strings.Fields(trimmed)
	if len(tokens) == 0 {
		return false
	}

	lower := strings.ToLower(tokens[0])

	switch lower {
	case "%else", "%elif", "%elifarch", "%elifnarch", "%elifos", "%elifnos":
		return true
	default:
		return false
	}
}

// InsertTag inserts a tag into the spec, placing it after the last existing tag from the
// same "family" (e.g., Source9999 is placed after the last Source* tag). If no tags from
// the same family exist, the tag is placed after the last tag of any kind. If there are no
// tags at all, it falls back to [AddTag] behavior (appending to the section end).
//
// The tag family is determined by stripping trailing digits from the tag name
// (case-insensitive). For example, "Source0", "Source1", and "Source" all belong to the
// "source" family.
//
// If the chosen insertion point falls inside a conditional block (%if/%endif), the tag is
// placed after the closing %endif instead, so it remains unconditional.
//
// Note: When inserting into a sub-package (non-empty packageName), the corresponding
// %package section must already exist in the spec; otherwise, an [ErrSectionNotFound]
// error is returned.
func (s *Spec) InsertTag(packageName string, tag string, value string) error {
	slog.Debug("Inserting tag to spec", "package", packageName, "tag", tag, "value", value)

	return s.insertLinearTag(packageName, tag, value, true)
}

//nolint:cyclop,gocognit,funlen // The linear scan deliberately handles each tag-placement case together.
func (s *Spec) insertLinearTag(packageName, tag, value string, preferFamily bool) error {
	targetSection := ""
	if packageName != "" {
		targetSection = packageSectionName
	}

	foundSection := packageName == ""
	lastOwnedLine := -1
	lastAnyTag := -1
	lastFamilyTag := -1
	secName, secPkg := "", ""

	for lineIdx, line := range s.rawLines {
		if isSectionHeaderLine(line) {
			secName, secPkg = getSectionNameAndPackageFromHeader(line)
			if secName == targetSection && secPkg == packageName {
				foundSection = true
				lastOwnedLine = lineIdx
			}

			continue
		}

		if secName != targetSection || secPkg != packageName {
			continue
		}

		lastOwnedLine = lineIdx

		if !isTagBearingSection(secName) {
			continue
		}

		parsedTag, _, isTag := parseTagLine(line)
		if !isTag {
			continue
		}

		lastAnyTag = lineIdx
		if tagFamily(parsedTag) == tagFamily(tag) {
			lastFamilyTag = lineIdx
		}
	}

	if !foundSection {
		return fmt.Errorf("section %#q (package=%#q) not found:\n%w", targetSection, packageName, ErrSectionNotFound)
	}

	insertAfter := lastOwnedLine
	if preferFamily && lastAnyTag >= 0 {
		insertAfter = lastAnyTag
		if lastFamilyTag >= 0 {
			insertAfter = lastFamilyTag
		}

		pairs, err := collectConditionalPairs(s.rawLines)
		if err != nil {
			return fmt.Errorf("parsing conditional structure:\n%w", err)
		}

		for _, pair := range pairs {
			if pair.ifLine < insertAfter && insertAfter < pair.endifLine && pair.endifLine > insertAfter {
				insertAfter = pair.endifLine
			}
		}
	}

	newLine := fmt.Sprintf("%s: %s", tag, value)
	if insertAfter < 0 {
		s.rawLines = append([]string{newLine}, s.rawLines...)
	} else {
		s.rawLines = append(s.rawLines, "")
		copy(s.rawLines[insertAfter+2:], s.rawLines[insertAfter+1:])
		s.rawLines[insertAfter+1] = newLine
	}

	return nil
}

// PrependLines prepends the given lines to the very top of the spec file.
func (s *Spec) PrependLines(lines []string) {
	slog.Debug("Prepending lines to spec file", "lines", lines)
	s.rawLines = append(append([]string{}, lines...), s.rawLines...)
}

// AppendLines appends the given lines at the very bottom of the spec file.
func (s *Spec) AppendLines(lines []string) {
	slog.Debug("Appending lines to spec file", "lines", lines)
	s.rawLines = append(s.rawLines, lines...)
}

// PrependLinesToSection prepends the given lines to the start of the first section matching
// the specified name and package, placing them just after the section header (or at the top
// of the file in the global section). An error is returned if the identified section cannot
// be found in the spec.
func (s *Spec) PrependLinesToSection(sectionName, packageName string, lines []string) (err error) {
	slog.Debug("Prepending lines to spec", "section", sectionName, "package", packageName, "lines", lines)

	return s.mutateTree(func(tree *specTree) error {
		sect := tree.Section(sectionName, packageName)
		if sect == nil {
			return fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
		}

		sect.PrependLines(lines)

		return nil
	})
}

// PrependLinesToAllSections prepends the given lines to the start of every section matching
// the specified name and package, placing them just after each section header. This is useful
// when a spec contains multiple sections with the same name (e.g., two %check sections gated
// by different conditionals) and all of them need the same modification.
// An error is returned if no matching section exists.
func (s *Spec) PrependLinesToAllSections(sectionName, packageName string, lines []string) (err error) {
	slog.Debug("Prepending lines to all matching sections", "section", sectionName, "package", packageName, "lines", lines)

	return s.mutateTree(func(tree *specTree) error {
		sections := tree.Sections(sectionName, packageName)
		if len(sections) == 0 {
			return fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
		}

		for _, sect := range sections {
			sect.PrependLines(lines)
		}

		return nil
	})
}

// AppendLinesToSection appends the given lines at the end of the specified section, placing
// them just after the current last line of the section's content. When a conditional block
// (%if/%endif) straddles the section boundary, the appended lines are placed before the
// conditional — they do not land inside it.
//
// An error is returned if the identified section cannot be found in the spec.
func (s *Spec) AppendLinesToSection(sectionName, packageName string, lines []string) (err error) {
	slog.Debug("Appending lines to spec", "section", sectionName, "package", packageName, "lines", lines)

	return s.mutateTree(func(tree *specTree) error {
		sect := tree.Section(sectionName, packageName)
		if sect == nil {
			return fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
		}

		sect.AppendLines(lines)

		return nil
	})
}

// SearchAndReplace performs a regex-based search-and-replace against all lines in the specified
// section. If `sectionName` is empty, the operation acts against all sections. If no matches were
// found to replace, an error is returned. The replacement is performed literally; regex capture
// group references like $1 are not expanded.
//
// Unlike [specTree.VisitAllLines] (which skips structural lines), this function
// walks every line in the tree including macro definitions (%define/%global) and
// conditional directives (%if/%else/%endif), so patterns that match those lines
// are found correctly.
func (s *Spec) SearchAndReplace(sectionName, packageName, regex, replacement string) (err error) {
	slog.Debug("Searching and replacing in spec",
		"section", sectionName,
		"package", packageName,
		"regex", regex,
		"replacement", replacement,
	)

	// Compile the regex once.
	compiledRegex, err := regexp.Compile(regex)
	if err != nil {
		return fmt.Errorf("failed to compile regex %#q:\n%w", regex, err)
	}

	if sectionName == "" && packageName == "" {
		updated := false

		rawLines := append([]string(nil), s.rawLines...)

		for lineIdx, line := range rawLines {
			if replacementLine := compiledRegex.ReplaceAllLiteralString(line, replacement); replacementLine != line {
				rawLines[lineIdx] = replacementLine
				updated = true
			}
		}

		if !updated {
			return fmt.Errorf(
				"pattern %#q not found (section=%#q, package=%#q):\n%w",
				regex, sectionName, packageName, ErrPatternNotFound,
			)
		}

		s.rawLines = rawLines

		return nil
	}

	rawLines := append([]string(nil), s.rawLines...)
	updated := false

	visitLinearSpecLines(rawLines, func(lineIdx int, secName, secPkg string) {
		if (sectionName != "" && sectionName != secName) ||
			(packageName != "" && packageName != secPkg) {
			return
		}

		if newLine := compiledRegex.ReplaceAllLiteralString(rawLines[lineIdx], replacement); newLine != rawLines[lineIdx] {
			rawLines[lineIdx] = newLine
			updated = true
		}
	})

	if !updated {
		return fmt.Errorf(
			"pattern %#q not found (section=%#q, package=%#q):\n%w",
			regex, sectionName, packageName, ErrPatternNotFound,
		)
	}

	s.rawLines = rawLines

	return nil
}

// AddChangelogEntry adds a changelog entry to the spec's changelog section. An error is returned if
// no %changelog section exists in the spec.
func (s *Spec) AddChangelogEntry(user, email, version, release string, time time.Time, details []string) (err error) {
	slog.Debug("Adding changelog entry to spec",
		"user", user, "email", email, "version", version, "release", release, "details", details)

	formattedDate := time.Format("Mon Jan 02 2006")
	header := fmt.Sprintf("* %s %s <%s> - %s-%s", formattedDate, user, email, version, release)

	lines := []string{header}
	for _, detail := range details {
		lines = append(lines, "- "+detail)
	}

	lines = append(lines, "")

	return s.mutateTree(func(tree *specTree) error {
		sect := tree.Section("%changelog", "")
		if sect == nil {
			return errors.New("existing changelog section could not be found")
		}

		sect.PrependLines(lines)

		return nil
	})
}

// ParsePatchTagNumber checks if the given tag name is a PatchN tag (case-insensitive)
// and returns the numeric suffix N. Returns -1, false if the tag is not a PatchN tag
// or the suffix is not a valid integer.
func ParsePatchTagNumber(tag string) (int, bool) {
	suffix, found := strings.CutPrefix(strings.ToLower(tag), "patch")
	if !found || suffix == "" {
		return -1, false
	}

	num, err := strconv.Atoi(suffix)
	if err != nil {
		return -1, false
	}

	return num, true
}

// HasSection returns true if the spec contains a section with the given name.
// The comparison is exact (case-sensitive), consistent with [AppendLinesToSection].
func (s *Spec) HasSection(sectionName string) (bool, error) {
	var found bool

	err := s.inspectTree(func(tree *specTree) error {
		found = tree.HasSection(sectionName)

		return nil
	})

	return found, err
}

// AddPatchEntry registers a patch in the spec, either by appending to an existing %patchlist
// section or by adding a new PatchN tag with the next available number. Returns an error
// if the spec cannot be examined or updated.
func (s *Spec) AddPatchEntry(packageName, filename string) error {
	slog.Debug("Adding patch entry to spec", "package", packageName, "filename", filename)

	hasPatchlist, err := s.HasSection("%patchlist")
	if err != nil {
		return fmt.Errorf("failed to check for %%patchlist section:\n%w", err)
	}

	if hasPatchlist {
		return s.AppendLinesToSection("%patchlist", "", []string{filename})
	}

	highest, err := s.GetHighestPatchTagNumber()
	if err != nil {
		return fmt.Errorf("failed to scan for existing patch tags:\n%w", err)
	}

	return s.AddTag(packageName, fmt.Sprintf("Patch%d", highest+1), filename)
}

// RemovePatchEntry removes all references to patches matching the given pattern from the spec.
// The pattern is a glob pattern (supporting doublestar syntax) matched against PatchN tag values
// and %patchlist entries across all packages. Returns an error if no references matched the pattern.
func (s *Spec) RemovePatchEntry(pattern string) error {
	slog.Debug("Removing patch entry from spec", "pattern", pattern)

	totalRemoved := 0

	tagsRemoved, err := s.removePatchTagsMatching(pattern)
	if err != nil {
		return fmt.Errorf("failed to remove matching patch tags:\n%w", err)
	}

	totalRemoved += tagsRemoved

	hasPatchlist, err := s.HasSection("%patchlist")
	if err != nil {
		return fmt.Errorf("failed to check for %%patchlist section:\n%w", err)
	}

	if hasPatchlist {
		patchlistRemoved, err := s.removePatchlistEntriesMatching(pattern)
		if err != nil {
			return fmt.Errorf("failed to remove matching patchlist entries:\n%w", err)
		}

		totalRemoved += patchlistRemoved
	}

	if totalRemoved == 0 {
		return fmt.Errorf("no patches matching %#q found in spec", pattern)
	}

	return nil
}

// removePatchTagsMatching removes all PatchN tags across all packages whose values match the
// given glob pattern. Returns the number of tags removed.
func (s *Spec) removePatchTagsMatching(pattern string) (int, error) {
	removed := 0

	err := s.mutateTree(func(tree *specTree) error {
		return tree.VisitAllLines(func(secName, _ string, line *lineHandle) error {
			if !isTagBearingSection(secName) {
				return nil
			}

			parsedTag, parsedValue, isTag := parseTagLine(line.Text)
			if !isTag {
				return nil
			}

			if _, ok := ParsePatchTagNumber(parsedTag); !ok {
				return nil
			}

			matched, matchErr := doublestar.Match(pattern, parsedValue)
			if matchErr != nil {
				return fmt.Errorf("failed to match glob pattern %#q against %#q:\n%w", pattern, parsedValue, matchErr)
			}

			if matched {
				line.Remove()

				removed++
			}

			return nil
		})
	})

	return removed, err
}

// removePatchlistEntriesMatching removes lines from the %patchlist section whose trimmed content
// matches the given glob pattern. Returns the number of entries removed.
func (s *Spec) removePatchlistEntriesMatching(pattern string) (int, error) {
	removed := 0

	err := s.mutateTree(func(tree *specTree) error {
		sect := tree.Section("%patchlist", "")
		if sect == nil {
			return nil
		}

		return sect.VisitLines(func(line *lineHandle) error {
			trimmed := strings.TrimSpace(line.Text)
			if trimmed == "" {
				return nil
			}

			matched, matchErr := doublestar.Match(pattern, trimmed)
			if matchErr != nil {
				return fmt.Errorf("failed to match glob pattern %#q against %#q:\n%w", pattern, trimmed, matchErr)
			}

			if matched {
				line.Remove()

				removed++
			}

			return nil
		})
	})

	return removed, err
}

// GetHighestPatchTagNumber scans the spec for all PatchN tags (where N is a decimal number)
// across all packages and returns the highest N found. Unnumbered "Patch:" tags (no numeric
// suffix) are treated as auto-numbered starting from 0, consistent with RPM's behavior.
// Returns -1 if no numbered PatchN tags and no unnumbered "Patch:" tags are found. Tags with
// non-numeric suffixes (e.g., macro-based names like Patch%{n}) are silently skipped.
func (s *Spec) GetHighestPatchTagNumber() (int, error) {
	highest := -1
	unnumberedCount := 0

	err := s.inspectTree(func(tree *specTree) error {
		return tree.VisitAllLines(func(secName, _ string, line *lineHandle) error {
			if !isTagBearingSection(secName) {
				return nil
			}

			parsedTag, _, isTag := parseTagLine(line.Text)
			if !isTag {
				return nil
			}

			num, isPatchTag := ParsePatchTagNumber(parsedTag)
			if isPatchTag && num > highest {
				highest = num
			} else if strings.EqualFold(parsedTag, "patch") {
				// Bare "Patch:" with no numeric suffix — RPM auto-numbers these
				// sequentially starting from 0.
				unnumberedCount++
			}

			return nil
		})
	})

	// Unnumbered patches occupy slots 0..unnumberedCount-1.
	if unnumberedCount > 0 && (unnumberedCount-1) > highest {
		highest = unnumberedCount - 1
	}

	return highest, err
}

// RemoveSection removes every section from the spec whose name and package qualifier
// match the supplied values, including each section's header line and all body lines.
//
// In valid RPM specs the `(sectionName, packageName)` pair is unique, so this is
// effectively a single-section removal. When a spec lexically contains multiple
// sections with the same identity (e.g. inside mutually-exclusive `%if`/`%else`
// branches), every such section is removed. Returns [ErrSectionNotFound] if no
// matching section exists.
func (s *Spec) RemoveSection(sectionName, packageName string) error {
	slog.Debug("Removing section from spec", "section", sectionName, "package", packageName)

	if sectionName == "" {
		return errors.New("cannot remove the global/preamble section")
	}

	return s.mutateTree(func(tree *specTree) error {
		matches := tree.Sections(sectionName, packageName)
		if len(matches) == 0 {
			return fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
		}

		return tree.RemoveSections(matches)
	})
}

// RemoveSubpackage removes every section in the spec that is associated with the given
// sub-package name (i.e. every section whose package qualifier equals packageName).
// This includes the sub-package's own `%package` preamble section as well as any
// per-section directives that target it (e.g. `%description -n pkg`, `%files pkg`,
// `%post pkg`, etc.).
//
// Returns an error if packageName is empty or if the spec contains no sections
// associated with the given sub-package.
//
// packageName matching: RPM permits two forms for declaring sub-package sections — the
// suffix form (e.g. `%package devel`, which declares a sub-package named `<base>-devel`)
// and the absolute form (e.g. `%package -n my-pkg`). Each section is matched against
// packageName using the form that appears on its header line; callers should pass
// whichever form the spec uses. Specs that mix both forms for the same sub-package
// (uncommon but legal) require a call per form.
//
// Conditional handling: section ranges are automatically trimmed to maintain balanced
// `%if`/`%endif` nesting. Sections wrapped in a conditional block will have trailing
// `%endif` lines excluded from the removal, leaving an empty (but valid) conditional
// wrapper. Trailing `%if` lines that belong to the next section are similarly excluded.
// If a conditional block is interleaved with section content in a way that cannot be
// resolved by trimming, an [ErrConditionalSpansSections] error is returned.
func (s *Spec) RemoveSubpackage(packageName string) error {
	slog.Debug("Removing sub-package from spec", "package", packageName)

	if packageName == "" {
		return errors.New("cannot remove sub-package with empty name")
	}

	return s.mutateTree(func(tree *specTree) error {
		matches := tree.SectionsByPackage(packageName)
		if len(matches) == 0 {
			return fmt.Errorf("sub-package %#q not found:\n%w", packageName, ErrSectionNotFound)
		}

		return tree.RemoveSections(matches)
	})
}

// conditionalPair represents a matched `%if`/`%endif` pair by their line numbers.
type conditionalPair struct {
	ifLine    int
	endifLine int
}

// collectConditionalPairs walks the raw lines and returns all matched `%if`/`%endif`
// pairs using a stack. Nested pairs are properly matched. Lines inside
// macro definition continuations are skipped — `%if`/`%endif` that appear
// inside multi-line `%define`/`%global` bodies (e.g. `%define foo() \` …
// `%if …\` … `%endif\`) are RPM macro body text, not structural conditionals.
// However, `%if`/`%endif` inside general shell continuations (e.g.
// `configure \` … `%if …` … `%endif`) ARE structural — RPM evaluates them
// as preprocessor directives before shell interpretation. Returns an error if
// there are unmatched `%if` or `%endif` directives.
func collectConditionalPairs(rawLines []string) ([]conditionalPair, error) {
	var (
		pairs []conditionalPair
		stack []int
	)

	inMacroCont := false
	braceDepth := 0

	for lineNum, line := range rawLines {
		if inMacroCont {
			braceDepth += macroBraceDelta(line)
			inMacroCont = strings.HasSuffix(line, "\\") || braceDepth > 0

			continue
		}

		// Only skip continuations that start from a %define/%global line —
		// those are macro body text where %if/%endif are not structural.
		if _, isMacro := isMacroDefLine(line); isMacro {
			braceDepth = macroBraceDelta(line)
			inMacroCont = strings.HasSuffix(line, "\\") || braceDepth > 0

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

			pairs = append(pairs, conditionalPair{ifLine: ifLine, endifLine: lineNum})
		}
	}

	if len(stack) > 0 {
		return nil, fmt.Errorf("unmatched %%if at line %d", stack[0]+1)
	}

	return pairs, nil
}
