// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package downloader_test

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/h2non/gock"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testURI = "https://example.com"

func TestDownloadFile_Success(t *testing.T) {
	// Intercept all requests via default HTTP transport.
	gock.New(testURI).Reply(200).BodyString("Hello, World!")

	defer gock.OffAll()

	testDownloader := buildTestDownloader(t)

	err := testDownloader.Download(t.Context(), testURI, "example.txt")
	require.NoError(t, err)
}

func TestFetchStream_Success(t *testing.T) {
	// Intercept all requests via default HTTP transport.
	gock.New(testURI).Reply(200).BodyString("Hello, World!")

	defer gock.OffAll()

	testDownloader := buildTestDownloader(t)

	stream, err := testDownloader.FetchStream(t.Context(), testURI)
	require.NoError(t, err)

	defer stream.Close()

	data, err := io.ReadAll(stream)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(data))
}

func TestDownloadFile_Failure(t *testing.T) {
	// Intercept all requests via default HTTP transport.
	gock.New(testURI).Reply(404)

	defer gock.OffAll()

	testDownloader := buildTestDownloader(t)

	err := testDownloader.Download(t.Context(), testURI, "example.txt")
	require.Error(t, err)
}

func TestFetchStream_Failure(t *testing.T) {
	// Intercept all requests via default HTTP transport.
	gock.New(testURI).Reply(404)

	defer gock.OffAll()

	testDownloader := buildTestDownloader(t)

	stream, err := testDownloader.FetchStream(t.Context(), testURI)
	require.Error(t, err)
	assert.Nil(t, stream)
}

func TestDownloadFile_MidDownloadFailure(t *testing.T) {
	const failureOffset = 8192

	testOutputFilePath := filepath.Join(t.TempDir(), "download.bin")

	// Intercept all requests via default HTTP transport. Provide an ok response,
	// but simulate a failure after a certain number of bytes.
	gock.New(testURI).
		Reply(200).
		SetHeader("Content-Length", strconv.Itoa(failureOffset)).
		Map(func(res *http.Response) *http.Response {
			res.Body = io.NopCloser(&faultyZeroReader{FailureOffset: failureOffset})

			return res
		})

	defer gock.OffAll()

	// Setup the test context with a temporary directory in the real filesystem so we can
	// get the right production atomicity semantics on failure. (When used with an
	// in-memory filesystem, atomic update is not guaranteed.)
	realFS := afero.NewOsFs()
	testDownloader := buildTestDownloader(t, testctx.WithFS(realFS))

	err := testDownloader.Download(t.Context(), testURI, testOutputFilePath)
	require.Error(t, err)

	_, statErr := realFS.Stat(testOutputFilePath)
	require.Error(t, statErr)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

type faultyZeroReader struct {
	FailureOffset int

	readOffset int
}

func (f *faultyZeroReader) Read(buffer []byte) (n int, err error) {
	if f.readOffset >= f.FailureOffset {
		return 0, errors.New("injected failure")
	}

	n = min(len(buffer), f.FailureOffset-f.readOffset)
	f.readOffset += n

	copy(buffer, make([]byte, n))

	return n, nil
}

func buildTestDownloader(t *testing.T, options ...testctx.TestCtxOption) *downloader.HTTPDownloader {
	t.Helper()

	ctx := testctx.NewCtx(options...)

	testDownloader, err := downloader.NewHTTPDownloader(ctx, ctx, ctx.FS())
	require.NoError(t, err)

	return testDownloader
}
