// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test private functions
package rpm

import (
	"errors"
	"os/exec"
	"testing"

	rpmversion "github.com/knqyf263/go-rpm-version"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const (
	dummyPackageName = "dummy-package"
	validURL         = "https://example.com/repo"
)

func buildQuerierWithTestCmdFactory(t *testing.T) (*testctx.TestCmdFactory, RepoQuerier) {
	t.Helper()

	testCmdFactory := testctx.NewTestCmdFactory()
	testCmdFactory.RegisterCommandInSearchPath("repoquery")

	querier, err := NewRQRepoQuerier(testCmdFactory, WithBaseURLs(validURL))
	require.NoError(t, err)

	return testCmdFactory, querier
}

func TestNewRepoQuerierFailsForInvalidURL(t *testing.T) {
	ctrl := gomock.NewController(t)
	dummyCmdFactory := opctx_test.NewMockCmdFactory(ctrl)
	tests := []struct {
		name     string
		repoURLs []string
	}{
		{
			name:     "single invalid URL",
			repoURLs: []string{"invalid-url"},
		},
		{
			name:     "empty URL",
			repoURLs: []string{""},
		},
		{
			name:     "whitespace URL",
			repoURLs: []string{"   "},
		},
		{
			name:     "URL surrounded by whitespace",
			repoURLs: []string{"   https://example.com/repo   "},
		},
		{
			name:     "multiple invalid URLs",
			repoURLs: []string{"invalid-url-1", "invalid-url-2"},
		},
		{
			name:     "mixed valid and invalid URLs",
			repoURLs: []string{validURL, "invalid-url"},
		},
		{
			name: "mixed valid URL and empty URLs",
			repoURLs: []string{
				"",
				validURL,
				"   ",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRQRepoQuerier(dummyCmdFactory, WithBaseURLs(test.repoURLs...))
			assert.Error(t, err)
		})
	}
}

func TestNewRepoQuerierFailsForNilCmdFactory(t *testing.T) {
	querier, err := NewRQRepoQuerier(nil, WithBaseURLs(validURL))

	require.Error(t, err)
	assert.Nil(t, querier)
}

func TestNewRepoQuerierSucceedsForValidURL(t *testing.T) {
	ctrl := gomock.NewController(t)
	dummyCmdFactory := opctx_test.NewMockCmdFactory(ctrl)
	tests := []struct {
		name     string
		repoURLs []string
	}{
		{
			name:     "simple URL",
			repoURLs: []string{"https://example.com/repo"},
		},
		{
			name:     "URL with trailing slash",
			repoURLs: []string{"https://example.com/repo/"},
		},
		{
			name:     "URL with query parameters",
			repoURLs: []string{"https://example.com/repo?param=value"},
		},
		{
			name:     "multiple valid URLs",
			repoURLs: []string{"https://example.com/repo1", "https://example.com/repo2"},
		},
		{
			name:     "duplicate URLs",
			repoURLs: []string{validURL, validURL},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := NewRQRepoQuerier(dummyCmdFactory, WithBaseURLs(test.repoURLs...))
			if assert.NoError(t, err) {
				assert.NotNil(t, output)
			}
		})
	}
}

func TestGetLatestVersionSuccess(t *testing.T) {
	testCmdFactory, querier := buildQuerierWithTestCmdFactory(t)

	tests := []struct {
		name           string
		mockStdout     string
		expectedResult *Version
	}{
		{
			name:           "successful query E:X.Y.Z-A.DIST version",
			mockStdout:     "1:1.2.3-4.fc36",
			expectedResult: &Version{version: rpmversion.NewVersion("1:1.2.3-4.fc36")},
		},
		{
			name:           "successful query X.Y.Z-A.DIST version",
			mockStdout:     "1.2.3-4.fc36",
			expectedResult: &Version{version: rpmversion.NewVersion("1.2.3-4.fc36")},
		},
		{
			name:           "successful query X.Y.Z-A version",
			mockStdout:     "1.2.3-4",
			expectedResult: &Version{version: rpmversion.NewVersion("1.2.3-4")},
		},
		{
			name:           "successful query X-A version",
			mockStdout:     "1-4",
			expectedResult: &Version{version: rpmversion.NewVersion("1-4")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var capturedCmd *exec.Cmd

			testCmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				capturedCmd = cmd

				return test.mockStdout, nil
			}

			result, err := querier.GetLatestVersion(t.Context(), dummyPackageName)

			// Require the command was executed with the expected arguments
			require.NotNil(t, capturedCmd)
			assert.Contains(t, capturedCmd.Args, dummyPackageName, "Expected package name not found in command arguments")

			// Verify the command output
			if assert.NoError(t, err) {
				assert.Equal(t, test.expectedResult.Epoch(), result.Epoch())
				assert.Equal(t, test.expectedResult.Release(), result.Release())
				assert.Equal(t, test.expectedResult.Version(), result.Version())
				assert.Equal(t, test.expectedResult.String(), result.String())
			}
		})
	}
}

