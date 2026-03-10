// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package rpm_test

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/buildenv/buildenv_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testMockConfigPath = "/mock/config"
	testSpecPath       = "/sources/component.spec"
	testSourceDirPath  = "/sources"
)

func TestNewSpecQuerier(t *testing.T) {
	tests := []struct {
		name         string
		buildOptions rpm.BuildOptions
	}{
		{
			name:         "default build options",
			buildOptions: rpm.BuildOptions{},
		},
		{
			name: "build options using with flags",
			buildOptions: rpm.BuildOptions{
				With: []string{"feature1", "feature2"},
			},
		},
		{
			name: "build options using without flags",
			buildOptions: rpm.BuildOptions{
				Without: []string{"feature3", "feature4"},
			},
		},
		{
			name: "build options using defines",
			buildOptions: rpm.BuildOptions{
				Defines: map[string]string{
					"macro1": "value1",
					"macro2": "value2",
				},
			},
		},
		{
			name: "build options using all options",
			buildOptions: rpm.BuildOptions{
				With:    []string{"feature1"},
				Without: []string{"feature2"},
				Defines: map[string]string{
					"macro1": "value1",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := testctx.NewCtx()

			buildEnv := buildenv_testutils.NewTestBuildEnv(ctx)
			querier := rpm.NewSpecQuerier(buildEnv, test.buildOptions)

			require.NotNil(t, querier)
		})
	}
}

func rpmspecOutputForInfo(specInfo *rpm.SpecInfo) string {
	lines := []string{
		"name=" + specInfo.Name,
		fmt.Sprintf("epoch=%d", specInfo.Version.Epoch()),
		"version=" + specInfo.Version.Version(),
		"release=" + specInfo.Version.Release(),
	}

	for _, file := range specInfo.RequiredFiles {
		lines = append(lines, "source="+file)
	}

	return strings.Join(lines, "\n") + "\n"
}

func TestQuerySpecSuccess(t *testing.T) {
	tests := []struct {
		name             string
		buildOptions     rpm.BuildOptions
		expectedSpecInfo *rpm.SpecInfo
	}{
		{
			name:         "basic spec info with epoch and files",
			buildOptions: rpm.BuildOptions{},
			expectedSpecInfo: &rpm.SpecInfo{
				Name:          "test-package",
				Version:       requireNewVersion(t, "1:2.3.4-5.azl3"),
				RequiredFiles: []string{"source1.tar.gz", "patch1.patch"},
			},
		},
		{
			name:         "spec info with (none) epoch",
			buildOptions: rpm.BuildOptions{},
			expectedSpecInfo: &rpm.SpecInfo{
				Name:          "test-package",
				Version:       requireNewVersion(t, "2.3.4-5.azl3"),
				RequiredFiles: []string{"source1.tar.gz"},
			},
		},
		{
			name:         "spec info with no files",
			buildOptions: rpm.BuildOptions{},
			expectedSpecInfo: &rpm.SpecInfo{
				Name:          "test-package",
				Version:       requireNewVersion(t, "2:1.0.0-1.azl3"),
				RequiredFiles: []string{},
			},
		},
		{
			name:         "spec info with empty file lines",
			buildOptions: rpm.BuildOptions{},
			expectedSpecInfo: &rpm.SpecInfo{
				Name:          "test-package",
				Version:       requireNewVersion(t, "1.2.3-4.azl3"),
				RequiredFiles: []string{"source1.tar.gz", "patch1.patch"},
			},
		},
		{
			name:         "spec info with trailing spaces",
			buildOptions: rpm.BuildOptions{},
			expectedSpecInfo: &rpm.SpecInfo{
				Name:          "test-package",
				Version:       requireNewVersion(t, "1:2.3.4-5.azl3"),
				RequiredFiles: []string{"source1.tar.gz", "patch1.patch"},
			},
		},
		{
			name: "spec info with build options",
			buildOptions: rpm.BuildOptions{
				With:    []string{"feature1"},
				Without: []string{"feature2"},
				Defines: map[string]string{
					"macro1": "value1",
				},
			},
			expectedSpecInfo: &rpm.SpecInfo{
				Name:          "test-package",
				Version:       requireNewVersion(t, "1:2.3.4-5.azl3"),
				RequiredFiles: []string{"source1.tar.gz"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := testctx.NewCtx()

			// Set up the mock filesystem
			require.NoError(t, ctx.FS().MkdirAll(filepath.Dir(testSpecPath), fileperms.PublicExecutable))
			require.NoError(t, fileutils.WriteFile(ctx.FS(), testSpecPath, []byte("test spec content"), fileperms.PublicFile))

			var capturedCmd *exec.Cmd

			ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				capturedCmd = cmd

				return rpmspecOutputForInfo(test.expectedSpecInfo), nil
			}

			buildEnv := buildenv_testutils.NewTestBuildEnv(ctx)
			querier := rpm.NewSpecQuerier(buildEnv, test.buildOptions)

			result, err := querier.QuerySpec(ctx, testSpecPath)

			// Verify command was executed
			require.NotNil(t, capturedCmd)
			assert.Equal(t, "mock", filepath.Base(capturedCmd.Path))

			found := false

			// Check that rpmspec is in the chroot command
			for _, arg := range capturedCmd.Args {
				if strings.Contains(arg, "rpmspec") {
					found = true

					break
				}
			}

			assert.True(t, found, "rpmspec command not found in mock arguments")

			// Verify result
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, test.expectedSpecInfo.Name, result.Name)
			assert.Equal(t, test.expectedSpecInfo.Version.String(), result.Version.String())
			assert.Equal(t, test.expectedSpecInfo.RequiredFiles, result.RequiredFiles)
		})
	}
}

