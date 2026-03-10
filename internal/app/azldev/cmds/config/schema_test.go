// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config_test

import (
	"encoding/json"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/config"
	"github.com/stretchr/testify/require"
)

func TestGenerateAzlDevTOMLJSONSchema(t *testing.T) {
	// Generate the schema.
	schemaText, err := config.GenerateAzlDevTOMLJSONSchema()
	require.NoError(t, err)
	require.NotEmpty(t, schemaText)

	deserialized := make(map[string]any)

	// Make sure the schema is valid JSON.
	err = json.Unmarshal([]byte(schemaText), &deserialized)
	require.NoError(t, err)
}
