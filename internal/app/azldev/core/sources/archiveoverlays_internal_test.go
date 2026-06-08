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

		groups, err := groupOverlaysByArchive(overlays)
		require.NoError(t, err)

		require.Len(t, groups, 2)

		assert.Equal(t, "pkg-1.0.tar.gz", groups[0].archive)
		require.Len(t, groups[0].overlays, 2)
		assert.Equal(t, "unwanted.conf", groups[0].overlays[0].Filename)
		assert.Equal(t, "config.h", groups[0].overlays[1].Filename)

		assert.Equal(t, "other-2.0.tar.xz", groups[1].archive)
		require.Len(t, groups[1].overlays, 1)
	})

	t.Run("skips overlays that are not archive-scoped", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Version", Value: "1.0"},
			{Type: projectconfig.ComponentOverlayRemoveFile, Archive: "pkg.tar.gz", Filename: "f"},
			// Plain (non-archive) file overlay: no Archive set, so it must be skipped.
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "loose.txt"},
			{Type: projectconfig.ComponentOverlayAddFile, Filename: "new.txt", Source: "src"},
		}

		groups, err := groupOverlaysByArchive(overlays)
		require.NoError(t, err)

		require.Len(t, groups, 1)
		assert.Equal(t, "pkg.tar.gz", groups[0].archive)
		require.Len(t, groups[0].overlays, 1)
	})

	t.Run("reconciles matching archive-root overrides", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{
				Type:        projectconfig.ComponentOverlayRemoveFile,
				Archive:     "pkg-1.0.tar.gz",
				ArchiveRoot: "custom-root",
				Filename:    "a.conf",
			},
			{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Archive:  "pkg-1.0.tar.gz",
				Filename: "b.conf",
			},
		}

		groups, err := groupOverlaysByArchive(overlays)
		require.NoError(t, err)

		require.Len(t, groups, 1)
		assert.Equal(t, "custom-root", groups[0].root)
	})

	t.Run("errors on conflicting archive-root overrides", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{
				Type:        projectconfig.ComponentOverlayRemoveFile,
				Archive:     "pkg-1.0.tar.gz",
				ArchiveRoot: "root-a",
				Filename:    "a.conf",
			},
			{
				Type:        projectconfig.ComponentOverlayRemoveFile,
				Archive:     "pkg-1.0.tar.gz",
				ArchiveRoot: "root-b",
				Filename:    "b.conf",
			},
		}

		_, err := groupOverlaysByArchive(overlays)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "conflicting")
	})
}

func TestResolveExtractRoot(t *testing.T) {
	t.Run("infers single top-level directory", func(t *testing.T) {
		workDir := t.TempDir()
		require.NoError(t, os.MkdirAll(workDir+"/pkg-1.0", 0o755))

		root, err := resolveExtractRoot(workDir, "")
		require.NoError(t, err)
		assert.Equal(t, workDir+"/pkg-1.0", root)
	})

	t.Run("falls back to workDir for multiple top-level entries", func(t *testing.T) {
		workDir := t.TempDir()
		require.NoError(t, os.MkdirAll(workDir+"/dirA", 0o755))
		require.NoError(t, os.WriteFile(workDir+"/loose.txt", nil, fileperms.PrivateFile))

		root, err := resolveExtractRoot(workDir, "")
		require.NoError(t, err)
		assert.Equal(t, workDir, root)
	})

	t.Run("override selects named subdirectory", func(t *testing.T) {
		workDir := t.TempDir()
		// Two top-level dirs so the heuristic would not pick one.
		require.NoError(t, os.MkdirAll(workDir+"/dirA", 0o755))
		require.NoError(t, os.MkdirAll(workDir+"/dirB", 0o755))

		root, err := resolveExtractRoot(workDir, "dirB")
		require.NoError(t, err)
		assert.Equal(t, workDir+"/dirB", root)
	})

	t.Run("override missing directory errors", func(t *testing.T) {
		workDir := t.TempDir()

		_, err := resolveExtractRoot(workDir, "does-not-exist")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("override pointing at a file errors", func(t *testing.T) {
		workDir := t.TempDir()
		require.NoError(t, os.WriteFile(workDir+"/afile", nil, fileperms.PrivateFile))

		_, err := resolveExtractRoot(workDir, "afile")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a directory")
	})

	t.Run("non-local override is rejected", func(t *testing.T) {
		workDir := t.TempDir()

		_, err := resolveExtractRoot(workDir, "../escape")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a local path")
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
			Archive:  "pkg.tar.gz",
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
			Archive:  "pkg.tar.gz",
			Filename: "*.conf",
		}

		err = applyNonSpecOverlay(ctx, ctx.FS(), extractFS, overlay)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "did not match any files")
	})
}
