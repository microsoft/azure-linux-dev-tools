// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders_test

import (
	"errors"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/rpmprovider"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/rpmprovider/rpmprovider_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/rpm_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils/fileutils_test"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const (
	testDestinationDir = "test_rpm_extractor_output"
)

func TestNewRPMContentsProviderImpl(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockRPMProvider := rpmprovider_test.NewMockRPMProvider(ctrl)
	mockRPMExtractor := rpm_test.NewMockRPMExtractor(ctrl)

	t.Run("valid dependencies succeed", func(t *testing.T) {
		provider, err := sourceproviders.NewRPMContentsProviderImpl(mockRPMExtractor, mockRPMProvider)
		require.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("nil extractor fails", func(t *testing.T) {
		provider, err := sourceproviders.NewRPMContentsProviderImpl(nil, mockRPMProvider)
		require.Error(t, err)
		assert.Nil(t, provider)
		assert.Contains(t, err.Error(), "RPM extractor cannot be nil")
	})

	t.Run("nil provider fails", func(t *testing.T) {
		provider, err := sourceproviders.NewRPMContentsProviderImpl(mockRPMExtractor, nil)
		require.Error(t, err)
		assert.Nil(t, provider)
		assert.Contains(t, err.Error(), "RPM provider cannot be nil")
	})
}

func TestGetComponent(t *testing.T) {
	expectedFiles := []string{
		"kernel-mft-4.30.0.tgz",
		"mft_kernel.spec",
	}

	ctrl := gomock.NewController(t)
	localFS := afero.NewMemMapFs()

	eventListener := opctx_test.NewNoOpMockEventListener(ctrl)

	httpDownloader, err := downloader.NewHTTPDownloader(
		opctx_test.NewNoOpMockDryRunnable(ctrl),
		opctx_test.NewNoOpMockEventListener(ctrl),
		localFS,
	)
	require.NoError(t, err)
	require.NotNil(t, httpDownloader)

	// Create a mock querier.
	// Real one runs the 'repoquery' command, which may not be available on the host.
	mockQuerier := rpm_test.NewMockRepoQuerier(ctrl)

	rpmProvider, err := rpmprovider.NewRPMProviderImpl(eventListener, httpDownloader, mockQuerier)
	require.NoError(t, err)

	rpmExtractor, err := rpm.NewRPMExtractorImpl(localFS)
	require.NoError(t, err)

	provider, err := sourceproviders.NewRPMContentsProviderImpl(rpmExtractor, rpmProvider)
	require.NoError(t, err)

	packageURL := testServerURL + packagePath

	// Create a mock component
	mockComponent := components_testutils.NewMockComponent(ctrl)
	mockComponent.EXPECT().GetName().AnyTimes().Return(packageName)
	mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
		Name: packageName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeUpstream,
		},
	})

	t.Run("download latest version", func(t *testing.T) {
		mockQuerier.EXPECT().
			GetRPMLocation(t.Context(), packageName, nil).
			Return(packageURL, nil).
			Times(1)

		err = provider.GetComponent(t.Context(), mockComponent, testDestinationDir)
		require.NoError(t, err)

		entries, err := afero.ReadDir(localFS, testDestinationDir)
		require.NoError(t, err)
		assert.Len(t, entries, len(expectedFiles))

		for _, file := range entries {
			assert.Contains(t, expectedFiles, file.Name())
		}
	})

	t.Run("empty component name fails", func(t *testing.T) {
		emptyNameComponent := components_testutils.NewMockComponent(ctrl)
		emptyNameComponent.EXPECT().GetName().AnyTimes().Return("")

		err = provider.GetComponent(t.Context(), emptyNameComponent, testDestinationDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "component name cannot be empty")
	})

	t.Run("empty destination path fails", func(t *testing.T) {
		err = provider.GetComponent(t.Context(), mockComponent, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "destination path cannot be empty")
	})
}

func TestGetComponentFailureSimulation(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock component
	mockComponent := components_testutils.NewMockComponent(ctrl)
	mockComponent.EXPECT().GetName().AnyTimes().Return(packageName)
	mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
		Name: packageName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeUpstream,
		},
	})

	t.Run("RPM provider failure propagates", func(t *testing.T) {
		mockExtractor := rpm_test.NewNoOpMockRPMExtractor(ctrl)

		rpmProviderError := errors.New("RPM provider failed")
		mockRPMProvider := rpmprovider_test.NewMockRPMProvider(ctrl)
		mockRPMProvider.EXPECT().
			GetRPM(gomock.Any(), packageName, nil).
			Return(nil, rpmProviderError).
			Times(1)

		provider, err := sourceproviders.NewRPMContentsProviderImpl(mockExtractor, mockRPMProvider)
		require.NoError(t, err)

		err = provider.GetComponent(t.Context(), mockComponent, testDestinationDir)
		require.Error(t, err)
		assert.ErrorIs(t, err, rpmProviderError)
	})

	t.Run("extractor failure propagates", func(t *testing.T) {
		// Set up successful RPM provider call
		dummyReadCloser := fileutils_test.NewNoOpReadCloser()
		mockRPMProvider := rpmprovider_test.NewMockRPMProvider(ctrl)
		mockRPMProvider.EXPECT().
			GetRPM(gomock.Any(), packageName, nil).
			Return(dummyReadCloser, nil).
			Times(1)

		// Set up failing extractor call
		extractorError := errors.New("extractor failed")
		mockExtractor := rpm_test.NewMockRPMExtractor(ctrl)
		mockExtractor.EXPECT().
			Extract(dummyReadCloser, testDestinationDir).
			Return(extractorError).
			Times(1)

		provider, err := sourceproviders.NewRPMContentsProviderImpl(mockExtractor, mockRPMProvider)
		require.NoError(t, err)

		err = provider.GetComponent(t.Context(), mockComponent, testDestinationDir)
		require.Error(t, err)
		assert.ErrorIs(t, err, extractorError)
	})
}
