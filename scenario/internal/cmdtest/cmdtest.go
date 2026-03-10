// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cmdtest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/containertest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/testhelpers"
	"github.com/stretchr/testify/require"
)

type commonTestParams struct {
	// Command-line args.
	Args []string
	// Additional environment vars.
	Env map[string]string
	// Timeout for the test.
	Timeout time.Duration
	// FilesToCopy to copy into the test environment: maps from test path (destination) to host path (source).
	FilesToCopy map[string]string
	// Files to write into the test environment: maps from test path (destination) to io.Reader to read contents.
	FilesToWrite map[string]io.Reader
	// Custom command to run in the container. The command must be in the PATH or provided via Files.
	CustomCmd string
}

type localTestParams struct {
	commonTestParams

	// Reader to use as stdin for the test command.
	Stdin io.Reader
}

type containerTestParams struct {
	commonTestParams

	// Enable networking in the container.
	Network bool

	// Run the container in privileged mode.
	Privileged bool

	// Extra mounts to add to the container.
	ExtraMounts []containertest.ContainerMount
}

// NewScenarioTest creates a new test with the specified arguments. It will default to invoking the azldev
// binary.
func NewScenarioTest(args ...string) *commonTestParams {
	params := &commonTestParams{
		Args:         args,
		Env:          map[string]string{},
		FilesToCopy:  map[string]string{},
		FilesToWrite: map[string]io.Reader{},
	}

	return params
}

// WithArgs replaces the command-line arguments for the test. This will replace any previously registered
// arguments.
func (params *commonTestParams) WithArgs(args ...string) *commonTestParams {
	params.Args = args

	return params
}

// WithEnv sets additional environment variables for the test.
func (params *commonTestParams) WithEnv(env map[string]string) *commonTestParams {
	params.Env = env

	return params
}

// WithTimeout sets a time limit for the test. This is independent of the timeout for the tests themselves.
func (params *commonTestParams) WithTimeout(timeout time.Duration) *commonTestParams {
	params.Timeout = timeout

	return params
}

// WithScript takes a string containing the contents of a test script to run and arranges for it to be
// run in the test environment as the primary command-under-test. Any previously registered commands
// or arguments will be replaced.
func (params *commonTestParams) WithScript(scriptContents io.Reader) *commonTestParams {
	const scriptName = "test_script.sh"

	params.AddFileContents(scriptName, scriptContents)
	params.WithCustomCmd("bash")
	params.WithArgs(scriptName)

	return params
}

// AddSingleFile takes a destination path (with respect to the test environment) and a path to a source file
// (relative to the host environment), and arranges for the file to be copied into the test environment. If the
// destination path is a relative path, it will be considered relative to the working directory that the test is run
// within in the test environment.
func (params *commonTestParams) AddSingleFile(dst, src string) *commonTestParams {
	params.FilesToCopy[dst] = src

	return params
}

// AddFileContents takes a destination path (with respect to the test environment) and a string holding the contents
// of that file, and arranges for the tests to write that file into the test environment. If the destination path
// is a relative path, it will be considered relative to the working directory that the test is run within in
// the test environment.
func (params *commonTestParams) AddFileContents(dst string, contents io.Reader) *commonTestParams {
	params.FilesToWrite[dst] = contents

	return params
}

// AddDirRecursive takes a destination path (with respect to the test environment) and a source directory
// (relative to the host environment), and arranges for the directory to be copied recursively into the test
// environment. If the destination path is a relative path, it will be considered relative to the working directory
// that the test is run within in the test environment.
func (params *commonTestParams) AddDirRecursive(t *testing.T, dst, src string) *commonTestParams {
	t.Helper()

	// Recursively walk just the files in the source directory.
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		require.NoError(t, err)

		if entry.IsDir() {
			return nil // Skip directories.
		}

		// Get the relative path to the source file.
		relPath, err := filepath.Rel(src, path)
		require.NoError(t, err)

		// Construct the destination path.
		destPath := filepath.Join(dst, relPath)

		params.AddSingleFile(destPath, path)

		return nil
	})

	require.NoError(t, err)

	return params
}

// AddFiles takes a map of dst -> src files to copy into the test environment, and adds them to the existing
// mappings configured in the parameters. Relative destination paths will be considered relative to the
// working directory that the test is run within in the test environment.
func (params *commonTestParams) AddFiles(files map[string]string) *commonTestParams {
	for dst, src := range files {
		params.AddSingleFile(dst, src)
	}

	return params
}

