// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/archive"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupOverlaysByArchive(t *testing.T) {
	t.Run("groups overlays by archive name preserving order", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Archive:  "pkg-1.0.tar.gz",
				Filename: "unwanted.conf",
			},
			{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Archive:  "pkg-1.0.tar.gz",
				Filename: "config.h",
			},
			{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Archive:  "other-2.0.tar.xz",
				Filename: "docs/*.md",
			},
		}

		groups := groupOverlaysByArchive(overlays)

		require.Len(t, groups, 2)

		assert.Equal(t, "pkg-1.0.tar.gz", groups[0].archive)
		require.Len(t, groups[0].overlays, 2)
		assert.Equal(t, "unwanted.conf", groups[0].overlays[0].Filename)
		assert.Equal(t, "config.h", groups[0].overlays[1].Filename)

		assert.Equal(t, "other-2.0.tar.xz", groups[1].archive)
		require.Len(t, groups[1].overlays, 1)
		assert.Equal(t, "docs/*.md", groups[1].overlays[0].Filename)
	})

	t.Run("skips overlays that are not archive-scoped", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Version", Value: "1.0"},
			{Type: projectconfig.ComponentOverlayRemoveFile, Archive: "pkg.tar.gz", Filename: "f"},
			// Plain (non-archive) file overlay: no archive field, so it must be skipped.
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "loose.txt"},
			// Bare archive name with no archive field: a loose removal of the archive itself.
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "drop-me.tar.gz"},
			{Type: projectconfig.ComponentOverlayAddFile, Filename: "new.txt", Source: "src"},
		}

		groups := groupOverlaysByArchive(overlays)

		require.Len(t, groups, 1)
		assert.Equal(t, "pkg.tar.gz", groups[0].archive)
		require.Len(t, groups[0].overlays, 1)
		assert.Equal(t, "f", groups[0].overlays[0].Filename)
	})
}

// TestProcessArchive_DryRunDoesNotModifyArchive verifies that, in dry-run mode,
// processArchive skips the extract/repack cycle entirely and leaves the original
// archive on disk byte-for-byte unchanged (repacking would otherwise rewrite it).
func TestProcessArchive_DryRunDoesNotModifyArchive(t *testing.T) {
	ctx := testctx.NewCtx()
	ctx.DryRunValue = true

	sourcesDir := t.TempDir()

	const archiveName = "pkg-1.0.tar.gz"

	archivePath := sourcesDir + "/" + archiveName

	// Content need not be a valid archive: dry-run returns before extraction, and
	// the test only asserts the bytes are untouched.
	original := []byte("original archive bytes")
	require.NoError(t, os.WriteFile(archivePath, original, fileperms.PrivateFile))

	overlays := []projectconfig.ComponentOverlay{
		{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "remove.conf"},
	}

	repacked, err := processArchive(ctx, sourcesDir, archiveName, overlays)
	require.NoError(t, err)
	assert.False(t, repacked, "dry-run must report that no archive was repacked")

	after, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	assert.Equal(t, original, after, "dry-run must not modify the archive on disk")
}

// stageFiles writes the given slash-separated relative path -> content map under
// root, creating parent directories as needed.
func stageFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), fileperms.PublicDir))
		require.NoError(t, os.WriteFile(full, []byte(content), fileperms.PrivateFile))
	}
}

// listRegularFiles returns the sorted, slash-separated relative paths of all
// regular files under root (directories are ignored).
func listRegularFiles(t *testing.T, root string) []string {
	t.Helper()

	var files []string

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

		files = append(files, filepath.ToSlash(rel))

		return nil
	}))

	sort.Strings(files)

	return files
}

// extractedRegularFiles extracts archivePath into a fresh temp dir and returns
// the sorted relative paths of the regular files it contains.
func extractedRegularFiles(t *testing.T, archivePath string) []string {
	t.Helper()

	out := t.TempDir()
	require.NoError(t, archive.ExtractAuto(archivePath, out))

	return listRegularFiles(t, out)
}

// rawTarEntry describes a single entry for [buildTarGz], which writes a tarball
// directly so that entry types [archive.CreateDeterministicArchive] never emits
// (e.g. hardlinks) can be constructed for tests.
type rawTarEntry struct {
	name     string
	typeflag byte
	content  string
	linkname string
}

