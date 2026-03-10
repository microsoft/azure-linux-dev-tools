// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
)

const (
	// RPM test constants (used by rpmcontentsprovider_test.go).
	packageName          = "mft_kernel"
	packageVersionString = "4.30.0-11.azl3"
	packagePath          = "/" + packageName + "-" + packageVersionString + ".src.rpm"

	// Fedora/Git lookaside cache test constants (used by fedorasourceprovider_test.go).
	testPackageName = "test-package"
	testFileName    = "test-1.0.tar.gz"
	testHashType    = "SHA512"
	testHash        = "3aa9f84dffec0912051dc4b876741c04e4ba200dc32c8617cb21601c948cd516a3ff14369645e2e40ef600c6646ef4b2" +
		"1414b72139648a9a4b663564ced629ec"

	// Additional test file paths.
	txtFilePath = "/test.txt"
)

//nolint:gochecknoglobals // Global set during test setup, shared across package tests.
var (
	testServerURL    string
	testGitServerURL string
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	rpmServer, err := setUpRPMTestInfrastructure()
	if err != nil {
		panic("Failed to set up RPM test server: " + err.Error())
	}
	defer rpmServer.Close()

	gitServer := setupGitTestInfrastructure()
	defer gitServer.Close()

	return m.Run()
}

// setUpRPMTestInfrastructure gets a real RPM file for testing and serves it via HTTP.
func setUpRPMTestInfrastructure() (*httptest.Server, error) {
	const (
		testRPMFilePath = "test.rpm"
		testTxtFilePath = "test.txt"
	)

	serverFS := afero.NewMemMapFs()

	err := setUpRPMFile(serverFS, testRPMFilePath)
	if err != nil {
		return nil, err
	}

	err = setUpNonRPMFile(serverFS, testTxtFilePath)
	if err != nil {
		return nil, err
	}

	return setUpRPMTestServer(serverFS, testRPMFilePath, testTxtFilePath), nil
}

func setUpRPMTestServer(serverFS afero.Fs, testRPMFilePath string, testTxtFilePath string) *httptest.Server {
	testServer := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case packagePath:
				file, err := serverFS.Open(testRPMFilePath)
				if err != nil {
					panic("Failed to open test RPM file: " + err.Error())
				}
				defer file.Close()

				http.ServeContent(writer, request, "application/x-rpm", time.Now(), file)
			case txtFilePath:
				file, err := serverFS.Open(testTxtFilePath)
				if err != nil {
					panic("Failed to open test txt file: " + err.Error())
				}
				defer file.Close()

				http.ServeContent(writer, request, "text/plain", time.Now(), file)
			default:
				http.NotFound(writer, request)
			}
		}))

	testServerURL = testServer.URL

	return testServer
}

func setUpNonRPMFile(fs afero.Fs, filePath string) error {
	data := []byte("This is a test file that is not an RPM.")

	return fileutils.WriteFile(fs, filePath, data, fileperms.PublicFile)
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

	return fileutils.WriteFile(serverFS, filePath, data, fileperms.PublicFile)
}

// setupGitTestInfrastructure creates a mock lookaside cache server that serves test source files.
func setupGitTestInfrastructure() *httptest.Server {
	testServer := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			// Serve lookaside cache files
			// Expected path format: /<packageName>/<filename>/<hashtype>/<hash>/<filename>
			// Note: hashtype is lowercase in URLs (see fedorasource.go line 169)
			expectedPath := "/" + testPackageName + "/" + testFileName + "/" +
				strings.ToLower(testHashType) + "/" + testHash + "/" + testFileName
			if request.URL.Path == expectedPath {
				writer.Header().Set("Content-Type", "application/gzip")
				_, _ = writer.Write([]byte("tarball content"))

				return
			}

			http.NotFound(writer, request)
		}))

	testGitServerURL = testServer.URL

	return testServer
}
