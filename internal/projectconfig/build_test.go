// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckConfig_Validate(t *testing.T) {
	testCases := []struct {
		name          string
		config        projectconfig.CheckConfig
		errorExpected bool
		errorContains string
	}{
		{
			name:          "empty config is valid",
			config:        projectconfig.CheckConfig{},
			errorExpected: false,
		},
		{
			name: "skip false without reason is valid",
			config: projectconfig.CheckConfig{
				Skip: false,
			},
			errorExpected: false,
		},
		{
			name: "skip true with reason is valid",
			config: projectconfig.CheckConfig{
				Skip:       true,
				SkipReason: "Tests require network access",
			},
			errorExpected: false,
		},
		{
			name: "skip true without reason is invalid",
			config: projectconfig.CheckConfig{
				Skip: true,
			},
			errorExpected: true,
			errorContains: "skip_reason",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.config.Validate()

			if testCase.errorExpected {
				require.Error(t, err)

				if testCase.errorContains != "" {
					assert.Contains(t, err.Error(), testCase.errorContains)
				}

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestComponentBuildConfig_Validate(t *testing.T) {
	testCases := []struct {
		name          string
		config        projectconfig.ComponentBuildConfig
		errorExpected bool
		errorContains string
	}{
		{
			name:          "empty config is valid",
			config:        projectconfig.ComponentBuildConfig{},
			errorExpected: false,
		},
		{
			name: "config with with/without/defines is valid",
			config: projectconfig.ComponentBuildConfig{
				With:    []string{"feature1"},
				Without: []string{"feature2"},
				Defines: map[string]string{"key": "value"},
			},
			errorExpected: false,
		},
		{
			name: "config with valid check skip is valid",
			config: projectconfig.ComponentBuildConfig{
				Check: projectconfig.CheckConfig{
					Skip:       true,
					SkipReason: "Tests require network access",
				},
			},
			errorExpected: false,
		},
		{
			name: "config with invalid check skip propagates error",
			config: projectconfig.ComponentBuildConfig{
				Check: projectconfig.CheckConfig{
					Skip: true,
					// Missing SkipReason
				},
			},
			errorExpected: true,
			errorContains: "skip_reason",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.config.Validate()

			if testCase.errorExpected {
				require.Error(t, err)

				if testCase.errorContains != "" {
					assert.Contains(t, err.Error(), testCase.errorContains)
				}

				return
			}

			require.NoError(t, err)
		})
	}
}
