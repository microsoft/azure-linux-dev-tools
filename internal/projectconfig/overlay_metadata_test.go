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
			name: "upstream-backport with commits is valid",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryUpstreamBackport,
				Commits: []projectconfig.URLRef{
					{URL: "https://src.fedoraproject.org/rpms/xclock/c/abc123"},
				},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamed,
			},
		},
		{
			name: "upstream-backport requires commits",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategoryUpstreamBackport,
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamed,
			},
			errorContains: "commits",
		},
		{
			name: "upstream-backport with bug refs valid",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryUpstreamBackport,
				Commits: []projectconfig.URLRef{
					{URL: "https://src.fedoraproject.org/rpms/xclock/c/abc123"},
				},
				Bugs: []projectconfig.URLRef{
					{URL: "https://github.com/example/repo/issues/1"},
				},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamed,
			},
		},
		{
			name: "azl-branding-policy with inapplicable status is valid",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategoryAZLBrandingPolicy,
				UpstreamStatus: projectconfig.OverlayUpstreamStatusInapplicable,
			},
		},
		{
			name: "azl-dep-missing-workaround with unknown status is valid",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategoryAZLDepMissingWorkaround,
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUnknown,
			},
		},
		{
			name: "azl-branding-policy may carry bugs and commits",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryAZLBrandingPolicy,
				Bugs: []projectconfig.URLRef{
					{URL: "https://bugzilla.redhat.com/show_bug.cgi?id=2234567"},
					{URL: "https://github.com/example/repo/issues/2"},
				},
				Commits: []projectconfig.URLRef{
					{URL: "https://github.com/example/repo/commit/deadbeef"},
				},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamable,
			},
		},
		{
			name: "upstream-status inapplicable is valid",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategoryAZLCompatibility,
				UpstreamStatus: projectconfig.OverlayUpstreamStatusInapplicable,
			},
		},
		{
			name: "upstream-status needs-upstream-hook is valid",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategoryAZLPruning,
				UpstreamStatus: projectconfig.OverlayUpstreamStatusNeedsUpstreamHook,
			},
		},
		{
			name: "upstream-status upstreamed is valid",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryUpstreamBackport,
				Commits: []projectconfig.URLRef{
					{URL: "https://src.fedoraproject.org/rpms/xclock/c/abc123"},
				},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamed,
			},
		},
		{
			name: "unknown upstream-status is rejected",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategoryAZLCompatibility,
				UpstreamStatus: projectconfig.OverlayUpstreamStatus("bogus"),
			},
			errorContains: "unknown overlay upstream-status",
		},
		{
			name: "missing category",
			metadata: projectconfig.OverlayMetadata{
				Commits: []projectconfig.URLRef{
					{URL: "https://example.com/commit/abc"},
				},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamed,
			},
			errorContains: "category",
		},
		{
			name: "unknown category",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategory("bogus"),
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUnknown,
			},
			errorContains: "unknown overlay category",
		},
		{
			name: "missing upstream-status",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryAZLBrandingPolicy,
			},
			errorContains: "upstream-status",
		},
		{
			name: "bug requires url",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategoryAZLCompatibility,
				Bugs:           []projectconfig.URLRef{{}},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusInapplicable,
			},
			errorContains: "URL",
		},
		{
			name: "bug rejects non-http url",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryAZLCompatibility,
				Bugs: []projectconfig.URLRef{
					{URL: "ftp://example.com/bug/1"},
				},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusInapplicable,
			},
			errorContains: "URL",
		},
		{
			name: "commit url must be http",
			metadata: projectconfig.OverlayMetadata{
				Category:       projectconfig.OverlayCategoryUpstreamBackport,
				Commits:        []projectconfig.URLRef{{URL: "not-a-url"}},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamed,
			},
			errorContains: "URL",
		},
		{
			name: "upstream-backport with explicit upstreamed status is valid",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryUpstreamBackport,
				Commits: []projectconfig.URLRef{
					{URL: "https://src.fedoraproject.org/rpms/xclock/c/abc123"},
				},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamed,
			},
		},
		{
			name: "upstream-backport with upstreamable status is valid",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryUpstreamBackport,
				Commits: []projectconfig.URLRef{
					{URL: "https://src.fedoraproject.org/rpms/xclock/c/abc123"},
				},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamable,
			},
		},
		{
			name: "upstream-backport with needs-upstream-hook status is contradictory",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryUpstreamBackport,
				Commits: []projectconfig.URLRef{
					{URL: "https://src.fedoraproject.org/rpms/xclock/c/abc123"},
				},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusNeedsUpstreamHook,
			},
			errorContains: "contradictory",
		},
		{
			name: "upstream-backport with inapplicable status is contradictory",
			metadata: projectconfig.OverlayMetadata{
				Category: projectconfig.OverlayCategoryUpstreamBackport,
				Commits: []projectconfig.URLRef{
					{URL: "https://src.fedoraproject.org/rpms/xclock/c/abc123"},
				},
				UpstreamStatus: projectconfig.OverlayUpstreamStatusInapplicable,
			},
			errorContains: "contradictory",
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
			Category:       projectconfig.OverlayCategoryAZLBrandingPolicy,
			UpstreamStatus: projectconfig.OverlayUpstreamStatusInapplicable,
		},
	}
	require.NoError(t, overlay.Validate())

	overlay.Metadata.Category = projectconfig.OverlayCategory("bogus")

	err := overlay.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown overlay category")
}
