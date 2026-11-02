// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package imagebuilder_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/imagebuilder"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveImageWithAvailableList_NoImages(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	// testEnv already has an empty Images map by default.

	_, err := imagebuilder.ResolveImageByName(testEnv.Env, "some-image")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "no images defined")
}

func TestResolveImageWithAvailableList_EmptyName(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	_, err := imagebuilder.ResolveImageByName(testEnv.Env, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "image name is required")
}

func TestResolveImageWithAvailableList_NotFound(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"image-a": {Name: "image-a"},
		"image-b": {Name: "image-b"},
	}

	_, err := imagebuilder.ResolveImageByName(testEnv.Env, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "image-a")
	assert.Contains(t, err.Error(), "image-b")
}

func TestResolveImageWithAvailableList_Found(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"my-image": {
			Name:        "my-image",
			Description: "My test image",
		},
	}

	cfg, err := imagebuilder.ResolveImageByName(testEnv.Env, "my-image")
	require.NoError(t, err)
	assert.Equal(t, "my-image", cfg.Name)
	assert.Equal(t, "My test image", cfg.Description)
}
