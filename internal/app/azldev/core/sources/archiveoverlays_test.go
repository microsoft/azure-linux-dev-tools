// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/sourceproviders_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/archive"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// globRemovalTree returns the source tree shared by the file-remove glob tests.
// Every entry is a regular file (the glob matcher used by file-remove is
// files-only).
func globRemovalTree() []string {
	return []string{
		"keep.txt",
		"some_folder/a.txt",
		"some_folder/b.txt",
		"some_folder/sub/c.txt",
		"some_folder/sub/deep/d.txt",
		"top1/nested/some_file.txt",
		"top1/some_file.txt",
		"top2/some_file.txt",
	}
}

// globRemovalCase describes the expected outcome of applying a file-remove
// overlay with a given glob pattern against [globRemovalTree].
type globRemovalCase struct {
	name string
	// pattern is the in-archive / in-sources glob (no archive prefix).
	pattern string
	// wantErr is true when the pattern matches no files and the overlay errors.
	wantErr bool
	// remaining is the sorted set of regular files left after removal (equal to
	// the whole tree when wantErr is true, since nothing is removed).
	remaining []string
}

// globRemovalCases enumerates the documented file-remove glob behaviors. The
// expectations were captured against the live doublestar matcher and encode two
// behaviors worth pinning: (1) the matcher is files-only, so a bare directory
// name matches nothing, and (2) `*` matches a single path segment while `**`
// matches any depth. Directories are never removed (only files), so emptied
// directories survive a removal; these assertions look at regular files only.
func globRemovalCases() []globRemovalCase {
	return []globRemovalCase{
		{
			name:      "bare folder name matches nothing (files-only matcher)",
			pattern:   "some_folder",
			wantErr:   true,
			remaining: globRemovalTree(),
		},
		{
			name:    "single star removes immediate file children only",
			pattern: "some_folder/*",
			remaining: []string{
				"keep.txt",
				"some_folder/sub/c.txt",
				"some_folder/sub/deep/d.txt",
				"top1/nested/some_file.txt",
				"top1/some_file.txt",
				"top2/some_file.txt",
			},
		},
		{
			name:    "doublestar removes all files under the folder recursively",
			pattern: "some_folder/**",
			remaining: []string{
				"keep.txt",
				"top1/nested/some_file.txt",
				"top1/some_file.txt",
				"top2/some_file.txt",
			},
		},
		{
			name:    "single-star prefix matches only top-level folders",
			pattern: "*/some_file.txt",
			remaining: []string{
				"keep.txt",
				"some_folder/a.txt",
				"some_folder/b.txt",
				"some_folder/sub/c.txt",
				"some_folder/sub/deep/d.txt",
				"top1/nested/some_file.txt",
			},
		},
		{
			name:    "doublestar prefix matches files at any depth",
			pattern: "**/some_file.txt",
			remaining: []string{
				"keep.txt",
				"some_folder/a.txt",
				"some_folder/b.txt",
				"some_folder/sub/c.txt",
				"some_folder/sub/deep/d.txt",
			},
		},
	}
}

// writeTreeFiles materializes the given slash-separated relative file paths
// under root, each with placeholder content.
func writeTreeFiles(t *testing.T, root string, files []string) {
	t.Helper()

	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), fileperms.PublicDir))
		require.NoError(t, os.WriteFile(full, []byte("x"), fileperms.PrivateFile))
	}
}

// collectRegularFiles returns the sorted, slash-separated relative paths of all
// regular files under root (directories are ignored).
func collectRegularFiles(t *testing.T, root string) []string {
	t.Helper()

	var out []string

	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}

		out = append(out, filepath.ToSlash(rel))

		return nil
	}))

	sort.Strings(out)

	return out
}

