// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/image"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewImageListCommand(t *testing.T) {
	cmd := image.NewImageListCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, "list [image-name-pattern...]", cmd.Use)
}

func TestListImages_NoConfig(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config = nil

	options := &image.ListImageOptions{}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestListImages_NoImages(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{}

	options := &image.ListImageOptions{}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestListImages_AllImages(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"image-a": {
			Name:        "image-a",
			Description: "Image A description",
			Definition: projectconfig.ImageDefinition{
				DefinitionType: projectconfig.ImageDefinitionTypeKiwi,
				Path:           "/path/to/image-a.kiwi",
			},
		},
		"image-b": {
			Name:        "image-b",
			Description: "Image B description",
			Definition: projectconfig.ImageDefinition{
				DefinitionType: projectconfig.ImageDefinitionTypeKiwi,
				Path:           "/path/to/image-b.kiwi",
			},
		},
	}

	options := &image.ListImageOptions{}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Results should be sorted alphabetically by name.
	assert.Equal(t, "image-a", results[0].Name)
	assert.Equal(t, "Image A description", results[0].Description)
	assert.Equal(t, "kiwi", results[0].Definition.Type)
	assert.Equal(t, "/path/to/image-a.kiwi", results[0].Definition.Path)

	assert.Equal(t, "image-b", results[1].Name)
	assert.Equal(t, "Image B description", results[1].Description)
}

func TestListImages_WithCapabilitiesAndTests(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"vm-base": {
			Name:        "vm-base",
			Description: "VM Base Image",
			Capabilities: projectconfig.ImageCapabilities{
				MachineBootable: lo.ToPtr(true),
				Systemd:         lo.ToPtr(true),
			},
			Tests: projectconfig.ImageTestsConfig{
				TestSuites: []projectconfig.TestSuiteRef{
					{Name: "smoke"},
					{Name: "integration"},
				},
			},
			Publish: projectconfig.ImagePublishConfig{
				Channels: []string{"registry-prod", "registry-staging"},
			},
		},
		"container-base": {
			Name:        "container-base",
			Description: "Container Base Image",
			Capabilities: projectconfig.ImageCapabilities{
				Container: lo.ToPtr(true),
			},
			Tests: projectconfig.ImageTestsConfig{
				TestSuites: []projectconfig.TestSuiteRef{
					{Name: "smoke"},
				},
			},
			Publish: projectconfig.ImagePublishConfig{
				Channels: []string{"registry-prod"},
			},
		},
		"minimal": {
			Name:        "minimal",
			Description: "Minimal image with no capabilities or tests",
		},
	}

	options := &image.ListImageOptions{}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Results sorted alphabetically.
	assert.Equal(t, "container-base", results[0].Name)
	assert.Equal(t, lo.ToPtr(true), results[0].Capabilities.Container)
	assert.Nil(t, results[0].Capabilities.MachineBootable)
	assert.Equal(t, "container", results[0].CapabilitiesSummary)
	assert.Equal(t, projectconfig.ImageTestsConfig{
		TestSuites: []projectconfig.TestSuiteRef{{Name: "smoke"}},
	}, results[0].Tests)
	assert.Equal(t, "smoke", results[0].TestsSummary)
	assert.Equal(t, projectconfig.ImagePublishConfig{
		Channels: []string{"registry-prod"},
	}, results[0].Publish)
	assert.Equal(t, "registry-prod", results[0].PublishSummary)

	assert.Equal(t, "minimal", results[1].Name)
	assert.Nil(t, results[1].Capabilities.MachineBootable)
	assert.Nil(t, results[1].Capabilities.Container)
	assert.Empty(t, results[1].CapabilitiesSummary)
	assert.Empty(t, results[1].Tests.TestSuites)
	assert.Empty(t, results[1].TestsSummary)
	assert.Empty(t, results[1].Publish.Channels)
	assert.Empty(t, results[1].PublishSummary)

	assert.Equal(t, "vm-base", results[2].Name)
	assert.Equal(t, lo.ToPtr(true), results[2].Capabilities.MachineBootable)
	assert.Equal(t, lo.ToPtr(true), results[2].Capabilities.Systemd)
	assert.Nil(t, results[2].Capabilities.Container)
	assert.Equal(t, "machine-bootable, systemd", results[2].CapabilitiesSummary)
	assert.Equal(t, projectconfig.ImageTestsConfig{
		TestSuites: []projectconfig.TestSuiteRef{{Name: "smoke"}, {Name: "integration"}},
	}, results[2].Tests)
	assert.Equal(t, "smoke, integration", results[2].TestsSummary)
	assert.Equal(t, projectconfig.ImagePublishConfig{
		Channels: []string{"registry-prod", "registry-staging"},
	}, results[2].Publish)
	assert.Equal(t, "registry-prod, registry-staging", results[2].PublishSummary)
}