func TestGetLatestVersionFailure(t *testing.T) {
	testCmdFactory, querier := buildQuerierWithTestCmdFactory(t)

	tests := []struct {
		name        string
		packageName string
		mockStdout  string
		mockRunErr  error
	}{
		{
			name:        "command execution error",
			packageName: dummyPackageName,
			mockStdout:  "",
			mockRunErr:  errors.New("command failed"),
		},
		{
			name:        "empty output",
			packageName: dummyPackageName,
			mockStdout:  "",
			mockRunErr:  nil,
		},
		{
			name:        "invalid output format",
			packageName: dummyPackageName,
			mockStdout:  "invalid-version-format",
			mockRunErr:  nil,
		},
		{
			name:        "empty package name",
			packageName: "",
			mockStdout:  "",
			mockRunErr:  nil,
		},
		{
			name:        "only whitespace package name",
			packageName: "   ",
			mockStdout:  "",
			mockRunErr:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testCmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				return test.mockStdout, test.mockRunErr
			}

			result, err := querier.GetLatestVersion(t.Context(), test.packageName)

			assert.Nil(t, result)

			// Verify the command output
			if assert.Error(t, err) && test.mockRunErr != nil {
				assert.Contains(t, err.Error(), test.mockRunErr.Error())
			}
		})
	}
}

func TestGetRPMLocationSuccess(t *testing.T) {
	testCmdFactory, querier := buildQuerierWithTestCmdFactory(t)

	tests := []struct {
		name           string
		version        *Version
		mockStdout     string
		expectedOutput string
	}{
		{
			name:           "with version and release",
			version:        &Version{version: rpmversion.NewVersion("1.2.3-4.fc36")},
			mockStdout:     "https://example.com/repo/test-package-1.2.3-4.fc36.rpm",
			expectedOutput: "https://example.com/repo/test-package-1.2.3-4.fc36.rpm",
		},
		{
			name:           "with no version",
			version:        nil,
			mockStdout:     "https://example.com/repo/test-package-2.0.0-1.fc36.rpm",
			expectedOutput: "https://example.com/repo/test-package-2.0.0-1.fc36.rpm",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var capturedCmd *exec.Cmd

			testCmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				capturedCmd = cmd

				return test.mockStdout, nil
			}

			result, err := querier.GetRPMLocation(t.Context(), dummyPackageName, test.version)

			// Require the command was executed
			require.NotNil(t, capturedCmd)

			// Verify result
			if assert.NoError(t, err) {
				assert.Equal(t, test.expectedOutput, result)
			}
		})
	}
}

func TestGetRPMLocationFailure(t *testing.T) {
	testCmdFactory, querier := buildQuerierWithTestCmdFactory(t)

	tests := []struct {
		name        string
		packageName string
		mockStdout  string
		mockRunErr  error
	}{
		{
			name:        "command execution error",
			packageName: "error-package",
			mockStdout:  "",
			mockRunErr:  errors.New("command failed"),
		},
		{
			name:        "empty output",
			packageName: "nonexistent-package",
			mockStdout:  "",
			mockRunErr:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var capturedCmd *exec.Cmd

			testCmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				capturedCmd = cmd

				return test.mockStdout, test.mockRunErr
			}

			result, err := querier.GetRPMLocation(t.Context(), dummyPackageName, nil)

			// Require the command was executed
			require.NotNil(t, capturedCmd)

			// Verify result
			assert.Empty(t, result)

			if assert.Error(t, err) && test.mockRunErr != nil {
				assert.Contains(t, err.Error(), test.mockRunErr.Error())
			}
		})
	}
}

func TestBuildPackageArgSuccess(t *testing.T) {
	tests := []struct {
		name        string
		packageName string
		version     *Version
		expected    string
	}{
		{
			name:        "version only",
			packageName: "test-package",
			version:     &Version{version: rpmversion.NewVersion("1.2.3")},
			expected:    "test-package-1.2.3",
		},
		{
			name:        "version and release",
			packageName: "test-package",
			version:     &Version{version: rpmversion.NewVersion("1.2.3-4.fc36")},
			expected:    "test-package-1.2.3-4.fc36",
		},
		{
			name:        "epoch, version, and release",
			packageName: "test-package",
			version:     &Version{version: rpmversion.NewVersion("1:1.2.3-4.fc36")},
			expected:    "test-package-1:1.2.3-4.fc36",
		},
		{
			name:        "nil version",
			packageName: "test-package",
			version:     nil,
			expected:    "test-package",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := buildPackageWithVersionArg(test.packageName, test.version)
			require.NoError(t, err)
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestBuildPackageArgFailsWithEmptyPackageName(t *testing.T) {
	tests := []struct {
		name        string
		packageName string
		version     *Version
	}{
		{
			name:        "empty package name",
			packageName: "",
			version:     &Version{version: rpmversion.NewVersion("1.2.3")},
		},
		{
			name:        "whitespaces only package name",
			packageName: "   ",
			version:     &Version{version: rpmversion.NewVersion("1.2.3")},
		},
		{
			name:        "empty package name and no version",
			packageName: "",
			version:     nil,
		},
		{
			name:        "whitespaces only package name and no version",
			packageName: "   ",
			version:     nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildPackageWithVersionArg(test.packageName, test.version)
			assert.Error(t, err)
		})
	}
}
