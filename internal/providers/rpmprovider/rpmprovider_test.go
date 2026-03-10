// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package rpmprovider_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/rpmprovider"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/rpm_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader/downloader_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const (
	packageName          = "mft_kernel"
	packageVersionString = "4.30.0-11.azl3"
	packagePath          = "/" + packageName + "-" + packageVersionString + ".src.rpm"
)

//nolint:gochecknoglobals // Global set inside 'setUpTestServer' during test set-up and tests need access to it.
var testServerURL string

// We set-up the test HTTP server to serve a real RPM file
// from packages.microsoft.com. This requires network access.
func TestMain(m *testing.M) {
	os.Exit(startTests(m))
}

func startTests(m *testing.M) int {
	testServer, err := setUpTestInfrastructure()
	if err != nil {
		panic("Failed to set up test server: " + err.Error())
	}
	defer testServer.Close()

	return m.Run()
}

// setUpTestInfrastructure gets a real RPM file for testing.
// The RPM is retrieved from packages.microsoft.com and
// stored in an in-memory filesystem to avoid multiple network requests.
// We also create a local HTTP server to serve the downloaded RPM.
func setUpTestInfrastructure() (*httptest.Server, error) {
	serverFS := afero.NewMemMapFs()

	err := setUpRPMFile(serverFS, packagePath)
	if err != nil {
		return nil, err
	}

	return setUpTestServer(serverFS, packagePath), nil
}

func setUpTestServer(fs afero.Fs, testRPMFilePath string) *httptest.Server {
	testServer := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == packagePath {
				file, err := fs.Open(testRPMFilePath)
				if err != nil {
					panic("Failed to open test RPM file: " + err.Error())
				}
				defer file.Close()

				http.ServeContent(writer, request, "application/x-rpm", time.Now(), file)
			} else {
				http.NotFound(writer, request)
			}
		}))

	// Set the global test server URL to be used in tests.
	testServerURL = testServer.URL

	return testServer
}

// setUpRPMFile retrieves a real RPM file from packages.microsoft.com
// and stores it in the in-memory filesystem.
//
// Issue #208: replace with an RPM file generated during the test initialization phase.
func setUpRPMFile(serverFS afero.Fs, filePath string) error {
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

	err = fileutils.WriteFile(serverFS, filePath, data, fileperms.PublicFile)
	if err != nil {
		return err
	}

	return nil
}

// buildWorkingDownloader creates a downloader instance that can be used in tests.
// It uses an in-memory filesystem but will attempt to make network calls.
func buildWorkingDownloader(t *testing.T, ctrl *gomock.Controller) *downloader.HTTPDownloader {
	t.Helper()

	downloader, err := downloader.NewHTTPDownloader(
		opctx_test.NewNoOpMockDryRunnable(ctrl),
		opctx_test.NewNoOpMockEventListener(ctrl),
		afero.NewMemMapFs())
	require.NoError(t, err)
	require.NotNil(t, downloader)

	return downloader
}

func TestNewRPMProvider(t *testing.T) {
	downloader := &downloader.HTTPDownloader{}
	querier := &rpm.RQRepoQuerier{}

	ctrl := gomock.NewController(t)
	eventListener := opctx_test.NewNoOpMockEventListener(ctrl)

	t.Run("valid deps succeed", func(t *testing.T) {
		provider, err := rpmprovider.NewRPMProviderImpl(eventListener, downloader, querier)
		require.NoError(t, err)

		assert.NotNil(t, provider)
	})

	t.Run("nil downloader fails", func(t *testing.T) {
		provider, err := rpmprovider.NewRPMProviderImpl(eventListener, nil, querier)
		require.Error(t, err)

		assert.Nil(t, provider)
	})

	t.Run("nil querier fails", func(t *testing.T) {
		provider, err := rpmprovider.NewRPMProviderImpl(eventListener, downloader, nil)
		require.Error(t, err)

		assert.Nil(t, provider)
	})
}

