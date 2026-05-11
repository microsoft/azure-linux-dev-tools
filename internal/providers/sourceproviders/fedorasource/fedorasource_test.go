// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test private functions
package fedorasource

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader/downloader_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
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
	testFilePerms    = fileperms.PublicFile
	testDirPerms     = fileperms.PublicDir

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

	err = extractor.ExtractSourcesFromRepo(context.Background(), testRepoDir, testPackageName, testLookasideURI, nil)
	require.NoError(t, err)
}

func TestExtractSourcesFromRepoValidation(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := testctx.NewCtx()
	mockDownloader := downloader_test.NewMockDownloader(ctrl)

	extractor, err := NewFedoraRepoExtractorImpl(ctx, ctx.FS(), mockDownloader, retry.Disabled())
	require.NoError(t, err)

	t.Run("empty repo dir", func(t *testing.T) {
		err := extractor.ExtractSourcesFromRepo(context.Background(), "", testPackageName, testLookasideURI, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "repository directory cannot be empty")
	})

	t.Run("empty lookaside URI", func(t *testing.T) {
		err := extractor.ExtractSourcesFromRepo(context.Background(), testRepoDir, testPackageName, "", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lookaside base URI cannot be empty")
	})

	t.Run("missing 'sources' file", func(t *testing.T) {
		require.NoError(t, ctx.FS().MkdirAll(testEmptyRepoDir, fileperms.PublicDir))

		// Missing 'sources' file is valid - it means no external sources to download
		err := extractor.ExtractSourcesFromRepo(
			context.Background(), testEmptyRepoDir, testPackageName, testLookasideURI, nil,
		)
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

	err = extractor.ExtractSourcesFromRepo(context.Background(), testRepoDir, testPackageName, testLookasideURI, nil)
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

	err = extractor.ExtractSourcesFromRepo(context.Background(), testRepoDir, testPackageName, testLookasideURI, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hash mismatch")
}

// setupSourcesFile creates a 'sources' file in the specified directory with the given content.
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
		assert.Equal(t, fileutils.HashTypeSHA512, sources[0].hashType)
		assert.Equal(t, "abc123", sources[0].expectedHash)
		assert.Equal(t, "https://example.com/sha512/abc123/pkg/file.tar.gz", sources[0].uri)
	})

	t.Run("legacy format defaults to MD5", func(t *testing.T) {
		content := "abc123def456  legacy.tar.gz\n"

		sources, err := parseSourcesFile(content, "pkg", "https://example.com/$hashtype/$hash/$pkg/$filename")

		require.NoError(t, err)
		require.Len(t, sources, 1)
		assert.Equal(t, "legacy.tar.gz", sources[0].fileName)
		assert.Equal(t, fileutils.HashTypeMD5, sources[0].hashType)
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

	t.Run("path traversal filename is rejected", func(t *testing.T) {
		content := "SHA512 (../../etc/passwd) = abc123\n"

		_, err := parseSourcesFile(content, "pkg", "https://example.com/$hashtype/$hash/$pkg/$filename")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsafe filename")
	})

	t.Run("absolute path filename is rejected", func(t *testing.T) {
		content := "SHA512 (/etc/passwd) = abc123\n"

		_, err := parseSourcesFile(content, "pkg", "https://example.com/$hashtype/$hash/$pkg/$filename")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsafe filename")
	})
}

func TestReadSourcesFile(t *testing.T) {
	t.Run("empty content yields nil slice", func(t *testing.T) {
		lines, err := ReadSourcesFile("")

		require.NoError(t, err)
		assert.Nil(t, lines)
	})

	t.Run("preserves comments and blank lines verbatim alongside entries", func(t *testing.T) {
		// Each element is one line of the input 'sources' file. Joining with "\n"
		// plus a trailing "\n" yields the on-disk form, while keeping the per-line
		// view that lets us assert lines[i].Raw == content[i] in a loop.
		content := []string{
			"# top comment",
			"",
			"SHA512 (one.tar.gz) = aabbcc",
			"# inline comment",
			"",
			"SHA256 (two.patch) = ddeeff",
		}

		lines, err := ReadSourcesFile(strings.Join(content, "\n") + "\n")

		require.NoError(t, err)
		require.Len(t, lines, len(content))

		// Every line's Raw text must be preserved verbatim, in order.
		for i, expectedRaw := range content {
			assert.Equal(t, expectedRaw, lines[i].Raw, "lines[%d].Raw mismatch", i)
		}

		// Comment and blank lines have no parsed entry.
		assert.Nil(t, lines[0].Entry, "top-level comment must not have an Entry")
		assert.Nil(t, lines[1].Entry, "leading blank line must not have an Entry")
		assert.Nil(t, lines[3].Entry, "inline comment must not have an Entry")
		assert.Nil(t, lines[4].Entry, "blank line between entries must not have an Entry")

		// Entry lines have a fully-populated parsed entry.
		require.NotNil(t, lines[2].Entry)
		assert.Equal(t, "one.tar.gz", lines[2].Entry.Filename)
		assert.Equal(t, fileutils.HashTypeSHA512, lines[2].Entry.HashType)
		assert.Equal(t, "aabbcc", lines[2].Entry.Hash)

		require.NotNil(t, lines[5].Entry)
		assert.Equal(t, "two.patch", lines[5].Entry.Filename)
		assert.Equal(t, fileutils.HashTypeSHA256, lines[5].Entry.HashType)
		assert.Equal(t, "ddeeff", lines[5].Entry.Hash)
	})

	t.Run("drops trailing empty element produced by terminating newline", func(t *testing.T) {
		// "foo\n" splits to ["foo", ""]. Re-joining ["foo"] with "\n" + final "\n" gives
		// "foo\n" (well-formed). If the trailing "" weren't dropped, the result would be
		// "foo\n\n" — a doubled blank line at the end.
		lines, err := ReadSourcesFile("SHA512 (only.tar.gz) = abc123\n")

		require.NoError(t, err)
		require.Len(t, lines, 1)
		assert.Equal(t, "SHA512 (only.tar.gz) = abc123", lines[0].Raw)
		require.NotNil(t, lines[0].Entry)
	})

	t.Run("preserves an intentional blank line near EOF when content ends with a newline", func(t *testing.T) {
		// "a\n\n" splits to ["a", "", ""]; the trailing "" is dropped, leaving the
		// intentional middle blank line intact.
		lines, err := ReadSourcesFile("SHA512 (a.tar.gz) = abc\n\n")

		require.NoError(t, err)
		require.Len(t, lines, 2)
		assert.Equal(t, "SHA512 (a.tar.gz) = abc", lines[0].Raw)
		require.NotNil(t, lines[0].Entry)
		assert.Empty(t, lines[1].Raw)
		assert.Nil(t, lines[1].Entry)
	})

	t.Run("malformed line returns error citing the line number and inner cause", func(t *testing.T) {
		content := "SHA512 (good.tar.gz) = abc123\n" +
			"this is not a valid entry line\n"

		lines, err := ReadSourcesFile(content)

		require.Error(t, err)
		assert.Nil(t, lines)
		// Outer wrapping from ReadSourcesFile.
		assert.Contains(t, err.Error(), "invalid entry in 'sources' file")
		assert.Contains(t, err.Error(), "line 2")
		// Inner cause from parseSourcesEntryLine, including the offending text.
		assert.Contains(t, err.Error(), "does not match")
		assert.Contains(t, err.Error(), "this is not a valid entry line")
	})

	t.Run("duplicate filename is an error citing both line numbers", func(t *testing.T) {
		content := "SHA512 (dup.tar.gz) = aaaa\n" +
			"# spacer comment\n" +
			"SHA512 (dup.tar.gz) = bbbb\n"

		lines, err := ReadSourcesFile(content)

		require.Error(t, err)
		assert.Nil(t, lines)
		assert.Contains(t, err.Error(), "duplicate filename")
		assert.Contains(t, err.Error(), "dup.tar.gz")
		// The intervening comment line counts toward the line numbering.
		assert.Contains(t, err.Error(), "line 3")
		assert.Contains(t, err.Error(), "line 1")
	})
}

func TestIsBlankOrComment(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{name: "empty string is blank", line: "", want: true},
		{name: "whitespace-only is blank", line: "   \t  ", want: true},
		{name: "leading hash is comment", line: "# hello", want: true},
		{name: "indented hash is comment", line: "   # indented", want: true},
		{name: "tab-indented hash is comment", line: "\t# tabbed", want: true},
		{name: "valid modern entry is not blank/comment", line: "SHA512 (a.tar.gz) = abc", want: false},
		{name: "valid legacy entry is not blank/comment", line: "abc123  a.tar.gz", want: false},
		{name: "hash mid-line is not comment", line: "SHA512 # not a comment", want: false},
		{name: "garbage non-empty line is not blank/comment", line: "garbage here", want: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, isBlankOrComment(testCase.line))
		})
	}
}

