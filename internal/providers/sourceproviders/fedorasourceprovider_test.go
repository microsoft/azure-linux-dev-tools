// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource/fedorasource_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git/git_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const (
	destDir            = "/output"
	repoURL            = "https://example.com/" + testPackageName + ".git"
	branch             = "main"
	distGitBaseURI     = "https://example.com/$pkg.git"
	testUpstreamCommit = "abc1234def5678"
)

// mockDistroResolver returns a mock distro resolver that returns the default test distro config.
// Note: This uses testGitServerURL which is set up by TestMain in testmain_test.go.
func mockDistroResolver() sourceproviders.DistroResolver {
	return func(distroRef projectconfig.DistroReference) (
		projectconfig.DistroDefinition, projectconfig.DistroVersionDefinition, error,
	) {
		return projectconfig.DistroDefinition{
				DistGitBaseURI:   distGitBaseURI,
				LookasideBaseURI: testGitServerURL + "/$pkg/$filename/$hashtype/$hash/$filename",
			},
			projectconfig.DistroVersionDefinition{
				DistGitBranch: branch,
			}, nil
	}
}

func TestNewGitContentsProviderImpl(t *testing.T) {
	ctrl := gomock.NewController(t)
	env := testutils.NewTestEnv(t)
	mockGitProvider := git_test.NewMockGitProvider(ctrl)
	mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

	t.Run("success", func(t *testing.T) {
		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("nil filesystem fails", func(t *testing.T) {
		_, err := sourceproviders.NewFedoraSourcesProviderImpl(
			nil,
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "filesystem cannot be nil")
	})

	t.Run("nil dryRunnable fails", func(t *testing.T) {
		_, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			nil,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dryRunnable cannot be nil")
	})

	t.Run("nil git provider fails", func(t *testing.T) {
		_, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			nil,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "git provider cannot be nil")
	})

	t.Run("nil extractor fails", func(t *testing.T) {
		_, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			nil,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "downloader cannot be nil")
	})

	t.Run("nil distro resolver fails", func(t *testing.T) {
		_, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			nil,
			retry.Disabled(),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "distro resolver cannot be nil")
	})
}

