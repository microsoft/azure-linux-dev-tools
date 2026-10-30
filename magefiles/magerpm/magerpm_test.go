// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Tests unexported RPM packaging helpers.
package magerpm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{name: "plain", input: "0.4.0", expected: "0.4.0"},
		{name: "tag", input: "v0.4.0", expected: "0.4.0"},
		{name: "surrounding whitespace", input: " v0.4.0\n", expected: "0.4.0"},
		{name: "prerelease", input: "0.4.0-rc.1", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := normalizeVersion(test.input)
			if test.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.expected, actual)
		})
	}
}

func TestParseSpecVersion(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
		wantErr  bool
	}{
		{name: "valid", content: "Name: azldev\nVersion: 0.4.0\n", expected: "0.4.0"},
		{name: "missing", content: "Name: azldev\n", wantErr: true},
		{name: "macro", content: "Version: %{package_version}\n", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := parseSpecVersion(test.content)
			if test.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.expected, actual)
		})
	}
}
