// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // We want to test the internal package implementation.
package snapshot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterTimestamps(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "No timestamps",
			input:    "This is a test string without timestamps.",
			expected: "This is a test string without timestamps.",
		},
		{
			name:     "Single timestamp no ansi",
			input:    "23:45:06 WRN SOME ERROR",
			expected: "##:##:## WRN SOME ERROR",
		},
		{
			name:     "Multiple timestamps",
			input:    "23:45:06 WRN SOME ERROR\n12:34:56 ERR Another error",
			expected: "##:##:## WRN SOME ERROR\n##:##:## ERR Another error",
		},
		{
			name:     "Mixed content",
			input:    "This is a test string with a timestamp 23:45:06 WRN SOME ERROR and some more text.",
			expected: "This is a test string with a timestamp ##:##:## WRN SOME ERROR and some more text.",
		},
		{
			name:     "Ansi escape codes",
			input:    "\x1b[1m23:45:06\x1b[0m \x1b[93mWRN\x1b[0m SOME ERROR",
			expected: "\x1b[1m##:##:##\x1b[0m \x1b[93mWRN\x1b[0m SOME ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterTimestamps(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFilterVersions(t *testing.T) {
	currentVersion := "1.2.3"
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "No versions",
			input:    "This is a test string without versions.",
			expected: "This is a test string without versions.",
		},
		{
			name:     "Single version",
			input:    "azldev version 1.2.3",
			expected: "azldev version " + replacementVersionString,
		},
		{
			name:     "Multiple different versions",
			input:    "azldev version 1.2.3\nazldev version 4.5.6",
			expected: "azldev version " + replacementVersionString + "\nazldev version 4.5.6",
		},
		{
			name:  "Multiple same versions",
			input: "text\nazldev version 1.2.3\nazldev version 1.2.3\ntext",
			expected: "text\nazldev version " + replacementVersionString + "\nazldev version " +
				replacementVersionString + "\ntext",
		},
		{
			name:     "Mixed content",
			input:    "This is a test string with a version azldev version 1.2.3 and some more text.",
			expected: "This is a test string with a version azldev version " + replacementVersionString + " and some more text.",
		},
		{
			name:     "No spaces",
			input:    "azldev-version1.2.3",
			expected: "azldev-version" + replacementVersionString,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterVersion(currentVersion, tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTimestampAndVersionFiltering(t *testing.T) {
	currentVersion := "1.2.3"
	input := "23:45:06 WRN azldev version 1.2.3\n12:34:56 ERR Another error"
	expected := "##:##:## WRN azldev version " + replacementVersionString + "\n##:##:## ERR Another error"

	result := filterTimestamps(input)
	result = filterVersion(currentVersion, result)

	assert.Equal(t, expected, result)
}
