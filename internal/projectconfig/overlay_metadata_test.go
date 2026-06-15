// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
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
			name: "backport-fedora with commits is valid",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryBackportFedora,
				Commits:  []string{"https://src.fedoraproject.org/rpms/xclock/c/abc123"},
			},
		},
		{
			name: "backport-fedora with fixed-in is valid",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryBackportFedora,
				FixedIn:  "xclock-1.1.1-11.fc44",
			},
		},
		{
			name: "backport-fedora with removable-after is valid",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategoryBackportFedora,
				FixedIn:        "xclock-1.1.1-11.fc44",
				RemovableAfter: "f44",
			},
		},
		{
			name: "backport-fedora requires commits or fixed-in",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryBackportFedora,
			},
			errorContains: "commits' or 'fixed-in",
		},
		{
			name: "upstream-fix valid",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryUpstreamFix,
				PR:       "https://github.com/example/repo/pull/1",
			},
		},
		{
			name: "azl-branding-policy needs no extras",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryAZLBrandingPolicy,
			},
		},
		{
			name: "azl-branding-policy may carry pr and commits",
			metadata: projectconfig.OverlayMetadata{
				Category:        projectconfig.OverlayCategoryAZLBrandingPolicy,
				PR:              "https://github.com/example/repo/pull/2",
				Commits:         []string{"https://github.com/example/repo/commit/deadbeef"},
				Upstreamability: projectconfig.OverlayUpstreamabilityYes,
			},
		},
		{
			name: "upstreamability no is valid",
			metadata: projectconfig.OverlayMetadata{
				Category:        projectconfig.OverlayCategoryAZLBuild,
				Upstreamability: projectconfig.OverlayUpstreamabilityNo,
			},
		},
		{
			name: "unknown upstreamability rejected",
			metadata: projectconfig.OverlayMetadata{
				Category:        projectconfig.OverlayCategoryAZLBuild,
				Upstreamability: projectconfig.OverlayUpstreamability("maybe"),
			},
			errorContains: "unknown upstreamability",
		},
		{
			name: "removable-after rejected outside backport-fedora",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategoryAZLBuild,
				RemovableAfter: "f44",
			},
			errorContains: "removable-after",
		},
		{
			name: "missing category",
			metadata: projectconfig.OverlayMetadata{
				FixedIn: "1.0",
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
			name: "invalid pr url",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryUpstreamFix,
				PR:       "not-a-url",
			},
			errorContains: "PR",
		},
		{
			name: "non-http pr url rejected",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryUpstreamFix,
				PR:       "ftp://example.com/pr/1",
			},
			errorContains: "PR",
		},
		{
			name: "invalid bug url",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryAZLBuild,
				BugLinks: []string{"https://valid.example", "not a url"},
			},
			errorContains: "BugLinks[1]",
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
	// Overlay with valid metadata succeeds.
	overlay := projectconfig.ComponentOverlay{
		Type:  projectconfig.ComponentOverlayAddSpecTag,
		Tag:   "Vendor",
		Value: "Microsoft",
		Metadata: &projectconfig.OverlayMetadata{
			Category: projectconfig.OverlayCategoryAZLBrandingPolicy,
		},
	}
	require.NoError(t, overlay.Validate())

	// Overlay with invalid metadata fails — wraps the metadata error.
	overlay.Metadata.RemovableAfter = "f44"

	err := overlay.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removable-after")
}
