// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../../tools/mockgen/go.mod mockgen -source=downloader.go -destination=downloader_test/downloader_mocks.go -package=downloader_test --copyright_file=../../../.license-preamble

package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/binalyze/ctxio"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// [Downloader] defines an interface for downloading files from a given URI to a specified destination path.
type Downloader interface {
	// Download downloads a file from the specified URI and saves it to the destination path.
	Download(ctx context.Context, uri string, destPath string) error

	// FetchStream initiates fetching the specified URI and returns it as an io.ReadCloser.
	// The caller is responsible for closing the returned io.ReadCloser.
	FetchStream(ctx context.Context, uri string) (io.ReadCloser, error)
}

// [HTTPDownloader] implements the [Downloader] interface.
// It uses the [http] module to download files.
type HTTPDownloader struct {
	dryRunInfo    opctx.DryRunnable
	eventListener opctx.EventListener
	fs            opctx.FS
}

// Ensure HTTPDownloader implements [Downloader].
var _ Downloader = (*HTTPDownloader)(nil)

func NewHTTPDownloader(
	dryRunInfo opctx.DryRunnable,
	eventListener opctx.EventListener,
	fs opctx.FS,
) (*HTTPDownloader, error) {
	if dryRunInfo == nil {
		return nil, errors.New("dryRunInfo cannot be nil")
	}

	if eventListener == nil {
		return nil, errors.New("eventListener cannot be nil")
	}

	if fs == nil {
		return nil, errors.New("filesystem cannot be nil")
	}

	return &HTTPDownloader{
		dryRunInfo:    dryRunInfo,
		eventListener: eventListener,
		fs:            fs,
	}, nil
}

// Downloads a file from the location specified by the URI, saving it to destPath.
func (h *HTTPDownloader) Download(ctx context.Context, uri string, destPath string) error {
	if h.dryRunInfo.DryRun() {
		slog.Info("Dry run; would download file", "uri", uri, "dest", destPath)

		return nil
	}

	slog.Debug("Downloading", "uri", uri)

	event := h.eventListener.StartEvent("")
	defer event.End()

	// Best-effort attempt to make sure the dir exists.
	_ = fileutils.MkdirAll(h.fs, filepath.Dir(destPath))

	// Use an updater to write the file. In non-test scenarios, we'll be using a real
	// filesystem and this writer will handle atomic updates for us so that external
	// observers won't see a partially-written file.
	writer, err := fileutils.NewFileUpdateWriter(h.fs, destPath)
	if err != nil {
		return fmt.Errorf("failed to create file writer for %#q:\n%w", destPath, err)
	}

	err = h.getFileUsingWriter(ctx, event, uri, writer)
	if err != nil {
		return fmt.Errorf("failed to download to %#q:\n%w", destPath, err)
	}

	err = writer.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit file %#q:\n%w", destPath, err)
	}

	return nil
}

// Downloads a file from the location specified by URI, returning it as an io.ReadCloser.
func (h *HTTPDownloader) FetchStream(ctx context.Context, uri string) (io.ReadCloser, error) {
	if h.dryRunInfo.DryRun() {
		slog.Info("Dry run; would fetch file to a stream", "uri", uri)

		//nolint: nilnil // We're intentionally returning nothing.
		return nil, nil
	}

	slog.Debug("Fetching", "uri", uri)

	event := h.eventListener.StartEvent("")
	defer event.End()

	//nolint: bodyclose // We don't close the body here because the caller is expected to do so.
	response, err := h.getFileData(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %#q:\n%w", uri, err)
	}

	return response.Body, nil
}

func (h *HTTPDownloader) getFileUsingWriter(
	ctx context.Context, event opctx.Event, uri string, out io.Writer,
) error {
	//nolint: bodyclose // We close the body inside 'HandleDeferError'
	resp, err := h.getFileData(ctx, uri)
	if err != nil {
		return fmt.Errorf("failed to fetch %#q:\n%w", uri, err)
	}
	defer defers.HandleDeferError(resp.Body.Close, &err)

	err = h.saveResponseBody(ctx, event, resp, out)
	if err != nil {
		return fmt.Errorf("failed to save response from fetching %#q:\n%w", uri, err)
	}

	return nil
}

func (*HTTPDownloader) getFileData(ctx context.Context, uri string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request to download %#q:\n%w", uri, err)
	}

	// Set headers to bypass anti-bot measures that may block automated requests.
	request.Header.Set("Accept", "*/*")
	// Without this header, there's a change a compressed file will be transparently decompressed
	// for us. When that happens, the hash won't match anymore.
	request.Header.Set("Accept-Encoding", "identity")

	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to download %#q:\n%w", uri, err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()

		return nil, fmt.Errorf("failed to download %#q: status %#q", uri, resp.Status)
	}

	return resp, nil
}

func (h *HTTPDownloader) saveResponseBody(
	ctx context.Context, event opctx.Event, resp *http.Response, out io.Writer,
) error {
	// Estimate total len (may not be accurately reported).
	totalLen := resp.ContentLength

	// Wrap writer so we can update event progress.
	writer := ctxio.NewWriter(ctx, out, func(bytesCopied int64) {
		if totalLen > 0 {
			event.SetProgress(bytesCopied, totalLen)
		}
	})

	// Write the body to file
	_, err := io.Copy(writer, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write downloaded bytes to output file:\n%w", err)
	}

	return nil
}
