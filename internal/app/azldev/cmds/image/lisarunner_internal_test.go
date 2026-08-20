// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildLisaArgs(t *testing.T) {
	imageConfig := &projectconfig.ImageConfig{
		Capabilities: projectconfig.ImageCapabilities{
			MachineBootable: lo.ToPtr(true),
			Systemd:         lo.ToPtr(true),
		},
	}

	options := &ImageTestOptions{
		ImageName: "vm-base",
		ImagePath: "relative/image.qcow2",
	}

	args := buildLisaArgs(
		"/work/lisa/framework/abc/azldev-generated-suite.yml",
		[]string{
			"--image", "{image-path}",
			"--name", "{image-name}",
			"--caps", "{capabilities}",
			"-v",
		},
		imageConfig,
		options,
	)

	absImagePath, err := filepath.Abs("relative/image.qcow2")
	require.NoError(t, err)

	expected := []string{
		"-r", "/work/lisa/framework/abc/azldev-generated-suite.yml",
		"--image", absImagePath,
		"--name", "vm-base",
		"--caps", "machine-bootable,systemd",
		"-v",
	}

	assert.Equal(t, expected, args)
}

func TestBuildLisaArgs_NoExtraArgs(t *testing.T) {
	imageConfig := &projectconfig.ImageConfig{}
	options := &ImageTestOptions{ImageName: "vm-base", ImagePath: "/abs/image.qcow2"}

	args := buildLisaArgs("/runbook.yml", nil, imageConfig, options)

	assert.Equal(t, []string{"-r", "/runbook.yml"}, args)
}

func TestBuildLisaArgs_EmptyCapabilities(t *testing.T) {
	imageConfig := &projectconfig.ImageConfig{}
	options := &ImageTestOptions{ImageName: "vm-base", ImagePath: "/abs/image.qcow2"}

	args := buildLisaArgs("/runbook.yml", []string{"{capabilities}"}, imageConfig, options)

	assert.Equal(t, []string{"-r", "/runbook.yml", ""}, args)
}

func TestRequireQcow2Image(t *testing.T) {
	t.Run("accepts qcow2", func(t *testing.T) {
		assert.NoError(t, requireQcow2Image("/path/to/image.qcow2"))
	})

	t.Run("rejects raw", func(t *testing.T) {
		err := requireQcow2Image("/path/to/image.raw")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LISA requires a qcow2 image")
	})

	t.Run("rejects vhdx", func(t *testing.T) {
		err := requireQcow2Image("/path/to/image.vhdx")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LISA requires a qcow2 image")
	})

	t.Run("propagates inference error for unknown extension", func(t *testing.T) {
		err := requireQcow2Image("/path/to/image")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot infer image format")
	})
}
