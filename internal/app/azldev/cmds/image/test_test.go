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
	assert.Equal(t, "test IMAGE_NAME", cmd.Use)
	assert.Contains(t, cmd.Short, "test")
}

func TestNewImageTestCmd_Flags(t *testing.T) {
	cmd := image.NewImageTestCmd()

	assert.NotNil(t, cmd.Flags().Lookup("test-suite"))
	assert.NotNil(t, cmd.Flags().Lookup("image-path"))
	assert.NotNil(t, cmd.Flags().Lookup("junit-xml"))
}
