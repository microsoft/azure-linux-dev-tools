// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewImageTestCmd(t *testing.T) {
	cmd := image.NewImageTestCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "test", cmd.Use)
	assert.Contains(t, cmd.Short, "test")
}

func TestNewImageTestCmd_Flags(t *testing.T) {
	cmd := image.NewImageTestCmd()

	assert.NotNil(t, cmd.Flags().Lookup("image-path"))
	assert.NotNil(t, cmd.Flags().Lookup("test-runner"))
	assert.NotNil(t, cmd.Flags().Lookup("runbook-path"))
	assert.NotNil(t, cmd.Flags().Lookup("admin-private-key-path"))
}

func TestCheckTestRunner_UnsupportedRunner(t *testing.T) {
	err := image.CheckTestRunner("unsupported-runner")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
	assert.Contains(t, err.Error(), "unsupported-runner")
	assert.Contains(t, err.Error(), "lisa")
}

func TestCheckTestRunner_Lisa(t *testing.T) {
	err := image.CheckTestRunner("lisa")
	require.NoError(t, err)
}

func TestCheckTestRunner_LisaCaseInsensitive(t *testing.T) {
	err := image.CheckTestRunner("LISA")
	require.NoError(t, err)
}

func TestResolveQcow2Image_UnsupportedFormats(t *testing.T) {
	tests := []struct {
		name      string
		imagePath string
		wantErr   string
	}{
		{
			name:      "raw format",
			imagePath: "/path/to/image.raw",
			wantErr:   "not supported for testing",
		},
		{
			name:      "vhdx format",
			imagePath: "/path/to/image.vhdx",
			wantErr:   "not supported for testing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := image.ResolveQcow2Image(nil, tc.imagePath)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestResolveQcow2Image_UnknownExtension(t *testing.T) {
	_, err := image.ResolveQcow2Image(nil, "/path/to/image.iso")
	require.Error(t, err)
	// InferImageFormat rejects iso before we even check format support.
	assert.Contains(t, err.Error(), "unsupported image format")
}
