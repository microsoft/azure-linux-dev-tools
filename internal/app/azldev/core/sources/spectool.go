// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// isURL returns true if the value looks like a URL (has a scheme).
func isURL(value string) bool {
	return strings.Contains(value, "://")
}

// extractFilenameFromURL parses a URL and returns just the filename from
// its path component, stripping query strings and fragments.
func extractFilenameFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		// Fall back to filepath.Base if URL parsing fails.
		return filepath.Base(rawURL)
	}

	return path.Base(parsed.Path)
}

// parseSpectoolOutput parses the raw stdout from spectool -l -a into a list of
// source/patch filenames. For URL values, extracts the basename. For local paths,
// preserves the relative directory structure.
func parseSpectoolOutput(stdout string) []string {
	// Parse output lines like:
	//   Source0: ftp://ftp.gnu.org/pub/gnu/sed/sed-4.9.tar.xz
	//   Patch0: sed-b-flag.patch
	//   Patch1: patches/fix.patch
	var files []string

	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}

		// spectool always separates tag from value with ": " (colon-space).
		_, value, found := strings.Cut(line, ": ")
		if !found {
			continue
		}

		trimmed := strings.TrimSpace(value)

		var name string
		if isURL(trimmed) {
			name = extractFilenameFromURL(trimmed)
		} else {
			// Local paths: clean but preserve relative directory structure.
			cleaned := filepath.Clean(trimmed)
			if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
				continue
			}

			name = cleaned
		}

		if name != "" && name != "." && name != "/" {
			files = append(files, name)
		}
	}

	return files
}
