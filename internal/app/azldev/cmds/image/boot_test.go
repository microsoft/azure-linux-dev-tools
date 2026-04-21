// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/image"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/qemu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewImageBootCmd(t *testing.T) {
	cmd := image.NewImageBootCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "boot [IMAGE_NAME]", cmd.Use)
	assert.Contains(t, cmd.Short, "Boot")
}

func TestNewImageBootCmd_Flags(t *testing.T) {
	cmd := image.NewImageBootCmd()

	// Verify key flags exist.
	assert.NotNil(t, cmd.Flags().Lookup("image-path"))
	assert.NotNil(t, cmd.Flags().Lookup("format"))
	assert.NotNil(t, cmd.Flags().Lookup("test-user"))
	assert.NotNil(t, cmd.Flags().Lookup("test-password"))
	assert.NotNil(t, cmd.Flags().Lookup("test-password-file"))
	assert.NotNil(t, cmd.Flags().Lookup("authorized-public-key"))
	assert.NotNil(t, cmd.Flags().Lookup("rwdisk"))
	assert.NotNil(t, cmd.Flags().Lookup("secure-boot"))
	assert.NotNil(t, cmd.Flags().Lookup("ssh-port"))
	assert.NotNil(t, cmd.Flags().Lookup("cpus"))
	assert.NotNil(t, cmd.Flags().Lookup("memory"))
	assert.NotNil(t, cmd.Flags().Lookup("arch"))
	assert.NotNil(t, cmd.Flags().Lookup("iso"))
	assert.NotNil(t, cmd.Flags().Lookup("disk-size"))
}

func TestNewImageBootCmd_DiskSizeDefault(t *testing.T) {
	cmd := image.NewImageBootCmd()
	diskSizeFlag := cmd.Flags().Lookup("disk-size")
	require.NotNil(t, diskSizeFlag)
	assert.Equal(t, "10G", diskSizeFlag.DefValue)
}

func TestNewImageBootCmd_NoImageSourceErrors(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := image.NewImageBootCmd()
	cmd.SetArgs([]string{})

	err := cmd.ExecuteContext(testEnv.Env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "IMAGE_NAME")
	assert.Contains(t, err.Error(), "--image-path")
	assert.Contains(t, err.Error(), "--iso")
}

func TestNewImageBootCmd_DiskSizeWithoutEmptyDiskErrors(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := image.NewImageBootCmd()
	// '--disk-size' is only meaningful when '--iso' is used without a disk image.
	cmd.SetArgs([]string{"--image-path", "/tmp/test.qcow2", "--iso", "/tmp/live.iso", "--disk-size", "20G"})

	err := cmd.ExecuteContext(testEnv.Env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--disk-size")
}

func TestNewImageBootCmd_CommaInImagePathErrors(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := image.NewImageBootCmd()
	cmd.SetArgs([]string{"--image-path", "/tmp/weird,name.qcow2"})

	err := cmd.ExecuteContext(testEnv.Env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--image-path")
	assert.Contains(t, err.Error(), ",")
}

func TestNewImageBootCmd_CommaInISOPathErrors(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := image.NewImageBootCmd()
	cmd.SetArgs([]string{"--iso", "/tmp/weird,name.iso"})

	err := cmd.ExecuteContext(testEnv.Env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--iso")
	assert.Contains(t, err.Error(), ",")
}

func TestImageFormat_Set_InvalidFormat(t *testing.T) {
	var format image.ImageFormat

	err := format.Set("invalid-format")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported image format")
	assert.Contains(t, err.Error(), "invalid-format")
}

func TestImageFormat_Set_ValidFormats(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{input: "raw", expected: "raw"},
		{input: "qcow2", expected: "qcow2"},
		{input: "vhdx", expected: "vhdx"},
		{input: "vhd", expected: "vhd"},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			var format image.ImageFormat

			err := format.Set(test.input)
			require.NoError(t, err)
			assert.Equal(t, test.expected, format.String())
		})
	}
}

func TestImageFormat_Type(t *testing.T) {
	var format image.ImageFormat
	assert.Equal(t, "format", format.Type())
}

func TestQEMUArch_Set_InvalidArch(t *testing.T) {
	var arch qemu.Arch

	err := arch.Set("arm32")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported architecture")
}

func TestQEMUArch_Set_ValidArchitectures(t *testing.T) {
	validArchs := []string{"x86_64", "aarch64"}
	for _, archStr := range validArchs {
		t.Run(archStr, func(t *testing.T) {
			var arch qemu.Arch

			err := arch.Set(archStr)
			require.NoError(t, err)
			assert.Equal(t, archStr, arch.String())
		})
	}
}

func TestQEMUArch_Type(t *testing.T) {
	var arch qemu.Arch
	assert.Equal(t, "arch", arch.Type())
}

func TestResolveImageWithAvailableList_NoImages(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	// testEnv already has an empty Images map by default.

	_, err := image.ResolveImageByName(testEnv.Env, "some-image")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "no images defined")
}

func TestResolveImageWithAvailableList_EmptyName(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	_, err := image.ResolveImageByName(testEnv.Env, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "image name is required")
}

func TestResolveImageWithAvailableList_NotFound(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"image-a": {Name: "image-a"},
		"image-b": {Name: "image-b"},
	}

	_, err := image.ResolveImageByName(testEnv.Env, "nonexistent")
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

	cfg, err := image.ResolveImageByName(testEnv.Env, "my-image")
	require.NoError(t, err)
	assert.Equal(t, "my-image", cfg.Name)
	assert.Equal(t, "My test image", cfg.Description)
}

func TestSupportedImageFormats(t *testing.T) {
	formats := image.SupportedImageFormats()
	require.NotEmpty(t, formats)
	assert.Contains(t, formats, "raw")
	assert.Contains(t, formats, "qcow2")
	assert.Contains(t, formats, "vhdx")
	assert.Contains(t, formats, "vhd")
}

func TestInferImageFormat(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{name: "raw", path: "/path/to/image.raw", expected: "raw"},
		{name: "qcow2", path: "/path/to/image.qcow2", expected: "qcow2"},
		{name: "vhd", path: "/path/to/image.vhd", expected: "vhd"},
		{name: "vhdfixed", path: "/path/to/image.vhdfixed", expected: "vhd"},
		{name: "vhdx", path: "/path/to/image.vhdx", expected: "vhdx"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			format, err := image.InferImageFormat(test.path)
			require.NoError(t, err)
			assert.Equal(t, test.expected, format)
		})
	}
}

func TestInferImageFormat_NoExtension(t *testing.T) {
	_, err := image.InferImageFormat("/path/to/imagefile")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no file extension")
}

func TestInferImageFormat_UnsupportedExtension(t *testing.T) {
	_, err := image.InferImageFormat("/path/to/image.iso")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported image format")
	assert.Contains(t, err.Error(), "iso")
}

func TestSupportedArchitectures(t *testing.T) {
	archs := qemu.SupportedArchitectures()
	require.NotEmpty(t, archs)
	assert.Contains(t, archs, "x86_64")
	assert.Contains(t, archs, "aarch64")
}