// TestPrepareSources_RemoveFileGlob_Archive exercises archive-scoped file-remove
// globs through the real exported [sources.SourcePreparer.PrepareSources] path
// (which drives the extract/remove/repack cycle), asserting which files survive
// inside the repacked archive for each glob pattern.
func TestPrepareSources_RemoveFileGlob_Archive(t *testing.T) {
	const (
		componentName = "test-component"
		archiveName   = "pkg.tar.gz"
	)

	for _, testCase := range globRemovalCases() {
		t.Run(testCase.name, func(t *testing.T) {
			// Host FS + real temp dir: archive extraction/repacking happens on disk.
			ctx := testctx.NewCtx(testctx.WithHostFS())
			outputDir := t.TempDir()
			archivePath := filepath.Join(outputDir, archiveName)

			// Stage a tree with multiple top-level entries so the extraction root
			// is the archive root and the glob is matched relative to it.
			staging := t.TempDir()
			writeTreeFiles(t, staging, globRemovalTree())
			require.NoError(t, archive.CreateDeterministicArchive(archivePath, staging, archive.CompressionGzip))

			// Seed a 'sources' entry for the archive so the post-overlay rehash
			// has an entry to update (a missing entry is itself an error).
			originalHash, err := fileutils.ComputeFileHash(ctx.FS(), fileutils.HashTypeSHA256, archivePath)
			require.NoError(t, err)

			sourcesPath := filepath.Join(outputDir, fedorasource.SourcesFileName)
			entry := fedorasource.FormatSourcesEntry(archiveName, fileutils.HashTypeSHA256, originalHash)
			require.NoError(t, fileutils.WriteFile(
				ctx.FS(), sourcesPath, []byte(entry+"\n"), fileperms.PublicFile))

			ctrl := gomock.NewController(t)
			component := components_testutils.NewMockComponent(ctrl)
			sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)

			component.EXPECT().GetName().AnyTimes().Return(componentName)
			component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				Overlays: []projectconfig.ComponentOverlay{
					{
						Type:     projectconfig.ComponentOverlayRemoveFile,
						Archive:  archiveName,
						Filename: testCase.pattern,
					},
				},
			})

			sourceManager.EXPECT().FetchFiles(gomock.Any(), component, outputDir).Return(nil)
			sourceManager.EXPECT().FetchComponent(gomock.Any(), component, outputDir, gomock.Any()).DoAndReturn(
				func(_ interface{}, _ interface{}, dir string, _ ...sourceproviders.FetchComponentOption) error {
					return fileutils.WriteFile(
						ctx.FS(), filepath.Join(dir, componentName+".spec"),
						[]byte("# test spec"), fileperms.PublicFile)
				},
			)

			preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx, sources.WithAllowNoHashes())
			require.NoError(t, err)

			err = preparer.PrepareSources(ctx, component, outputDir, true /*applyOverlays*/)

			if testCase.wantErr {
				// A no-match pattern errors and leaves the archive untouched.
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			out := t.TempDir()
			require.NoError(t, archive.ExtractAuto(archivePath, out))
			assert.Equal(t, testCase.remaining, collectRegularFiles(t, out))
		})
	}
}

// TestApplyOverlayToSources_RemoveFileGlob_LooseFiles exercises the same glob
// patterns against loose files in the sources tree (no archive prefix) through
// the real exported [sources.ApplyOverlayToSources] entry point, confirming
// archive-scoped and loose-file removal share identical glob semantics.
func TestApplyOverlayToSources_RemoveFileGlob_LooseFiles(t *testing.T) {
	for _, testCase := range globRemovalCases() {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := testctx.NewCtx(testctx.WithHostFS())
			sourcesDir := t.TempDir()
			writeTreeFiles(t, sourcesDir, globRemovalTree())

			overlay := projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Filename: testCase.pattern,
			}

			// file-remove does not touch the spec, so specPath is unused.
			err := sources.ApplyOverlayToSources(ctx, ctx.FS(), overlay, sourcesDir, "")

			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, testCase.remaining, collectRegularFiles(t, sourcesDir))
		})
	}
}

// findSourcesEntry returns the parsed 'sources' entry for filename, or nil.
func findSourcesEntry(t *testing.T, sourcesContent, filename string) *fedorasource.SourcesFileEntry {
	t.Helper()

	parsed, err := fedorasource.ReadSourcesFile(sourcesContent)
	require.NoError(t, err)

	for i := range parsed {
		if parsed[i].Entry != nil && parsed[i].Entry.Filename == filename {
			return parsed[i].Entry
		}
	}

	return nil
}

