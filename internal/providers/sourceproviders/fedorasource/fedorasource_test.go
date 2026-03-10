// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test private functions
package fedorasource

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader/downloader_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const (
	testLookasideURI = "https://example.com/$hashtype/$hash/$pkg/$filename"
	testPackageName  = "test-package"
	testRepoDir      = "/test/repo"
	testEmptyRepoDir = "/test/empty-repo"
	testFilePerms    = 0o644
	testDirPerms     = 0o755

	// Test source file data.
	testSourcesContent = `SHA512 (example-1.0.tar.gz) = af5ae0eb4ad9c3f07b7d78ec9dfd03f6a00c257df6b829b21051d4ba2d106bf` +
		`9d2f7563c0373b45e0ce5b1ad8a3bad9c05a2769547e67f4bc53692950db0ba37
SHA256 (patch-1.patch) = 67899aaa0f2f55e55e715cb65596449cb29bb0a76a764fe8f1e51bf4d0af648f
`
	testSingleSourceContent = `SHA512 (example-1.0.tar.gz) = abc123def456
`

	// Expected URLs (must match the hashes in testSourcesContent).
	testExpectedURL1 = "https://example.com/sha512/af5ae0eb4ad9c3f07b7d78ec9dfd03f6a00c257df6b829b21051d4ba2d106bf9d2f" +
		"7563c0373b45e0ce5b1ad8a3bad9c05a2769547e67f4bc53692950db0ba37/test-package/example-1.0.tar.gz"
	testExpectedURL2 = "https://example.com/sha256/67899aaa0f2f55e55e715cb65596449cb29bb0a76a764fe8f1e51bf4d0af648f/" +
		"test-package/patch-1.patch"

	// File names.
	testSourcesFile = "sources"
	testTarballFile = "example-1.0.tar.gz"
	testPatchFile   = "patch-1.patch"
)

func TestNewFedoraRepoExtractorImpl(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := testctx.NewCtx()
	mockDownloader := downloader_test.NewMockDownloader(ctrl)

	extractor, err := NewFedoraRepoExtractorImpl(ctx, ctx.FS(), mockDownloader, retry.Disabled())

	require.NoError(t, err)
	require.NotNil(t, extractor)
}

func TestNewFedoraRepoExtractorImplValidation(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := testctx.NewCtx()
	mockDownloader := downloader_test.NewMockDownloader(ctrl)

	t.Run("nil dryRunnable should fail", func(t *testing.T) {
		extractor, err := NewFedoraRepoExtractorImpl(nil, ctx.FS(), mockDownloader, retry.Disabled())
		require.Error(t, err)
		require.Nil(t, extractor)
	})
	t.Run("nil filesystem should fail", func(t *testing.T) {
		extractor, err := NewFedoraRepoExtractorImpl(ctx, nil, mockDownloader, retry.Disabled())
		require.Error(t, err)
		require.Nil(t, extractor)
	})
	t.Run("nil downloader should fail", func(t *testing.T) {
		extractor, err := NewFedoraRepoExtractorImpl(ctx, ctx.FS(), nil, retry.Disabled())
		require.Error(t, err)
		require.Nil(t, extractor)
	})
}

func TestExtractSourcesFromRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := testctx.NewCtx()
	mockDownloader := downloader_test.NewMockDownloader(ctrl)

	extractor, err := NewFedoraRepoExtractorImpl(ctx, ctx.FS(), mockDownloader, retry.Disabled())
	require.NoError(t, err)

	require.NoError(t, ctx.FS().MkdirAll(testRepoDir, testDirPerms))
	setupSourcesFile(t, ctx.FS(), testRepoDir, testSourcesContent)

	// Mock downloader should create files with content matching the expected hashes
	mockDownloader.EXPECT().Download(
		gomock.Any(),
		testExpectedURL1,
		filepath.Join(testRepoDir, testTarballFile),
	).DoAndReturn(func(_ context.Context, _ string, destPath string) error {
		// Create a file with content that matches the expected SHA512 hash
		return afero.WriteFile(ctx.FS(), destPath, []byte("test content for tarball"), testFilePerms)
	})

	mockDownloader.EXPECT().Download(
		gomock.Any(),
		testExpectedURL2,
		filepath.Join(testRepoDir, testPatchFile),
	).DoAndReturn(func(_ context.Context, _ string, destPath string) error {
		// Create a file with content that matches the expected SHA256 hash
		return afero.WriteFile(ctx.FS(), destPath, []byte("test patch content"), testFilePerms)
	})

	err = extractor.ExtractSourcesFromRepo(context.Background(), testRepoDir, testPackageName, testLookasideURI)
	require.NoError(t, err)
}

