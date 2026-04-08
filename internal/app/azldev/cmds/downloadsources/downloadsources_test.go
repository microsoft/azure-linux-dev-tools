// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package downloadsources_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/downloadsources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource/fedorasource_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const testLookasideURI = "https://example.com/lookaside/$pkg/$filename/$hashtype/$hash/$filename"

const testPkgDir = "/project/curl"

func TestOnAppInit(t *testing.T) {
	ctrl := gomock.NewController(t)
	app := azldev.NewApp(opctx_test.NewMockFileSystemFactory(ctrl), opctx_test.NewMockOSEnvFactory(ctrl))

	parentCmd := &cobra.Command{Use: "test-parent"}
	downloadsources.OnAppInit(app, parentCmd)

	var found bool

	for _, child := range parentCmd.Commands() {
		if child.Name() == "download-sources" {
			found = true

			break
		}
	}

	assert.True(t, found, "download-sources should be registered as a subcommand")
}

func TestNewDownloadSourcesCmd(t *testing.T) {
	cmd := downloadsources.NewDownloadSourcesCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "download-sources", cmd.Use)

	outputDirFlag := cmd.Flags().Lookup("output-dir")
	require.NotNil(t, outputDirFlag, "--output-dir flag should be registered")
	assert.Equal(t, "o", outputDirFlag.Shorthand)
	assert.Empty(t, outputDirFlag.DefValue)

	lookasideFlag := cmd.Flags().Lookup("lookaside-uri")
	require.NotNil(t, lookasideFlag, "--lookaside-uri flag should be registered")

	componentFlag := cmd.Flags().Lookup("component")
	require.NotNil(t, componentFlag, "--component flag should be registered")
}

func TestDownloadSources_StandaloneMode(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)

	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, testPkgDir))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testPkgDir+"/sources", []byte(""), fileperms.PrivateFile))

	mockDownloader := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)
	mockDownloader.EXPECT().
		ExtractSourcesFromRepo(gomock.Any(), testPkgDir, "curl", testLookasideURI, gomock.Any()).
		Return(nil)

	options := &downloadsources.DownloadSourcesOptions{
		Directory:           testPkgDir,
		LookasideBaseURIs:   []string{testLookasideURI},
		LookasideDownloader: mockDownloader,
	}

	// Package name "curl" derived from directory basename.
	err := downloadsources.DownloadSources(testEnv.Env, options)
	require.NoError(t, err)
}

func TestDownloadSources_StandaloneMode_NoSourcesFile(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)

	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, testPkgDir))

	// ExtractSourcesFromRepo returns nil when no sources file exists.
	mockDownloader := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)
	mockDownloader.EXPECT().
		ExtractSourcesFromRepo(gomock.Any(), testPkgDir, "curl", testLookasideURI, gomock.Any()).
		Return(nil)

	options := &downloadsources.DownloadSourcesOptions{
		Directory:           testPkgDir,
		LookasideBaseURIs:   []string{testLookasideURI},
		LookasideDownloader: mockDownloader,
	}

	err := downloadsources.DownloadSources(testEnv.Env, options)
	require.NoError(t, err)
}

func TestDownloadSources_ComponentMode(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)

	// Register a component with an upstream-name that differs from the
	// component name, verifying that the upstream name is used for $pkg.
	testEnv.Config.Components["my-curl"] = projectconfig.ComponentConfig{
		Name: "my-curl",
		Spec: projectconfig.SpecSource{
			SourceType:   projectconfig.SpecSourceTypeLocal,
			Path:         testPkgDir + "/curl.spec",
			UpstreamName: "curl",
		},
	}

	// The test distro already has a LookasideBaseURI configured.
	expectedURI := testEnv.Config.Distros["test-distro"].LookasideBaseURI

	mockDownloader := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)
	mockDownloader.EXPECT().
		ExtractSourcesFromRepo(
			gomock.Any(), testPkgDir, "curl", expectedURI, gomock.Any(),
		).
		Return(nil)

	options := &downloadsources.DownloadSourcesOptions{
		Directory:           testPkgDir,
		ComponentName:       "my-curl",
		LookasideDownloader: mockDownloader,
	}

	err := downloadsources.DownloadSources(testEnv.Env, options)
	require.NoError(t, err)
}
