// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

const spectoolBinary = "spectool"

// ListSpecFiles runs spectool to list the source and patch filenames referenced
// by a spec file. For URL values (sources), returns just the basename (e.g.,
// "curl-8.0.tar.xz"). For local paths (patches), returns the cleaned relative
// path as referenced in the spec (e.g., "patches/fix.patch").
//
// Returns an error if spectool is not installed or the spec can't be parsed
// (e.g., due to undefined macros like %gometa).
func ListSpecFiles(ctx context.Context, cmdFactory opctx.CmdFactory, specPath string) ([]string, error) {
	if !cmdFactory.CommandInSearchPath(spectoolBinary) {
		return nil, fmt.Errorf(
			"spectool not found in PATH; install via: "+
				"dnf install rpmdevtools:\n%w", exec.ErrNotFound)
	}

	slog.Debug("Running spectool to list spec files",
		"spec", specPath,
	)

	rawCmd := exec.CommandContext(ctx, spectoolBinary, "-l", "-a", specPath)

	var stderr strings.Builder

	rawCmd.Stderr = &stderr

	cmd, err := cmdFactory.Command(rawCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to wrap spectool command:\n%w", err)
	}

	stdout, err := cmd.RunAndGetOutput(ctx)
	if err != nil {
		return nil, fmt.Errorf("spectool failed for %#q:\n%s\n%w",
			specPath, stderr.String(), err)
	}

	// Parse output lines like:
	//   Source0: ftp://ftp.gnu.org/pub/gnu/sed/sed-4.9.tar.xz
	//   Patch0: sed-b-flag.patch
	//   Patch1: patches/fix.patch
	// For URL values, extract the basename. For local paths, keep the
	// relative path so callers can preserve directory structure.
	var files []string

	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}

		// Each line is "SourceN: <url-or-filename>" or "PatchN: <filename>"
		_, value, found := strings.Cut(line, ": ")
		if !found {
			continue
		}

		trimmed := strings.TrimSpace(value)

		var name string
		if isURL(trimmed) {
			// URLs: parse properly to handle query strings and fragments,
			// then extract the filename from the path component.
			name = extractFilenameFromURL(trimmed)
		} else {
			// Local paths: clean but preserve relative directory structure.
			// Reject absolute or traversal paths.
			cleaned := filepath.Clean(trimmed)
			if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
				continue
			}

			name = cleaned
		}

		if name != "" && name != "." {
			files = append(files, name)
		}
	}

	slog.Debug("spectool listed spec files",
		"spec", specPath,
		"count", len(files),
		"files", files,
	)

	return files, nil
}

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
