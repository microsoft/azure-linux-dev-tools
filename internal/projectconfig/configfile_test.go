// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectConfigFileValidation_EmptyFile(t *testing.T) {
	file := projectconfig.ConfigFile{}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_DefaultProjectInfo(t *testing.T) {
	file := projectconfig.ConfigFile{
		Project: &projectconfig.ProjectInfo{},
	}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_InvalidIncludePath(t *testing.T) {
	file := projectconfig.ConfigFile{
		Includes: []string{""},
	}
	assert.Error(t, file.Validate())
}

func TestProjectConfigFileValidation_ValidBuildCheckSkip(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				Build: projectconfig.ComponentBuildConfig{
					Check: projectconfig.CheckConfig{
						Skip:       true,
						SkipReason: "Tests require network access",
					},
				},
			},
		},
	}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_InvalidBuildCheckSkip(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				Build: projectconfig.ComponentBuildConfig{
					Check: projectconfig.CheckConfig{
						Skip: true,
						// Missing Reason
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reason")
	assert.Contains(t, err.Error(), "test-component")
}
