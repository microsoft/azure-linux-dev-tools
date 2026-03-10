// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package testhelpers

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/buildtestenv"
)

// Default binary name for the main AZL Dev CLI command.
const AzlDevBinaryName = "azldev"

// FindTestBinary finds the azldev binary to use for testing. It first checks if the testing tools have passed an
// environment variable with the path to the azldev binary. If not, it will look for the azldev binary in the out
// directory of the repo. If all else fails, it will look for the azldev binary in the $PATH.
func FindTestBinary() (string, error) {
	// Find the test binary.
	testBinaryPath := os.Getenv(buildtestenv.TestingAzldevBinPathEnvVar)
	if testBinaryPath == "" {
		var err error

		// Find default binary.
		testBinaryPath, err = findDefaultTestBinary()
		if err != nil {
			return "", fmt.Errorf("env var '%s' not set and couldn't find default binary: %w",
				buildtestenv.TestingAzldevBinPathEnvVar, err)
		}
	}

	// Require an absolute path because the caller's working directory isn't necessarily the
	// same as what we're running out of now.
	if !filepath.IsAbs(testBinaryPath) {
		return "", fmt.Errorf("test binary path (%s) '%s' is not absolute",
			buildtestenv.TestingAzldevBinPathEnvVar, testBinaryPath)
	}

	// Make sure we can find the binary.
	resolvedTestBinaryPath, err := exec.LookPath(testBinaryPath)
	if errors.Is(err, exec.ErrNotFound) {
		// If we didn't find our local copy, try looking at $PATH for any azldev binary.
		resolvedTestBinaryPath, err = exec.LookPath(AzlDevBinaryName)
	}

	if err != nil {
		return "", fmt.Errorf("failed to resolve test binary path '%s': %w", testBinaryPath, err)
	}

	return resolvedTestBinaryPath, nil
}

// FindTestDockerDirectory finds the directory containing the collateral files for creating the testing container.
func FindTestDockerDirectory() (string, error) {
	moduleRootPath, err := findModuleRoot()
	if err != nil {
		return "", fmt.Errorf("couldn't find go module root: %w", err)
	}

	dockerPath := filepath.Join(moduleRootPath, "scenario", "docker")

	absDockerPath, err := filepath.Abs(dockerPath)
	if err != nil {
		return "", fmt.Errorf("failed to compute absolute path for default docker directory '%s': %w", dockerPath, err)
	}

	return absDockerPath, nil
}

func findDefaultTestBinary() (string, error) {
	moduleRootPath, err := findModuleRoot()
	if err != nil {
		return "", fmt.Errorf("couldn't find go module root: %w", err)
	}

	binPath := filepath.Join(moduleRootPath, "out", "bin", AzlDevBinaryName)

	absBinPath, err := filepath.Abs(binPath)
	if err != nil {
		return "", fmt.Errorf("failed to compute absolute path for default test binary '%s': %w", binPath, err)
	}

	return absBinPath, nil
}

func findModuleRoot() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	candidatePath, err := filepath.Abs(workingDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path for working directory '%s': %w", workingDir, err)
	}

	// Walk up the directory tree until we find a go.mod file.
	for {
		if _, err := os.Stat(filepath.Join(candidatePath, "go.mod")); err == nil {
			return candidatePath, nil
		}

		if candidatePath == "/" {
			return "", fmt.Errorf("couldn't find go.mod file in dir tree containing '%s'", workingDir)
		}

		candidatePath = filepath.Dir(candidatePath)
	}
}