func TestParseSourcesEntryLine(t *testing.T) {
	tests := []struct {
		name              string
		line              string
		wantErr           bool
		wantErrSubstrings []string
		wantFilename      string
		wantHashType      fileutils.HashType
		wantHash          string
	}{
		{
			name:         "modern SHA512 entry parses",
			line:         "SHA512 (file.tar.gz) = abc123",
			wantFilename: "file.tar.gz",
			wantHashType: fileutils.HashTypeSHA512,
			wantHash:     "abc123",
		},
		{
			name:         "modern SHA256 entry parses",
			line:         "SHA256 (patch-1.patch) = deadbeef",
			wantFilename: "patch-1.patch",
			wantHashType: fileutils.HashTypeSHA256,
			wantHash:     "deadbeef",
		},
		{
			name:         "legacy MD5 entry parses with implicit MD5 hash type",
			line:         "abc123def456  legacy.tar.gz",
			wantFilename: "legacy.tar.gz",
			wantHashType: fileutils.HashTypeMD5,
			wantHash:     "abc123def456",
		},
		{
			name:         "leading and trailing whitespace are trimmed before matching",
			line:         "   SHA512 (file.tar.gz) = abc123   ",
			wantFilename: "file.tar.gz",
			wantHashType: fileutils.HashTypeSHA512,
			wantHash:     "abc123",
		},
		{
			name:              "empty line is rejected (caller is expected to skip blanks first)",
			line:              "",
			wantErr:           true,
			wantErrSubstrings: []string{"does not match"},
		},
		{
			name:              "comment line is rejected (caller is expected to skip comments first)",
			line:              "# a comment",
			wantErr:           true,
			wantErrSubstrings: []string{"does not match"},
		},
		{
			name:              "garbage text is rejected with the offending line in the error",
			line:              "this is not a 'sources' line",
			wantErr:           true,
			wantErrSubstrings: []string{"does not match", "this is not a 'sources' line"},
		},
		{
			name:              "modern format with non-hex hash is rejected",
			line:              "SHA512 (file.tar.gz) = not-hex-here",
			wantErr:           true,
			wantErrSubstrings: []string{"does not match"},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			entry, err := parseSourcesEntryLine(testCase.line)

			if testCase.wantErr {
				require.Error(t, err)
				assert.Equal(t, SourcesFileEntry{}, entry)

				for _, substring := range testCase.wantErrSubstrings {
					assert.Contains(t, err.Error(), substring)
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantFilename, entry.Filename)
			assert.Equal(t, testCase.wantHashType, entry.HashType)
			assert.Equal(t, testCase.wantHash, entry.Hash)
		})
	}
}

