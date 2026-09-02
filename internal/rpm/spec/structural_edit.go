// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// SetTag sets the value of the given tag in the spec, under the specified package. It first
// attempts to update the first instance of the tag found in the spec; if no such tag exists,
// a new tag is added under the given package.
func (s *structuralSpec) SetTag(packageName string, tag string, value string) (err error) {
	err = s.UpdateExistingTag(packageName, tag, value)
	if err == nil {
		return nil
	}

	if errors.Is(err, ErrNoSuchTag) {
		err = s.AddTag(packageName, tag, value)
	}

	return err
}

// UpdateExistingTag replaces every instance of the named tag in the given
// package with the provided value. If no such tag exists, it returns an error.
func (s *structuralSpec) UpdateExistingTag(packageName string, tag string, value string) (err error) {
	slog.Debug("Updating tag in spec", "package", packageName, "tag", tag, "newValue", value)

	tagToCompareAgainst := strings.ToLower(tag)

	var updated bool

	err = s.mutateTree(func(tree *specTree) error {
		return tree.VisitAllLines(func(secName, secPkg string, line *lineHandle) error {
			if secPkg != packageName || !isTagBearingSection(secName) {
				return nil
			}

			parsedTag, _, isTag := parseTagLine(line.Text)
			if !isTag || strings.ToLower(parsedTag) != tagToCompareAgainst {
				return nil
			}

			line.Replace(fmt.Sprintf("%s: %s", tag, value))

			updated = true

			return nil
		})
	})
	if err != nil {
		return err
	}

	if !updated {
		return fmt.Errorf("tag %#q not found in spec:\n%w", tag, ErrNoSuchTag)
	}

	return nil
}