//nolint:maintidx // It's long because of multiple test cases.
func TestGetComponentFromGit(t *testing.T) {
	env := testutils.NewTestEnv(t)

	t.Run("successful extraction with real components", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		// Create real downloader
		httpDownloader, err := downloader.NewHTTPDownloader(
			env.DryRunnable,
			env.EventListener,
			env.FS(),
		)
		require.NoError(t, err)

		// Create real git extractor with the correct arguments
		realExtractor, err := fedorasource.NewFedoraRepoExtractorImpl(
			env.DryRunnable,
			env.FS(),
			httpDownloader,
			retry.Disabled(),
		)
		require.NoError(t, err)

		// Mock only the git provider
		mockGitProvider := git_test.NewMockGitProvider(ctrl)

		// Setup the mock to simulate a git clone that creates test files
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, repoURL, cloneDir string, opts ...git.GitOptions) error {
				specPath := cloneDir + "/" + testPackageName + ".spec"

				err := fileutils.WriteFile(
					env.FS(), specPath,
					[]byte("Name: "+testPackageName+"\nVersion: 1.0"),
					fileperms.PublicFile,
				)
				if err != nil {
					return err
				}

				sourcesPath := cloneDir + "/sources"
				sourcesContent := testHashType + " (" + testFileName + ") = " + testHash

				return fileutils.WriteFile(env.FS(), sourcesPath, []byte(sourcesContent), fileperms.PublicFile)
			})

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			realExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
		})

		// Execute the method under test
		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.NoError(t, err)

		// Verify the spec file was copied to destination
		specPath := destDir + "/" + testPackageName + ".spec"
		exists, err := fileutils.Exists(env.FS(), specPath)
		require.NoError(t, err)
		assert.True(t, exists, "spec file should exist in destination")

		// Verify spec file content
		content, err := fileutils.ReadFile(env.FS(), specPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "Name: "+testPackageName)

		// Verify lookaside source was downloaded
		tarballPath := destDir + "/" + testFileName
		exists, err = fileutils.Exists(env.FS(), tarballPath)
		require.NoError(t, err)
		assert.True(t, exists, "tarball from lookaside cache should be downloaded")

		// Verify tarball content
		tarballContent, err := fileutils.ReadFile(env.FS(), tarballPath)
		require.NoError(t, err)
		assert.Equal(t, "tarball content", string(tarballContent))
	})

	t.Run("successful extraction without lookaside sources", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		httpDownloader, err := downloader.NewHTTPDownloader(
			env.DryRunnable,
			env.EventListener,
			env.FS(),
		)
		require.NoError(t, err)

		realExtractor, err := fedorasource.NewFedoraRepoExtractorImpl(
			env.DryRunnable,
			env.FS(),
			httpDownloader,
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockGitProvider := git_test.NewMockGitProvider(ctrl)

		// Simulate a repo without sources file
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, repoURL, cloneDir string, opts ...git.GitOptions) error {
				specPath := cloneDir + "/" + testPackageName + ".spec"

				return fileutils.WriteFile(env.FS(), specPath, []byte("Name: "+testPackageName), fileperms.PublicFile)
			})

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			realExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
		})

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.NoError(t, err)

		// Verify only spec file exists (no lookaside downloads)
		specPath := destDir + "/" + testPackageName + ".spec"
		exists, err := fileutils.Exists(env.FS(), specPath)
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("validation failures", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		emptyNameComponent := components_testutils.NewMockComponent(ctrl)
		emptyNameComponent.EXPECT().GetName().AnyTimes().Return("")

		err = provider.GetComponent(context.Background(), emptyNameComponent, destDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "component name cannot be empty")

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
		})

		err = provider.GetComponent(context.Background(), mockComponent, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "destination path cannot be empty")
	})

	t.Run("git clone failure propagates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
		})

		cloneError := errors.New("clone failed")
		mockGitProvider.EXPECT().Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).Return(cloneError)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.Error(t, err)
		assert.ErrorIs(t, err, cloneError)
	})

	t.Run("extractor failure propagates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
		})

		// Git clone succeeds
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			Return(nil)

		// But extractor fails - note it receives the component name, not destDir
		extractorError := errors.New("extraction failed")
		mockExtractor.EXPECT().
			ExtractSourcesFromRepo(gomock.Any(), gomock.Any(), testPackageName, gomock.Any()).
			Return(extractorError)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.Error(t, err)
		assert.ErrorIs(t, err, extractorError)
	})

	t.Run("spec file renamed when upstream name differs from component name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		componentName := "my-component"
		upstreamName := "upstream-pkg"

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(componentName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: componentName,
			Spec: projectconfig.SpecSource{
				UpstreamName: upstreamName,
			},
		})

		// Git clone creates spec file with upstream name
		upstreamRepoURL := "https://example.com/" + upstreamName + ".git"
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), upstreamRepoURL, gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, repoURL, cloneDir string, opts ...git.GitOptions) error {
				// Create spec file with upstream name
				specPath := cloneDir + "/" + upstreamName + ".spec"

				return fileutils.WriteFile(
					env.FS(), specPath,
					[]byte("Name: "+upstreamName+"\nVersion: 1.0"),
					fileperms.PublicFile,
				)
			})

		// Extractor succeeds
		mockExtractor.EXPECT().
			ExtractSourcesFromRepo(gomock.Any(), gomock.Any(), upstreamName, gomock.Any()).
			Return(nil)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.NoError(t, err)

		// Verify spec file was renamed to component name
		renamedSpecPath := destDir + "/" + componentName + ".spec"
		exists, err := fileutils.Exists(env.FS(), renamedSpecPath)
		require.NoError(t, err)
		assert.True(t, exists, "spec file should be renamed to component name")

		// Verify original upstream spec file no longer exists
		originalSpecPath := destDir + "/" + upstreamName + ".spec"
		exists, err = fileutils.Exists(env.FS(), originalSpecPath)
		require.NoError(t, err)
		assert.False(t, exists, "original upstream spec file should not exist after rename")
	})

	t.Run("spec rename failure propagates when source file missing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		componentName := "my-component"
		upstreamName := "upstream-pkg"

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(componentName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: componentName,
			Spec: projectconfig.SpecSource{
				UpstreamName: upstreamName,
			},
		})

		// Git clone succeeds but does NOT create the spec file (simulating missing file)
		upstreamRepoURL := "https://example.com/" + upstreamName + ".git"
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), upstreamRepoURL, gomock.Any(), gomock.Any()).
			Return(nil)

		// Extractor succeeds
		mockExtractor.EXPECT().
			ExtractSourcesFromRepo(gomock.Any(), gomock.Any(), upstreamName, gomock.Any()).
			Return(nil)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to rename fetched spec file")
	})
}

