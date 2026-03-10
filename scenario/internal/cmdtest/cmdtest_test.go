// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

//nolint:testpackage // Intentionally testing internal structs
package cmdtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dummyFile(t *testing.T) string {
	t.Helper()

	tempDir := t.TempDir()

	tmpFile := filepath.Join(tempDir, "test.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("test content"), fileperms.PrivateFile))

	return tmpFile
}

func dummyDir(t *testing.T) string {
	t.Helper()

	tempDir := t.TempDir()

	subdir := filepath.Join(tempDir, "subdir")

	// Create the subdirectory and place a file in it.
	require.NoError(t, os.MkdirAll(subdir, fileperms.PrivateDir))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "nested.txt"), []byte("test content"), fileperms.PrivateFile))

	// DBG:RRO
	t.Logf("XXXXXX Created dummy directory at %s", subdir)

	return tempDir
}

func Test_localTestParamsRun(t *testing.T) {
	t.Parallel()

	type fields struct {
		Timeout time.Duration
		Files   map[string]string
	}

	tests := []struct {
		name       string
		fields     fields
		wantStdOut bool
		wantStdErr bool
		wantErr    bool
	}{
		{
			name: "Basic cmd",
			fields: fields{
				Timeout: 0,
				Files:   map[string]string{},
			},
			wantStdOut: true,
			wantStdErr: false,
			wantErr:    false,
		},
		{
			name: "Ensure files are unsupported",
			fields: fields{
				Timeout: 0,
				Files:   map[string]string{"test.txt": dummyFile(t)},
			},
			wantStdOut: false,
			wantStdErr: true,
			wantErr:    true,
		},
		{
			name: "Ensure timeout is unsupported",
			fields: fields{
				Timeout: time.Minute,
				Files:   map[string]string{},
			},
			wantStdOut: true,
			wantStdErr: false,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Create a default scenario test with the --version command
			params := *NewScenarioTest("--version")
			params.WithTimeout(test.fields.Timeout)
			params.AddFiles(test.fields.Files)

			gotResults, err := params.Locally().Run(t)

			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if err == nil {
				if test.wantStdOut {
					assert.NotEmpty(t, gotResults.Stdout)
				}

				if test.wantStdErr {
					assert.NotEmpty(t, gotResults.Stderr)
				}

				assert.NotEmpty(t, gotResults.Workdir)
				assert.DirExists(t, gotResults.Workdir, "Test workdir should exist")
			}
		})
	}
}

func Test_localTestParamsRunWithStdin(t *testing.T) {
	t.Parallel()

	const input = "test input"

	// Send some data into `cat` and make sure we get it back out.
	test := NewScenarioTest().WithCustomCmd("cat").Locally().WithStdin(strings.NewReader(input))

	results, err := test.Run(t)
	require.NoError(t, err)

	require.Equal(t, results.Stdout, input, "Expected stdout to match input")
}

func Test_containerTestParamsRun(t *testing.T) {
	t.Parallel()

	t.Run("Basic cmd", func(t *testing.T) {
		t.Parallel()

		// Create a scenario test that runs `ls` so we can see any files or directories that should have been copied.
		params := *NewScenarioTest().WithCustomCmd("whoami").InContainer()

		results, err := params.Run(t)
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(results.Stdout), "testuser", "Expected to run as testuser in the container")
	})

	t.Run("Add files", func(t *testing.T) {
		t.Parallel()

		// Create a scenario test that runs `ls` so we can see any files or directories
		// that should have been copied.
		params := *NewScenarioTest().WithCustomCmd("ls").WithArgs("-R").InContainer()

		params.AddFiles(map[string]string{"test.txt": dummyFile(t)})
		params.AddDirRecursive(t, "testdir", dummyDir(t))

		results, err := params.Run(t)
		require.NoError(t, err)

		// Create a map from the files we find.
		lines := strings.Split(results.Stdout, "\n")
		lineMap := lo.SliceToMap(lines, func(line string) (string, bool) {
			return strings.TrimSpace(line), true
		})

		// Basic existence checks for the items we copied.
		require.Contains(t, lineMap, "test.txt", "Expected test.txt to be listed in the output")
		require.Contains(t, lineMap, "testdir", "Expected testdir to be listed in the output")
	})
}

func Test_OverrideCommands(t *testing.T) {
	t.Parallel()

	const expectedContent = "some string"

	testParams := NewScenarioTest("-c", fmt.Sprintf("echo '%s' > test.txt", expectedContent)).
		WithCustomCmd("bash").
		InContainer()

	testResults, err := testParams.Run(t)
	require.NoError(t, err)

	// Now check the results.
	filePath := filepath.Join(testResults.Workdir, "test.txt")
	fileContent, err := os.ReadFile(filePath)
	require.NoError(t, err)

	// Convert and trim the content to remove any trailing newlines or spaces.
	actualContent := strings.TrimSpace(string(fileContent))
	assert.Equal(t, expectedContent, actualContent, "File content should match expected value")
}
