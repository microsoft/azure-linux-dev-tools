// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package containertest_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/containertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const referenceUri = "https://www.example.com"

func TestUidGid(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	results, err := containertest.RunCmdInContainer(
		t,
		containertest.NewContainerTestCollateral(t),
		[]string{"id", "-u"},
		containertest.NoTimeout,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, results.ExitCode)
	assert.Equal(t, strconv.Itoa(os.Getuid()), strings.TrimSpace(results.Stdout))
	assert.Empty(t, results.Stderr)

	results, err = containertest.RunCmdInContainer(
		t,
		containertest.NewContainerTestCollateral(t),
		[]string{"id", "-g"},
		containertest.NoTimeout,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, results.ExitCode)
	assert.Equal(t, strconv.Itoa(os.Getgid()), strings.TrimSpace(results.Stdout))
	assert.Empty(t, results.Stderr)
}

func TestTimeout(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Test the timeout for a command to run in a container. The command will sleep for 10 seconds, and the
	// timeout is set to 5 seconds.
	_, err := containertest.RunCmdInContainer(
		t,
		containertest.NewContainerTestCollateral(t),
		[]string{"bash", "-c", "sleep 10"},
		5*time.Second,
	)
	assert.ErrorIs(t, err, containertest.ErrTimeout)
}

// Test the scenario runner. Create some input files, then inside the container move them to the mounted
// workdir, and echo them out. The files should be in the collateral's workdir after the run.
func TestRunInContainer(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	const (
		file1Str        = "file1"
		file2Str        = "file2"
		expectedRetCode = 42
	)

	// The inputs
	testDir := t.TempDir()
	filePath1 := filepath.Join(testDir, file1Str)
	filePath2 := filepath.Join(testDir, file2Str)

	require.NoError(t, os.WriteFile(filePath1, []byte(file1Str), fileperms.PrivateFile))
	require.NoError(t, os.WriteFile(filePath2, []byte(file2Str), fileperms.PrivateFile))

	// Define the test collateral to give to the container. The azldev tool will be copied in automatically.
	collateral := containertest.NewContainerTestCollateral(t).
		WithExtraFiles(
			map[string]string{
				"/file1":                filePath1,
				"/some/other/dir/file2": filePath2,
			},
		)

	// Define the test command to run in the container. This could just be running a shell script as well.
	cmd := []string{
		"bash",
		"-c",
		fmt.Sprintf("cp /file1 . && cp /some/other/dir/file2 . && cat %s && cat %s >&2 && exit %d",
			file1Str,
			file2Str,
			expectedRetCode),
	}

	// Run the scenario test.
	results, err := containertest.RunCmdInContainer(
		t, collateral, cmd, containertest.NoTimeout)

	require.NoError(t, err)
	assert.Equal(t, expectedRetCode, results.ExitCode)
	assert.Equal(t, file1Str, results.Stdout)
	assert.Equal(t, file2Str, results.Stderr)

	// Make sure the files are in the workdir.
	workdirFile1 := filepath.Join(collateral.Workdir(), file1Str)
	workdirFile2 := filepath.Join(collateral.Workdir(), file2Str)

	require.FileExists(t, workdirFile1)
	require.FileExists(t, workdirFile2)
}

func TestNetworkOn(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	collateral := containertest.NewContainerTestCollateral(t).WithNetwork()

	// Networking can be unreliable, so set a timeout and retry a few times.
	const maxRetries = 10
	const timeout = 5 * time.Second
	gotGoodResult := false
	for range maxRetries {
		results, err := containertest.RunCmdInContainer(
			t, collateral, []string{"curl", referenceUri}, timeout)
		if err != nil {
			if errors.Is(err, containertest.ErrTimeout) {
				continue
			}
		}
		require.NoError(t, err)

		if results.ExitCode == 0 {
			gotGoodResult = true
			break
		}

		time.Sleep(timeout)
	}

	assert.Truef(t, gotGoodResult, "curl failed to reach %s", referenceUri)
}

func TestNetworkOff(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	collateral := containertest.NewContainerTestCollateral(t)

	// Ensure that the container is not able to reach the internet.
	curlCmd := []string{"curl", referenceUri}
	results, err := containertest.RunCmdInContainer(
		t, collateral, curlCmd, containertest.NoTimeout)
	require.NoError(t, err)

	assert.NotEqual(t, 0, results.ExitCode)
}

func TestEnv(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	collateral := containertest.NewContainerTestCollateral(t).WithEnv(
		map[string]string{
			"TEST_ENV_VAR": "test_value",
		},
	)

	results, err := containertest.RunCmdInContainer(
		t,
		collateral,
		[]string{"bash", "-c", "echo $TEST_ENV_VAR"},
		containertest.NoTimeout,
	)
	require.NoError(t, err)

	assert.Equal(t, 0, results.ExitCode)
	assert.Equal(t, "test_value\n", results.Stdout)
}