func TestListImages_ExactMatch(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"image-a": {
			Name:        "image-a",
			Description: "Image A description",
		},
		"image-b": {
			Name:        "image-b",
			Description: "Image B description",
		},
		"image-c": {
			Name:        "image-c",
			Description: "Image C description",
		},
	}

	options := &image.ListImageOptions{
		ImageNamePatterns: []string{"image-b"},
	}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "image-b", results[0].Name)
}

func TestListImages_ExactMatchNoMatch(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"image-a": {Name: "image-a"},
		"image-b": {Name: "image-b"},
	}

	options := &image.ListImageOptions{
		ImageNamePatterns: []string{"non-existent"},
	}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestListImages_GlobPatternStar(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"container-base": {Name: "container-base"},
		"container-dev":  {Name: "container-dev"},
		"vm-base":        {Name: "vm-base"},
		"vm-dev":         {Name: "vm-dev"},
	}

	options := &image.ListImageOptions{
		ImageNamePatterns: []string{"container-*"},
	}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Results should be sorted alphabetically.
	assert.Equal(t, "container-base", results[0].Name)
	assert.Equal(t, "container-dev", results[1].Name)
}

func TestListImages_GlobPatternQuestion(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"image-1":  {Name: "image-1"},
		"image-2":  {Name: "image-2"},
		"image-10": {Name: "image-10"},
	}

	options := &image.ListImageOptions{
		ImageNamePatterns: []string{"image-?"},
	}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "image-1", results[0].Name)
	assert.Equal(t, "image-2", results[1].Name)
}

func TestListImages_GlobPatternBrackets(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"image-a": {Name: "image-a"},
		"image-b": {Name: "image-b"},
		"image-c": {Name: "image-c"},
		"image-d": {Name: "image-d"},
	}

	options := &image.ListImageOptions{
		ImageNamePatterns: []string{"image-[ac]"},
	}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "image-a", results[0].Name)
	assert.Equal(t, "image-c", results[1].Name)
}

func TestListImages_MultiplePatterns(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"container-base": {Name: "container-base"},
		"container-dev":  {Name: "container-dev"},
		"vm-base":        {Name: "vm-base"},
		"vm-dev":         {Name: "vm-dev"},
	}

	options := &image.ListImageOptions{
		ImageNamePatterns: []string{"container-base", "vm-*"},
	}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Results should be sorted alphabetically.
	assert.Equal(t, "container-base", results[0].Name)
	assert.Equal(t, "vm-base", results[1].Name)
	assert.Equal(t, "vm-dev", results[2].Name)
}

func TestListImages_OverlappingPatterns(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"image-a": {Name: "image-a"},
		"image-b": {Name: "image-b"},
	}

	// Both patterns match image-a, but it should only appear once.
	options := &image.ListImageOptions{
		ImageNamePatterns: []string{"image-a", "image-*"},
	}

	results, err := image.ListImages(testEnv.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "image-a", results[0].Name)
	assert.Equal(t, "image-b", results[1].Name)
}

func TestListImages_InvalidPattern(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"image-a": {Name: "image-a"},
	}

	// Invalid bracket pattern.
	options := &image.ListImageOptions{
		ImageNamePatterns: []string{"image-["},
	}

	_, err := image.ListImages(testEnv.Env, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matching pattern")
}
