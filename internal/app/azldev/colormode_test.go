// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestColorModeSet(t *testing.T) {
	var mode azldev.ColorMode

	require.NoError(t, mode.Set("always"))
	assert.Equal(t, azldev.ColorModeAlways, mode)

	require.NoError(t, mode.Set("auto"))
	assert.Equal(t, azldev.ColorModeAuto, mode)

	require.NoError(t, mode.Set("never"))
	assert.Equal(t, azldev.ColorModeNever, mode)

	require.Error(t, mode.Set("unsupported"))
	assert.Equal(t, azldev.ColorModeAuto, mode, "unsupported value should default to 'auto'")
}
