// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverlayMetadata_Validate(t *testing.T) {
	testCases := []struct {
		name          string
		metadata      projectconfig.OverlayMetadata
		errorContains string
	}{
		{
			name: "backport-dist-git with commits is valid",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryBackportDistGit,
				Commits:  []string{"https://src.fedoraproject.org/rpms/xclock/c/abc123"},
			},
		},
		{
			name: "backport-dist-git requires commits",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryBackportDistGit,
			},
			errorContains: "commits",
		},
		{
			name: "backport-dist-git with bug refs valid",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryBackportDistGit,
				Commits:  []string{"https://src.fedoraproject.org/rpms/xclock/c/abc123"},
				Bugs: []projectconfig.BugRef{
					{URL: "https://github.com/example/repo/issues/1"},
				},
			},
		},
		{
			name: "azl-branding-policy needs no extras",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryAZLBrandingPolicy,
			},
		},
		{
			name: "azl-dep-missing-workaround needs no extras",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryAZLDepMissingWorkaround,
			},
		},
		{
			name: "azl-branding-policy may carry bugs and commits",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryAZLBrandingPolicy,
				Bugs: []projectconfig.BugRef{
					{URL: "https://bugzilla.redhat.com/show_bug.cgi?id=2234567"},
					{URL: "https://github.com/example/repo/issues/2"},
				},
				Commits:      []string{"https://github.com/example/repo/commit/deadbeef"},
				Upstreamable: lo.ToPtr(true),
			},
		},
		{
			name: "upstreamable false is valid",
			metadata: projectconfig.OverlayMetadata{
				Category:     projectconfig.OverlayCategoryAZLCompatibility,
				Upstreamable: lo.ToPtr(false),
			},
		},
		{
			name: "missing category",
			metadata: projectconfig.OverlayMetadata{
				Commits: []string{"https://example.com/commit/abc"},
			},
			errorContains: "category",
		},
		{
			name: "unknown category",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategory("bogus"),
			},
			errorContains: "unknown overlay category",
		},
		{
			name: "bug requires url",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryAZLCompatibility,
				Bugs:     []projectconfig.BugRef{{}},
			},
			errorContains: "URL",
		},
		{
			name: "bug rejects non-http url",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryAZLCompatibility,
				Bugs: []projectconfig.BugRef{
					{URL: "ftp://example.com/bug/1"},
				},
			},
			errorContains: "URL",
		},
		{
			name: "commit url must be http",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryBackportDistGit,
				Commits:  []string{"not-a-url"},
			},
			errorContains: "Commits",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.metadata.Validate()

			if testCase.errorContains == "" {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.errorContains)
		})
	}
}

func TestComponentOverlay_Validate_Metadata(t *testing.T) {
	overlay := projectconfig.ComponentOverlay{
		Type:  projectconfig.ComponentOverlayAddSpecTag,
		Tag:   "Vendor",
		Value: "Microsoft",
		Metadata: &projectconfig.OverlayMetadata{
			Category: projectconfig.OverlayCategoryAZLBrandingPolicy,
		},
	}
	require.NoError(t, overlay.Validate())

	overlay.Metadata.Category = projectconfig.OverlayCategory("bogus")

	err := overlay.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown overlay category")
}