// buildTarGz writes a gzip-compressed tar archive of the given entries to path.
func buildTarGz(t *testing.T, path string, entries []rawTarEntry) {
	t.Helper()

	file, err := os.Create(path)
	require.NoError(t, err)

	defer func() { require.NoError(t, file.Close()) }()

	gzWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzWriter)

	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Typeflag: entry.typeflag, Mode: 0o644}

		switch entry.typeflag {
		case tar.TypeReg:
			header.Size = int64(len(entry.content))
		case tar.TypeLink, tar.TypeSymlink:
			header.Linkname = entry.linkname
		}

		require.NoError(t, tarWriter.WriteHeader(header))

		if entry.typeflag == tar.TypeReg {
			_, writeErr := tarWriter.Write([]byte(entry.content))
			require.NoError(t, writeErr)
		}
	}

	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzWriter.Close())
}

// TestProcessArchive_AppliesMultipleOverlaysInSingleCycle verifies that two
// overlays targeting the same archive are both applied within a single
// extract/repack cycle (the [processArchive] overlay loop).
func TestProcessArchive_AppliesMultipleOverlaysInSingleCycle(t *testing.T) {
	ctx := testctx.NewCtx()
	sourcesDir := t.TempDir()

	const archiveName = "pkg.tar.gz"

	archivePath := filepath.Join(sourcesDir, archiveName)

	// Single top-level directory so the extract root is "pkg-1.0/".
	staging := t.TempDir()
	stageFiles(t, staging, map[string]string{
		"pkg-1.0/remove-me.conf": "junk",
		"pkg-1.0/keep.conf":      "keep",
		"pkg-1.0/version.txt":    "version = old_value",
	})
	require.NoError(t, archive.CreateDeterministicArchive(archivePath, staging, archive.CompressionGzip))

	// A removal and a search-replace targeting the same archive. Both must be
	// applied to the same extracted tree before the single repack.
	overlays := []projectconfig.ComponentOverlay{
		{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "remove-me.conf"},
		{
			Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
			Filename:    "version.txt",
			Regex:       "old_value",
			Replacement: "new_value",
		},
	}

	repacked, err := processArchive(ctx, sourcesDir, archiveName, overlays)
	require.NoError(t, err)
	assert.True(t, repacked)

	out := t.TempDir()
	require.NoError(t, archive.ExtractAuto(archivePath, out))

	// Removal applied; untouched sibling preserved.
	assert.NoFileExists(t, filepath.Join(out, "pkg-1.0", "remove-me.conf"))
	assert.FileExists(t, filepath.Join(out, "pkg-1.0", "keep.conf"))

	// Search-replace applied in the same cycle.
	content, err := os.ReadFile(filepath.Join(out, "pkg-1.0", "version.txt"))
	require.NoError(t, err)
	assert.Equal(t, "version = new_value", string(content))
}

// TestProcessArchive_ResolveExtractRootFallback drives the extract-root
// resolution through the full [processArchive] cycle, confirming that overlay
// globs are matched relative to the resolved root in both the single-top-level-
// directory case and the multiple-top-level-entries fallback.
func TestProcessArchive_ResolveExtractRootFallback(t *testing.T) {
	t.Run("single top-level directory: glob is relative to that directory", func(t *testing.T) {
		ctx := testctx.NewCtx()
		sourcesDir := t.TempDir()

		const archiveName = "pkg.tar.gz"

		archivePath := filepath.Join(sourcesDir, archiveName)

		staging := t.TempDir()
		stageFiles(t, staging, map[string]string{
			"pkg-1.0/remove.conf": "x",
			"pkg-1.0/keep.txt":    "x",
		})
		require.NoError(t, archive.CreateDeterministicArchive(archivePath, staging, archive.CompressionGzip))

		overlays := []projectconfig.ComponentOverlay{
			// Path is relative to the extract root (pkg-1.0/), not the archive root.
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "remove.conf"},
		}

		repacked, err := processArchive(ctx, sourcesDir, archiveName, overlays)
		require.NoError(t, err)
		assert.True(t, repacked)
		assert.Equal(t, []string{"pkg-1.0/keep.txt"}, extractedRegularFiles(t, archivePath))
	})

	t.Run("multiple top-level entries: glob is relative to the archive root", func(t *testing.T) {
		ctx := testctx.NewCtx()
		sourcesDir := t.TempDir()

		const archiveName = "pkg.tar.gz"

		archivePath := filepath.Join(sourcesDir, archiveName)

		// Two top-level entries (no single wrapping directory) => extract root is
		// the archive root, so the glob is matched there.
		staging := t.TempDir()
		stageFiles(t, staging, map[string]string{
			"remove.conf": "x",
			"keep.txt":    "x",
		})
		require.NoError(t, archive.CreateDeterministicArchive(archivePath, staging, archive.CompressionGzip))

		overlays := []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "remove.conf"},
		}

		repacked, err := processArchive(ctx, sourcesDir, archiveName, overlays)
		require.NoError(t, err)
		assert.True(t, repacked)
		assert.Equal(t, []string{"keep.txt"}, extractedRegularFiles(t, archivePath))
	})
}

