// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build linux

package archive_test

import (
	"archive/tar"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/archive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtract_PreservesModesUnderRestrictiveUmask(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "source.tar.gz")

	createTestTarGz(t, archivePath, []testTarEntry{
		{name: "implicit/file.txt", typeflag: tar.TypeReg, content: "content", mode: 0o666},
	})

	repack := func(name string, umask int) []byte {
		extractDir := filepath.Join(tmpDir, name)
		repackedPath := filepath.Join(tmpDir, name+".tar.gz")

		previousUmask := syscall.Umask(umask)
		defer syscall.Umask(previousUmask)

		require.NoError(t, archive.Extract(archivePath, extractDir, archive.CompressionGzip))
		require.NoError(t, archive.CreateDeterministicArchive(repackedPath, extractDir, archive.CompressionGzip))

		directoryInfo, err := os.Stat(filepath.Join(extractDir, "implicit"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), directoryInfo.Mode().Perm())

		fileInfo, err := os.Stat(filepath.Join(extractDir, "implicit", "file.txt"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o666), fileInfo.Mode().Perm())

		data, err := os.ReadFile(repackedPath)
		require.NoError(t, err)

		return data
	}

	standard := repack("standard", 0o022)
	restrictive := repack("restrictive", 0o077)
	assert.Equal(t, standard, restrictive, "repacked archive must not depend on the process umask")
}

func TestExtract_ImplicitDirectorySymlinkAliasDoesNotOverrideExplicitMode(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "source.tar.gz")
	extractDir := filepath.Join(tmpDir, "extracted")

	createTestTarGz(t, archivePath, []testTarEntry{
		{name: "real/", typeflag: tar.TypeDir, mode: 0o700},
		{name: "x", typeflag: tar.TypeSymlink, linkname: "real"},
		{name: "x/subdir/file", typeflag: tar.TypeReg, content: "content"},
		{name: "real/subdir/", typeflag: tar.TypeDir, mode: 0o700},
	})

	require.NoError(t, archive.Extract(archivePath, extractDir, archive.CompressionGzip))

	for _, path := range []string{"real/subdir", "x/subdir"} {
		info, err := os.Stat(filepath.Join(extractDir, path))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "directory %#q mode", path)
	}
}
