// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPrepareSourcesCmd(t *testing.T) {
	cmd := component.NewPrepareSourcesCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "prepare-sources", cmd.Use)

	forceFlag := cmd.Flags().Lookup("force")
	require.NotNil(t, forceFlag, "--force flag should be registered")
	assert.Equal(t, "false", forceFlag.DefValue)
	assert.Contains(t, forceFlag.Usage, "delete and recreate the output directory")
}

func TestPrepareSourcesCmd_NoMatch(t *testing.T) {
	const testComponentName = "test-component"

	testEnv := testutils.NewTestEnv(t)

	cmd := component.NewPrepareSourcesCmd()
	cmd.SetArgs([]string{testComponentName, "--output-dir", "/output/dir"})

	err := cmd.ExecuteContext(testEnv.Env)

	// We expect an error because we haven't set up any components.
	require.Error(t, err)
}

func TestCheckOutputDir(t *testing.T) {
	const (
		outputDir = "/test/output"
		staleFile = "/test/output/stale.txt"
	)

	tests := []struct {
		name             string
		force            bool
		setupDir         bool
		addFile          bool
		expectError      bool
		errorMsgContains []string
	}{
		{
			name:        "default with nonexistent dir succeeds",
			force:       false,
			setupDir:    false,
			addFile:     false,
			expectError: false,
		},
		{
			name:        "default with empty dir succeeds",
			force:       false,
			setupDir:    true,
			addFile:     false,
			expectError: false,
		},
		{
			name:             "default with non-empty dir returns actionable error",
			force:            false,
			setupDir:         true,
			addFile:          true,
			expectError:      true,
			errorMsgContains: []string{"--force", outputDir},
		},
		{
			name:        "force with nonexistent dir succeeds",
			force:       true,
			setupDir:    false,
			addFile:     false,
			expectError: false,
		},
		{
			name:        "force with empty dir succeeds",
			force:       true,
			setupDir:    true,
			addFile:     false,
			expectError: false,
		},
		{
			name:        "force with non-empty dir removes dir",
			force:       true,
			setupDir:    true,
			addFile:     true,
			expectError: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			testEnv := testutils.NewTestEnv(t)
			testFS := testEnv.TestFS

			if testCase.setupDir {
				require.NoError(t, testFS.MkdirAll(outputDir, fileperms.PublicDir))
			}

			if testCase.addFile {
				require.NoError(t, testFS.MkdirAll(outputDir, fileperms.PublicDir))

				f, err := testFS.Create(staleFile)
				require.NoError(t, err)
				require.NoError(t, f.Close())
			}

			options := &component.PrepareSourcesOptions{
				OutputDir: outputDir,
				Force:     testCase.force,
			}

			err := component.CheckOutputDir(testEnv.Env, options)

			if testCase.expectError {
				require.Error(t, err)

				for _, msg := range testCase.errorMsgContains {
					assert.Contains(t, err.Error(), msg)
				}
			} else {
				require.NoError(t, err)
			}

			// Verify force actually removed the directory.
			if testCase.force && testCase.addFile {
				exists, err := fileutils.Exists(testFS, outputDir)
				require.NoError(t, err)
				assert.False(t, exists, "output directory should be removed when --force is used")
			}
		})
	}
}
