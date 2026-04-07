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

	packageNameFlag := cmd.Flags().Lookup("package-name")
	require.NotNil(t, packageNameFlag, "--package-name flag should be registered")
}

func TestResolveFromDirectory_DerivesPackageName(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)

	pkgDir := "/project/curl"
	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, pkgDir))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, pkgDir+"/sources", []byte(""), fileperms.PrivateFile))

	mockDownloader := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)
	mockDownloader.EXPECT().
		ExtractSourcesFromRepo(gomock.Any(), pkgDir, "curl", gomock.Any(), gomock.Any()).
		Return(nil)

	options := &downloadsources.DownloadSourcesOptions{
		Directory:           pkgDir,
		LookasideDownloader: mockDownloader,
	}

	err := downloadsources.DownloadSources(testEnv.Env, options)
	require.NoError(t, err)
}

func TestResolveFromDirectory_NonexistentDir(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	options := &downloadsources.DownloadSourcesOptions{
		Directory: "/project/nonexistent",
	}

	err := downloadsources.DownloadSources(testEnv.Env, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no 'sources' file found")
}

func TestDownloadSources_NoSourcesFile(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	// Create a directory without a 'sources' file.
	pkgDir := "/project/curl"
	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, pkgDir))

	options := &downloadsources.DownloadSourcesOptions{
		Directory: pkgDir,
	}

	err := downloadsources.DownloadSources(testEnv.Env, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no 'sources' file found")
}

func TestResolveLookasideURI_FollowsUpstreamDistro(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)

	// Reconfigure: default distro has NO lookaside URI, but points to an upstream that does.
	testEnv.Config.Distros["test-distro"] = projectconfig.DistroDefinition{
		Versions: map[string]projectconfig.DistroVersionDefinition{
			"1.0": {
				DefaultComponentConfig: projectconfig.ComponentConfig{
					Spec: projectconfig.SpecSource{
						UpstreamDistro: projectconfig.DistroReference{
							Name:    "upstream-distro",
							Version: "42",
						},
					},
				},
			},
		},
	}

	expectedURI := "https://upstream.example.com/lookaside/$pkg/$filename/$hashtype/$hash/$filename"

	testEnv.Config.Distros["upstream-distro"] = projectconfig.DistroDefinition{
		LookasideBaseURI: expectedURI,
		Versions: map[string]projectconfig.DistroVersionDefinition{
			"42": {},
		},
	}

	pkgDir := "/project/testpkg"
	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, pkgDir))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, pkgDir+"/sources", []byte(""), fileperms.PrivateFile))

	mockDownloader := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)
	mockDownloader.EXPECT().
		ExtractSourcesFromRepo(gomock.Any(), pkgDir, "testpkg", expectedURI, gomock.Any()).
		Return(nil)

	options := &downloadsources.DownloadSourcesOptions{
		Directory:           pkgDir,
		LookasideDownloader: mockDownloader,
	}

	err := downloadsources.DownloadSources(testEnv.Env, options)
	require.NoError(t, err)
}
