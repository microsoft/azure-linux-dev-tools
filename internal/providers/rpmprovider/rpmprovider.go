// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../../tools/mockgen/go.mod mockgen -source=rpmprovider.go -destination=rpmprovider_test/rpmprovider_mocks.go -package=rpmprovider_test --copyright_file=../../../.license-preamble

package rpmprovider

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
)

type RPMProvider interface {
	// GetRPM retrieves an RPM file for the given package and version, and returns it in form of a closeable stream.
	// The caller is responsible for closing the returned [io.ReadCloser].
	GetRPM(ctx context.Context, name string, version *rpm.Version) (io.ReadCloser, error)
}

// RPMProviderImpl implements [RPMProvider].
// It relies on an [rpm.RepoQuerier] to query the repository for the package URL.
type RPMProviderImpl struct {
	eventListener opctx.EventListener
	downloader    downloader.Downloader
	querier       rpm.RepoQuerier
}

// Ensure RPMProviderImpl implements [RPMProvider].
var _ RPMProvider = (*RPMProviderImpl)(nil)

// NewRPMProviderImpl creates a new RPM provider.
func NewRPMProviderImpl(
	eventListener opctx.EventListener, downloader downloader.Downloader, querier rpm.RepoQuerier,
) (*RPMProviderImpl, error) {
	if eventListener == nil {
		return nil, errors.New("event listener cannot be nil")
	}

	if downloader == nil {
		return nil, errors.New("downloader cannot be nil")
	}

	if querier == nil {
		return nil, errors.New("repo querier cannot be nil")
	}

	return &RPMProviderImpl{
		eventListener: eventListener,
		downloader:    downloader,
		querier:       querier,
	}, nil
}

// GetRPM retrieves an RPM file for the given package and returns it in form of a closeable stream.
func (p *RPMProviderImpl) GetRPM(ctx context.Context, name string, version *rpm.Version) (io.ReadCloser, error) {
	if name == "" {
		return nil, errors.New("package name cannot be empty")
	}

	eventArgs := []any{"name", name}
	if version != nil {
		eventArgs = append(eventArgs, "version", version)
	}

	evt := p.eventListener.StartEvent("Retrieving RPM package", eventArgs...)

	defer evt.End()

	// Get the RPM URL
	rpmURL, err := p.querier.GetRPMLocation(ctx, name, version)
	if err != nil {
		return nil, fmt.Errorf("failed to get RPM location for package %#q, version %#q:\n%w", name, version, err)
	}

	evt = p.eventListener.StartEvent("Downloading RPM", "name", name, "url", rpmURL)

	defer evt.End()

	fileStream, err := p.downloader.FetchStream(ctx, rpmURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch RPM file for package %#q, version %#q:\n%w", name, version, err)
	}

	return fileStream, nil
}
