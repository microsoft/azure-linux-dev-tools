// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package rpm_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	packagePath        = "/mft_kernel-4.30.0-11.azl3.src.rpm"
	txtFilePath        = "/test.txt"
	testDestinationDir = "test_rpm_extractor_output"
)

// Global in-memory filesystem storing the test files pre-cached
// in the initialization phase.
// This avoids the need for network access during tests.
//
//nolint:gochecknoglobals
var cacheFS = afero.NewMemMapFs()

// We set-up the test HTTP server to serve a real RPM file
// from packages.microsoft.com. This requires network access.
func TestMain(m *testing.M) {
	err := setUpTestFiles()
	if err != nil {
		panic("Failed to set up test server: " + err.Error())
	}

	os.Exit(m.Run())
}

// setUpTestFiles gets a real RPM file for testing.
// The RPM is retrieved from packages.microsoft.com and
// stored in an in-memory filesystem to avoid multiple network requests.
// We also create an in-memory, dummy txt file.
func setUpTestFiles() error {
	err := setUpRPMFile()
	if err != nil {
		return err
	}

	return setUpNonRPMFile()
}

func setUpNonRPMFile() error {
	data := []byte("This is a test file that is not an RPM.")

	return fileutils.WriteFile(cacheFS, txtFilePath, data, fileperms.PublicFile)
}

// setUpRPMFile retrieves a real RPM file from packages.microsoft.com
// and stores it in the in-memory filesystem.
//
// Issue #208: replace with an RPM file generated during the test initialization phase.
func setUpRPMFile() error {
	pmcPackageURL := "https://packages.microsoft.com/azurelinux/3.0/prod/base/srpms/Packages/m" + packagePath

	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		pmcPackageURL,
		nil)
	if err != nil {
		return err
	}

	pmcRPM, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}

	defer pmcRPM.Body.Close()

	data, err := io.ReadAll(pmcRPM.Body)
	if err != nil {
		return err
	}

	err = fileutils.WriteFile(cacheFS, packagePath, data, fileperms.PublicFile)
	if err != nil {
		return err
	}

	return nil
}

// getFileStream gets a real file stream from the test FS.
func getFileStream(t *testing.T, filePath string) io.ReadCloser {
	t.Helper()

	file, err := cacheFS.Open(filePath)
	require.NoError(t, err)

	return file
}

func TestNewRPMExtractor(t *testing.T) {
	t.Run("valid filesystem succeeds", func(t *testing.T) {
		extractor, err := rpm.NewRPMExtractorImpl(afero.NewMemMapFs())
		require.NoError(t, err)

		assert.NotNil(t, extractor)
	})

	t.Run("nil filesystem fails", func(t *testing.T) {
		extractor, err := rpm.NewRPMExtractorImpl(nil)
		require.Error(t, err)

		assert.Nil(t, extractor)
	})
}

func TestExtractSucceedsForValidRPM(t *testing.T) {
	expectedFiles := []string{
		"kernel-mft-4.30.0.tgz",
		"mft_kernel.spec",
	}

	// Using a local FS to avoid interference with other tests.
	localFS := afero.NewMemMapFs()

	testRPMStream := getFileStream(t, packagePath)
	defer testRPMStream.Close()

	extractor, err := rpm.NewRPMExtractorImpl(localFS)
	require.NoError(t, err)
	require.NotNil(t, extractor)

	err = extractor.Extract(testRPMStream, testDestinationDir)
	require.NoError(t, err)

	entries, err := afero.ReadDir(localFS, testDestinationDir)
	require.NoError(t, err)
	assert.Len(t, entries, len(expectedFiles))

	for _, file := range entries {
		assert.Contains(t, expectedFiles, file.Name())
	}
}

func TestExtractFailsForNonRPMFile(t *testing.T) {
	// Using a local FS to avoid interference with other tests.
	localFS := afero.NewMemMapFs()

	testTxtStream := getFileStream(t, txtFilePath)
	defer testTxtStream.Close()

	extractor, err := rpm.NewRPMExtractorImpl(localFS)
	require.NoError(t, err)
	require.NotNil(t, extractor)

	err = extractor.Extract(testTxtStream, testDestinationDir)
	require.Error(t, err)

	exists, err := afero.DirExists(localFS, testDestinationDir)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestExtractFailureSimulation(t *testing.T) {
	// Using a local FS to avoid interference with other tests.
	localFS := afero.NewMemMapFs()

	extractor, err := rpm.NewRPMExtractorImpl(localFS)
	require.NoError(t, err)
	require.NotNil(t, extractor)

	t.Run("nil stream should fail", func(t *testing.T) {
		err := extractor.Extract(nil, testDestinationDir)
		assert.Error(t, err)
	})

	t.Run("empty destination path should fail", func(t *testing.T) {
		testRPMStream := getFileStream(t, packagePath)
		defer testRPMStream.Close()

		err := extractor.Extract(testRPMStream, "")
		assert.Error(t, err)
	})
}
