// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:maintidx // Test table complexity scales with the number of overlay types.
func TestComponentOverlay_Validate(t *testing.T) {
	testCases := []struct {
		name          string
		overlay       projectconfig.ComponentOverlay
		errorExpected bool
		errorContains string
	}{
		// spec-add-tag tests
		{
			name: "spec-add-tag valid",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayAddSpecTag,
				Tag:   "BuildRequires",
				Value: "some-package",
			},
			errorExpected: false,
		},
		{
			name: "spec-add-tag missing tag",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayAddSpecTag,
				Value: "some-value",
			},
			errorExpected: true,
			errorContains: "tag",
		},
		{
			name: "spec-add-tag missing value",
			overlay: projectconfig.ComponentOverlay{
				Type: projectconfig.ComponentOverlayAddSpecTag,
				Tag:  "BuildRequires",
			},
			errorExpected: true,
			errorContains: "value",
		},
		// spec-insert-tag tests
		{
			name: "spec-insert-tag valid",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayInsertSpecTag,
				Tag:   "Source9999",
				Value: "macros.azl.macros",
			},
			errorExpected: false,
		},
		{
			name: "spec-insert-tag missing tag",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayInsertSpecTag,
				Value: "some-value",
			},
			errorExpected: true,
			errorContains: "tag",
		},
		{
			name: "spec-insert-tag missing value",
			overlay: projectconfig.ComponentOverlay{
				Type: projectconfig.ComponentOverlayInsertSpecTag,
				Tag:  "Source9999",
			},
			errorExpected: true,
			errorContains: "value",
		},
		// spec-set-tag tests
		{
			name: "spec-set-tag valid",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlaySetSpecTag,
				Tag:   "Version",
				Value: "1.0.0",
			},
			errorExpected: false,
		},
		{
			name: "spec-set-tag missing tag",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlaySetSpecTag,
				Value: "1.0.0",
			},
			errorExpected: true,
			errorContains: "tag",
		},
		// spec-update-tag tests
		{
			name: "spec-update-tag valid",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayUpdateSpecTag,
				Tag:   "Release",
				Value: "2",
			},
			errorExpected: false,
		},
		// spec-remove-tag tests
		{
			name: "spec-remove-tag valid",
			overlay: projectconfig.ComponentOverlay{
				Type: projectconfig.ComponentOverlayRemoveSpecTag,
				Tag:  "Obsoletes",
			},
			errorExpected: false,
		},
		{
			name: "spec-remove-tag missing tag",
			overlay: projectconfig.ComponentOverlay{
				Type: projectconfig.ComponentOverlayRemoveSpecTag,
			},
			errorExpected: true,
			errorContains: "tag",
		},
		// spec-prepend-lines tests
		{
			name: "spec-prepend-lines valid",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayPrependSpecLines,
				Lines: []string{"# Comment"},
			},
			errorExpected: false,
		},
		{
			name: "spec-prepend-lines missing lines",
			overlay: projectconfig.ComponentOverlay{
				Type: projectconfig.ComponentOverlayPrependSpecLines,
			},
			errorExpected: true,
			errorContains: "lines",
		},
		// spec-append-lines tests
		{
			name: "spec-append-lines valid",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayAppendSpecLines,
				Lines: []string{"# Footer"},
			},
			errorExpected: false,
		},
		// spec-search-replace tests
		{
			name: "spec-search-replace valid",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInSpec,
				Regex:       "pattern",
				Replacement: "replacement",
			},
			errorExpected: false,
		},
		{
			name: "spec-search-replace valid with empty replacement",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInSpec,
				Regex:       "pattern-to-delete",
				Replacement: "",
			},
			errorExpected: false,
		},
		{
			name: "spec-search-replace missing regex",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInSpec,
				Replacement: "replacement",
			},
			errorExpected: true,
			errorContains: "regex",
		},
		// file-prepend-lines tests
		{
			name: "file-prepend-lines valid",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayPrependLinesToFile,
				Filename: "test.txt",
				Lines:    []string{"# Header"},
			},
			errorExpected: false,
		},
		{
			name: "file-prepend-lines missing file",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayPrependLinesToFile,
				Lines: []string{"# Header"},
			},
			errorExpected: true,
			errorContains: "file",
		},
		{
			name: "file-prepend-lines missing lines",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayPrependLinesToFile,
				Filename: "test.txt",
			},
			errorExpected: true,
			errorContains: "lines",
		},
		// file-search-replace tests
		{
			name: "file-search-replace valid",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
				Filename:    "config.txt",
				Regex:       "old",
				Replacement: "new",
			},
			errorExpected: false,
		},
		{
			name: "file-search-replace missing file",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
				Regex:       "pattern",
				Replacement: "replacement",
			},
			errorExpected: true,
			errorContains: "file",
		},
		{
			name: "file-search-replace missing regex",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
				Filename:    "test.txt",
				Replacement: "replacement",
			},
			errorExpected: true,
			errorContains: "regex",
		},
		// file-add tests
		{
			name: "file-add valid",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayAddFile,
				Filename: "new-file.txt",
				Source:   "/path/to/source.txt",
			},
			errorExpected: false,
		},
		{
			name: "file-add missing file",
			overlay: projectconfig.ComponentOverlay{
				Type:   projectconfig.ComponentOverlayAddFile,
				Source: "/path/to/source.txt",
			},
			errorExpected: true,
			errorContains: "file",
		},
		{
			name: "file-add missing source",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayAddFile,
				Filename: "new-file.txt",
			},
			errorExpected: true,
			errorContains: "source",
		},
		// Description included in error
		{
			name: "error includes description",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayAddSpecTag,
				Description: "Add vendor tag for compliance",
			},
			errorExpected: true,
			errorContains: "Add vendor tag for compliance",
		},
		// patch-add tests
		{
			name: "patch-add valid with source only",
			overlay: projectconfig.ComponentOverlay{
				Type:   projectconfig.ComponentOverlayAddPatch,
				Source: "patches/fix-foo.patch",
			},
			errorExpected: false,
		},
		{
			name: "patch-add valid with source and file",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayAddPatch,
				Source:   "patches/fix-foo.patch",
				Filename: "my-fix.patch",
			},
			errorExpected: false,
		},
		{
			name: "patch-add missing source",
			overlay: projectconfig.ComponentOverlay{
				Type: projectconfig.ComponentOverlayAddPatch,
			},
			errorExpected: true,
			errorContains: "source",
		},
		{
			name: "patch-add absolute file path rejected",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayAddPatch,
				Source:   "patches/fix-foo.patch",
				Filename: "/absolute/path.patch",
			},
			errorExpected: true,
			errorContains: "relative path",
		},
		// patch-remove tests
		{
			name: "patch-remove valid",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayRemovePatch,
				Filename: "fix-foo.patch",
			},
			errorExpected: false,
		},
		{
			name: "patch-remove missing file",
			overlay: projectconfig.ComponentOverlay{
				Type: projectconfig.ComponentOverlayRemovePatch,
			},
			errorExpected: true,
			errorContains: "file",
		},
		{
			name: "patch-remove absolute file path rejected",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayRemovePatch,
				Filename: "/absolute/fix.patch",
			},
			errorExpected: true,
			errorContains: "relative path",
		},
		{
			name: "patch-remove with glob pattern valid",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayRemovePatch,
				Filename: "CVE-*.patch",
			},
			errorExpected: false,
		},
		{
			name: "patch-remove with doublestar glob valid",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayRemovePatch,
				Filename: "**/*.patch",
			},
			errorExpected: false,
		},
		{
			name: "patch-remove with invalid glob pattern",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayRemovePatch,
				Filename: "[invalid",
			},
			errorExpected: true,
			errorContains: "invalid glob pattern",
		},
		// spec-remove-section tests
		{
			name: "spec-remove-section valid with section only",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayRemoveSection,
				SectionName: "%generate_buildrequires",
			},
			errorExpected: false,
		},
		{
			name: "spec-remove-section valid with section and package",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayRemoveSection,
				SectionName: "%files",
				PackageName: "devel",
			},
			errorExpected: false,
		},
		{
			name: "spec-remove-section missing section",
			overlay: projectconfig.ComponentOverlay{
				Type: projectconfig.ComponentOverlayRemoveSection,
			},
			errorExpected: true,
			errorContains: "section",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.overlay.Validate()

			if testCase.errorExpected {
				require.Error(t, err)

				if testCase.errorContains != "" {
					assert.Contains(t, err.Error(), testCase.errorContains)
				}

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestComponentOverlay_ModifiesSpec(t *testing.T) {
	specOverlayTypes := []projectconfig.ComponentOverlayType{
		projectconfig.ComponentOverlayAddSpecTag,
		projectconfig.ComponentOverlayInsertSpecTag,
		projectconfig.ComponentOverlaySetSpecTag,
		projectconfig.ComponentOverlayUpdateSpecTag,
		projectconfig.ComponentOverlayRemoveSpecTag,
		projectconfig.ComponentOverlayPrependSpecLines,
		projectconfig.ComponentOverlayAppendSpecLines,
		projectconfig.ComponentOverlaySearchAndReplaceInSpec,
		projectconfig.ComponentOverlayRemoveSection,
		projectconfig.ComponentOverlayAddPatch,
		projectconfig.ComponentOverlayRemovePatch,
	}

	nonSpecOverlayTypes := []projectconfig.ComponentOverlayType{
		projectconfig.ComponentOverlayPrependLinesToFile,
		projectconfig.ComponentOverlaySearchAndReplaceInFile,
		projectconfig.ComponentOverlayAddFile,
	}

	for _, overlayType := range specOverlayTypes {
		t.Run(string(overlayType)+"_is_spec_overlay", func(t *testing.T) {
			overlay := projectconfig.ComponentOverlay{Type: overlayType}
			assert.True(t, overlay.ModifiesSpec(), "expected %s to be a spec overlay", overlayType)
		})
	}

	for _, overlayType := range nonSpecOverlayTypes {
		t.Run(string(overlayType)+"_is_not_spec_overlay", func(t *testing.T) {
			overlay := projectconfig.ComponentOverlay{Type: overlayType}
			assert.False(t, overlay.ModifiesSpec(), "expected %s to not be a spec overlay", overlayType)
		})
	}
}
