// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewComponentMetadataCommand(t *testing.T) {
	cmd := component.NewComponentMetadataCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, "metadata", cmd.Use)
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
					Category: projectconfig.OverlayCategoryBackportDistGit,
					Commits:  []string{"https://src.fedoraproject.org/rpms/pkg-a/c/abc"},
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
					Category:     projectconfig.OverlayCategoryAZLBuild,
					PR:           "https://github.com/example/repo/pull/1",
					Upstreamable: lo.ToPtr(true),
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
	require.Len(t, results, 4)

	// Sorted by (component, source, index).
	assert.Equal(t, "pkg-a", results[0].Component)
	assert.Equal(t, component.MetadataSourceOverlay, results[0].Source)
	assert.Equal(t, 1, results[0].Index)
	assert.Equal(t, projectconfig.OverlayCategoryAZLBrandingPolicy, results[0].Category)

	assert.Equal(t, "pkg-a", results[1].Component)
	assert.Equal(t, 2, results[1].Index)
	assert.Equal(t, projectconfig.OverlayCategoryBackportDistGit, results[1].Category)
	require.NotNil(t, results[1].Metadata)
	assert.Equal(t, []string{"https://src.fedoraproject.org/rpms/pkg-a/c/abc"}, results[1].Metadata.Commits)

	assert.Equal(t, "pkg-a", results[2].Component)
	assert.Equal(t, 3, results[2].Index)
	assert.Nil(t, results[2].Metadata, "overlay without metadata reports nil")
	assert.Empty(t, results[2].Category)

	assert.Equal(t, "pkg-b", results[3].Component)
	assert.Equal(t, projectconfig.OverlayCategoryAZLBuild, results[3].Category)
}

func TestListMetadata_OnlyAnnotated(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		OnlyAnnotated:   true,
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 3, "unannotated search-replace overlay must be excluded")

	for _, info := range results {
		assert.NotNil(t, info.Metadata)
		assert.NotEmpty(t, info.Category)
	}
}

func TestListMetadata_FilterByCategory(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Category:        string(projectconfig.OverlayCategoryBackportDistGit),
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

func TestListMetadata_Upstreamable(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Upstreamable:    "true",
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1, "only the upstreamable overlay in pkg-b should be included")
	assert.Equal(t, "pkg-b", results[0].Component)
	require.NotNil(t, results[0].Upstreamable)
	assert.True(t, *results[0].Upstreamable)
	assert.Equal(t, projectconfig.OverlayCategoryAZLBuild, results[0].Category)
}

func TestListMetadata_UnknownUpstreamableRejected(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Upstreamable:    "maybe",
	}

	_, err := component.ListMetadata(testEnv.Env, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown upstreamable value")
}

func TestListMetadata_UpstreamableFalse(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentWithGroups(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Upstreamable:    "false",
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1, "only the group annotated as not-upstreamable should be included")
	assert.Equal(t, component.MetadataSourceGroup, results[0].Source)
	assert.Equal(t, "annotated-group", results[0].Group)
	require.NotNil(t, results[0].Upstreamable)
	assert.False(t, *results[0].Upstreamable)
}

func TestListMetadata_UpstreamableUnknown(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentWithGroups(t, testEnv)

	options := &component.MetadataOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Upstreamable:    "unknown",
	}

	results, err := component.ListMetadata(testEnv.Env, options)
	require.NoError(t, err)
	// 'unknown' matches both annotated entries that omit the field (the overlay) and
	// unannotated entries (the bare group). The 'false'-annotated group is excluded.
	require.Len(t, results, 2)

	for _, info := range results {
		assert.Nil(t, info.Upstreamable)
	}

	assert.Equal(t, component.MetadataSourceOverlay, results[0].Source)
	require.NotNil(t, results[0].Metadata, "annotated overlay without 'upstreamable' must still match 'unknown'")

	assert.Equal(t, component.MetadataSourceGroup, results[1].Source)
	assert.Equal(t, "bare-group", results[1].Group)
	assert.Nil(t, results[1].Metadata, "unannotated entry must also match 'unknown'")
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
			Category:     projectconfig.OverlayCategoryAZLTestDisablement,
			Upstreamable: lo.ToPtr(false),
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
	require.Len(t, results, 3, "one overlay plus two group memberships")

	// Overlay sorts before groups.
	assert.Equal(t, component.MetadataSourceOverlay, results[0].Source)
	assert.Equal(t, 1, results[0].Index)
	assert.Empty(t, results[0].Group)
	assert.Equal(t, projectconfig.OverlayCategoryAZLBrandingPolicy, results[0].Category)

	// Groups follow, ordered by group name.
	assert.Equal(t, component.MetadataSourceGroup, results[1].Source)
	assert.Equal(t, "annotated-group", results[1].Group)
	assert.Equal(t, 0, results[1].Index)
	require.NotNil(t, results[1].Metadata)
	assert.Equal(t, projectconfig.OverlayCategoryAZLTestDisablement, results[1].Category)
	require.NotNil(t, results[1].Upstreamable)
	assert.False(t, *results[1].Upstreamable)

	assert.Equal(t, component.MetadataSourceGroup, results[2].Source)
	assert.Equal(t, "bare-group", results[2].Group)
	assert.Nil(t, results[2].Metadata)
	assert.Nil(t, results[2].Upstreamable)
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
	require.Len(t, results, 2, "only the two group entries are listed")

	for _, info := range results {
		assert.Equal(t, component.MetadataSourceGroup, info.Source)
	}
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
	require.Len(t, results, 3, "both selectors set lists overlays and groups")
}