func TestGetRPM(t *testing.T) {
	ctrl := gomock.NewController(t)

	downloader := buildWorkingDownloader(t, ctrl)

	// Create a mock querier.
	// Real one runs the 'repoquery' command, which may not be available on the host.
	mockQuerier := rpm_test.NewMockRepoQuerier(ctrl)

	eventListener := opctx_test.NewNoOpMockEventListener(ctrl)

	provider, err := rpmprovider.NewRPMProviderImpl(eventListener, downloader, mockQuerier)
	require.NoError(t, err)
	require.NotNil(t, provider)

	packageURL := testServerURL + packagePath

	t.Run("download specific version", func(t *testing.T) {
		packageVersion, err := rpm.NewVersion(packageVersionString)
		require.NoError(t, err)

		mockQuerier.EXPECT().
			GetRPMLocation(t.Context(), packageName, packageVersion).
			Return(packageURL, nil).
			Times(1)

		rpmStream, err := provider.GetRPM(t.Context(), packageName, packageVersion)
		require.NoError(t, err)
		require.NotNil(t, rpmStream)

		rpmData, err := io.ReadAll(rpmStream)
		require.NoError(t, err)

		stringOutput := string(rpmData)
		assert.Contains(t, stringOutput, packageName)
		assert.Contains(t, stringOutput, packageVersionString)
	})

	t.Run("download latest version", func(t *testing.T) {
		mockQuerier.EXPECT().
			GetRPMLocation(t.Context(), packageName, nil).
			Return(packageURL, nil).
			Times(1)

		rpmStream, err := provider.GetRPM(t.Context(), packageName, nil)
		require.NoError(t, err)
		require.NotNil(t, rpmStream)

		rpmData, err := io.ReadAll(rpmStream)
		require.NoError(t, err)

		assert.Contains(t, string(rpmData), packageName)
	})

	t.Run("empty package name should fail", func(t *testing.T) {
		_, err := provider.GetRPM(t.Context(), "", nil)
		assert.Error(t, err)
	})

	t.Run("querier error should fail", func(t *testing.T) {
		errorMessage := "some error"
		mockQuerier.EXPECT().
			GetRPMLocation(t.Context(), packageName, nil).
			Return("", errors.New(errorMessage)).
			Times(1)

		_, err := provider.GetRPM(t.Context(), packageName, nil)
		assert.Contains(t, err.Error(), errorMessage)
		assert.Contains(t, err.Error(), packageName)
	})
}

func TestGetRPMFailureSimulation(t *testing.T) {
	ctrl := gomock.NewController(t)

	downloader := buildWorkingDownloader(t, ctrl)
	mockQuerier := rpm_test.NewMockRepoQuerier(ctrl)
	eventListener := opctx_test.NewNoOpMockEventListener(ctrl)

	packageVersion, err := rpm.NewVersion(packageVersionString)
	require.NoError(t, err)

	provider, err := rpmprovider.NewRPMProviderImpl(eventListener, downloader, mockQuerier)
	require.NoError(t, err)

	t.Run("querier error should propagate", func(t *testing.T) {
		querierError := errors.New("querier failed")
		mockQuerier.EXPECT().
			GetRPMLocation(gomock.Any(), packageName, packageVersion).
			Return("", querierError).
			Times(1)

		stream, err := provider.GetRPM(t.Context(), packageName, packageVersion)
		require.Error(t, err)
		assert.Nil(t, stream)

		assert.ErrorIs(t, err, querierError)
	})

	t.Run("downloader error should propagate", func(t *testing.T) {
		const validURL = "http://test.example.com/package.rpm"

		// Mock querier should not fail here.
		mockQuerier.EXPECT().
			GetRPMLocation(gomock.Any(), packageName, packageVersion).
			Return(validURL, nil).
			Times(1)

		downloaderError := errors.New("downloader failed")
		mockDownloader := downloader_test.NewMockDownloader(ctrl)
		mockDownloader.EXPECT().
			FetchStream(gomock.Any(), validURL).
			Return(nil, downloaderError).
			Times(1)

		eventListener := opctx_test.NewNoOpMockEventListener(ctrl)

		mockedDownloadProvider, err := rpmprovider.NewRPMProviderImpl(eventListener, mockDownloader, mockQuerier)
		require.NoError(t, err)

		stream, err := mockedDownloadProvider.GetRPM(t.Context(), packageName, packageVersion)
		require.Error(t, err)
		assert.Nil(t, stream)

		assert.ErrorIs(t, err, downloaderError)
	})

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "empty download URL should fail",
			url:  "",
		},
		{
			name: "non-existent URL should fail",
			url:  "http://invalid-domain.there.is.no.such.resource",
		},
		{
			name: "invalid URL format should fail",
			url:  "not-a-protocol://whatever.com/package.rpm",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockQuerier.EXPECT().
				GetRPMLocation(gomock.Any(), packageName, packageVersion).
				Return(test.url, nil).
				Times(1)

			stream, err := provider.GetRPM(t.Context(), packageName, packageVersion)
			require.Error(t, err)
			assert.Nil(t, stream)
			assert.Contains(t, err.Error(), test.url)
		})
	}
}