// TestProcessArchive_UnsupportedEntryTypeErrors verifies the data-loss guard:
// an archive containing an entry that cannot be repacked (here a hardlink) must
// fail rather than silently drop the entry, and the original archive must be
// left untouched (extraction fails before the repack runs).
func TestProcessArchive_UnsupportedEntryTypeErrors(t *testing.T) {
	ctx := testctx.NewCtx()
	sourcesDir := t.TempDir()

	const archiveName = "pkg.tar.gz"

	archivePath := filepath.Join(sourcesDir, archiveName)

	// CreateDeterministicArchive never emits hardlinks, so build the tarball raw.
	buildTarGz(t, archivePath, []rawTarEntry{
		{name: "pkg-1.0/real.txt", typeflag: tar.TypeReg, content: "hello"},
		{name: "pkg-1.0/hard.txt", typeflag: tar.TypeLink, linkname: "pkg-1.0/real.txt"},
	})

	before, err := os.ReadFile(archivePath)
	require.NoError(t, err)

	overlays := []projectconfig.ComponentOverlay{
		{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "real.txt"},
	}

	repacked, err := processArchive(ctx, sourcesDir, archiveName, overlays)
	require.Error(t, err, "an unsupported (hardlink) entry must fail rather than be silently dropped")
	assert.False(t, repacked)
	assert.Contains(t, err.Error(), "unsupported type")

	// The original archive must be byte-for-byte intact (the repack never ran).
	after, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a failed extraction must not modify the source archive")
}

// TestProcessArchive_DirectoryHandling pins two documented behaviors of
// file-remove inside an archive: emptied directories survive (file-remove never
// deletes directories), and a bare directory pattern matches nothing and errors.
func TestProcessArchive_DirectoryHandling(t *testing.T) {
	t.Run("emptied directory survives file removal", func(t *testing.T) {
		ctx := testctx.NewCtx()
		sourcesDir := t.TempDir()

		const archiveName = "pkg.tar.gz"

		archivePath := filepath.Join(sourcesDir, archiveName)

		staging := t.TempDir()
		stageFiles(t, staging, map[string]string{
			"pkg-1.0/sub/a.txt": "x",
			"pkg-1.0/sub/b.txt": "x",
			"pkg-1.0/keep.txt":  "x",
		})
		require.NoError(t, archive.CreateDeterministicArchive(archivePath, staging, archive.CompressionGzip))

		overlays := []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "sub/**"},
		}

		repacked, err := processArchive(ctx, sourcesDir, archiveName, overlays)
		require.NoError(t, err)
		assert.True(t, repacked)

		out := t.TempDir()
		require.NoError(t, archive.ExtractAuto(archivePath, out))

		// Every file under sub/ is gone...
		assert.Equal(t, []string{"pkg-1.0/keep.txt"}, listRegularFiles(t, out))

		// ...but the now-empty directory survives (file-remove can't delete dirs).
		info, err := os.Stat(filepath.Join(out, "pkg-1.0", "sub"))
		require.NoError(t, err)
		assert.True(t, info.IsDir(), "emptied directory should survive file removal")
	})

	t.Run("bare directory pattern matches nothing and errors", func(t *testing.T) {
		ctx := testctx.NewCtx()
		sourcesDir := t.TempDir()

		const archiveName = "pkg.tar.gz"

		archivePath := filepath.Join(sourcesDir, archiveName)

		staging := t.TempDir()
		stageFiles(t, staging, map[string]string{
			"pkg-1.0/sub/a.txt": "x",
		})
		require.NoError(t, archive.CreateDeterministicArchive(archivePath, staging, archive.CompressionGzip))

		overlays := []projectconfig.ComponentOverlay{
			// A bare directory name: the files-only matcher matches nothing.
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "sub"},
		}

		repacked, err := processArchive(ctx, sourcesDir, archiveName, overlays)
		require.Error(t, err, "removing a directory is unsupported; the pattern should match no files")
		assert.False(t, repacked)
	})
}

