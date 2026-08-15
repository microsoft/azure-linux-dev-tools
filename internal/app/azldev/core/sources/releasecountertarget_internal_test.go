// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMacrosCounterComponent = "test-pkg"

func writeTestMacrosFile(t *testing.T, memFS afero.Fs, lines []string) {
	t.Helper()

	name := testMacrosCounterComponent
	macrosPath := filepath.Join(testSourcesDir, name, name+MacrosFileExtension)
	require.NoError(t, fileutils.WriteFile(
		memFS, macrosPath, []byte(strings.Join(lines, "\n")+"\n"), fileperms.PublicFile))
}

func TestParseMacrosFileDefinition(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		rawLine       string
		wantMatch     bool
		expectedName  string
		expectedValue string
	}{
		{
			name:          "bare integer macro",
			rawLine:       "%azl_pkgrelease 11",
			wantMatch:     true,
			expectedName:  "azl_pkgrelease",
			expectedValue: "11",
		},
		{
			name:          "underscore prefixed macro",
			rawLine:       "%_without_debug 1",
			wantMatch:     true,
			expectedName:  "_without_debug",
			expectedValue: "1",
		},
		{
			name:          "trailing whitespace is trimmed",
			rawLine:       "%azl_pkgrelease 11\t ",
			wantMatch:     true,
			expectedName:  "azl_pkgrelease",
			expectedValue: "11",
		},
		{
			name:          "macro with no body",
			rawLine:       "%azl_pkgrelease",
			wantMatch:     true,
			expectedName:  "azl_pkgrelease",
			expectedValue: "",
		},
		{name: "comment line", rawLine: "# %azl_pkgrelease 11"},
		{name: "blank line", rawLine: ""},
		{name: "macro reference is not a definition", rawLine: "%{load:%{_sourcedir}/x.azl.macros}"},
		{name: "bare percent", rawLine: "%"},
		{name: "conditional directive is not a definition", rawLine: "%if 0"},
		{name: "ifarch directive is not a definition", rawLine: "%ifarch x86_64"},
		{name: "endif directive is not a definition", rawLine: "%endif"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			definition, matches := parseMacrosFileDefinition(testCase.rawLine)
			assert.Equal(t, testCase.wantMatch, matches)

			if !testCase.wantMatch {
				return
			}

			assert.Equal(t, testCase.expectedName, definition.name)
			assert.Equal(t, testCase.expectedValue, definition.counterValue)
		})
	}
}

// TestCollectMacrosFileMacroNames proves the release-tag validator sees the names a '%{load:}'
// brings into scope, and that a component without a macros file is not an error.
func TestCollectMacrosFileMacroNames(t *testing.T) {
	memFS := afero.NewMemMapFs()
	specDir := filepath.Join(testSourcesDir, testMacrosCounterComponent)
	require.NoError(t, fileutils.MkdirAll(memFS, specDir))

	names, err := collectMacrosFileMacroNames(memFS, specDir)
	require.NoError(t, err)
	assert.Empty(t, names, "a component with no macros file contributes no names")

	writeTestMacrosFile(t, memFS, []string{
		"# Auto-generated",
		"%_without_debug 1",
		"%azl_pkgrelease 11",
		"%if 0",
		"%{load:%{_sourcedir}/other.azl.macros}",
	})

	names, err = collectMacrosFileMacroNames(memFS, specDir)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"_without_debug": true, "azl_pkgrelease": true}, names)
}

// TestCollectMacrosFileMacroNames_MultipleFiles proves the visibility walk merges every macros
// file next to a rendered spec rather than requiring exactly one.
func TestCollectMacrosFileMacroNames_MultipleFiles(t *testing.T) {
	memFS := afero.NewMemMapFs()
	specDir := filepath.Join(testSourcesDir, testMacrosCounterComponent)
	require.NoError(t, fileutils.MkdirAll(memFS, specDir))

	require.NoError(t, fileutils.WriteFile(
		memFS, filepath.Join(specDir, "a"+MacrosFileExtension), []byte("%a 1\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(
		memFS, filepath.Join(specDir, "b"+MacrosFileExtension), []byte("%b 2\n"), fileperms.PublicFile))

	names, err := collectMacrosFileMacroNames(memFS, specDir)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"a": true, "b": true}, names)
}
