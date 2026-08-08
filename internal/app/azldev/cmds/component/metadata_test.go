// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewComponentMetadataCommand(t *testing.T) {
	cmd := component.NewComponentMetadataCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, "metadata", cmd.Use)
}

func TestComponentMetadataCmd_NoMatch(t *testing.T) {
	const testComponentName = "test-component"

	testEnv := testutils.NewTestEnv(t)

	cmd := component.NewComponentMetadataCommand()
	cmd.SetArgs([]string{testComponentName})

	err := cmd.ExecuteContext(testEnv.Env)

	// We expect an error because we haven't set up any components.
	require.Error(t, err)
}

// seedComponentsWithOverlays adds two components with a mix of annotated and unannotated
// overlays (no group membership).
func seedComponentsWithOverlays(t *testing.T, testEnv *testutils.TestEnv) {
	t.Helper()

	testEnv.Config.Components["pkg-a"] = projectconfig.ComponentConfig{
		Name: "pkg-a",
		Spec: projectconfig.SpecSource{Path: "/specs/pkg-a.spec"},
		Overlays: []projectconfig.ComponentOverlay{
			{
				Type:  projectconfig.ComponentOverlayAddSpecTag,
				Tag:   "Vendor",
				Value: "Microsoft",
				Metadata: &projectconfig.OverlayMetadata{
					Category: projectconfig.OverlayCategoryAZLBrandingPolicy,
				},
			},
			{
				Type:   projectconfig.ComponentOverlayAddPatch,
				Source: "patches/fix.patch",
				Metadata: &projectconfig.OverlayMetadata{
					Category:       projectconfig.OverlayCategoryUpstreamBackport,
					Commits:        []projectconfig.URLRef{{URL: "https://src.fedoraproject.org/rpms/pkg-a/c/abc"}},
					UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamable,
				},
			},
			{
				// No metadata.
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInSpec,
				Regex:       "foo",
				Replacement: "bar",
			},
		},
	}

	testEnv.Config.Components["pkg-b"] = projectconfig.ComponentConfig{
		Name: "pkg-b",
		Spec: projectconfig.SpecSource{Path: "/specs/pkg-b.spec"},
		Overlays: []projectconfig.ComponentOverlay{
			{
				Type:  projectconfig.ComponentOverlayAddSpecTag,
				Tag:   "Packager",
				Value: "azl",
				Metadata: &projectconfig.OverlayMetadata{
					Category:       projectconfig.OverlayCategoryAZLCompatibility,
					Bugs:           []projectconfig.URLRef{{URL: "https://github.com/example/repo/issues/1"}},
					UpstreamStatus: projectconfig.OverlayUpstreamStatusUpstreamable,
				},
			},
		},
	}
}

func TestListMetadata_AllOverlays(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 3, "unannotated search-replace overlay must be excluded")

	// Sorted by (component, source, index).
	assert.Equal(t, "pkg-a", results[0].Component)
	assert.Equal(t, component.MetadataSourceOverlay, results[0].Source)
	assert.Equal(t, 1, results[0].Index)
	assert.Equal(t, projectconfig.OverlayCategoryAZLBrandingPolicy, results[0].Category)

	assert.Equal(t, "pkg-a", results[1].Component)
	assert.Equal(t, 2, results[1].Index)
	assert.Equal(t, projectconfig.OverlayCategoryUpstreamBackport, results[1].Category)
	require.NotNil(t, results[1].Metadata)
	assert.Equal(t,
		[]projectconfig.URLRef{{URL: "https://src.fedoraproject.org/rpms/pkg-a/c/abc"}},
		results[1].Metadata.Commits,
	)

	assert.Equal(t, "pkg-b", results[2].Component)
	assert.Equal(t, projectconfig.OverlayCategoryAZLCompatibility, results[2].Category)
}

func TestListMetadata_FilterByCategory(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Category:        string(projectconfig.OverlayCategoryUpstreamBackport),
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "pkg-a", results[0].Component)
	assert.Equal(t, 2, results[0].Index)
}

func TestListMetadata_UnknownCategoryRejected(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Category:        "bogus",
	}

	_, err := component.ListMetadata(testEnv.Env, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown overlay category")
}

