// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validateTestCase is a generic test case for validatable types.
type validateTestCase struct {
	name          string
	validate      func() error
	errorExpected bool
	errorContains string
}

func runValidateTests(t *testing.T, testCases []validateTestCase) {
	t.Helper()

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.validate()

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

func TestCheckConfig_Validate(t *testing.T) {
	runValidateTests(t, []validateTestCase{
		{
			name:     "empty config is valid",
			validate: func() error { return (&projectconfig.CheckConfig{}).Validate() },
		},
		{
			name: "skip false without reason is valid",
			validate: func() error {
				return (&projectconfig.CheckConfig{Skip: false}).Validate()
			},
		},
		{
			name: "skip true with reason is valid",
			validate: func() error {
				return (&projectconfig.CheckConfig{
					Skip: true, SkipReason: "Tests require network access",
				}).Validate()
			},
		},
		{
			name: "skip true without reason is invalid",
			validate: func() error {
				return (&projectconfig.CheckConfig{Skip: true}).Validate()
			},
			errorExpected: true,
			errorContains: "skip_reason",
		},
	})
}

func TestComponentBuildFailureConfig_Validate(t *testing.T) {
	runValidateTests(t, []validateTestCase{
		{
			name:     "empty config is valid",
			validate: func() error { return (&projectconfig.ComponentBuildFailureConfig{}).Validate() },
		},
		{
			name: "expected false without reason is valid",
			validate: func() error {
				return (&projectconfig.ComponentBuildFailureConfig{Expected: false}).Validate()
			},
		},
		{
			name: "expected true with reason is valid",
			validate: func() error {
				return (&projectconfig.ComponentBuildFailureConfig{
					Expected: true, ExpectedReason: "Known upstream build failure tracked in issue #123",
				}).Validate()
			},
		},
		{
			name: "expected true without reason is invalid",
			validate: func() error {
				return (&projectconfig.ComponentBuildFailureConfig{Expected: true}).Validate()
			},
			errorExpected: true,
			errorContains: "expected-reason",
		},
	})
}

func TestComponentBuildConfig_Validate_FailureConfig(t *testing.T) {
	t.Run("expected failure without reason propagates error", func(t *testing.T) {
		config := projectconfig.ComponentBuildConfig{
			Failure: projectconfig.ComponentBuildFailureConfig{
				Expected: true,
			},
		}

		err := config.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected-reason")
	})

	t.Run("expected failure with reason is valid", func(t *testing.T) {
		config := projectconfig.ComponentBuildConfig{
			Failure: projectconfig.ComponentBuildFailureConfig{
				Expected:       true,
				ExpectedReason: "known issue",
			},
		}

		err := config.Validate()
		require.NoError(t, err)
	})
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

// TestComponentBuildConfig_HashInclude verifies the fingerprint override:
// EmitUpstreamProvenance is included only when true (so enabling it is the only
// thing that perturbs a component's hash), while all other fields are always
// included.
func TestComponentBuildConfig_HashInclude(t *testing.T) {
	disabled := projectconfig.ComponentBuildConfig{}
	enabled := projectconfig.ComponentBuildConfig{EmitUpstreamProvenance: true}

	include, err := disabled.HashInclude("EmitUpstreamProvenance", nil)
	require.NoError(t, err)
	assert.False(t, include, "a false flag is omitted so it does not perturb the fingerprint")

	include, err = enabled.HashInclude("EmitUpstreamProvenance", nil)
	require.NoError(t, err)
	assert.True(t, include, "a true flag is included so enabling it changes the fingerprint")

	include, err = disabled.HashInclude("With", nil)
	require.NoError(t, err)
	assert.True(t, include, "unrelated fields are always included")
}

// TestComponentBuildConfig_ProvenanceFingerprint confirms that enabling
// provenance changes the hashstructure fingerprint, so an opted-in component
// rebuilds while others are unaffected.
func TestComponentBuildConfig_ProvenanceFingerprint(t *testing.T) {
	hash := func(c projectconfig.ComponentBuildConfig) uint64 {
		h, err := hashstructure.Hash(c, hashstructure.FormatV2, &hashstructure.HashOptions{TagName: "fingerprint"})
		require.NoError(t, err)

		return h
	}

	disabled := projectconfig.ComponentBuildConfig{With: []string{"feature"}}
	enabled := disabled
	enabled.EmitUpstreamProvenance = true

	assert.NotEqual(t, hash(disabled), hash(enabled),
		"enabling provenance changes the fingerprint so the component rebuilds")
}