func TestBuildLookasideURL(t *testing.T) {
	tests := []struct {
		name          string
		template      string
		pkg           string
		filename      string
		hashType      string
		hash          string
		expected      string
		expectedError string
	}{
		{
			name:     "standard template",
			template: "https://example.com/repo/pkgs/$pkg/$filename/$hashtype/$hash/$filename",
			pkg:      "my-pkg",
			filename: "source.tar.gz",
			hashType: "SHA512",
			hash:     "abc123",
			expected: "https://example.com/repo/pkgs/my-pkg/source.tar.gz/sha512/abc123/source.tar.gz",
		},
		{
			name:     "different placeholder order",
			template: "https://example.com/$hashtype/$hash/$pkg/$filename",
			pkg:      "test-pkg",
			filename: "file.tar.gz",
			hashType: "SHA256",
			hash:     "def456",
			expected: "https://example.com/sha256/def456/test-pkg/file.tar.gz",
		},
		{
			name:     "template without filename placeholder",
			template: "https://example.com/$pkg/$hashtype/$hash",
			pkg:      "my-pkg",
			filename: "source.tar.gz",
			hashType: "SHA512",
			hash:     "abc123",
			expected: "https://example.com/my-pkg/sha512/abc123",
		},
		{
			name:          "packageName containing placeholder",
			template:      "https://example.com/$pkg/$filename/$hashtype/$hash",
			pkg:           "evil-$filename-pkg",
			filename:      "source.tar.gz",
			hashType:      "SHA512",
			hash:          "abc123",
			expectedError: "ambiguous substitution",
		},
		{
			name:          "fileName containing placeholder",
			template:      "https://example.com/$pkg/$filename/$hashtype/$hash",
			pkg:           "my-pkg",
			filename:      "$hash-source.tar.gz",
			hashType:      "SHA512",
			hash:          "abc123",
			expectedError: "ambiguous substitution",
		},
		{
			name:     "filename with slash is path-escaped",
			template: "https://example.com/$pkg/$filename/$hashtype/$hash",
			pkg:      "my-pkg",
			filename: "foo/bar",
			hashType: "SHA512",
			hash:     "abc123",
			expected: "https://example.com/my-pkg/foo%2Fbar/sha512/abc123",
		},
		{
			name:     "filename with question mark is path-escaped",
			template: "https://example.com/$pkg/$filename/$hashtype/$hash",
			pkg:      "my-pkg",
			filename: "file?x=1",
			hashType: "SHA512",
			hash:     "abc123",
			expected: "https://example.com/my-pkg/file%3Fx=1/sha512/abc123",
		},
		{
			name:     "filename with hash is path-escaped",
			template: "https://example.com/$pkg/$filename/$hashtype/$hash",
			pkg:      "my-pkg",
			filename: "file#frag",
			hashType: "SHA512",
			hash:     "abc123",
			expected: "https://example.com/my-pkg/file%23frag/sha512/abc123",
		},
		{
			name:     "filename with malformed percent is path-escaped",
			template: "https://example.com/$pkg/$filename/$hashtype/$hash",
			pkg:      "my-pkg",
			filename: "file%zz",
			hashType: "SHA512",
			hash:     "abc123",
			expected: "https://example.com/my-pkg/file%25zz/sha512/abc123",
		},
		{
			name:     "packageName with slash is path-escaped",
			template: "https://example.com/$pkg/$filename/$hashtype/$hash",
			pkg:      "foo/bar",
			filename: "source.tar.gz",
			hashType: "SHA512",
			hash:     "abc123",
			expected: "https://example.com/foo%2Fbar/source.tar.gz/sha512/abc123",
		},
		{
			name:     "packageName with hash is path-escaped",
			template: "https://example.com/$pkg/$filename/$hashtype/$hash",
			pkg:      "foo#bar",
			filename: "source.tar.gz",
			hashType: "SHA512",
			hash:     "abc123",
			expected: "https://example.com/foo%23bar/source.tar.gz/sha512/abc123",
		},
		{
			name:          "hashType containing uppercase placeholder is caught after lowercasing",
			template:      "https://example.com/$pkg/$filename/$hashtype/$hash",
			pkg:           "my-pkg",
			filename:      "source.tar.gz",
			hashType:      "$PKG",
			hash:          "abc123",
			expectedError: "ambiguous substitution",
		},
		{
			name:          "template without scheme is rejected",
			template:      "example.com/$pkg/$filename/$hashtype/$hash",
			pkg:           "my-pkg",
			filename:      "source.tar.gz",
			hashType:      "SHA512",
			hash:          "abc123",
			expectedError: "missing scheme or host",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := BuildLookasideURL(
				testCase.template, testCase.pkg, testCase.filename, testCase.hashType, testCase.hash,
			)
			if testCase.expectedError != "" {
				assert.ErrorContains(t, err, testCase.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, result)
			}
		})
	}
}

