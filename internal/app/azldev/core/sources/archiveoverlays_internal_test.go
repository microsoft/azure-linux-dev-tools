// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"os"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupOverlaysByArchive(t *testing.T) {
	t.Run("groups overlays by archive name preserving order and strips the prefix", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Filename: "pkg-1.0.tar.gz/unwanted.conf",
			},
			{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Filename: "pkg-1.0.tar.gz/config.h",
			},
			{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Filename: "other-2.0.tar.xz/docs/*.md",
			},
		}

		groups := groupOverlaysByArchive(overlays)

		require.Len(t, groups, 2)

		assert.Equal(t, "pkg-1.0.tar.gz", groups[0].archive)
		require.Len(t, groups[0].overlays, 2)
		// Filename is rewritten to the in-archive glob (archive prefix stripped).
		assert.Equal(t, "unwanted.conf", groups[0].overlays[0].Filename)
		assert.Equal(t, "config.h", groups[0].overlays[1].Filename)

		assert.Equal(t, "other-2.0.tar.xz", groups[1].archive)
		require.Len(t, groups[1].overlays, 1)
		assert.Equal(t, "docs/*.md", groups[1].overlays[0].Filename)
	})

	t.Run("skips overlays that are not archive-scoped", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Version", Value: "1.0"},
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "pkg.tar.gz/f"},
			// Plain (non-archive) file overlay: no archive prefix, so it must be skipped.
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "loose.txt"},
			// Bare archive name with no inner path: a loose removal of the archive itself.
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

func TestResolveExtractRoot(t *testing.T) {
	t.Run("infers single top-level directory", func(t *testing.T) {
		workDir := t.TempDir()
		require.NoError(t, os.MkdirAll(workDir+"/pkg-1.0", 0o755))

		root, err := resolveExtractRoot(workDir)
		require.NoError(t, err)
		assert.Equal(t, workDir+"/pkg-1.0", root)
	})

	t.Run("falls back to workDir for multiple top-level entries", func(t *testing.T) {
		workDir := t.TempDir()
		require.NoError(t, os.MkdirAll(workDir+"/dirA", 0o755))
		require.NoError(t, os.WriteFile(workDir+"/loose.txt", nil, fileperms.PrivateFile))

		root, err := resolveExtractRoot(workDir)
		require.NoError(t, err)
		assert.Equal(t, workDir, root)
	})
}

// TestArchiveFileRemove verifies that archive-scoped file-remove overlays are
// routed through the shared [applyNonSpecOverlay] machinery against the
// extract-root FS (i.e., the same code path that [processArchive] uses).
func TestArchiveFileRemove(t *testing.T) {
	ctx := testctx.NewCtx()

	t.Run("deletes matching files in the extracted tree", func(t *testing.T) {
		extractRoot := t.TempDir()
		require.NoError(t, os.WriteFile(extractRoot+"/keep.txt", []byte("keep"), fileperms.PrivateFile))
		require.NoError(t, os.WriteFile(extractRoot+"/remove.conf", []byte("x"), fileperms.PrivateFile))

		extractFS, err := rootfs.New(extractRoot)
		require.NoError(t, err)

		defer extractFS.Close()

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemoveFile,
			Filename: "*.conf",
		}

		err = applyNonSpecOverlay(ctx, ctx.FS(), extractFS, overlay)
		require.NoError(t, err)

		assert.FileExists(t, extractRoot+"/keep.txt")
		assert.NoFileExists(t, extractRoot+"/remove.conf")
	})

	t.Run("with no match errors like a loose-file overlay", func(t *testing.T) {
		extractRoot := t.TempDir()
		require.NoError(t, os.WriteFile(extractRoot+"/file.txt", nil, fileperms.PrivateFile))

		extractFS, err := rootfs.New(extractRoot)
		require.NoError(t, err)

		defer extractFS.Close()

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemoveFile,
			Filename: "*.conf",
		}

		err = applyNonSpecOverlay(ctx, ctx.FS(), extractFS, overlay)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "did not match any files")
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

	group := archiveGroup{
		archive: archiveName,
		overlays: []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "remove.conf"},
		},
	}

	require.NoError(t, processArchive(ctx, sourcesDir, group))

	after, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	assert.Equal(t, original, after, "dry-run must not modify the archive on disk")
}
