// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package tarball_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/tarball"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectCompression(t *testing.T) {
	tests := []struct {
		filename string
		expected tarball.Compression
		wantErr  bool
	}{
		{"pkg-1.0.tar.gz", tarball.CompressionGzip, false},
		{"pkg-1.0.tgz", tarball.CompressionGzip, false},
		{"pkg-1.0.tar.bz2", tarball.CompressionBzip2, false},
		{"pkg-1.0.tar.xz", tarball.CompressionXZ, false},
		{"pkg-1.0.tar.zst", tarball.CompressionZstd, false},
		{"pkg-1.0.tar", tarball.CompressionNone, false},
		{"pkg-1.0.zip", tarball.CompressionNone, true},
		{"PKG-1.0.TAR.GZ", tarball.CompressionGzip, false},
	}

	for _, testCase := range tests {
		t.Run(testCase.filename, func(t *testing.T) {
			comp, err := tarball.DetectCompression(testCase.filename)

			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.expected, comp)
		})
	}
}

func TestResolveExtractRoot(t *testing.T) {
	t.Run("single top-level directory", func(t *testing.T) {
		workDir := t.TempDir()
		subDir := filepath.Join(workDir, "pkg-1.0")
		require.NoError(t, os.MkdirAll(subDir, 0o755))

		root, err := tarball.ResolveExtractRoot(workDir)
		require.NoError(t, err)
		assert.Equal(t, subDir, root)
	})

	t.Run("multiple entries returns workDir", func(t *testing.T) {
		workDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(workDir, "dir1"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(workDir, "dir2"), 0o755))

		root, err := tarball.ResolveExtractRoot(workDir)
		require.NoError(t, err)
		assert.Equal(t, workDir, root)
	})

	t.Run("single file returns workDir", func(t *testing.T) {
		workDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("content"), 0o600))

		root, err := tarball.ResolveExtractRoot(workDir)
		require.NoError(t, err)
		assert.Equal(t, workDir, root)
	})
}

func TestExtractAndRepack(t *testing.T) {
	testFS := afero.NewOsFs()
	tmpDir := t.TempDir()

	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	extractDir := filepath.Join(tmpDir, "extracted")
	repackDir := filepath.Join(tmpDir, "repacked")

	require.NoError(t, os.MkdirAll(extractDir, 0o755))
	require.NoError(t, os.MkdirAll(repackDir, 0o755))

	createTestTarGz(t, archivePath, map[string]string{
		"pkg-1.0/hello.txt":  "hello world",
		"pkg-1.0/config.cfg": "key=value",
	})

	err := tarball.Extract(testFS, archivePath, extractDir, tarball.CompressionGzip)
	require.NoError(t, err)

	content, readErr := os.ReadFile(filepath.Join(extractDir, "pkg-1.0", "hello.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "hello world", string(content))

	repackPath := filepath.Join(tmpDir, "repacked.tar.gz")

	err = tarball.RepackDeterministic(testFS, repackPath, extractDir, tarball.CompressionGzip)
	require.NoError(t, err)

	err = tarball.Extract(testFS, repackPath, repackDir, tarball.CompressionGzip)
	require.NoError(t, err)

	content, readErr = os.ReadFile(filepath.Join(repackDir, "pkg-1.0", "hello.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "hello world", string(content))

	// Repack twice and verify byte-for-byte identical output.
	repackPath2 := filepath.Join(tmpDir, "repacked2.tar.gz")

	err = tarball.RepackDeterministic(testFS, repackPath2, extractDir, tarball.CompressionGzip)
	require.NoError(t, err)

	data1, _ := os.ReadFile(repackPath)
	data2, _ := os.ReadFile(repackPath2)
	assert.Equal(t, data1, data2, "deterministic repack should produce identical output")
}

func createTestTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()

	var buf bytes.Buffer

	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)

	for name, content := range files {
		header := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}

		require.NoError(t, tarWriter.WriteHeader(header))

		_, writeErr := tarWriter.Write([]byte(content))
		require.NoError(t, writeErr)
	}

	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzWriter.Close())
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
}