func TestBuildDistGitURL(t *testing.T) {
	tests := []struct {
		name          string
		template      string
		pkg           string
		expected      string
		expectedError string
	}{
		{
			name:     "standard template",
			template: "https://src.example.com/rpms/$pkg.git",
			pkg:      "curl",
			expected: "https://src.example.com/rpms/curl.git",
		},
		{
			name:          "packageName containing $pkg placeholder",
			template:      "https://src.example.com/rpms/$pkg.git",
			pkg:           "evil-$pkg-name",
			expectedError: "ambiguous substitution",
		},
		{
			name:     "packageName with slash is path-escaped",
			template: "https://src.example.com/rpms/$pkg.git",
			pkg:      "foo/bar",
			expected: "https://src.example.com/rpms/foo%2Fbar.git",
		},
		{
			name:     "packageName with hash is path-escaped",
			template: "https://src.example.com/rpms/$pkg.git",
			pkg:      "foo#bar",
			expected: "https://src.example.com/rpms/foo%23bar.git",
		},
		{
			name:     "packageName with question mark is path-escaped",
			template: "https://src.example.com/rpms/$pkg.git",
			pkg:      "foo?bar",
			expected: "https://src.example.com/rpms/foo%3Fbar.git",
		},
		{
			name:     "packageName with malformed percent is path-escaped",
			template: "https://src.example.com/rpms/$pkg.git",
			pkg:      "foo%zz",
			expected: "https://src.example.com/rpms/foo%25zz.git",
		},
		{
			name:          "template without scheme is rejected",
			template:      "example.com/rpms/$pkg.git",
			pkg:           "curl",
			expectedError: "missing scheme or host",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := BuildDistGitURL(testCase.template, testCase.pkg)
			if testCase.expectedError != "" {
				assert.ErrorContains(t, err, testCase.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, result)
			}
		})
	}
}

func TestFormatSourcesEntry(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		hashType fileutils.HashType
		hash     string
		expected string
	}{
		{
			name:     "SHA512 format",
			filename: "example-1.0.tar.gz",
			hashType: fileutils.HashTypeSHA512,
			hash:     "abc123def456",
			expected: "SHA512 (example-1.0.tar.gz) = abc123def456",
		},
		{
			name:     "SHA256 format",
			filename: "patch-1.patch",
			hashType: fileutils.HashTypeSHA256,
			hash:     "67899aaa0f2f55e55e715cb65596449cb29bb0a76a764fe8f1e51bf4d0af648f",
			expected: "SHA256 (patch-1.patch) = 67899aaa0f2f55e55e715cb65596449cb29bb0a76a764fe8f1e51bf4d0af648f",
		},
		{
			name:     "filename with spaces",
			filename: "my file.tar.gz",
			hashType: fileutils.HashTypeSHA512,
			hash:     "xyz789",
			expected: "SHA512 (my file.tar.gz) = xyz789",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result := FormatSourcesEntry(testCase.filename, testCase.hashType, testCase.hash)
			assert.Equal(t, testCase.expected, result)
		})
	}
}
