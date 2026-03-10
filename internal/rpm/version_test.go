// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package rpm_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewVersionSuccess(t *testing.T) {
	tests := []struct {
		name       string
		versionStr string
	}{
		{
			name:       "valid version with release",
			versionStr: "1.2.3-4.azl3",
		},
		{
			name:       "valid version with epoch",
			versionStr: "1:1.2.3-4.azl3",
		},
		{
			name:       "valid zero version",
			versionStr: "0",
		},
		{
			name:       "version only",
			versionStr: "1.2.3",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testVersion, err := rpm.NewVersion(test.versionStr)

			require.NoError(t, err)
			assert.NotNil(t, testVersion)
			assert.Equal(t, test.versionStr, testVersion.String())
		})
	}
}

func TestNewVersionFailure(t *testing.T) {
	tests := []struct {
		name       string
		versionStr string
	}{
		{
			name:       "empty version",
			versionStr: "",
		},
		{
			name:       "version with slash character",
			versionStr: "1/2",
		},
		{
			name:       "version with space character",
			versionStr: "1.2.3 4",
		},
		{
			name:       "version with backslash character",
			versionStr: "1.2.3\\4",
		},
		{
			name:       "version with exclamation mark",
			versionStr: "1.2.3!4",
		},
		{
			name:       "version with @ symbol",
			versionStr: "1.2.3@4",
		},
		{
			name:       "version with multiple hyphens",
			versionStr: "1.2.3-4-5",
		},
		{
			name:       "version with multiple colons",
			versionStr: "1:2:3.4-5",
		},
		{
			name:       "version with non-numeric epoch",
			versionStr: "abc:1.2.3-4",
		},
		{
			name:       "version with negative epoch",
			versionStr: "-1:1.2.3-4",
		},
		{
			name:       "version with empty epoch",
			versionStr: ":1.2.3-4",
		},
		{
			name:       "release only",
			versionStr: "-4.azl3",
		},
		{
			name:       "epoch only",
			versionStr: "1:",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, err := rpm.NewVersion(test.versionStr)

			assert.Nil(t, version)
			assert.Error(t, err)
		})
	}
}

func TestNewVersionFromEVRSuccess(t *testing.T) {
	tests := []struct {
		name           string
		epoch          string
		version        string
		release        string
		expectedString string
		expectedEpoch  int
	}{
		{
			name:           "with empty epoch",
			epoch:          "",
			version:        "1.2.3",
			release:        "4.azl3",
			expectedString: "1.2.3-4.azl3",
			expectedEpoch:  0,
		},
		{
			name:           "with all components",
			epoch:          "1",
			version:        "2.3.4",
			release:        "5.azl3",
			expectedString: "1:2.3.4-5.azl3",
			expectedEpoch:  1,
		},
		{
			name:           "with zero epoch",
			epoch:          "0",
			version:        "1.0.0",
			release:        "1.azl3",
			expectedString: "1.0.0-1.azl3",
			expectedEpoch:  0,
		},
		{
			name:           "with large epoch",
			epoch:          "999",
			version:        "1.2.3",
			release:        "4.azl3",
			expectedString: "999:1.2.3-4.azl3",
			expectedEpoch:  999,
		},
		{
			name:           "with complex version",
			epoch:          "1",
			version:        "1.2.3.beta1",
			release:        "rc1.azl3",
			expectedString: "1:1.2.3.beta1-rc1.azl3",
			expectedEpoch:  1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, err := rpm.NewVersionFromEVR(test.epoch, test.version, test.release)

			require.NoError(t, err)
			assert.NotNil(t, version)
			assert.Equal(t, test.expectedString, version.String())
			assert.Equal(t, test.expectedEpoch, version.Epoch())
			assert.Equal(t, test.version, version.Version())
			assert.Equal(t, test.release, version.Release())
		})
	}
}