// TestPrepareSources_SearchReplaceInArchiveRehashesEntry is an end-to-end check
// that a file-search-replace overlay scoped to an archive rewrites the file
// inside the archive, repacks it, and rehashes the matching 'sources' entry
// (preserving the original hash type). file-remove already has this coverage;
// this exercises the search-replace path through the exported PrepareSources.
func TestPrepareSources_SearchReplaceInArchiveRehashesEntry(t *testing.T) {
	const (
		componentName = "test-component"
		archiveName   = "pkg.tar.gz"
	)

	// Host FS + real temp dir: archive extraction/repacking happens on disk.
	ctx := testctx.NewCtx(testctx.WithHostFS())
	outputDir := t.TempDir()
	archivePath := filepath.Join(outputDir, archiveName)

	// Single top-level directory => extract root is "pkg-1.0/", so the inner
	// path "configure.ac" resolves relative to that root.
	staging := t.TempDir()
	pkgRoot := filepath.Join(staging, "pkg-1.0")
	require.NoError(t, os.MkdirAll(pkgRoot, fileperms.PublicDir))
	require.NoError(t, os.WriteFile(filepath.Join(pkgRoot, "configure.ac"),
		[]byte("AC_CHECK_LIB(old_lib, main)\n"), fileperms.PrivateFile))
	require.NoError(t, archive.CreateDeterministicArchive(archivePath, staging, archive.CompressionGzip))

	// Seed a SHA256 'sources' entry (not the SHA512 default) so the test also
	// proves the hash type is preserved.
	originalHash, err := fileutils.ComputeFileHash(ctx.FS(), fileutils.HashTypeSHA256, archivePath)
	require.NoError(t, err)

	sourcesPath := filepath.Join(outputDir, fedorasource.SourcesFileName)
	entry := fedorasource.FormatSourcesEntry(archiveName, fileutils.HashTypeSHA256, originalHash)
	require.NoError(t, fileutils.WriteFile(ctx.FS(), sourcesPath, []byte(entry+"\n"), fileperms.PublicFile))

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)

	component.EXPECT().GetName().AnyTimes().Return(componentName)
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
		Overlays: []projectconfig.ComponentOverlay{
			{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
				Archive:     archiveName,
				Filename:    "configure.ac",
				Regex:       "old_lib",
				Replacement: "new_lib",
			},
		},
	})

	sourceManager.EXPECT().FetchFiles(gomock.Any(), component, outputDir).Return(nil)
	sourceManager.EXPECT().FetchComponent(gomock.Any(), component, outputDir, gomock.Any()).DoAndReturn(
		func(_ interface{}, _ interface{}, dir string, _ ...sourceproviders.FetchComponentOption) error {
			return fileutils.WriteFile(ctx.FS(), filepath.Join(dir, componentName+".spec"),
				[]byte("# test spec"), fileperms.PublicFile)
		},
	)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx, sources.WithAllowNoHashes())
	require.NoError(t, err)

	require.NoError(t, preparer.PrepareSources(ctx, component, outputDir, true /*applyOverlays*/))

	// Rewrite: the file content inside the repacked archive reflects the replacement.
	out := t.TempDir()
	require.NoError(t, archive.ExtractAuto(archivePath, out))
	content, err := os.ReadFile(filepath.Join(out, "pkg-1.0", "configure.ac"))
	require.NoError(t, err)
	assert.Equal(t, "AC_CHECK_LIB(new_lib, main)\n", string(content))

	// Repack: the archive's hash changed.
	repackedHash, err := fileutils.ComputeFileHash(ctx.FS(), fileutils.HashTypeSHA256, archivePath)
	require.NoError(t, err)
	require.NotEqual(t, originalHash, repackedHash,
		"precondition: rewriting a file in the archive should change its hash")

	// Rehash: the 'sources' entry was rewritten to the repacked hash, type preserved.
	sourcesContent, err := fileutils.ReadFile(ctx.FS(), sourcesPath)
	require.NoError(t, err)

	got := findSourcesEntry(t, string(sourcesContent), archiveName)
	require.NotNil(t, got, "rewritten 'sources' file should still contain an entry for %q", archiveName)
	assert.Equal(t, fileutils.HashTypeSHA256, got.HashType, "original hash type must be preserved")
	assert.Equal(t, repackedHash, got.Hash, "'sources' entry must record the repacked archive's hash")
}

