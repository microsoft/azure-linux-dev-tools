// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils_test

import (
	"embed"
	"io"
	"os"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/embedfs/*
var testFS embed.FS

func TestWrapEmbedFS(t *testing.T) {
	embedfs := fileutils.WrapEmbedFS(&testFS)
	require.NotNil(t, embedfs, "WrapEmbedFS should return a non-nil FS")
	require.Equal(t, "embed", embedfs.Name(), "Name should return 'embed'")
}

func TestEmbedFSRead(t *testing.T) {
	embedfs := fileutils.WrapEmbedFS(&testFS)

	t.Run("ReadExistingFile", func(t *testing.T) {
		file, err := embedfs.Open("testdata/embedfs/file1.txt")
		require.NoError(t, err)

		defer file.Close()

		content, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, "content of file1\n", string(content))
	})

	t.Run("ReadWithLeadingSlash", func(t *testing.T) {
		file, err := embedfs.Open("/testdata/embedfs/file1.txt")
		require.NoError(t, err)

		defer file.Close()

		content, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, "content of file1\n", string(content))
	})

	t.Run("ReadNonExistentFile", func(t *testing.T) {
		_, err := embedfs.Open("/non/existent/file.txt")
		require.Error(t, err)
	})

	t.Run("OpenFileReadsExistingFile", func(t *testing.T) {
		file, err := embedfs.OpenFile("testdata/embedfs/file1.txt", os.O_RDONLY, 0)
		require.NoError(t, err)

		defer file.Close()

		content, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, "content of file1\n", string(content))
	})
}

func TestEmbedFSDirectoryOperations(t *testing.T) {
	embedfs := fileutils.WrapEmbedFS(&testFS)

	t.Run("ReadDir", func(t *testing.T) {
		file, err := embedfs.Open("testdata/embedfs")
		require.NoError(t, err)

		defer file.Close()

		fileInfos, err := file.Readdir(-1)
		require.NoError(t, err)

		// Verify we have the expected files and directories
		var fileNames []string
		for _, fi := range fileInfos {
			fileNames = append(fileNames, fi.Name())
		}

		require.Contains(t, fileNames, "file1.txt", "Directory should contain file1.txt")
		require.Contains(t, fileNames, "file2.txt", "Directory should contain file2.txt")
		require.Contains(t, fileNames, "subdir", "Directory should contain subdir")
	})

	t.Run("ReaddirNames", func(t *testing.T) {
		file, err := embedfs.Open("testdata/embedfs")
		require.NoError(t, err)

		defer file.Close()

		fileNames, err := file.Readdirnames(-1)
		require.NoError(t, err)

		require.Contains(t, fileNames, "file1.txt", "Directory should contain file1.txt")
		require.Contains(t, fileNames, "file2.txt", "Directory should contain file2.txt")
		require.Contains(t, fileNames, "subdir", "Directory should contain subdir")
	})

	t.Run("Stat", func(t *testing.T) {
		fileInfo, err := embedfs.Stat("testdata/embedfs/file1.txt")
		require.NoError(t, err)
		require.Equal(t, "file1.txt", fileInfo.Name())
		require.False(t, fileInfo.IsDir())

		dirInfo, err := embedfs.Stat("testdata/embedfs/subdir")
		require.NoError(t, err)
		require.Equal(t, "subdir", dirInfo.Name())
		require.True(t, dirInfo.IsDir())
	})
}

func TestEmbedFileProperties(t *testing.T) {
	embedfs := fileutils.WrapEmbedFS(&testFS)

	t.Run("FileName", func(t *testing.T) {
		file, err := embedfs.Open("testdata/embedfs/file1.txt")
		require.NoError(t, err)

		defer file.Close()

		require.Equal(t, "testdata/embedfs/file1.txt", file.Name())
	})

	t.Run("FileStat", func(t *testing.T) {
		file, err := embedfs.Open("testdata/embedfs/file1.txt")
		require.NoError(t, err)

		defer file.Close()

		fileInfo, err := file.Stat()
		require.NoError(t, err)
		require.Equal(t, "file1.txt", fileInfo.Name())
		require.False(t, fileInfo.IsDir())
	})
}

func TestEmbedFSReadOnlyOperations(t *testing.T) {
	embedfs := fileutils.WrapEmbedFS(&testFS)

	readOnlyOps := []struct {
		name string
		fn   func() error
	}{
		{
			name: "Create",
			fn: func() error {
				_, err := embedfs.Create("test.txt")

				return err
			},
		},
		{
			name: "Mkdir",
			fn: func() error {
				return embedfs.Mkdir("newdir", 0o755)
			},
		},
		{
			name: "MkdirAll",
			fn: func() error {
				return embedfs.MkdirAll("new/nested/dir", 0o755)
			},
		},
		{
			name: "Remove",
			fn: func() error {
				return embedfs.Remove("testdata/embedfs/file1.txt")
			},
		},
		{
			name: "RemoveAll",
			fn: func() error {
				return embedfs.RemoveAll("testdata/embedfs")
			},
		},
		{
			name: "Rename",
			fn: func() error {
				return embedfs.Rename("testdata/embedfs/file1.txt", "testdata/embedfs/renamed.txt")
			},
		},
		{
			name: "Chmod",
			fn: func() error {
				return embedfs.Chmod("testdata/embedfs/file1.txt", 0o644)
			},
		},
		{
			name: "Chown",
			fn: func() error {
				return embedfs.Chown("testdata/embedfs/file1.txt", 0, 0)
			},
		},
		{
			name: "Chtimes",
			fn: func() error {
				now := time.Now()

				return embedfs.Chtimes("testdata/embedfs/file1.txt", now, now)
			},
		},
	}

	for _, op := range readOnlyOps {
		t.Run(op.name, func(t *testing.T) {
			err := op.fn()
			require.Error(t, err)
			require.Equal(t, fileutils.ErrReadOnlyEmbedFS, err)
		})
	}
}

func TestEmbedFileReadOnlyOperations(t *testing.T) {
	fs := fileutils.WrapEmbedFS(&testFS)
	file, err := fs.Open("testdata/embedfs/file1.txt")
	require.NoError(t, err)

	defer file.Close()

	readOnlyOps := []struct {
		name string
		fn   func() error
	}{
		{
			name: "Write",
			fn: func() error {
				_, err := file.Write([]byte("new content"))

				return err
			},
		},
		{
			name: "WriteAt",
			fn: func() error {
				_, err := file.WriteAt([]byte("new content"), 0)

				return err
			},
		},
		{
			name: "WriteString",
			fn: func() error {
				_, err := file.WriteString("new content")

				return err
			},
		},
		{
			name: "Truncate",
			fn: func() error {
				return file.Truncate(0)
			},
		},
		{
			name: "Sync",
			fn: func() error {
				return file.Sync()
			},
		},
	}

	for _, op := range readOnlyOps {
		t.Run(op.name, func(t *testing.T) {
			err := op.fn()
			require.Error(t, err)
			require.Equal(t, fileutils.ErrReadOnlyEmbedFS, err)
		})
	}
}

func TestEmbedFileUnimplementedOperations(t *testing.T) {
	fs := fileutils.WrapEmbedFS(&testFS)
	file, err := fs.Open("testdata/embedfs/file1.txt")
	require.NoError(t, err)

	defer file.Close()

	unimplementedOps := []struct {
		name string
		fn   func() error
	}{
		{
			name: "ReadAt",
			fn: func() error {
				buffer := make([]byte, 10)
				_, err := file.ReadAt(buffer, 0)

				return err
			},
		},
		{
			name: "Seek",
			fn: func() error {
				_, err := file.Seek(0, 0)

				return err
			},
		},
	}

	for _, op := range unimplementedOps {
		t.Run(op.name, func(t *testing.T) {
			err := op.fn()
			require.Error(t, err)
			require.Equal(t, fileutils.ErrUnimplementedForEmbedFS, err)
		})
	}
}

func TestEmbedFSWithSubdir(t *testing.T) {
	embedfs := fileutils.WrapEmbedFS(&testFS)

	t.Run("ReadFileInSubdir", func(t *testing.T) {
		file, err := embedfs.Open("testdata/embedfs/subdir/subfile.txt")
		require.NoError(t, err)

		defer file.Close()

		content, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, "content of subfile\n", string(content))
	})

	t.Run("StatFileInSubdir", func(t *testing.T) {
		fileInfo, err := embedfs.Stat("testdata/embedfs/subdir/subfile.txt")
		require.NoError(t, err)
		require.Equal(t, "subfile.txt", fileInfo.Name())
		require.False(t, fileInfo.IsDir())
	})
}
