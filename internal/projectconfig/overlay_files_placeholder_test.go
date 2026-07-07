// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test unexported helpers.
package projectconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOverlayFilesPlaceholder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   string
		wantErr bool
	}{
		{name: "empty", entry: "", wantErr: true},
		{name: "missing placeholder", entry: "comps/*/overlays/*.overlay.toml", wantErr: true},
		{name: "multiple placeholders", entry: "comps/{component}/{component}/x.toml", wantErr: true},
		{name: "placeholder is prefix substring", entry: "prefix-{component}/x.toml", wantErr: true},
		{name: "placeholder is suffix substring", entry: "{component}-suffix/x.toml", wantErr: true},
		{name: "backslash separator rejected", entry: "comps\\{component}\\x.toml", wantErr: true},
		{name: "glob metachar before placeholder", entry: "**/{component}/*.overlay.toml", wantErr: true},
		{name: "star before placeholder", entry: "comps*/{component}/*.overlay.toml", wantErr: true},
		{name: "whole segment at start", entry: "{component}/overlays/*.overlay.toml", wantErr: false},
		{name: "whole segment in middle", entry: "comps/{component}/overlays/*.overlay.toml", wantErr: false},
		{name: "placeholder at end rejected", entry: "comps/overlays/{component}", wantErr: true},
		{name: "placeholder followed by only slash rejected", entry: "comps/{component}/", wantErr: true},
		{name: "doublestar in suffix allowed", entry: "comps/{component}/**/*.overlay.toml", wantErr: false},
		{name: "absolute path", entry: "/project/base/comps/{component}/overlays/*.overlay.toml", wantErr: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := validateOverlayFilesPlaceholder(testCase.entry)
			if testCase.wantErr {
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidOverlayFilesEntry,
					"expected ErrInvalidOverlayFilesEntry, got %v", err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSplitOverlayFilesPlaceholder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		entry      string
		wantPrefix string
		wantSuffix string
	}{
		{
			name:       "middle",
			entry:      "comps/{component}/overlays/*.overlay.toml",
			wantPrefix: "comps/",
			wantSuffix: "/overlays/*.overlay.toml",
		},
		{
			name:       "start",
			entry:      "{component}/overlays/*.overlay.toml",
			wantPrefix: "",
			wantSuffix: "/overlays/*.overlay.toml",
		},
		{
			name:       "end",
			entry:      "comps/overlays/{component}",
			wantPrefix: "comps/overlays/",
			wantSuffix: "",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			prefix, suffix := SplitOverlayFilesPlaceholder(testCase.entry)
			assert.Equal(t, testCase.wantPrefix, prefix)
			assert.Equal(t, testCase.wantSuffix, suffix)
		})
	}
}

func TestSplitOverlayFilesPlaceholder_PanicsWithoutPlaceholder(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t,
		`SplitOverlayFilesPlaceholder: entry "comps/foo/overlays/x.toml" does not contain "{component}"`,
		func() {
			SplitOverlayFilesPlaceholder("comps/foo/overlays/x.toml")
		})
}

func TestSubstituteOverlayFilesPlaceholder(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		"comps/foo/overlays/*.toml",
		substituteOverlayFilesPlaceholder("comps/{component}/overlays/*.toml", "foo"))

	// No placeholder → unchanged.
	assert.Equal(t,
		"overlays/*.toml",
		substituteOverlayFilesPlaceholder("overlays/*.toml", "foo"))
}
