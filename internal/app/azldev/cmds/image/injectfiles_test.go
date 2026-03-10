// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewImageInjectFilesCmd(t *testing.T) {
	cmd := image.NewImageInjectFilesCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "inject-files", cmd.Use)
}