// TestPrepareSources_SkipSourcesSkipsArchiveOverlays verifies the --skip-sources
// branch: when source downloads are skipped, archive overlays are skipped (with a
// warning) instead of applied, leaving both the archive and its 'sources' entry
// untouched.
func TestPrepareSources_SkipSourcesSkipsArchiveOverlays(t *testing.T) {
	const (
		componentName = "test-component"
		archiveName   = "pkg.tar.gz"
	)

	ctx := testctx.NewCtx(testctx.WithHostFS())
	outputDir := t.TempDir()
	archivePath := filepath.Join(outputDir, archiveName)

	staging := t.TempDir()
	pkgRoot := filepath.Join(staging, "pkg-1.0")
	require.NoError(t, os.MkdirAll(pkgRoot, fileperms.PublicDir))
	require.NoError(t, os.WriteFile(filepath.Join(pkgRoot, "remove-me.txt"),
		[]byte("delete me"), fileperms.PrivateFile))
	require.NoError(t, archive.CreateDeterministicArchive(archivePath, staging, archive.CompressionGzip))

	// Snapshot the archive and seed its 'sources' entry so we can assert both
	// are left untouched.
	originalArchive, err := os.ReadFile(archivePath)
	require.NoError(t, err)

	originalHash, err := fileutils.ComputeFileHash(ctx.FS(), fileutils.HashTypeSHA256, archivePath)
	require.NoError(t, err)

	sourcesPath := filepath.Join(outputDir, fedorasource.SourcesFileName)
	entry := fedorasource.FormatSourcesEntry(archiveName, fileutils.HashTypeSHA256, originalHash)
	require.NoError(t, fileutils.WriteFile(ctx.FS(), sourcesPath, []byte(entry+"\n"), fileperms.PublicFile))

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)

	component.EXPECT().GetName().AnyTimes().Return(componentName)
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
		Overlays: []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlayRemoveFile, Archive: archiveName, Filename: "remove-me.txt"},
		},
		SourceFiles: []projectconfig.SourceFileReference{{
			Filename:        archiveName,
			Hash:            originalHash,
			HashType:        fileutils.HashTypeSHA256,
			Origin:          projectconfig.Origin{Type: projectconfig.OriginTypeOverlay},
			ReplaceUpstream: true,
			ReplaceReason:   "record post-overlay hash",
		}},
	})

	// With --skip-sources, FetchFiles must NOT be called (no EXPECT for it);
	// FetchComponent is still called to provide the spec.
	sourceManager.EXPECT().FetchComponent(gomock.Any(), component, outputDir, gomock.Any()).DoAndReturn(
		func(_ interface{}, _ interface{}, dir string, _ ...sourceproviders.FetchComponentOption) error {
			return fileutils.WriteFile(ctx.FS(), filepath.Join(dir, componentName+".spec"),
				[]byte("# test spec"), fileperms.PublicFile)
		},
	)

	preparer, err := sources.NewPreparer(
		sourceManager, ctx.FS(), ctx, ctx,
		sources.WithSkipLookaside(),
	)
	require.NoError(t, err)

	require.NoError(t, preparer.PrepareSources(ctx, component, outputDir, true /*applyOverlays*/))

	// The archive overlay is skipped: the archive is byte-for-byte unchanged...
	afterArchive, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	assert.Equal(t, originalArchive, afterArchive,
		"archive must not be modified when --skip-sources skips archive overlays")

	// ...and its 'sources' entry keeps the original hash.
	sourcesContent, err := fileutils.ReadFile(ctx.FS(), sourcesPath)
	require.NoError(t, err)

	got := findSourcesEntry(t, string(sourcesContent), archiveName)
	require.NotNil(t, got)
	assert.Equal(t, originalHash, got.Hash, "'sources' entry hash must be unchanged when overlays are skipped")
}
