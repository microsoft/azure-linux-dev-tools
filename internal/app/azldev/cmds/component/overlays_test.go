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

func TestNewComponentOverlaysCommand(t *testing.T) {
	cmd := component.NewComponentOverlaysCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, "overlays", cmd.Use)
}

// seedComponentsWithOverlays adds two components with a mix of annotated and
// unannotated overlays.
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
					Category:        projectconfig.OverlayCategoryAZLBuild,
					PR:              "https://github.com/example/repo/pull/1",
					Upstreamability: projectconfig.OverlayUpstreamabilityYes,
				},
			},
		},
	}
}

func TestListOverlays_AllComponents(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.OverlaysOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	}

	results, err := component.ListOverlays(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 4)

	// Sorted by (component, index).
	assert.Equal(t, "pkg-a", results[0].Component)
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

func TestListOverlays_OnlyAnnotated(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.OverlaysOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		OnlyAnnotated:   true,
	}

	results, err := component.ListOverlays(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 3, "unannotated search-replace overlay must be excluded")

	for _, info := range results {
		assert.NotNil(t, info.Metadata)
		assert.NotEmpty(t, info.Category)
	}
}

func TestListOverlays_FilterByCategory(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.OverlaysOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Category:        string(projectconfig.OverlayCategoryBackportDistGit),
	}

	results, err := component.ListOverlays(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "pkg-a", results[0].Component)
	assert.Equal(t, 2, results[0].Index)
}

func TestListOverlays_UnknownCategoryRejected(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.OverlaysOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Category:        "bogus",
	}

	_, err := component.ListOverlays(testEnv.Env, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown overlay category")
}

func TestListOverlays_Upstreamable(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.OverlaysOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Upstreamability: "yes",
	}

	results, err := component.ListOverlays(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1, "only the upstreamable overlay in pkg-b should be included")
	assert.Equal(t, "pkg-b", results[0].Component)
	assert.Equal(t, projectconfig.OverlayUpstreamabilityYes, results[0].Upstreamability)
	assert.Equal(t, projectconfig.OverlayCategoryAZLBuild, results[0].Category)
}

func TestListOverlays_UnknownUpstreamabilityRejected(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	seedComponentsWithOverlays(t, testEnv)

	options := &component.OverlaysOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Upstreamability: "maybe",
	}

	_, err := component.ListOverlays(testEnv.Env, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown upstreamability")
}