// WithCustomCmd sets a custom command to run in the container. The command must be in the PATH or provided via Files.
// This will override the default azldev.
func (params *commonTestParams) WithCustomCmd(cmd string) *commonTestParams {
	params.CustomCmd = cmd

	return params
}

// Locally takes a configured common test and prepares it to run locally (outside a container).
func (params *commonTestParams) Locally() *localTestParams {
	return &localTestParams{
		commonTestParams: *params,
	}
}

// InContainer takes a configured common test and converts it to run in a container.
func (params *commonTestParams) InContainer() *containerTestParams {
	return &containerTestParams{
		commonTestParams: *params,
		Network:          false,
	}
}

func (cParams *localTestParams) WithStdin(reader io.Reader) *localTestParams {
	cParams.Stdin = reader

	return cParams
}

// WithPrivilege sets the container to run in privileged mode. This is disabled by default.
func (cParams *containerTestParams) WithPrivilege() *containerTestParams {
	cParams.Privileged = true

	return cParams
}

// WithExtraMounts adds extra mounts to the container.
func (cParams *containerTestParams) WithExtraMounts(extraMounts []containertest.ContainerMount) *containerTestParams {
	cParams.ExtraMounts = extraMounts

	return cParams
}

// WithNetwork enables networking in the container. This is disabled by default.
func (cParams *containerTestParams) WithNetwork() *containerTestParams {
	cParams.Network = true

	return cParams
}

// Run runs the test as configured.
func (params *localTestParams) Run(t *testing.T) (results testhelpers.TestResults, err error) {
	t.Helper()

	var testBinary string

	if params.CustomCmd != "" {
		testBinary = params.CustomCmd
	} else {
		testBinary, err = testhelpers.FindTestBinary()
		if err != nil {
			return results, fmt.Errorf("failed to find test binary: %w", err)
		}
	}

	cmd := exec.CommandContext(t.Context(), testBinary, params.Args...)

	// Set the environment variables based on params.Env()
	for key, value := range params.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	// If provided, connect the provided reader to stdin of the command.
	if params.Stdin != nil {
		cmd.Stdin = params.Stdin
	}

	// Use a temporary working directory for the test.
	workdir := t.TempDir()
	cmd.Dir = workdir

	// Local tests don't support timeouts yet.
	if params.Timeout > 0 {
		return results, errors.New("timeout is not currently supported for local tests")
	}

	// Local tests don't support files yet.
	if len(params.FilesToCopy) > 0 {
		return results, errors.New("files are not currently supported for local tests")
	}

	// Local tests don't support writing files yet.
	if len(params.FilesToWrite) > 0 {
		return results, errors.New("writing files is not currently supported for local tests")
	}

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	output, cmdErr := cmd.Output()

	var exitErr *exec.ExitError

	if errors.As(cmdErr, &exitErr) {
		results.ExitCode = exitErr.ExitCode()
	} else if cmdErr != nil {
		return results, fmt.Errorf("failed to run test: %w", cmdErr)
	}

	results.Stdout = string(output)
	results.Stderr = stderr.String()
	results.Workdir = workdir

	return results, nil
}

// Run runs the test as configured.
func (cParams *containerTestParams) Run(t *testing.T) (results testhelpers.TestResults, err error) {
	t.Helper()

	testBinary, err := testhelpers.FindTestBinary()
	if err != nil {
		return results, fmt.Errorf("failed to find test binary: %w", err)
	}

	collateral := containertest.NewContainerTestCollateral(t).
		WithTestBinaryPath(testBinary).
		WithExtraFiles(cParams.FilesToCopy).
		WithExtraFileContents(cParams.FilesToWrite).
		WithEnv(cParams.Env)

	if cParams.Network {
		collateral = collateral.WithNetwork()
	}

	if cParams.Privileged {
		collateral = collateral.WithPrivilege()
	}

	if len(cParams.ExtraMounts) > 0 {
		collateral = collateral.WithExtraMounts(cParams.ExtraMounts)
	}

	var cmd []string

	if cParams.CustomCmd != "" {
		cmd = []string{cParams.CustomCmd}
	} else {
		cmd = []string{testhelpers.AzlDevBinaryName}
	}

	cmd = append(cmd, cParams.Args...)

	results, err = containertest.RunCmdInContainer(t, collateral, cmd, cParams.Timeout)
	if err != nil {
		return results, fmt.Errorf("failed to run test: %w", err)
	}

	return results, nil
}
