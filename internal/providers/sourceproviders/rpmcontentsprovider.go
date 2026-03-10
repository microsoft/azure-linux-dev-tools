// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"context"
	"errors"
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/rpmprovider"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
)

// RPMContentsProviderImpl implements [ComponentSourceProvider]. It relies on
// [rpmprovider.RPMProvider] to download the RPM file.
type RPMContentsProviderImpl struct {
	extractor   rpm.RPMExtractor
	rpmProvider rpmprovider.RPMProvider
}

// Ensure [RPMContentsProviderImpl] implements [ComponentSourceProvider].
var _ ComponentSourceProvider = (*RPMContentsProviderImpl)(nil)

// NewRPMContentsProviderImpl creates a new instance of [RPMContentsProviderImpl].
func NewRPMContentsProviderImpl(
	extractor rpm.RPMExtractor,
	rpmProvider rpmprovider.RPMProvider,
) (*RPMContentsProviderImpl, error) {
	if extractor == nil {
		return nil, errors.New("RPM extractor cannot be nil")
	}

	if rpmProvider == nil {
		return nil, errors.New("RPM provider cannot be nil")
	}

	return &RPMContentsProviderImpl{
		extractor:   extractor,
		rpmProvider: rpmProvider,
	}, nil
}

// GetComponent downloads the source RPM for a component and extracts its contents
// in the provided destination path.
func (r *RPMContentsProviderImpl) GetComponent(
	ctx context.Context, component components.Component, destDirPath string,
) (err error) {
	if component.GetName() == "" {
		return errors.New("component name cannot be empty")
	}

	if destDirPath == "" {
		return errors.New("destination path cannot be empty")
	}

	// Get the RPM
	rpmReader, err := r.rpmProvider.GetRPM(ctx, component.GetName(), nil)
	if err != nil {
		return fmt.Errorf("failed to get the RPM file for component %#q: %w",
			component.GetName(), err)
	}
	defer defers.HandleDeferError(rpmReader.Close, &err)

	// Extract the RPM contents
	err = r.extractor.Extract(rpmReader, destDirPath)
	if err != nil {
		return fmt.Errorf("failed to extract the RPM file of component %#q: %w",
			component.GetName(), err)
	}

	return nil
}
