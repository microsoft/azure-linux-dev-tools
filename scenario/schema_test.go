// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/buildtestenv"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/stretchr/testify/require"
)

func TestCheckedInSchemasAreUpToDate(t *testing.T) {
	t.Parallel()

	// We expect the well-known `schemas` dir to be a sibling of the directory containing
	// this test file.
	checkedInSchemaPath := composeCheckedInSchemaPath(t)

	// See if we were requested to update snapshots.
	updateMode := os.Getenv(buildtestenv.TestingUpdateSnapshotsEnvVar) == buildtestenv.TestingUpdateSnapshotsEnvValue

	// Find and read the contents of the checked-in schema file. We'll compare it against the one we
	// generate to ensure they match.
	checkedInSchemaContents, err := readCheckedInSchema(t, checkedInSchemaPath, updateMode)
	require.NoError(t, err)

	// Generate the schema file using the azldev command.
	currentSchemaContents, err := generateCurrentSchema(t)
	require.NoError(t, err)

	if updateMode {
		if checkedInSchemaContents != currentSchemaContents {
			t.Logf("Checked-in schema does not match; running in update mode, writing new schema to %q", checkedInSchemaPath)

			err := writeSchema(t, checkedInSchemaPath, currentSchemaContents)
			require.NoError(t, err, "failed to write updated schema to %q", checkedInSchemaPath)
		}
	} else {
		require.Equal(t, checkedInSchemaContents, currentSchemaContents,
			"Checked-in schema does not match the generated schema. Please run `mage scenarioUpdate` to update it.")
	}
}

func composeCheckedInSchemaPath(t *testing.T) string {
	t.Helper()

	// The most reliable way to find the schema location is to first figure out where *this* test file
	// is located, and then work back to it.
	_, ourFilename, _, _ := runtime.Caller(0)

	// Find the root directory of the azldev code project.
	scenarioTestDir := filepath.Dir(ourFilename)
	azldevRootDir := filepath.Dir(scenarioTestDir)

	// Basic check to make sure we really do have the right directory.
	require.FileExists(t, filepath.Join(azldevRootDir, "CONTRIBUTING.md"))

	// Now compose the path to the main schema file. Note that it may not exist--and that's okay if we're
	// in update mode.
	path := filepath.Join(azldevRootDir, "schemas", "azldev.schema.json")

	// Don't check for existence yet, but get a clean, absolute path for clearer error messages.
	absPath, err := filepath.Abs(path)
	require.NoError(t, err, "failed to get absolute path for %q", path)

	return absPath
}

func readCheckedInSchema(t *testing.T, checkedInSchemaPath string, updateMode bool) (string, error) {
	t.Helper()

	checkedInSchemaFile, err := os.Open(checkedInSchemaPath)

	// If we're updating, then treat non-existence as a non-error (and as if the file existed but was empty).
	if updateMode && errors.Is(err, os.ErrNotExist) {
		return "", nil
	}

	require.NoError(t, err, "failed to open checked-in schema file at %q", checkedInSchemaPath)

	defer checkedInSchemaFile.Close()

	checkedInSchemaBytes, err := io.ReadAll(checkedInSchemaFile)
	require.NoError(t, err, "failed to read checked-in schema file at %q", checkedInSchemaPath)

	return string(checkedInSchemaBytes), nil
}

func generateCurrentSchema(t *testing.T) (string, error) {
	t.Helper()

	test := cmdtest.NewScenarioTest("config", "generate-schema").Locally()

	// Run and make sure it exits with 0.
	results, err := test.Run(t)
	require.NoError(t, err)
	require.Zero(t, results.ExitCode)

	return results.Stdout, nil
}

func writeSchema(t *testing.T, checkedInSchemaPath, contents string) error {
	// Make sure the containing directory exists.
	err := os.MkdirAll(filepath.Dir(checkedInSchemaPath), fileperms.PrivateDir)
	require.NoError(t, err, "failed to ensure dir existence for %q", checkedInSchemaPath)

	err = os.WriteFile(checkedInSchemaPath, []byte(contents), fileperms.PrivateFile)
	require.NoError(t, err, "failed to write updated schema to %q", checkedInSchemaPath)

	return nil
}
