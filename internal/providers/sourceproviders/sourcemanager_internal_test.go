// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader/downloader_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type regeneratingFileProvider struct {
	fs     opctx.FS
	called bool
}

func (p *regeneratingFileProvider) GetFile(
	_ context.Context,
	_ components.Component,
	ref projectconfig.SourceFileReference,
	destDirPath string,
) error {
	p.called = true

	return fileutils.WriteFile(p.fs, filepath.Join(destDirPath, ref.Filename), nil, fileperms.PublicFile)
}

func TestFetchSourceFile_ConfiguredSourceReplacesExistingFile(t *testing.T) {
	const (
		destDir  = "/output"
		filename = "source.tar.gz"
		// SHA-256 of the empty replacement produced by each test acquisition path.
		emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	)

	tests := []struct {
		name        string
		origin      projectconfig.Origin
		useProvider bool
		downloadURL string
	}{
		{
			name:        "custom source is always regenerated",
			origin:      projectconfig.Origin{Type: projectconfig.OriginTypeCustom},
			useProvider: true,
		},
		{
			name: "download source replaces upstream file",
			origin: projectconfig.Origin{
				Type: projectconfig.OriginTypeURI,
				Uri:  "https://origin.example.com/source.tar.gz",
			},
			downloadURL: "https://example.com/test-component/source.tar.gz/sha256/" +
				emptyHash + "/source.tar.gz",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := testctx.NewCtx()
			ctrl := gomock.NewController(t)
			component := components_testutils.NewMockComponent(ctrl)
			component.EXPECT().GetName().AnyTimes().Return("test-component")
			component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{})

			httpDownloader := downloader_test.NewMockDownloader(ctrl)
			if testCase.downloadURL != "" {
				httpDownloader.EXPECT().
					Download(gomock.Any(), testCase.downloadURL, filepath.Join(destDir, filename)).
					DoAndReturn(func(_ context.Context, _, destPath string) error {
						return fileutils.WriteFile(ctx.FS(), destPath, nil, fileperms.PublicFile)
					})
			}

			provider := &regeneratingFileProvider{fs: ctx.FS()}

			var fileProviders []FileSourceProvider

			if testCase.useProvider {
				fileProviders = []FileSourceProvider{provider}
			}

			manager := &sourceManager{
				dryRunnable:      ctx,
				fs:               ctx.FS(),
				lookasideBaseURI: "https://example.com/$pkg/$filename/$hashtype/$hash/$filename",
				retryConfig:      retry.Disabled(),
				fileProviders:    fileProviders,
			}

			require.NoError(t, fileutils.MkdirAll(ctx.FS(), destDir))
			require.NoError(t, fileutils.WriteFile(
				ctx.FS(), filepath.Join(destDir, filename), []byte("upstream content"), fileperms.PublicFile))

			ref := &projectconfig.SourceFileReference{
				Filename:        filename,
				Hash:            emptyHash,
				HashType:        fileutils.HashTypeSHA256,
				Origin:          testCase.origin,
				ReplaceUpstream: true,
				ReplaceReason:   "use configured replacement",
			}

			require.NoError(t, manager.fetchSourceFile(t.Context(), httpDownloader, component, ref, destDir))
			assert.Equal(t, testCase.useProvider, provider.called)

			content, err := fileutils.ReadFile(ctx.FS(), filepath.Join(destDir, filename))
			require.NoError(t, err)
			assert.Empty(t, content)
		})
	}
}