func TestExtractSourcesFromRepoValidation(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := testctx.NewCtx()
	mockDownloader := downloader_test.NewMockDownloader(ctrl)

	extractor, err := NewFedoraRepoExtractorImpl(ctx, ctx.FS(), mockDownloader, retry.Disabled())
	require.NoError(t, err)

	t.Run("empty repo dir", func(t *testing.T) {
		err := extractor.ExtractSourcesFromRepo(context.Background(), "", testPackageName, testLookasideURI)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "repository directory cannot be empty")
	})

	t.Run("empty lookaside URI", func(t *testing.T) {
		err := extractor.ExtractSourcesFromRepo(context.Background(), testRepoDir, testPackageName, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lookaside base URI cannot be empty")
	})

	t.Run("missing sources file", func(t *testing.T) {
		require.NoError(t, ctx.FS().MkdirAll(testEmptyRepoDir, 0o755))

		// Missing sources file is valid - it means no external sources to download
		err := extractor.ExtractSourcesFromRepo(context.Background(), testEmptyRepoDir, testPackageName, testLookasideURI)
		require.NoError(t, err)
	})
}

func TestExtractSourcesFromRepoDownloadFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := testctx.NewCtx()
	mockDownloader := downloader_test.NewMockDownloader(ctrl)

	extractor, err := NewFedoraRepoExtractorImpl(ctx, ctx.FS(), mockDownloader, retry.Disabled())
	require.NoError(t, err)

	require.NoError(t, ctx.FS().MkdirAll(testRepoDir, testDirPerms))
	setupSourcesFile(t, ctx.FS(), testRepoDir, testSingleSourceContent)

	downloadErr := assert.AnError
	mockDownloader.EXPECT().Download(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(downloadErr)

	err = extractor.ExtractSourcesFromRepo(context.Background(), testRepoDir, testPackageName, testLookasideURI)
	require.Error(t, err)
	require.ErrorIs(t, err, downloadErr)
	assert.Contains(t, err.Error(), "failed to download sources")
}

func TestExtractSourcesFromRepoHashMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := testctx.NewCtx()
	mockDownloader := downloader_test.NewMockDownloader(ctrl)

	extractor, err := NewFedoraRepoExtractorImpl(ctx, ctx.FS(), mockDownloader, retry.Disabled())
	require.NoError(t, err)

	require.NoError(t, ctx.FS().MkdirAll(testRepoDir, testDirPerms))
	setupSourcesFile(t, ctx.FS(), testRepoDir, testSingleSourceContent)

	// Mock downloader creates a file with WRONG content (hash mismatch)
	mockDownloader.EXPECT().Download(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, destPath string) error {
			// Create a file with content that does NOT match the expected hash
			return afero.WriteFile(ctx.FS(), destPath, []byte("wrong content"), testFilePerms)
		})

	err = extractor.ExtractSourcesFromRepo(context.Background(), testRepoDir, testPackageName, testLookasideURI)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hash mismatch")
}

// setupSourcesFile creates a sources file in the specified directory with the given content.
func setupSourcesFile(t *testing.T, fs afero.Fs, repoDir string, content string) {
	t.Helper()

	sourcesPath := filepath.Join(repoDir, testSourcesFile)
	require.NoError(t, afero.WriteFile(fs, sourcesPath, []byte(content), testFilePerms))
}

