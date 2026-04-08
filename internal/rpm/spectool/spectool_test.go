// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Testing unexported helper functions.
package spectool

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSpectoolOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple source and patch",
			input:    "Source0: curl-8.5.0.tar.xz\nPatch0: fix.patch",
			expected: []string{"curl-8.5.0.tar.xz", "fix.patch"},
		},
		{
			name:     "URL source extracts basename",
			input:    "Source0: https://example.com/files/curl-8.5.0.tar.xz",
			expected: []string{"curl-8.5.0.tar.xz"},
		},
		{
			name:     "URL with query string",
			input:    "Source0: https://example.com/file.tar.gz?raw=true",
			expected: []string{"file.tar.gz"},
		},
		{
			name:     "relative subdirectory path preserved",
			input:    "Patch0: patches/fix.patch",
			expected: []string{"patches/fix.patch"},
		},
		{
			name:     "absolute path rejected",
			input:    "Patch0: /etc/passwd",
			expected: nil,
		},
		{
			name:     "dotdot traversal rejected",
			input:    "Patch0: ../../etc/passwd",
			expected: nil,
		},
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name:     "blank lines skipped",
			input:    "\n\n\n",
			expected: nil,
		},
		{
			name:     "lines without separator skipped",
			input:    "this is not a valid line",
			expected: nil,
		},
		{
			name:     "bare domain URL produces slash rejected",
			input:    "Source0: https://example.com/",
			expected: nil,
		},
		{
			name:     "mixed valid and invalid",
			input:    "Source0: good.tar.gz\nPatch0: /bad/path\nPatch1: ok.patch",
			expected: []string{"good.tar.gz", "ok.patch"},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := ParseSpectoolOutput(testCase.input)
			assert.Equal(t, testCase.expected, result)
		})
	}
}

func TestFilenameFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		expectedName string
		expectedOK   bool
	}{
		{"https URL", "https://example.com/file.tar.gz", "file.tar.gz", true},
		{"URL with query", "https://example.com/file.tar.gz?raw=true", "file.tar.gz", true},
		{"URL with fragment", "https://example.com/file.tar.gz#section", "file.tar.gz", true},
		{"nested URL path", "https://example.com/a/b/c/file.tar.gz", "file.tar.gz", true},
		{"ftp URL", "ftp://ftp.gnu.org/pub/gnu/sed/sed-4.9.tar.xz", "sed-4.9.tar.xz", true},
		{"trailing slash", "https://example.com/", "/", true},
		{"not a URL - plain filename", "file.tar.gz", "", false},
		{"not a URL - relative path", "patches/fix.patch", "", false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			name, ok := filenameFromURL(testCase.input)
			assert.Equal(t, testCase.expectedOK, ok)

			if ok {
				assert.Equal(t, testCase.expectedName, name)
			}
		})
	}
}