// TestProcessArchive_PreservesMislabeledFormatOnRepack verifies that when an
// archive's extension lies about its real format (here a plain, uncompressed tar
// named ".tar.gz"), the repacked archive keeps the real on-disk format (plain
// tar) rather than being "healed" to match the extension. The misleading name is
// preserved; only the contents change.
func TestProcessArchive_PreservesMislabeledFormatOnRepack(t *testing.T) {
	ctx := testctx.NewCtx()
	sourcesDir := t.TempDir()

	// Misleading name: ".tar.gz" extension, but written as an uncompressed tar.
	const archiveName = "pkg.tar.gz"

	archivePath := filepath.Join(sourcesDir, archiveName)

	staging := t.TempDir()
	stageFiles(t, staging, map[string]string{
		"pkg-1.0/remove-me.txt": "junk",
		"pkg-1.0/keep.txt":      "keep",
	})
	require.NoError(t, archive.CreateDeterministicArchive(archivePath, staging, archive.CompressionNone))

	// Precondition: the file really is an uncompressed tar despite its name.
	preComp, err := archive.SniffCompressionFromFile(archivePath, archive.CompressionGzip)
	require.NoError(t, err)
	require.Equal(t, archive.CompressionNone, preComp)

	overlays := []projectconfig.ComponentOverlay{
		{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "remove-me.txt"},
	}

	repacked, err := processArchive(ctx, sourcesDir, archiveName, overlays)
	require.NoError(t, err)
	assert.True(t, repacked)

	// The overlay was applied (file removed)...
	assert.Equal(t, []string{"pkg-1.0/keep.txt"}, extractedRegularFiles(t, archivePath))

	// ...and the repacked archive preserves the real format: still an uncompressed
	// tar, NOT re-compressed to gzip to match the ".tar.gz" extension.
	postComp, err := archive.SniffCompressionFromFile(archivePath, archive.CompressionGzip)
	require.NoError(t, err)
	assert.Equal(t, archive.CompressionNone, postComp,
		"repack must preserve the original (sniffed) format, keeping the misleading extension")
}

// TestProcessArchive_RepackedArchivePreservesPermissions verifies that atomic
// replacement keeps the input archive's permission bits rather than inheriting
// the temporary file's mode.
func TestProcessArchive_RepackedArchivePreservesPermissions(t *testing.T) {
	for _, originalPerm := range []os.FileMode{0o600, 0o640, 0o644} {
		t.Run(originalPerm.String(), func(t *testing.T) {
			ctx := testctx.NewCtx()
			sourcesDir := t.TempDir()

			const archiveName = "pkg.tar.gz"

			archivePath := filepath.Join(sourcesDir, archiveName)

			staging := t.TempDir()
			stageFiles(t, staging, map[string]string{
				"pkg-1.0/remove-me.txt": "junk",
				"pkg-1.0/keep.txt":      "keep",
			})
			require.NoError(t, archive.CreateDeterministicArchive(archivePath, staging, archive.CompressionGzip))
			require.NoError(t, os.Chmod(archivePath, originalPerm))

			overlays := []projectconfig.ComponentOverlay{
				{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "remove-me.txt"},
			}

			repacked, err := processArchive(ctx, sourcesDir, archiveName, overlays)
			require.NoError(t, err)
			require.True(t, repacked)

			info, err := os.Stat(archivePath)
			require.NoError(t, err)
			assert.Equal(t, originalPerm, info.Mode().Perm(),
				"repacked archive must preserve the original permission bits")
		})
	}
}