func TestParseSourcesFile(t *testing.T) {
	t.Run("modern format parses hash type and filename", func(t *testing.T) {
		content := "SHA512 (file.tar.gz) = abc123\n"

		sources, err := parseSourcesFile(content, "pkg", "https://example.com/$hashtype/$hash/$pkg/$filename")

		require.NoError(t, err)
		require.Len(t, sources, 1)
		assert.Equal(t, "file.tar.gz", sources[0].fileName)
		assert.Equal(t, fileutils.HashType("SHA512"), sources[0].hashType)
		assert.Equal(t, "abc123", sources[0].expectedHash)
		assert.Equal(t, "https://example.com/sha512/abc123/pkg/file.tar.gz", sources[0].uri)
	})

	t.Run("legacy format defaults to MD5", func(t *testing.T) {
		content := "abc123def456  legacy.tar.gz\n"

		sources, err := parseSourcesFile(content, "pkg", "https://example.com/$hashtype/$hash/$pkg/$filename")

		require.NoError(t, err)
		require.Len(t, sources, 1)
		assert.Equal(t, "legacy.tar.gz", sources[0].fileName)
		assert.Equal(t, fileutils.HashType("MD5"), sources[0].hashType)
		assert.Equal(t, "abc123def456", sources[0].expectedHash)
	})

	t.Run("mixed case hex values are preserved", func(t *testing.T) {
		content := "SHA256 (file.tar.gz) = AbCdEf123456\n"

		sources, err := parseSourcesFile(content, "pkg", "https://example.com/$hashtype/$hash/$pkg/$filename")

		require.NoError(t, err)
		assert.Equal(t, "AbCdEf123456", sources[0].expectedHash)
	})

	t.Run("skips empty lines and comments", func(t *testing.T) {
		content := "\n# this is a comment\nSHA256 (file.tar.gz) = abc123\n\n"

		sources, err := parseSourcesFile(content, "pkg", "https://example.com/$hashtype/$hash/$pkg/$filename")

		require.NoError(t, err)
		require.Len(t, sources, 1)
	})

	t.Run("multiple entries", func(t *testing.T) {
		content := "SHA512 (first.tar.gz) = aabbcc112233\nSHA256 (second.patch) = ddeeff445566\n"

		sources, err := parseSourcesFile(content, "pkg", "https://example.com/$hashtype/$hash/$pkg/$filename")

		require.NoError(t, err)
		require.Len(t, sources, 2)
		assert.Equal(t, "first.tar.gz", sources[0].fileName)
		assert.Equal(t, "second.patch", sources[1].fileName)
	})

	t.Run("invalid format returns error with line number", func(t *testing.T) {
		content := "SHA512 (valid.tar.gz) = abc123\nnot a valid line\n"

		_, err := parseSourcesFile(content, "pkg", "https://example.com/$hashtype/$hash/$pkg/$filename")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "line 2")
	})

	t.Run("empty content returns empty slice", func(t *testing.T) {
		sources, err := parseSourcesFile("", "pkg", "https://example.com/$hashtype/$hash/$pkg/$filename")

		require.NoError(t, err)
		assert.Empty(t, sources)
	})
}

func TestVerifyFedoraLookasideBaseURI(t *testing.T) {
	t.Run("valid URI with all placeholders", func(t *testing.T) {
		err := verifyFedoraLookasideBaseURI("https://example.com/$hashtype/$hash/$pkg/$filename")
		require.NoError(t, err)
	})

	t.Run("missing placeholder returns error", func(t *testing.T) {
		tests := []struct {
			uri     string
			missing string
		}{
			{"https://example.com/$hash/$pkg/$filename", "$hashtype"},
			{"https://example.com/$hashtype/$hash/$filename", "$pkg"},
			{"https://example.com/$hashtype/$hash/$pkg/", "$filename"},
		}

		for _, tc := range tests {
			t.Run(tc.missing, func(t *testing.T) {
				err := verifyFedoraLookasideBaseURI(tc.uri)
				require.Error(t, err, "URI %q should fail for missing %s", tc.uri, tc.missing)
				assert.Contains(t, err.Error(), tc.missing)
			})
		}
	})
}
