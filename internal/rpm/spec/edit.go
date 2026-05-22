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

// ErrPatternNotFound is returned when a search pattern does not match any content in the spec.
var ErrPatternNotFound = errors.New("pattern not found")

// SetTag sets the value of the given tag in the spec, under the specified package. It first
// attempts to update the first instance of the tag found in the spec; if no such tag exists,
// a new tag is added under the given package.
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

// UpdateExistingTag looks for the first instance of the named tag in the given package; if it
// finds such a tag instance, it replaces its value with the provided value. If no such tag
// exists, it returns an error.
func (s *Spec) UpdateExistingTag(packageName string, tag string, value string) (err error) {
	slog.Debug("Updating tag in spec", "package", packageName, "tag", tag, "newValue", value)

	tagToCompareAgainst := strings.ToLower(tag)

	var updated bool

	err = s.VisitTagsPackage(packageName, func(tagLine *TagLine, ctx *Context) error {
		if strings.ToLower(tagLine.Tag) != tagToCompareAgainst {
			return nil
		}

		ctx.ReplaceLine(fmt.Sprintf("%s: %s", tag, value))

		updated = true

		return nil
	})

	if !updated {
		return fmt.Errorf("tag %#q not found in spec:\n%w", tag, ErrNoSuchTag)
	}

	return err
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

// VisitTags iterates over all tag lines across all packages, calling the visitor function
// for each one. The visitor receives the parsed [TagLine] and the mutation [Context].
func (s *Spec) VisitTags(visitor func(tagLine *TagLine, ctx *Context) error) error {
	return s.Visit(func(ctx *Context) error {
		if ctx.Target.TargetType != SectionLineTarget {
			return nil
		}

		if ctx.Target.Line.Parsed.GetType() != Tag {
			return nil
		}

		tagLine, isTagLine := ctx.Target.Line.Parsed.(*TagLine)
		if !isTagLine {
			return nil
		}

		return visitor(tagLine, ctx)
	})
}

// VisitTagsPackage iterates over all tag lines in the given package, calling the visitor
// function for each one. The visitor receives the parsed [TagLine] and the mutation [Context].
// This extracts the common target-type / package / tag-type filtering that many tag-oriented
// methods need.
func (s *Spec) VisitTagsPackage(packageName string, visitor func(tagLine *TagLine, ctx *Context) error) error {
	return s.VisitTags(func(tagLine *TagLine, ctx *Context) error {
		if ctx.CurrentSection.Package != packageName {
			return nil
		}

		return visitor(tagLine, ctx)
	})
}

// RemoveTagsMatching removes all tags in the given package for which the provided matcher
// function returns true. The matcher receives the tag name and value as arguments. Returns
// the number of tags removed. If no matching tags were found, returns 0 and no error.
func (s *Spec) RemoveTagsMatching(packageName string, matcher func(tag, value string) bool) (int, error) {
	removed := 0

	err := s.VisitTagsPackage(packageName, func(tagLine *TagLine, ctx *Context) error {
		if !matcher(tagLine.Tag, tagLine.Value) {
			return nil
		}

		ctx.RemoveLine()

		removed++

		return nil
	})

	return removed, err
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

	sectionName := ""
	if packageName != "" {
		sectionName = "%package"
	}

	return s.AppendLinesToSection(sectionName, packageName, []string{fmt.Sprintf("%s: %s", tag, value)})
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

	family := tagFamily(tag)
	newLine := fmt.Sprintf("%s: %s", tag, value)

	sectionName := ""
	if packageName != "" {
		sectionName = "%package"
	}

	result, err := s.findInsertTagPosition(sectionName, packageName, family)
	if err != nil {
		return err
	}

	// Determine insertion point: prefer same-family, then any tag, then fall back to AddTag.
	insertAfterLine := result.lastFamilyTagLineNum
	if insertAfterLine < 0 {
		insertAfterLine = result.lastAnyTagLineNum
	}

	if insertAfterLine < 0 {
		// No tags at all — fall back to AddTag behavior.
		return s.AddTag(packageName, tag, value)
	}

	// If the insertion point is inside a conditional block, move it forward past the
	// closing %endif so the new tag doesn't become conditional.
	insertAfterLine = s.skipPastConditional(insertAfterLine, result.sectionEndLineNum)

	// Insert after the found line (0-indexed, so insertAfterLine+1).
	s.InsertLinesAt([]string{newLine}, insertAfterLine+1)

	return nil
}

// insertTagScanResult holds the results of scanning a spec for a tag insertion point.
type insertTagScanResult struct {
	lastFamilyTagLineNum int
	lastAnyTagLineNum    int
	sectionEndLineNum    int
}

// findInsertTagPosition scans the spec to find the best insertion point for a tag of the
// given family within the specified section/package. Returns the scan results or an error
// if the target section is not found.
func (s *Spec) findInsertTagPosition(
	sectionName, packageName, family string,
) (insertTagScanResult, error) {
	result := insertTagScanResult{
		lastFamilyTagLineNum: -1,
		lastAnyTagLineNum:    -1,
		sectionEndLineNum:    len(s.rawLines),
	}

	sectionFound := false

	err := s.Visit(func(ctx *Context) error {
		if ctx.Target.TargetType == SectionStartTarget {
			if ctx.CurrentSection.SectName == sectionName && ctx.CurrentSection.Package == packageName {
				sectionFound = true
			}
		}

		if ctx.Target.TargetType == SectionEndTarget {
			if ctx.CurrentSection.SectName == sectionName && ctx.CurrentSection.Package == packageName {
				result.sectionEndLineNum = ctx.CurrentLineNum
			}
		}

		if ctx.Target.TargetType != SectionLineTarget {
			return nil
		}

		if ctx.CurrentSection.SectName != sectionName || ctx.CurrentSection.Package != packageName {
			return nil
		}

		if ctx.Target.Line.Parsed.GetType() != Tag {
			return nil
		}

		tagLine, ok := ctx.Target.Line.Parsed.(*TagLine)
		if !ok {
			return nil
		}

		result.lastAnyTagLineNum = ctx.CurrentLineNum

		if tagFamily(tagLine.Tag) == family {
			result.lastFamilyTagLineNum = ctx.CurrentLineNum
		}

		return nil
	})
	if err != nil {
		return result, fmt.Errorf("failed to scan spec for tag insertion point:\n%w", err)
	}

	if !sectionFound {
		return result, fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
	}

	return result, nil
}

// skipPastConditional checks whether lineNum falls inside a conditional block by computing
// the conditional nesting depth from the start of the file up to that line. If depth > 0,
// it scans forward to find the %endif that brings depth back to 0 and returns that line
// number. Otherwise it returns lineNum unchanged.
func (s *Spec) skipPastConditional(lineNum int, sectionEnd int) int {
	// Compute conditional depth at the insertion point by scanning from the start.
	depth := 0
	for i := 0; i <= lineNum && i < len(s.rawLines); i++ {
		depth += conditionalDepthChange(s.rawLines[i])
	}

	if depth <= 0 {
		return lineNum
	}

	// Scan forward to find the %endif that closes the conditional.
	for i := lineNum + 1; i < sectionEnd && i < len(s.rawLines); i++ {
		depth += conditionalDepthChange(s.rawLines[i])
		if depth <= 0 {
			return i
		}
	}

	// Could not find a closing %endif within the section; return the original position.
	return lineNum
}

// PrependLinesToSection prepends the given lines to the start of the specified section, placing
// them just after the section header (or at the top of the file in the global section). An error
// is returned if the identified section cannot be found in the spec.
func (s *Spec) PrependLinesToSection(sectionName, packageName string, lines []string) (err error) {
	slog.Debug("Prepending lines to spec", "section", sectionName, "package", packageName, "lines", lines)

	var updated bool

	err = s.Visit(func(ctx *Context) error {
		// Make sure this is a section start.
		if ctx.Target.TargetType != SectionStartTarget {
			return nil
		}

		// Make sure section name matches.
		if ctx.CurrentSection.SectName != sectionName {
			return nil
		}

		// Make sure package name matches.
		if ctx.CurrentSection.Package != packageName {
			return nil
		}

		// Insert the lines. The global section doesn't have a header line, so we insert the
		// lines *before* the start. For all other sections, including sub-package %package
		// sections, we need to make sure we insert the lines after the header line of the
		// section.
		if ctx.CurrentSection.SectName == "" && ctx.CurrentSection.Package == "" {
			ctx.InsertLinesBefore(lines)
		} else {
			ctx.InsertLinesAfter(lines)
		}

		// Note that we've made an update.
		updated = true

		return nil
	})

	if !updated {
		return fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
	}

	return err
}

// AppendLinesToSection appends the given lines at the end of the specified section, placing
// them just after the current last line of the section. An error is returned if the identified
// section cannot be found in the spec.
func (s *Spec) AppendLinesToSection(sectionName, packageName string, lines []string) (err error) {
	slog.Debug("Appending lines to spec", "section", sectionName, "package", packageName, "lines", lines)

	var updated bool

	err = s.Visit(func(ctx *Context) error {
		// Make sure this is a section start.
		if ctx.Target.TargetType != SectionEndTarget {
			return nil
		}

		// Make sure section name matches.
		if ctx.CurrentSection.SectName != sectionName {
			return nil
		}

		// Make sure package name matches.
		if ctx.CurrentSection.Package != packageName {
			return nil
		}

		// Insert the line.
		ctx.InsertLinesBefore(lines)

		// Note that we've made an update.
		updated = true

		return nil
	})

	if !updated {
		return fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
	}

	return err
}

// SearchAndReplace performs a regex-based search-and-replace against all lines in the specified
// section. If `sectionName` is empty, the operation acts against all sections. If no matches were
// found to replace, an error is returned. The replacement is performed literally; regex capture
// group references like $1 are not expanded.
func (s *Spec) SearchAndReplace(sectionName, packageName, regex, replacement string) (err error) {
	slog.Debug("Searching and replacing in spec",
		"section", sectionName,
		"package", packageName,
		"regex", regex,
		"replacement", replacement,
	)

	// Compile the regex once.
	compiledRegex := regexp.MustCompile(regex)

	var updated bool

	err = s.Visit(func(ctx *Context) error {
		// Make sure this is a section line.
		if ctx.Target.TargetType != SectionLineTarget {
			return nil
		}

		// Make sure section name matches (or was omitted).
		if sectionName != "" && ctx.CurrentSection.SectName != sectionName {
			return nil
		}

		// Make sure package name matches (or was omitted).
		if packageName != "" && ctx.CurrentSection.Package != packageName {
			return nil
		}

		// Get the line.
		line := ctx.Target.Line.Text

		// Try to replace. If no replacements were made, return.
		updatedLine := compiledRegex.ReplaceAllLiteralString(line, replacement)
		if line == updatedLine {
			return nil
		}

		ctx.ReplaceLine(updatedLine)

		// Note that we've made an update.
		updated = true

		return nil
	})

	if !updated {
		return fmt.Errorf(
			"pattern %#q not found (section=%#q, package=%#q):\n%w",
			regex, sectionName, packageName, ErrPatternNotFound,
		)
	}

	return err
}

// AddChangelogEntry adds a changelog entry to the spec's changelog section. An error is returned if
// no %changelog section exists in the spec.
func (s *Spec) AddChangelogEntry(user, email, version, release string, time time.Time, details []string) (err error) {
	slog.Debug("Adding changelog entry to spec",
		"user", user, "email", email, "version", version, "release", release, "details", details)

	var updated bool

	err = s.Visit(func(ctx *Context) error {
		// Make sure we're in the right section.
		if ctx.Target.TargetType != SectionStartTarget {
			return nil
		}

		if ctx.CurrentSection.SectName != "%changelog" {
			return nil
		}

		// Insert an entry.
		formattedDate := time.Format("Mon Jan 02 2006")
		header := fmt.Sprintf("* %s %s <%s> - %s-%s", formattedDate, user, email, version, release)

		lines := []string{header}
		for _, detail := range details {
			lines = append(lines, "- "+detail)
		}

		lines = append(lines, "")

		ctx.InsertLinesAfter(lines)

		// Note that we've made an update.
		updated = true

		return nil
	})

	if !updated {
		return errors.New("existing changelog section could not be found")
	}

	return err
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

	err := s.Visit(func(ctx *Context) error {
		if ctx.Target.TargetType == SectionStartTarget && ctx.CurrentSection.SectName == sectionName {
			found = true
		}

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

	err := s.VisitTags(func(tagLine *TagLine, ctx *Context) error {
		if _, ok := ParsePatchTagNumber(tagLine.Tag); !ok {
			return nil
		}

		matched, matchErr := doublestar.Match(pattern, tagLine.Value)
		if matchErr != nil {
			return fmt.Errorf("failed to match glob pattern %#q against %#q:\n%w", pattern, tagLine.Value, matchErr)
		}

		if matched {
			ctx.RemoveLine()

			removed++
		}

		return nil
	})

	return removed, err
}

// removePatchlistEntriesMatching removes lines from the %patchlist section whose trimmed content
// matches the given glob pattern. Returns the number of entries removed.
func (s *Spec) removePatchlistEntriesMatching(pattern string) (int, error) {
	removed := 0

	err := s.Visit(func(ctx *Context) error {
		if ctx.Target.TargetType != SectionLineTarget {
			return nil
		}

		if ctx.CurrentSection.SectName != "%patchlist" {
			return nil
		}

		line := strings.TrimSpace(ctx.Target.Line.Text)
		if line == "" {
			return nil
		}

		matched, err := doublestar.Match(pattern, line)
		if err != nil {
			return fmt.Errorf("failed to match glob pattern %#q against %#q:\n%w", pattern, line, err)
		}

		if matched {
			ctx.RemoveLine()

			removed++
		}

		return nil
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

	err := s.VisitTags(func(tagLine *TagLine, _ *Context) error {
		num, isPatchTag := ParsePatchTagNumber(tagLine.Tag)
		if isPatchTag && num > highest {
			highest = num
		} else if strings.EqualFold(tagLine.Tag, "patch") {
			// Bare "Patch:" with no numeric suffix — RPM auto-numbers these
			// sequentially starting from 0.
			unnumberedCount++
		}

		return nil
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

	ranges, err := s.collectSectionRanges(func(sn, pn string) bool {
		return sn == sectionName && pn == packageName
	})
	if err != nil {
		return fmt.Errorf("failed to scan spec for section %#q (package=%#q):\n%w", sectionName, packageName, err)
	}

	if len(ranges) == 0 {
		return fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
	}

	s.removeRanges(ranges)

	return nil
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

	ranges, err := s.collectSectionRanges(func(_, pn string) bool {
		return pn == packageName
	})
	if err != nil {
		return fmt.Errorf("failed to scan spec for sub-package %#q:\n%w", packageName, err)
	}

	if len(ranges) == 0 {
		return fmt.Errorf("sub-package %#q not found:\n%w", packageName, ErrSectionNotFound)
	}

	s.removeRanges(ranges)

	return nil
}

// sectionLineRange identifies a half-open `[start, end)` range of raw line numbers
// covering one section, from its header line through (but not including) the start
// of the next section.
type sectionLineRange struct {
	start int
	end   int
}

// collectSectionRanges walks the spec and returns one [sectionLineRange] for every
// section whose `(sectName, packageName)` pair satisfies the predicate, in the order
// they appear in the spec.
//
// Each returned range is adjusted to maintain conditional balance: if a range would
// include trailing `%if` or `%endif` lines that create a nesting imbalance, those
// lines are trimmed from the range so that removing the range does not break the
// spec's conditional structure. If a conditional block is interleaved with section
// content in a way that cannot be resolved by trimming, an [ErrConditionalSpansSections]
// error is returned.
func (s *Spec) collectSectionRanges(
	matches func(sectName, packageName string) bool,
) ([]sectionLineRange, error) {
	var (
		ranges   []sectionLineRange
		curStart = -1
	)

	err := s.Visit(func(ctx *Context) error {
		matched := matches(ctx.CurrentSection.SectName, ctx.CurrentSection.Package)

		//nolint:exhaustive // We intentionally only react to section boundaries.
		switch ctx.Target.TargetType {
		case SectionStartTarget:
			if matched {
				curStart = ctx.CurrentLineNum
			}
		case SectionEndTarget:
			if matched && curStart >= 0 {
				ranges = append(ranges, sectionLineRange{start: curStart, end: ctx.CurrentLineNum})
				curStart = -1
			}
		}

		return nil
	})

	// Defensive fallback: today [Spec.Visit] always emits a trailing SectionEndTarget at
	// EOF, so this branch is unreachable. We keep it so that this helper does not silently
	// misbehave if that invariant ever changes (a section running to EOF would otherwise
	// be silently dropped from the result).
	if curStart >= 0 {
		ranges = append(ranges, sectionLineRange{start: curStart, end: len(s.rawLines)})
	}

	// Skip conditional balancing when no matching ranges were found, so callers
	// get the expected empty-result / not-found behavior rather than a conditional
	// parse error from an unrelated part of the spec.
	if len(ranges) == 0 {
		return ranges, err
	}

	// Balance each range to avoid breaking conditional nesting.
	pairs, pairErr := collectConditionalPairs(s.rawLines)
	if pairErr != nil {
		return nil, fmt.Errorf("failed to parse conditional structure:\n%w", pairErr)
	}

	for idx := range ranges {
		balanced, balanceErr := balanceRange(ranges[idx], s.rawLines, pairs)
		if balanceErr != nil {
			return nil, balanceErr
		}

		ranges[idx] = balanced
	}

	return ranges, err
}

// conditionalPair represents a matched `%if`/`%endif` pair by their line numbers.
type conditionalPair struct {
	ifLine    int
	endifLine int
}

// collectConditionalPairs walks the raw lines and returns all matched `%if`/`%endif`
// pairs using a stack. Nested pairs are properly matched. Returns an error if there
// are unmatched `%if` or `%endif` directives.
func collectConditionalPairs(rawLines []string) ([]conditionalPair, error) {
	var (
		pairs []conditionalPair
		stack []int
	)

	for lineNum, line := range rawLines {
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

// balanceRange adjusts a section line range so that removing it does not leave
// unbalanced `%if`/`%endif` directives in the spec. It uses pre-computed conditional
// pairs to identify straddling conditionals — pairs where one half is inside the
// range and the other half is outside.
//
// Straddling conditional lines inside the range are excluded (the range is trimmed
// so they remain in the spec). This handles:
//   - Trailing `%endif` from a wrapping conditional: excluded, leaving an empty
//     `%if`/`%endif` wrapper.
//   - Trailing `%if` belonging to the next section: excluded, keeping the next
//     section's conditional intact.
//   - Balanced pairs fully inside the range: removed along with the section content.
//
// If a straddling conditional is interleaved with real section content (not just
// other conditional directives and blank lines), an [ErrConditionalSpansSections]
// error is returned.
func balanceRange(sectionRange sectionLineRange, rawLines []string, pairs []conditionalPair) (sectionLineRange, error) {
	// Find the earliest straddling line inside the range and validate that no
	// straddling %if has real content after it. A pair straddles if exactly one
	// of its lines falls within [sectionRange.start, sectionRange.end).
	trimmed := sectionRange.end

	for _, pair := range pairs {
		ifInside := pair.ifLine >= sectionRange.start && pair.ifLine < sectionRange.end
		endifInside := pair.endifLine >= sectionRange.start && pair.endifLine < sectionRange.end

		if ifInside == endifInside {
			// Both inside (fully contained) or both outside (irrelevant).
			continue
		}

		// Straddling: the line that's inside our range should be excluded.
		var insideLine int
		if ifInside {
			insideLine = pair.ifLine
		} else {
			insideLine = pair.endifLine
		}

		if insideLine < trimmed {
			trimmed = insideLine
		}

		// If the straddling line is an %if (opener inside, closer outside),
		// check for real content between the %if and the range end. Such content
		// would belong to this section but span into the next via the conditional.
		if ifInside {
			if err := validateNoContentAfter(pair.ifLine, sectionRange.end, rawLines); err != nil {
				return sectionRange, fmt.Errorf(
					"section at lines %d-%d has a conditional block that spans into the next section; "+
						"use a spec-search-replace overlay to adjust conditionals before removing:\n%w",
					sectionRange.start+1, sectionRange.end, ErrConditionalSpansSections,
				)
			}
		}
	}

	// Check for %else/%elif branch directives that would be broken by the removal.
	if err := validateNoBranchDirectivesInExternalConditional(sectionRange, rawLines, pairs); err != nil {
		return sectionRange, err
	}

	if trimmed == sectionRange.end {
		// No straddling pairs — range is already balanced.
		return sectionRange, nil
	}

	// Validate: the trimmed zone [trimmed, sectionRange.end) will remain in the spec.
	// If it contains real section content (not just conditional directives and blanks),
	// we'd be leaving behind part of the section the caller asked to remove.
	if err := validateNoContentAfter(trimmed-1, sectionRange.end, rawLines); err != nil {
		return sectionRange, fmt.Errorf(
			"section at lines %d-%d has a conditional block that spans into the next section; "+
				"use a spec-search-replace overlay to adjust conditionals before removing:\n%w",
			sectionRange.start+1, sectionRange.end, ErrConditionalSpansSections,
		)
	}

	return sectionLineRange{start: sectionRange.start, end: trimmed}, nil
}

// validateNoContentAfter checks that there is no real section content (non-blank,
// non-conditional lines) between startLine and endLine. Returns an error if any
// such content is found.
func validateNoContentAfter(startLine, endLine int, rawLines []string) error {
	for lineNum := startLine + 1; lineNum < endLine; lineNum++ {
		if !isBlankOrComment(rawLines[lineNum]) && conditionalDepthChange(rawLines[lineNum]) == 0 {
			return fmt.Errorf("real content found at line %d", lineNum+1)
		}
	}

	return nil
}

// validateNoBranchDirectivesInExternalConditional checks that the section range
// does not contain any `%else`/`%elif` branch directives whose enclosing
// `%if`/`%endif` pair extends beyond the range. Removing such a branch directive
// while keeping the enclosing conditional would change which branch is active.
func validateNoBranchDirectivesInExternalConditional(
	sectionRange sectionLineRange,
	rawLines []string,
	pairs []conditionalPair,
) error {
	for lineNum := sectionRange.start; lineNum < sectionRange.end; lineNum++ {
		if !isConditionalBranchDirective(rawLines[lineNum]) {
			continue
		}

		for _, pair := range pairs {
			if pair.ifLine <= lineNum && pair.endifLine >= lineNum {
				pairFullyInside := pair.ifLine >= sectionRange.start && pair.endifLine < sectionRange.end

				if !pairFullyInside {
					return fmt.Errorf(
						"section at lines %d-%d contains a %%else/%%elif branch directive inside a "+
							"conditional block that extends beyond the section boundary; "+
							"use a spec-search-replace overlay to adjust conditionals before removing:\n%w",
						sectionRange.start+1, sectionRange.end, ErrConditionalSpansSections,
					)
				}

				break
			}
		}
	}

	return nil
}

// isBlankOrComment returns true if the line is empty, whitespace-only, or a comment.
func isBlankOrComment(line string) bool {
	trimmed := strings.TrimSpace(line)

	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

// removeRanges deletes the given line ranges from the spec. Ranges must be
// non-overlapping and in ascending order (as produced by [Spec.collectSectionRanges]);
// they are removed from last to first so earlier indices remain valid.
func (s *Spec) removeRanges(ranges []sectionLineRange) {
	for i := len(ranges) - 1; i >= 0; i-- {
		s.RemoveLines(ranges[i].start, ranges[i].end)
	}
}