func TestQuerySpecSuccessWithWarningsAndErrors(t *testing.T) {
	tests := []struct {
		name             string
		mockStdout       []string
		expectedSpecInfo *rpm.SpecInfo
	}{
		{
			name: "spec with warnings and errors mixed in",
			mockStdout: []string{
				"warning: bogus date in %changelog: Mon Nov 28 2011 Joe User <juser@example.com> - 1.0.0-1",
				"name=test-package",
				"epoch=1",
				"version=2.3.4",
				"release=5.azl3",
				"error: bad date in %changelog: Mon Nov 28 201 Joe User <juser@example.com> - 1.0.0-2",
				"source=source1.tar.gz",
				"warning: some other warning",
				"patch=patch1.patch",
			},
			expectedSpecInfo: &rpm.SpecInfo{
				Name:          "test-package",
				Version:       requireNewVersion(t, "1:2.3.4-5.azl3"),
				RequiredFiles: []string{"source1.tar.gz", "patch1.patch"},
			},
		},
		{
			name: "spec with only warnings at beginning",
			mockStdout: []string{
				"warning: line 1",
				"warning: line 2",
				"name=test-package",
				"epoch=0",
				"version=1.0.0",
				"release=1.azl3",
				"source=source.tar.gz",
			},
			expectedSpecInfo: &rpm.SpecInfo{
				Name:          "test-package",
				Version:       requireNewVersion(t, "1.0.0-1.azl3"),
				RequiredFiles: []string{"source.tar.gz"},
			},
		},
		{
			name: "spec with errors at end",
			mockStdout: []string{
				"name=test-package",
				"epoch=2",
				"version=3.0.0",
				"release=1.azl3",
				"source=source.tar.gz",
				"error: some error at end",
			},
			expectedSpecInfo: &rpm.SpecInfo{
				Name:          "test-package",
				Version:       requireNewVersion(t, "2:3.0.0-1.azl3"),
				RequiredFiles: []string{"source.tar.gz"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := testctx.NewCtx()

			// Set up the mock filesystem
			require.NoError(t, ctx.FS().MkdirAll(filepath.Dir(testSpecPath), fileperms.PublicExecutable))
			require.NoError(t, fileutils.WriteFile(ctx.FS(), testSpecPath, []byte("test spec content"), fileperms.PublicFile))

			ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				return strings.Join(test.mockStdout, "\n"), nil
			}

			buildEnv := buildenv_testutils.NewTestBuildEnv(ctx)
			querier := rpm.NewSpecQuerier(buildEnv, rpm.BuildOptions{})

			result, err := querier.QuerySpec(ctx, testSpecPath)

			// Verify result
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, test.expectedSpecInfo.Name, result.Name)
			assert.Equal(t, test.expectedSpecInfo.Version.String(), result.Version.String())
			assert.Equal(t, test.expectedSpecInfo.RequiredFiles, result.RequiredFiles)
		})
	}
}

func TestQuerySpecFailure(t *testing.T) {
	tests := []struct {
		name       string
		mockStdout string
		mockRunErr error
		setupFS    bool
	}{
		{
			name:       "command execution error",
			mockStdout: "",
			mockRunErr: errors.New("command failed"),
			setupFS:    true,
		},
		{
			name:       "empty output",
			mockStdout: "",
			mockRunErr: nil,
			setupFS:    true,
		},
		{
			name:       "only warnings and errors",
			mockStdout: "warning: some warning\nerror: some error",
			mockRunErr: nil,
			setupFS:    true,
		},
		{
			name:       "invalid output format - wrong number of fields",
			mockStdout: "test-package|1|2.3.4",
			mockRunErr: nil,
			setupFS:    true,
		},
		{
			name:       "invalid output format - too many fields",
			mockStdout: "test-package|1|2.3.4|5.azl3|extra",
			mockRunErr: nil,
			setupFS:    true,
		},
		{
			name:       "invalid version format",
			mockStdout: "test-package|invalid-epoch|2.3.4|5.azl3",
			mockRunErr: nil,
			setupFS:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := testctx.NewCtx()

			if test.setupFS {
				// Set up the mock filesystem
				require.NoError(t, ctx.FS().MkdirAll(filepath.Dir(testSpecPath), fileperms.PublicExecutable))
				require.NoError(t, fileutils.WriteFile(ctx.FS(), testSpecPath, []byte("test spec content"), fileperms.PublicFile))
			}

			ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				return test.mockStdout, test.mockRunErr
			}

			buildEnv := buildenv_testutils.NewTestBuildEnv(ctx)
			querier := rpm.NewSpecQuerier(buildEnv, rpm.BuildOptions{})

			result, err := querier.QuerySpec(ctx, testSpecPath)

			// Verify failure
			assert.Nil(t, result)
			require.Error(t, err)

			if test.mockRunErr != nil {
				assert.Contains(t, err.Error(), "failed to run rpmspec in isolated root")
			}
		})
	}
}

// Helper function to create versions for testing.
func requireNewVersion(t *testing.T, versionStr string) rpm.Version {
	t.Helper()

	version, err := rpm.NewVersion(versionStr)
	require.NoError(t, err)

	return *version
}
