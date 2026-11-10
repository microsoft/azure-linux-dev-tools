// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/require"
)

func TestImagePropertiesJSONPreservesNilProperties(t *testing.T) {
	image := &projectconfig.ImageConfig{}
	properties, err := image.PropertiesJSON()
	require.NoError(t, err)
	require.Equal(t, "{}", properties)
	require.Nil(t, image.Properties)
}
