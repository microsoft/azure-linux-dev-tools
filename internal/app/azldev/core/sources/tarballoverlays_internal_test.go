// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"os"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupOverlaysByTarball(t *testing.T) {
	t.Run("groups overlays by tarball name preserving order", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Tarball:  "pkg-1.0.tar.gz",
				Filename: "unwanted.conf",
			},
			{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
				Tarball:     "pkg-1.0.tar.gz",
				Filename:    "config.h",
				Regex:       "old",
				Replacement: "new",
			},
			{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Tarball:  "other-2.0.tar.xz",
				Filename: "docs/*.md",
			},
		}

		groups, err := groupOverlaysByTarball(overlays)
		require.NoError(t, err)

		require.Len(t, groups, 2)

		assert.Equal(t, "pkg-1.0.tar.gz", groups[0].tarball)
		require.Len(t, groups[0].overlays, 2)
		assert.Equal(t, projectconfig.ComponentOverlayRemoveFile, groups[0].overlays[0].Type)
		assert.Equal(t, projectconfig.ComponentOverlaySearchAndReplaceInFile, groups[0].overlays[1].Type)

		assert.Equal(t, "other-2.0.tar.xz", groups[1].tarball)
		require.Len(t, groups[1].overlays, 1)
	})

	t.Run("skips overlays that are not archive-scoped", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Version", Value: "1.0"},
			{Type: projectconfig.ComponentOverlayRemoveFile, Tarball: "pkg.tar.gz", Filename: "f"},
			// Plain (non-archive) file overlay: no Tarball set, so it must be skipped.
			{Type: projectconfig.ComponentOverlayRemoveFile, Filename: "loose.txt"},
			{Type: projectconfig.ComponentOverlayAddFile, Filename: "new.txt", Source: "src"},
		}

		groups, err := groupOverlaysByTarball(overlays)
		require.NoError(t, err)

		require.Len(t, groups, 1)
		assert.Equal(t, "pkg.tar.gz", groups[0].tarball)
		require.Len(t, groups[0].overlays, 1)
	})

	t.Run("reconciles matching tarball-root overrides", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{
				Type:        projectconfig.ComponentOverlayRemoveFile,
				Tarball:     "pkg-1.0.tar.gz",
				TarballRoot: "custom-root",
				Filename:    "a.conf",
			},
			{
				Type:     projectconfig.ComponentOverlayRemoveFile,
				Tarball:  "pkg-1.0.tar.gz",
				Filename: "b.conf",
			},
		}

		groups, err := groupOverlaysByTarball(overlays)
		require.NoError(t, err)

		require.Len(t, groups, 1)
		assert.Equal(t, "custom-root", groups[0].root)
	})

	t.Run("errors on conflicting tarball-root overrides", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{
				Type:        projectconfig.ComponentOverlayRemoveFile,
				Tarball:     "pkg-1.0.tar.gz",
				TarballRoot: "root-a",
				Filename:    "a.conf",
			},
			{
				Type:        projectconfig.ComponentOverlayRemoveFile,
				Tarball:     "pkg-1.0.tar.gz",
				TarballRoot: "root-b",
				Filename:    "b.conf",
			},
		}

		_, err := groupOverlaysByTarball(overlays)
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

// TestApplyTarballOperation_FileOverlays verifies that archive-scoped file
// overlays are routed through the shared [applyNonSpecOverlay] machinery against
// the extract-root FS (i.e., the same code path as loose-file overlays).
func TestApplyTarballOperation_FileOverlays(t *testing.T) {
	ctx := testctx.NewCtx()

	t.Run("file-remove deletes matching files in the extracted tree", func(t *testing.T) {
		extractRoot := t.TempDir()
		require.NoError(t, os.WriteFile(extractRoot+"/keep.txt", []byte("keep"), fileperms.PrivateFile))
		require.NoError(t, os.WriteFile(extractRoot+"/remove.conf", []byte("x"), fileperms.PrivateFile))

		extractFS, err := rootfs.New(extractRoot)
		require.NoError(t, err)

		defer extractFS.Close()

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemoveFile,
			Tarball:  "pkg.tar.gz",
			Filename: "*.conf",
		}

		err = applyTarballOperation(context.Background(), nil, ctx, ctx.FS(), extractFS, extractRoot, overlay)
		require.NoError(t, err)

		assert.FileExists(t, extractRoot+"/keep.txt")
		assert.NoFileExists(t, extractRoot+"/remove.conf")
	})

	t.Run("file-search-replace rewrites matching content in the extracted tree", func(t *testing.T) {
		extractRoot := t.TempDir()
		require.NoError(t, os.WriteFile(
			extractRoot+"/config.h", []byte("#define OLD_VALUE 1\n"), fileperms.PrivateFile))

		extractFS, err := rootfs.New(extractRoot)
		require.NoError(t, err)

		defer extractFS.Close()

		overlay := projectconfig.ComponentOverlay{
			Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
			Tarball:     "pkg.tar.gz",
			Filename:    "config.h",
			Regex:       "OLD_VALUE",
			Replacement: "NEW_VALUE",
		}

		err = applyTarballOperation(context.Background(), nil, ctx, ctx.FS(), extractFS, extractRoot, overlay)
		require.NoError(t, err)

		content, readErr := os.ReadFile(extractRoot + "/config.h")
		require.NoError(t, readErr)
		assert.Equal(t, "#define NEW_VALUE 1\n", string(content))
	})

	t.Run("file-remove with no match errors like a loose-file overlay", func(t *testing.T) {
		extractRoot := t.TempDir()
		require.NoError(t, os.WriteFile(extractRoot+"/file.txt", nil, fileperms.PrivateFile))

		extractFS, err := rootfs.New(extractRoot)
		require.NoError(t, err)

		defer extractFS.Close()

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemoveFile,
			Tarball:  "pkg.tar.gz",
			Filename: "*.conf",
		}

		err = applyTarballOperation(context.Background(), nil, ctx, ctx.FS(), extractFS, extractRoot, overlay)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "did not match any files")
	})
}