func TestListMetadata_FilterByUpstreamStatus(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		UpstreamStatus:  string(projectconfig.OverlayUpstreamStatusUpstreamable),
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 2,
		"both overlays annotated with upstream-status=upstreamable should be included")

	// Sorted by (component, source, index).
	assert.Equal(t, "pkg-a", results[0].Component)
	assert.Equal(t, projectconfig.OverlayCategoryUpstreamBackport, results[0].Category)
	require.NotNil(t, results[0].Metadata)
	assert.Equal(t,
		projectconfig.OverlayUpstreamStatusUpstreamable,
		results[0].Metadata.UpstreamStatus,
	)

	assert.Equal(t, "pkg-b", results[1].Component)
	assert.Equal(t, projectconfig.OverlayCategoryAZLCompatibility, results[1].Category)
}

func TestListMetadata_UnknownUpstreamStatusRejected(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		UpstreamStatus:  "maybe",
	}

	_, err := component.ListMetadata(testEnv.Env, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown upstream-status value")
}

func TestListMetadata_FilterByUpstreamStatusInapplicable(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentWithGroups(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		UpstreamStatus:  string(projectconfig.OverlayUpstreamStatusInapplicable),
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1, "only the group annotated as inapplicable should be included")
	assert.Equal(t, component.MetadataSourceGroup, results[0].Source)
	assert.Equal(t, "annotated-group", results[0].Group)
	require.NotNil(t, results[0].Metadata)
	assert.Equal(t,
		projectconfig.OverlayUpstreamStatusInapplicable,
		results[0].Metadata.UpstreamStatus,
	)
}

// seedComponentWithGroups adds a component with one annotated overlay that belongs to two
// groups, one annotated and one bare.
func seedComponentWithGroups(t *testing.T, testEnv *testutils.TestEnv) {
	t.Helper()

	testEnv.Config.Components["pkg-a"] = projectconfig.ComponentConfig{
		Name: "pkg-a",
		Spec: projectconfig.SpecSource{Path: "/specs/pkg-a.spec"},
		Overlays: []projectconfig.ComponentOverlay{
			{
				Type:  projectconfig.ComponentOverlayAddSpecTag,
				Tag:   "Vendor",
				Value: "Microsoft",
				Metadata: &projectconfig.OverlayMetadata{
					Category: projectconfig.OverlayCategoryAZLBrandingPolicy,
				},
			},
		},
	}

	testEnv.Config.ComponentGroups["annotated-group"] = projectconfig.ComponentGroupConfig{
		Description: "Group with metadata",
		Components:  []string{"pkg-a"},
		Metadata: &projectconfig.OverlayMetadata{
			Category:       projectconfig.OverlayCategoryAZLDisableFlakyTests,
			UpstreamStatus: projectconfig.OverlayUpstreamStatusInapplicable,
		},
	}

	testEnv.Config.ComponentGroups["bare-group"] = projectconfig.ComponentGroupConfig{
		Description: "Group without metadata",
		Components:  []string{"pkg-a"},
	}

	// Mirror the reverse index the loader builds for explicit group membership.
	testEnv.Config.GroupsByComponent["pkg-a"] = []string{"annotated-group", "bare-group"}
}

func TestListMetadata_DefaultListsBothSources(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentWithGroups(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 2, "one overlay plus one annotated group (bare group excluded)")

	// Overlay sorts before groups.
	assert.Equal(t, component.MetadataSourceOverlay, results[0].Source)
	assert.Equal(t, 1, results[0].Index)
	assert.Empty(t, results[0].Group)
	assert.Equal(t, projectconfig.OverlayCategoryAZLBrandingPolicy, results[0].Category)

	// Only the annotated group appears; 'bare-group' has no metadata.
	assert.Equal(t, component.MetadataSourceGroup, results[1].Source)
	assert.Equal(t, "annotated-group", results[1].Group)
	assert.Equal(t, 0, results[1].Index)
	require.NotNil(t, results[1].Metadata)
	assert.Equal(t, projectconfig.OverlayCategoryAZLDisableFlakyTests, results[1].Category)
	assert.Equal(t,
		projectconfig.OverlayUpstreamStatusInapplicable,
		results[1].UpstreamStatus,
	)
}

func TestListMetadata_OverlaysOnly(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentWithGroups(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Overlays:        true,
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1, "only the overlay entry is listed")
	assert.Equal(t, component.MetadataSourceOverlay, results[0].Source)
}

func TestListMetadata_GroupsOnly(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentWithGroups(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Groups:          true,
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1, "only the annotated group entry is listed (bare group excluded)")

	assert.Equal(t, component.MetadataSourceGroup, results[0].Source)
	assert.Equal(t, "annotated-group", results[0].Group)
}

func TestListMetadata_BothSelectorsListBoth(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentWithGroups(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Overlays:        true,
		Groups:          true,
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 2, "both selectors set lists overlay and annotated group")
}