func TestNewVersionFromEVRFailure(t *testing.T) {
	tests := []struct {
		name    string
		epoch   string
		version string
		release string
	}{
		{
			name:    "empty version",
			epoch:   "1",
			version: "",
			release: "4.azl3",
		},
		{
			name:    "empty release",
			epoch:   "1",
			version: "1.2.3",
			release: "",
		},
		{
			name:    "non-numeric epoch",
			epoch:   "abc",
			version: "1.2.3",
			release: "4.azl3",
		},
		{
			name:    "negative epoch",
			epoch:   "-1",
			version: "1.2.3",
			release: "4.azl3",
		},
		{
			name:    "version with invalid character",
			epoch:   "1",
			version: "1.2.3!",
			release: "4.azl3",
		},
		{
			name:    "release with invalid character",
			epoch:   "1",
			version: "1.2.3",
			release: "4.azl3@",
		},
		{
			name:    "version with space",
			epoch:   "1",
			version: "1.2.3 4",
			release: "5.azl3",
		},
		{
			name:    "release with slash",
			epoch:   "1",
			version: "1.2.3",
			release: "4/azl3",
		},
		{
			name:    "epoch with colon",
			epoch:   "1:2",
			version: "1.2.3",
			release: "4.azl3",
		},
		{
			name:    "version with hyphen",
			epoch:   "1",
			version: "1.2.3-alpha",
			release: "4.azl3",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, err := rpm.NewVersionFromEVR(test.epoch, test.version, test.release)

			assert.Nil(t, version)
			assert.Error(t, err)
		})
	}
}

