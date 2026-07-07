// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// OverlayFilesComponentPlaceholder is the single supported placeholder in an
// [ComponentConfig.OverlayFiles] entry. It is required in every project-scope
// entry (discovery) and forbidden in distro/component-group/component-scope
// entries; when present it must appear exactly once and must be a whole path
// segment.
const OverlayFilesComponentPlaceholder = "{component}"

// ErrInvalidOverlayFilesEntry is returned when an entry in
// [ComponentConfig.OverlayFiles] does not conform to the placeholder rules
// documented on that field.
var ErrInvalidOverlayFilesEntry = errors.New("invalid overlay-files entry")

// hasOverlayFilesPlaceholder reports whether entry contains at least one
// occurrence of [OverlayFilesComponentPlaceholder].
func hasOverlayFilesPlaceholder(entry string) bool {
	return strings.Contains(entry, OverlayFilesComponentPlaceholder)
}

// validateOverlayFilesPlaceholder verifies that entry contains exactly one
// [OverlayFilesComponentPlaceholder] and that the placeholder is a whole path
// segment. Path separators must be '/' (POSIX-style), and no glob
// metacharacters ('*', '?', '[') may appear before the placeholder because
// pattern-discovery extracts the captured component name by literal-prefix
// matching against the concrete glob result. Metacharacters after the
// placeholder are allowed. Returns nil when the entry is well-formed.
func validateOverlayFilesPlaceholder(entry string) error {
	if entry == "" {
		return fmt.Errorf("%w: entry is empty", ErrInvalidOverlayFilesEntry)
	}

	count := strings.Count(entry, OverlayFilesComponentPlaceholder)
	if count == 0 {
		return fmt.Errorf(
			"%w: entry %#q must contain %q exactly once",
			ErrInvalidOverlayFilesEntry, entry, OverlayFilesComponentPlaceholder,
		)
	}

	if count > 1 {
		return fmt.Errorf(
			"%w: entry %#q contains %q %d times; it must appear exactly once",
			ErrInvalidOverlayFilesEntry, entry, OverlayFilesComponentPlaceholder, count,
		)
	}

	if strings.ContainsRune(entry, '\\') {
		return fmt.Errorf(
			"%w: entry %#q must use %q as the path separator (backslash is not accepted)",
			ErrInvalidOverlayFilesEntry, entry, "/",
		)
	}

	idx := strings.Index(entry, OverlayFilesComponentPlaceholder)
	if idx > 0 && entry[idx-1] != '/' {
		return fmt.Errorf(
			"%w: %q in entry %#q must be a whole path segment (preceded by %q or start of entry)",
			ErrInvalidOverlayFilesEntry, OverlayFilesComponentPlaceholder, entry, "/",
		)
	}

	tail := idx + len(OverlayFilesComponentPlaceholder)
	if tail == len(entry) {
		return fmt.Errorf(
			"%w: %q in entry %#q must be followed by %q; it names a directory, not a file",
			ErrInvalidOverlayFilesEntry, OverlayFilesComponentPlaceholder, entry, "/",
		)
	}

	if entry[tail] != '/' {
		return fmt.Errorf(
			"%w: %q in entry %#q must be a whole path segment (followed by %q)",
			ErrInvalidOverlayFilesEntry, OverlayFilesComponentPlaceholder, entry, "/",
		)
	}

	if tail+1 == len(entry) {
		return fmt.Errorf(
			"%w: %q in entry %#q must be followed by at least one more path segment; "+
				"it names a directory, not a file",
			ErrInvalidOverlayFilesEntry, OverlayFilesComponentPlaceholder, entry,
		)
	}

	prefix := entry[:idx]
	if globMetaIdx := strings.IndexAny(prefix, "*?["); globMetaIdx >= 0 {
		return fmt.Errorf(
			"%w: entry %#q must not contain glob metacharacters (%q) before %q; "+
				"the discovery capture uses a literal prefix",
			ErrInvalidOverlayFilesEntry, entry, string(prefix[globMetaIdx]), OverlayFilesComponentPlaceholder,
		)
	}

	// Substitute '*' for {component} to build the concrete glob that
	// pattern-discovery will hand to doublestar.
	globPattern := prefix + "*" + entry[tail:]
	if !doublestar.ValidatePattern(globPattern) {
		return fmt.Errorf(
			"%w: entry %#q expands to invalid glob %#q",
			ErrInvalidOverlayFilesEntry, entry, globPattern,
		)
	}

	return nil
}

// SplitOverlayFilesPlaceholder splits an entry at its
// [OverlayFilesComponentPlaceholder] and returns the prefix and suffix. The
// caller can then substitute a concrete component name (or a glob wildcard)
// for the placeholder. Passing an entry without a placeholder is a programming
// error and panics with a diagnostic message.
func SplitOverlayFilesPlaceholder(entry string) (prefix, suffix string) {
	idx := strings.Index(entry, OverlayFilesComponentPlaceholder)
	if idx < 0 {
		panic(fmt.Sprintf(
			"SplitOverlayFilesPlaceholder: entry %q does not contain %q",
			entry, OverlayFilesComponentPlaceholder,
		))
	}

	return entry[:idx], entry[idx+len(OverlayFilesComponentPlaceholder):]
}

// substituteOverlayFilesPlaceholder returns entry with the single
// [OverlayFilesComponentPlaceholder] replaced by componentName. Entries with
// no placeholder are returned unchanged.
func substituteOverlayFilesPlaceholder(entry, componentName string) string {
	if !hasOverlayFilesPlaceholder(entry) {
		return entry
	}

	return strings.Replace(entry, OverlayFilesComponentPlaceholder, componentName, 1)
}

// absolutizeOverlayFilesPlaceholderEntries returns a copy of entries where every
// entry containing [OverlayFilesComponentPlaceholder] has been absolutized
// against referenceDir. Non-placeholder entries are cloned verbatim so they can
// still be resolved relative to a per-component reference directory later.
func absolutizeOverlayFilesPlaceholderEntries(referenceDir string, entries []string) []string {
	if entries == nil {
		return nil
	}

	result := make([]string, len(entries))

	for i, entry := range entries {
		if hasOverlayFilesPlaceholder(entry) {
			result[i] = makeAbsolute(referenceDir, entry)
		} else {
			result[i] = entry
		}
	}

	return result
}
