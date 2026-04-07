// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Testing unexported functions.
package sources

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

			result := parseSpectoolOutput(testCase.input)
			assert.Equal(t, testCase.expected, result)
		})
	}
}

func TestIsURL(t *testing.T) {
	t.Parallel()

	assert.True(t, isURL("https://example.com/file.tar.gz"))
	assert.True(t, isURL("ftp://ftp.gnu.org/pub/file.tar.xz"))
	assert.False(t, isURL("file.tar.gz"))
	assert.False(t, isURL("patches/fix.patch"))
}

func TestExtractFilenameFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"simple", "https://example.com/file.tar.gz", "file.tar.gz"},
		{"with query", "https://example.com/file.tar.gz?raw=true", "file.tar.gz"},
		{"with fragment", "https://example.com/file.tar.gz#section", "file.tar.gz"},
		{"nested path", "https://example.com/a/b/c/file.tar.gz", "file.tar.gz"},
		{"trailing slash", "https://example.com/", "/"},
		{"ftp", "ftp://ftp.gnu.org/pub/gnu/sed/sed-4.9.tar.xz", "sed-4.9.tar.xz"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := extractFilenameFromURL(testCase.url)
			assert.Equal(t, testCase.expected, result)
		})
	}
}