func TestCheckoutTargetCommit(t *testing.T) {
	env := testutils.NewTestEnv(t)

	t.Run("uses upstream distro snapshot when configured", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		snapshotTimeStr := "2026-01-10T11:11:30-08:00"
		snapshotTime, _ := time.Parse(time.RFC3339, snapshotTimeStr)
		snapshotCommitHash := "snapshot789abc"

		// Create provider
		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
			Spec: projectconfig.SpecSource{
				UpstreamDistro: projectconfig.DistroReference{
					Name:     "fedora",
					Snapshot: snapshotTimeStr,
				},
			},
		})

		// Clone succeeds
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, repoURL, cloneDir string, opts ...git.GitOptions) error {
				specPath := cloneDir + "/" + testPackageName + ".spec"

				return fileutils.WriteFile(env.FS(), specPath, []byte("Name: "+testPackageName), fileperms.PublicFile)
			})

		// Should query for commit hash at or before snapshot time
		mockGitProvider.EXPECT().
			GetCommitHashBeforeDate(gomock.Any(), gomock.Any(), snapshotTime).
			Return(snapshotCommitHash, nil)

		// Then checkout that commit
		mockGitProvider.EXPECT().
			Checkout(gomock.Any(), gomock.Any(), snapshotCommitHash).
			Return(nil)

		// Extractor succeeds
		mockExtractor.EXPECT().
			ExtractSourcesFromRepo(gomock.Any(), gomock.Any(), testPackageName, gomock.Any()).
			Return(nil)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.NoError(t, err)
	})

	t.Run("uses HEAD when no snapshot time configured", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		// Create provider
		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
			// No Snapshot in UpstreamDistro
		})

		// Clone succeeds
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, repoURL, cloneDir string, opts ...git.GitOptions) error {
				specPath := cloneDir + "/" + testPackageName + ".spec"

				return fileutils.WriteFile(env.FS(), specPath, []byte("Name: "+testPackageName), fileperms.PublicFile)
			})

		// Should NOT call GetCommitHashBeforeDate or Checkout - uses HEAD from clone

		// Extractor succeeds
		mockExtractor.EXPECT().
			ExtractSourcesFromRepo(gomock.Any(), gomock.Any(), testPackageName, gomock.Any()).
			Return(nil)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.NoError(t, err)
	})

	t.Run("snapshot time GetCommitHashBeforeDate failure propagates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		snapshotTimeStr := "2020-01-01T00:00:00Z" // Very old date
		snapshotTime, _ := time.Parse(time.RFC3339, snapshotTimeStr)

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
			Spec: projectconfig.SpecSource{
				UpstreamDistro: projectconfig.DistroReference{
					Name:     "test-distro",
					Version:  "1",
					Snapshot: snapshotTimeStr,
				},
			},
		})

		// Clone succeeds
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			Return(nil)

		// GetCommitHashBeforeDate fails (no commits before snapshot time)
		hashError := errors.New("no commits found before date")
		mockGitProvider.EXPECT().
			GetCommitHashBeforeDate(gomock.Any(), gomock.Any(), snapshotTime).
			Return("", hashError)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get commit hash for snapshot time")
		assert.ErrorIs(t, err, hashError)
	})

	t.Run("snapshot time checkout failure propagates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		snapshotTimeStr := "2026-01-10T11:11:30Z"
		snapshotTime, _ := time.Parse(time.RFC3339, snapshotTimeStr)
		snapshotCommitHash := "snapshot789abc"

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
			Spec: projectconfig.SpecSource{
				UpstreamDistro: projectconfig.DistroReference{
					Name:     "test-distro",
					Version:  "1",
					Snapshot: snapshotTimeStr,
				},
			},
		})

		// Clone succeeds
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			Return(nil)

		// GetCommitHashBeforeDate succeeds
		mockGitProvider.EXPECT().
			GetCommitHashBeforeDate(gomock.Any(), gomock.Any(), snapshotTime).
			Return(snapshotCommitHash, nil)

		// But Checkout fails
		checkoutError := errors.New("checkout failed")
		mockGitProvider.EXPECT().
			Checkout(gomock.Any(), gomock.Any(), snapshotCommitHash).
			Return(checkoutError)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to checkout snapshot commit")
		assert.ErrorIs(t, err, checkoutError)
	})

	t.Run("invalid snapshot time format returns error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
			Spec: projectconfig.SpecSource{
				UpstreamDistro: projectconfig.DistroReference{
					Name:     "test-distro",
					Version:  "1",
					Snapshot: "invalid-date-format", // Bad format
				},
			},
		})

		// Clone succeeds
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			Return(nil)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid snapshot time")
	})
}

