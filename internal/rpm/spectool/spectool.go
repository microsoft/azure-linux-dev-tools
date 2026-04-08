// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package spectool provides utilities for parsing spectool output.
package spectool

import (
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// filenameFromURL attempts to parse value as a URL and extract the basename.
// Returns the filename and true if value is a URL, or ("", false) if not.
func filenameFromURL(value string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}

	return path.Base(parsed.Path), true
}

// ParseSpectoolOutput parses the raw stdout from spectool -l -a into a list of
// source/patch filenames. For URL values, extracts the basename. For local paths,
// preserves the relative directory structure.
func ParseSpectoolOutput(stdout string) []string {
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
		if urlName, ok := filenameFromURL(trimmed); ok {
			name = urlName
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