// RemoveTag removes all instances of the given tag from the spec, under the specified
// package (or globally if `packageName` is empty). If the provided `value` is non-empty,
// then only tag instances whose values are as specified will be removed. This function
// returns an error if a tag matching those criteria did not exist in the given package.
func (s *structuralSpec) RemoveTag(packageName string, tag string, value string) (err error) {
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
func (s *structuralSpec) VisitTags(visitor func(tagLine *TagLine, ctx *Context) error) error {
	root, err := parseTree(s.rawLines)
	if err != nil {
		return fmt.Errorf("parsing spec tree:\n%w", err)
	}

	tree := &specTree{root: root}

	err = tree.VisitAllLines(func(sectionName, packageName string, line *lineHandle) error {
		if !isTagBearingSection(sectionName) {
			return nil
		}

		tag, value, isTag := parseTagLine(line.Text)
		if !isTag {
			return nil
		}

		rawLine := line.Text

		return visitor(&TagLine{Tag: tag, Value: value}, &Context{
			Target: VisitTarget{
				TargetType: SectionLineTarget,
				Line:       &Line{Text: line.Text, Parsed: &TagLine{Tag: tag, Value: value}},
			},
			RawLine:        &rawLine,
			CurrentLineNum: line.lineNumber,
			CurrentSection: SectionTarget{
				SectName: sectionName,
				SectType: PackageSection,
				Package:  packageName,
			},
			structuralLine: line,
		})
	})
	if err != nil {
		return err
	}

	lines := serializeTree(root)
	if _, err := parseTree(lines); err != nil {
		return fmt.Errorf("validating mutated spec tree:\n%w", err)
	}

	s.rawLines = lines

	return nil
}

// VisitTagsPackage iterates over all tag lines in the given package, calling the visitor
// function for each one. The visitor receives the parsed [TagLine] and the mutation [Context].
func (s *structuralSpec) VisitTagsPackage(
	packageName string, visitor func(tagLine *TagLine, ctx *Context) error,
) error {
	return s.VisitTags(func(tagLine *TagLine, ctx *Context) error {
		if ctx.CurrentSection.Package != packageName {
			return nil
		}

		return visitor(tagLine, ctx)
	})
}

// GetTag returns the value of the first instance of the named tag in the given package.
// Returns [ErrNoSuchTag] if the tag does not exist.
func (s *structuralSpec) GetTag(packageName string, tag string) (string, error) {
	var (
		foundValue string
		found      bool
	)

	err := s.inspectTree(func(tree *specTree) error {
		foundValue, found = tree.GetTag(packageName, tag)

		return nil
	})
	if err != nil {
		return "", err
	}

	if !found {
		return "", fmt.Errorf("tag %#q not found in package %#q:\n%w", tag, packageName, ErrNoSuchTag)
	}

	return foundValue, nil
}

// GetLastTag returns the value of the final lexical instance of the named tag
// in the given package. Returns [ErrNoSuchTag] if the tag does not exist.
func (s *structuralSpec) GetLastTag(packageName string, tag string) (string, error) {
	var (
		value string
		found bool
	)

	err := s.inspectTree(func(tree *specTree) error {
		value, found = tree.GetLastTag(packageName, tag)

		return nil
	})
	if err != nil {
		return "", err
	}

	if !found {
		return "", fmt.Errorf("tag %#q not found in package %#q:\n%w", tag, packageName, ErrNoSuchTag)
	}

	return value, nil
}

// RemoveTagsMatching removes all tags in the given package for which the provided matcher
// function returns true. The matcher receives the tag name and value as arguments. Returns
// the number of tags removed. If no matching tags were found, returns 0 and no error.
func (s *structuralSpec) RemoveTagsMatching(packageName string, matcher func(tag, value string) bool) (int, error) {
	removed := 0

	err := s.mutateTree(func(tree *specTree) error {
		return tree.VisitAllLines(func(secName, secPkg string, line *lineHandle) error {
			if secPkg != packageName || !isTagBearingSection(secName) {
				return nil
			}

			parsedTag, parsedValue, isTag := parseTagLine(line.Text)
			if !isTag || !matcher(parsedTag, parsedValue) {
				return nil
			}

			line.Remove()

			removed++

			return nil
		})
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
func (s *structuralSpec) AddTag(packageName string, tag string, value string) (err error) {
	slog.Debug("Adding tag to spec", "package", packageName, "tag", tag, "value", value)

	sectionName := ""
	if packageName != "" {
		sectionName = packageSectionName
	}

	return s.AppendLinesToSection(sectionName, packageName, []string{fmt.Sprintf("%s: %s", tag, value)})
}

// For example, "Source9999" returns "source", "Patch100" returns "patch", and
// "BuildRequires" returns "buildrequires". The result is always lowercased.

// -1 for %endif, and 0 for everything else. Comments are ignored.
//
// The recognized conditional openers are: %if, %ifarch, %ifnarch, %ifos, %ifnos.

// within a conditional block. These do not change nesting depth but mark branch
// boundaries within an enclosing %if/%endif pair. Comments are ignored.
//
// The recognized branch directives are: %else, %elif, %elifarch, %elifnarch, %elifos, %elifnos.

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
func (s *structuralSpec) InsertTag(packageName string, tag string, value string) error {
	slog.Debug("Inserting tag to spec", "package", packageName, "tag", tag, "value", value)

	sectionName := ""
	if packageName != "" {
		sectionName = packageSectionName
	}

	insertAfter, found, err := findLinearTagInsertPosition(s.rawLines, sectionName, packageName, structuralTagFamily(tag))
	if err != nil {
		return err
	}

	if !found {
		return s.AddTag(packageName, tag, value)
	}

	lines := slices.Clone(s.rawLines)
	lines = append(lines, "")
	copy(lines[insertAfter+2:], lines[insertAfter+1:])
	lines[insertAfter+1] = fmt.Sprintf("%s: %s", tag, value)

	if _, err := parseTree(lines); err != nil {
		return fmt.Errorf("validating inserted tag:\n%w", err)
	}

	s.rawLines = lines

	return nil
}

// findLinearTagInsertPosition reproduces the legacy lexical tag ordering while
// ignoring directive-shaped macro bodies.
//
//nolint:cyclop,gocognit,nestif // One pass keeps macro, section, and conditional state synchronized.
func findLinearTagInsertPosition(lines []string, sectionName, packageName, family string) (int, bool, error) {
	lastAny, lastFamily := -1, -1
	lastAnyConditional, lastFamilyConditional := -1, -1
	currentName, currentPackage := "", ""
	sectionFound := sectionName == "" && packageName == ""
	inMacroBody := false
	macroParseState := macroState{}

	var conditionals []int

	for lineNum, line := range lines {
		if inMacroBody {
			macroParseState, inMacroBody = macroBodyStateAfter(line, macroParseState)

			continue
		}

		if _, isMacro := isMacroDefLine(line); isMacro {
			macroParseState, inMacroBody = macroBodyStateAfter(line, macroState{})

			continue
		}

		if isSectionHeaderLine(line) {
			currentName, currentPackage = getSectionNameAndPackageFromHeader(line)
			sectionFound = sectionFound || (currentName == sectionName && currentPackage == packageName)
		} else if currentName == sectionName && currentPackage == packageName {
			tag, _, isTag := parseTagLine(line)
			if isTag {
				lastAny = lineNum

				if len(conditionals) > 0 {
					lastAnyConditional = conditionals[0]
				} else {
					lastAnyConditional = -1
				}

				if structuralTagFamily(tag) == family {
					lastFamily = lineNum
					lastFamilyConditional = lastAnyConditional
				}
			}
		}

		switch structuralConditionalDepthChange(line) {
		case 1:
			conditionals = append(conditionals, lineNum)
		case -1:
			if len(conditionals) > 0 {
				conditionals = conditionals[:len(conditionals)-1]
			}
		}
	}

	if !sectionFound {
		return 0, false, fmt.Errorf("section %#q (package=%#q) not found:\n%w",
			sectionName, packageName, ErrSectionNotFound)
	}

	insertAfter, conditionalStart := lastAny, lastAnyConditional
	if lastFamily >= 0 {
		insertAfter, conditionalStart = lastFamily, lastFamilyConditional
	}

	if insertAfter < 0 {
		return 0, false, nil
	}

	if conditionalStart >= 0 {
		insertAfter = matchingConditionalEndInSection(lines, conditionalStart, insertAfter, sectionName, packageName)
	}

	return insertAfter, true, nil
}

func matchingConditionalEndInSection(lines []string, start, fallback int, sectionName, packageName string) int {
	depth := 0
	inMacroBody := false
	macroParseState := macroState{}

	for lineNum := start; lineNum < len(lines); lineNum++ {
		line := lines[lineNum]
		if inMacroBody {
			macroParseState, inMacroBody = macroBodyStateAfter(line, macroParseState)

			continue
		}

		if _, isMacro := isMacroDefLine(line); isMacro {
			macroParseState, inMacroBody = macroBodyStateAfter(line, macroState{})

			continue
		}

		if lineNum > fallback && isSectionHeaderLine(line) {
			name, pkg := getSectionNameAndPackageFromHeader(line)
			if name != sectionName || pkg != packageName {
				return fallback
			}
		}

		switch structuralConditionalDepthChange(line) {
		case 1:
			depth++
		case -1:
			depth--
			if depth == 0 {
				return lineNum
			}
		}
	}

	return fallback
}

// PrependLines prepends lines to the beginning of the spec without interpreting
// section structure.
func (s *structuralSpec) PrependLines(lines []string) {
	slog.Debug("Prepending lines to spec file", "lines", lines)
	s.rawLines = append(append([]string{}, lines...), s.rawLines...)
}

// AppendLines appends lines to the end of the spec without interpreting
// section structure.
func (s *structuralSpec) AppendLines(lines []string) {
	slog.Debug("Appending lines to spec file", "lines", lines)
	s.rawLines = append(s.rawLines, lines...)
}

// PrependLinesToSection prepends the given lines to the start of the specified section, placing
// them just after each matching section header (or at the top of the file in
// the global section). An error is returned if no matching section is found.
func (s *structuralSpec) PrependLinesToSection(sectionName, packageName string, lines []string) (err error) {
	slog.Debug("Prepending lines to spec", "section", sectionName, "package", packageName, "lines", lines)

	return s.mutateTree(func(tree *specTree) error {
		sections := tree.Sections(sectionName, packageName)
		if len(sections) == 0 {
			return fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
		}

		for _, section := range sections {
			section.PrependLines(lines)
		}

		return nil
	})
}

// AppendLinesToSection appends the given lines at the end of the specified section, placing
// them just after the current last line of each matching section's content. When a conditional block
// (%if/%endif) straddles the section boundary, the appended lines are placed before the
// conditional — they do not land inside it.
//
// An error is returned if the identified section cannot be found in the spec.
func (s *structuralSpec) AppendLinesToSection(sectionName, packageName string, lines []string) (err error) {
	slog.Debug("Appending lines to spec", "section", sectionName, "package", packageName, "lines", lines)

	err = s.mutateTree(func(tree *specTree) error {
		sections := tree.Sections(sectionName, packageName)
		if len(sections) == 0 {
			return fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
		}

		for _, section := range sections {
			section.AppendLines(lines)
		}

		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "unmatched %if") {
		return err
	}

	return s.appendLinesThatCompleteConditional(sectionName, packageName, lines)
}

// appendLinesThatCompleteConditional permits an append overlay to close an
// unmatched conditional introduced by an earlier overlay. The candidate must
// parse successfully, so it cannot preserve an otherwise malformed spec.
func (s *structuralSpec) appendLinesThatCompleteConditional(sectionName, packageName string, lines []string) error {
	headers := findSectionHeaderLines(s.rawLines)
	insertions := sectionAppendInsertionPositions(s.rawLines, headers, sectionName, packageName)

	if len(insertions) == 0 {
		return fmt.Errorf("section %#q (package=%#q) not found:\n%w", sectionName, packageName, ErrSectionNotFound)
	}

	candidate := slices.Clone(s.rawLines)
	for index := len(insertions) - 1; index >= 0; index-- {
		candidate = slices.Insert(candidate, insertions[index], lines...)
	}

	if _, err := parseTree(candidate); err != nil {
		return fmt.Errorf("validating appended lines:\n%w", err)
	}

	s.rawLines = candidate

	return nil
}

func sectionAppendInsertionPositions(lines []string, headers []int, sectionName, packageName string) []int {
	if sectionName == "" && packageName == "" {
		if len(headers) == 0 {
			return []int{len(lines)}
		}

		return []int{headers[0]}
	}

	insertions := make([]int, 0, len(headers))
	for index, header := range headers {
		name, pkg := getSectionNameAndPackageFromHeader(lines[header])
		if name != sectionName || pkg != packageName {
			continue
		}

		end := len(lines)
		if index+1 < len(headers) {
			end = headers[index+1]
		}

		insertions = append(insertions, end)
	}

	return insertions
}

// SearchAndReplace performs a regex-based search-and-replace against all lines in the specified
// section. If `sectionName` is empty, the operation acts against all sections. If no matches were
// found to replace, an error is returned. The replacement is performed literally; regex capture
// group references like $1 are not expanded.
//
// Search-and-replace is deliberately line-oriented rather than structural: a
// sequence of overlays may temporarily leave conditional directives unbalanced.
// Every non-section-header physical line is eligible, including macro
// definitions and bodies plus conditional directives.
func (s *structuralSpec) SearchAndReplace(sectionName, packageName, regex, replacement string) (err error) {
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

	updatedLines := slices.Clone(s.rawLines)
	updated := searchReplaceLines(updatedLines, sectionName, packageName, compiledRegex, replacement)

	if !updated {
		return fmt.Errorf(
			"pattern %#q not found (section=%#q, package=%#q):\n%w",
			regex, sectionName, packageName, ErrPatternNotFound,
		)
	}

	s.rawLines = updatedLines

	return nil
}

// searchReplaceLines applies replacement to physical lines under the requested
// lexical section. Section headers remain structural delimiters and are not
// replaced, matching the historical section-content behavior.
func searchReplaceLines(
	lines []string,
	filterSection, filterPkg string,
	compiledRegex *regexp.Regexp,
	replacement string,
) bool {
	updated := false
	headers := findSectionHeaderLines(lines)
	headerAt := make(map[int]bool, len(headers))

	for _, index := range headers {
		headerAt[index] = true
	}

	state := searchReplaceState{}

	for index, line := range lines {
		if headerAt[index] {
			state.sectionName, state.packageName = getSectionNameAndPackageFromHeader(line)

			continue
		}

		state.advanceBefore(line)

		if state.matchesFilter(filterSection, filterPkg) {
			if newLine := compiledRegex.ReplaceAllLiteralString(line, replacement); newLine != line {
				lines[index] = newLine
				updated = true
			}
		}

		state.advanceAfter(line)
	}

	return updated
}

type searchReplaceContext struct{ name, pkg string }

type searchReplaceState struct {
	sectionName, packageName string
	conditionalContexts      []searchReplaceContext
	inMacroBody              bool
	macroParseState          macroState
}

func (state *searchReplaceState) advanceBefore(line string) {
	if state.inMacroBody {
		return
	}

	switch {
	case structuralConditionalDepthChange(line) == 1:
		state.conditionalContexts = append(state.conditionalContexts, searchReplaceContext{
			state.sectionName, state.packageName,
		})
	case structuralIsConditionalBranchDirective(line), structuralConditionalDepthChange(line) == -1:
		if len(state.conditionalContexts) > 0 {
			context := state.conditionalContexts[len(state.conditionalContexts)-1]
			state.sectionName, state.packageName = context.name, context.pkg
		}
	}
}

func (state *searchReplaceState) matchesFilter(section, pkg string) bool {
	return (section == "" || section == state.sectionName) &&
		(pkg == "" || pkg == state.packageName)
}

func (state *searchReplaceState) advanceAfter(line string) {
	if state.inMacroBody {
		state.macroParseState, state.inMacroBody = macroBodyStateAfter(line, state.macroParseState)

		return
	}

	if structuralConditionalDepthChange(line) == -1 && len(state.conditionalContexts) > 0 {
		state.conditionalContexts = state.conditionalContexts[:len(state.conditionalContexts)-1]
	}

	if _, isMacro := isMacroDefLine(line); isMacro {
		state.macroParseState, state.inMacroBody = macroBodyStateAfter(line, macroState{})
	}
}

// AddChangelogEntry adds a changelog entry to the spec's changelog section. An error is returned if
// no %changelog section exists in the spec.
//
//nolint:lll
func (s *structuralSpec) AddChangelogEntry(user, email, version, release string, time time.Time, details []string) (err error) {
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

// StructuralParsePatchTagNumber checks if the given tag name is a PatchN tag (case-insensitive)
// and returns the numeric suffix N. Returns -1, false if the tag is not a PatchN tag
// or the suffix is not a valid integer.

// HasSection returns true if the spec contains a section with the given name.
// The comparison is exact (case-sensitive), consistent with [AppendLinesToSection].
func (s *structuralSpec) HasSection(sectionName string) (bool, error) {
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
func (s *structuralSpec) AddPatchEntry(packageName, filename string) error {
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
func (s *structuralSpec) RemovePatchEntry(pattern string) error {
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
func (s *structuralSpec) removePatchTagsMatching(pattern string) (int, error) {
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

			if _, ok := StructuralParsePatchTagNumber(parsedTag); !ok {
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
func (s *structuralSpec) removePatchlistEntriesMatching(pattern string) (int, error) {
	removed := 0

	err := s.mutateTree(func(tree *specTree) error {
		for _, section := range tree.Sections("%patchlist", "") {
			err := section.VisitLines(func(line *lineHandle) error {
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
			if err != nil {
				return err
			}
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
func (s *structuralSpec) GetHighestPatchTagNumber() (int, error) {
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

			num, isPatchTag := StructuralParsePatchTagNumber(parsedTag)
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
func (s *structuralSpec) RemoveSection(sectionName, packageName string) error {
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
func (s *structuralSpec) RemoveSubpackage(packageName string) error {
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

func structuralTagFamily(tag string) string {
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

func structuralConditionalDepthChange(rawLine string) int {
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

func structuralIsConditionalBranchDirective(rawLine string) bool {
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
	case elseDirective, "%elif", "%elifarch", "%elifnarch", "%elifos", "%elifnos":
		return true
	default:
		return false
	}
}

func StructuralParsePatchTagNumber(tag string) (int, bool) {
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