func TestCheckoutTargetCommit_UpstreamCommit(t *testing.T) {
	env := testutils.NewTestEnv(t)

	t.Run("uses upstream commit when configured", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		upstreamCommitHash := testUpstreamCommit

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
			Spec: projectconfig.SpecSource{
				UpstreamCommit: upstreamCommitHash,
			},
		})

		// Clone succeeds
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, repoURL, cloneDir string, opts ...git.GitOptions) error {
				specPath := cloneDir + "/" + testPackageName + ".spec"

				return fileutils.WriteFile(env.FS(), specPath, []byte("Name: "+testPackageName), fileperms.PublicFile)
			})

		// Should checkout the explicit commit hash
		mockGitProvider.EXPECT().
			Checkout(gomock.Any(), gomock.Any(), upstreamCommitHash).
			Return(nil)

		// Should NOT call GetCommitHashBeforeDate
		// (gomock will fail if it's called since we didn't set an expectation)

		// Extractor succeeds
		mockExtractor.EXPECT().
			ExtractSourcesFromRepo(gomock.Any(), gomock.Any(), testPackageName, gomock.Any()).
			Return(nil)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.NoError(t, err)
	})

	t.Run("upstream commit takes priority over snapshot", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		upstreamCommitHash := testUpstreamCommit
		snapshotTimeStr := "2026-01-10T11:11:30-08:00"

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
			Spec: projectconfig.SpecSource{
				UpstreamCommit: upstreamCommitHash,
				UpstreamDistro: projectconfig.DistroReference{
					Name:     "fedora",
					Snapshot: snapshotTimeStr,
				},
			},
		})

		// Clone succeeds
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, repoURL, cloneDir string, opts ...git.GitOptions) error {
				specPath := cloneDir + "/" + testPackageName + ".spec"

				return fileutils.WriteFile(env.FS(), specPath, []byte("Name: "+testPackageName), fileperms.PublicFile)
			})

		// Should use the explicit commit, NOT the snapshot
		mockGitProvider.EXPECT().
			Checkout(gomock.Any(), gomock.Any(), upstreamCommitHash).
			Return(nil)

		// Should NOT call GetCommitHashBeforeDate (snapshot is ignored)

		// Extractor succeeds
		mockExtractor.EXPECT().
			ExtractSourcesFromRepo(gomock.Any(), gomock.Any(), testPackageName, gomock.Any()).
			Return(nil)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.NoError(t, err)
	})

	t.Run("upstream commit checkout failure propagates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGitProvider := git_test.NewMockGitProvider(ctrl)
		mockExtractor := fedorasource_test.NewMockFedoraSourceDownloader(ctrl)

		upstreamCommitHash := testUpstreamCommit

		provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
			env.FS(),
			env.DryRunnable,
			mockGitProvider,
			mockExtractor,
			mockDistroResolver(),
			retry.Disabled(),
		)
		require.NoError(t, err)

		mockComponent := components_testutils.NewMockComponent(ctrl)
		mockComponent.EXPECT().GetName().AnyTimes().Return(testPackageName)
		mockComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
			Name: testPackageName,
			Spec: projectconfig.SpecSource{
				UpstreamCommit: upstreamCommitHash,
			},
		})

		// Clone succeeds
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			Return(nil)

		// Checkout fails
		checkoutError := errors.New("commit not found")
		mockGitProvider.EXPECT().
			Checkout(gomock.Any(), gomock.Any(), upstreamCommitHash).
			Return(checkoutError)

		err = provider.GetComponent(context.Background(), mockComponent, destDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to checkout upstream commit")
		assert.ErrorIs(t, err, checkoutError)
	})
}