func TestVersionCompare(t *testing.T) {
	tests := []struct {
		name     string
		version1 string
		version2 string
		expected int
	}{
		{
			name:     "equal versions",
			version1: "1.2.3-4.azl3",
			version2: "1.2.3-4.azl3",
			expected: 0,
		},
		{
			name:     "first version greater than second - version part",
			version1: "1.2.4-4.azl3",
			version2: "1.2.3-4.azl3",
			expected: 1,
		},
		{
			name:     "first version less than second - version part",
			version1: "1.2.3-4.azl3",
			version2: "1.2.4-4.azl3",
			expected: -1,
		},
		{
			name:     "first version greater than second - release part",
			version1: "1.2.3-5.azl3",
			version2: "1.2.3-4.azl3",
			expected: 1,
		},
		{
			name:     "first version less than second - release part",
			version1: "1.2.3-4.azl3",
			version2: "1.2.3-5.azl3",
			expected: -1,
		},
		{
			name:     "first version greater than second - epoch part",
			version1: "2:1.2.3-4.azl3",
			version2: "1:1.2.3-4.azl3",
			expected: 1,
		},
		{
			name:     "first version less than second - epoch part",
			version1: "1:1.2.3-4.azl3",
			version2: "2:1.2.3-4.azl3",
			expected: -1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			firstVersion, err := rpm.NewVersion(test.version1)
			require.NoError(t, err)

			secondVersion, err := rpm.NewVersion(test.version2)
			require.NoError(t, err)

			result := firstVersion.Compare(secondVersion)
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestVersionGreaterLesserThan(t *testing.T) {
	tests := []struct {
		name     string
		version1 string
		version2 string
		greater  bool
	}{
		{
			name:     "first version greater than second - version part",
			version1: "1.2.4-4.azl3",
			version2: "1.2.3-4.azl3",
			greater:  true,
		},
		{
			name:     "first version less than second - version part",
			version1: "1.2.3-4.azl3",
			version2: "1.2.4-4.azl3",
			greater:  false,
		},
		{
			name:     "first version greater than second - release part",
			version1: "1.2.3-5.azl3",
			version2: "1.2.3-4.azl3",
			greater:  true,
		},
		{
			name:     "first version greater than second - epoch part",
			version1: "2:1.2.3-4.azl3",
			version2: "1:1.2.3-4.azl3",
			greater:  true,
		},
	}

	for _, test := range tests {
		t.Run("[GreaterThan]"+test.name, func(t *testing.T) {
			newerVersion, err := rpm.NewVersion(test.version1)
			require.NoError(t, err)

			earlierVersion, err := rpm.NewVersion(test.version2)
			require.NoError(t, err)

			result := newerVersion.GreaterThan(earlierVersion)
			assert.Equal(t, test.greater, result)
		})
	}

	for _, test := range tests {
		t.Run("[LesserThan]"+test.name, func(t *testing.T) {
			newerVersion, err := rpm.NewVersion(test.version1)
			require.NoError(t, err)

			earlierVersion, err := rpm.NewVersion(test.version2)
			require.NoError(t, err)

			result := newerVersion.LessThan(earlierVersion)
			assert.Equal(t, !test.greater, result)
		})
	}
}

func TestVersionEqualVersions(t *testing.T) {
	version, err := rpm.NewVersion("1.2.3-4.azl3")
	require.NoError(t, err)

	t.Run("[GreaterThan]", func(t *testing.T) {
		result := version.GreaterThan(version)
		assert.False(t, result)
	})

	t.Run("[LessThan]", func(t *testing.T) {
		result := version.LessThan(version)
		assert.False(t, result)
	})
}

func TestVersionEqual(t *testing.T) {
	tests := []struct {
		name     string
		version1 string
		version2 string
		expected bool
	}{
		{
			name:     "equal versions",
			version1: "1.2.3-4.azl3",
			version2: "1.2.3-4.azl3",
			expected: true,
		},
		{
			name:     "different versions - version part",
			version1: "1.2.4-4.azl3",
			version2: "1.2.3-4.azl3",
			expected: false,
		},
		{
			name:     "different versions - release part",
			version1: "1.2.3-5.azl3",
			version2: "1.2.3-4.azl3",
			expected: false,
		},
		{
			name:     "different versions - epoch part",
			version1: "2:1.2.3-4.azl3",
			version2: "1:1.2.3-4.azl3",
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			firstVersion, err := rpm.NewVersion(test.version1)
			require.NoError(t, err)

			secondVersion, err := rpm.NewVersion(test.version2)
			require.NoError(t, err)

			result := firstVersion.Equal(secondVersion)
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestVersionParts(t *testing.T) {
	tests := []struct {
		name            string
		versionStr      string
		expectedVersion string
		expectedRelease string
		expectedEpoch   int
	}{
		{
			name:            "version and release",
			versionStr:      "1.2.3-4.azl3",
			expectedVersion: "1.2.3",
			expectedRelease: "4.azl3",
			expectedEpoch:   0,
		},
		{
			name:            "version, release, and epoch",
			versionStr:      "1:1.2.3-4.azl3",
			expectedVersion: "1.2.3",
			expectedRelease: "4.azl3",
			expectedEpoch:   1,
		},
		{
			name:            "version only",
			versionStr:      "1.2.3",
			expectedVersion: "1.2.3",
			expectedRelease: "",
			expectedEpoch:   0,
		},
		{
			name:            "epoch and version only",
			versionStr:      "1:1.2.3",
			expectedVersion: "1.2.3",
			expectedRelease: "",
			expectedEpoch:   1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testVersion, err := rpm.NewVersion(test.versionStr)
			require.NoError(t, err)

			assert.Equal(t, test.expectedVersion, testVersion.Version())
			assert.Equal(t, test.expectedRelease, testVersion.Release())
			assert.Equal(t, test.expectedEpoch, testVersion.Epoch())
		})
	}
}

func TestVersionString(t *testing.T) {
	tests := []struct {
		name           string
		versionStr     string
		expectedString string
	}{
		{
			name:           "version with release",
			versionStr:     "1.2.3-4.azl3",
			expectedString: "1.2.3-4.azl3",
		},
		{
			name:           "version with epoch",
			versionStr:     "1:1.2.3-4.azl3",
			expectedString: "1:1.2.3-4.azl3",
		},
		{
			name:           "version with epoch and release",
			versionStr:     "1:1.2.3-4.azl3",
			expectedString: "1:1.2.3-4.azl3",
		},
		{
			name:           "version only",
			versionStr:     "1.2.3",
			expectedString: "1.2.3",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testVersion, err := rpm.NewVersion(test.versionStr)
			require.NoError(t, err)

			assert.Equal(t, test.expectedString, testVersion.String())
		})
	}
}

func TestVersionSerializeToJSON(t *testing.T) {
	testVersion, err := rpm.NewVersion("1.2.3-4.azl3")
	require.NoError(t, err)

	// Serialize to JSON
	jsonBytes, err := json.Marshal(testVersion)
	require.NoError(t, err)

	decoder := json.NewDecoder(bytes.NewReader(jsonBytes))
	decoder.UseNumber()

	var result map[string]interface{}

	// Deserialize the JSON.
	err = decoder.Decode(&result)
	require.NoError(t, err)

	// Check for expected results.
	expected := map[string]interface{}{
		"Epoch":   json.Number("0"),
		"Version": "1.2.3",
		"Release": "4.azl3",
	}
	assert.Equal(t, expected, result)
}
