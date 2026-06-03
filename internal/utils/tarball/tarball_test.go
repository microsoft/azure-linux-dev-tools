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
		{"pkg-1.0.tar.bz2", tarball.CompressionNone, true},
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

func TestExtractAndRepack(t *testing.T) {
	tmpDir := t.TempDir()

	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	extractDir := filepath.Join(tmpDir, "extracted")
	repackDir := filepath.Join(tmpDir, "repacked")

	require.NoError(t, os.MkdirAll(extractDir, 0o755))
	require.NoError(t, os.MkdirAll(repackDir, 0o755))

	createTestTarGz(t, archivePath, []testTarEntry{
		{name: "pkg-1.0/hello.txt", typeflag: tar.TypeReg, content: "hello world"},
		{name: "pkg-1.0/config.cfg", typeflag: tar.TypeReg, content: "key=value"},
	})

	err := tarball.Extract(archivePath, extractDir, tarball.CompressionGzip)
	require.NoError(t, err)

	content, readErr := os.ReadFile(filepath.Join(extractDir, "pkg-1.0", "hello.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "hello world", string(content))

	repackPath := filepath.Join(tmpDir, "repacked.tar.gz")

	err = tarball.CreateDeterministicArchive(repackPath, extractDir, tarball.CompressionGzip)
	require.NoError(t, err)

	err = tarball.Extract(repackPath, repackDir, tarball.CompressionGzip)
	require.NoError(t, err)

	content, readErr = os.ReadFile(filepath.Join(repackDir, "pkg-1.0", "hello.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "hello world", string(content))

	// Repack twice and verify byte-for-byte identical output.
	repackPath2 := filepath.Join(tmpDir, "repacked2.tar.gz")

	err = tarball.CreateDeterministicArchive(repackPath2, extractDir, tarball.CompressionGzip)
	require.NoError(t, err)

	data1, err := os.ReadFile(repackPath)
	require.NoError(t, err)
	data2, err := os.ReadFile(repackPath2)
	require.NoError(t, err)
	assert.Equal(t, data1, data2, "deterministic repack should produce identical output")
}

func createTestTarGz(t *testing.T, path string, entries []testTarEntry) {
	t.Helper()

	var buf bytes.Buffer

	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)

	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Typeflag: entry.typeflag,
		}

		switch entry.typeflag {
		case tar.TypeDir:
			header.Mode = 0o755
		case tar.TypeReg:
			header.Mode = 0o644
			header.Size = int64(len(entry.content))
		case tar.TypeSymlink:
			header.Linkname = entry.linkname
		}

		require.NoError(t, tarWriter.WriteHeader(header))

		if entry.typeflag == tar.TypeReg && len(entry.content) > 0 {
			_, writeErr := tarWriter.Write([]byte(entry.content))
			require.NoError(t, writeErr)
		}
	}

	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzWriter.Close())
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
}

type testTarEntry struct {
	name     string
	typeflag byte
	content  string
	linkname string
}

func TestRoundTrip_AllCompressions(t *testing.T) {
	tests := []struct {
		name string
		ext  string
		comp tarball.Compression
	}{
		{"none", ".tar", tarball.CompressionNone},
		{"gzip", ".tar.gz", tarball.CompressionGzip},
		{"xz", ".tar.xz", tarball.CompressionXZ},
		{"zstd", ".tar.zst", tarball.CompressionZstd},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			sourceDir := filepath.Join(tmpDir, "src")
			extractDir := filepath.Join(tmpDir, "out")
			require.NoError(t, os.MkdirAll(filepath.Join(sourceDir, "sub"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "a.txt"), []byte("alpha"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "sub", "b.txt"), []byte("beta"), 0o600))

			archivePath := filepath.Join(tmpDir, "archive"+testCase.ext)

			require.NoError(t, tarball.CreateDeterministicArchive(archivePath, sourceDir, testCase.comp))
			require.NoError(t, tarball.Extract(archivePath, extractDir, testCase.comp))

			got, err := os.ReadFile(filepath.Join(extractDir, "sub", "b.txt"))
			require.NoError(t, err)
			assert.Equal(t, "beta", string(got))
		})
	}
}

func TestUnsupportedCompression(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "archive.bin")
	require.NoError(t, os.WriteFile(archivePath, []byte("dummy"), 0o600))

	bogus := tarball.Compression(99)

	err := tarball.Extract(archivePath, tmpDir, bogus)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported compression type")

	err = tarball.CreateDeterministicArchive(filepath.Join(tmpDir, "out.bin"), tmpDir, bogus)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported compression type")
}

func TestExtract_SymlinkSafety(t *testing.T) {
	tests := []struct {
		name    string
		entries []testTarEntry
		wantErr bool
	}{
		{
			name: "absolute symlink target rejected",
			entries: []testTarEntry{
				{name: "evil", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
			},
			wantErr: true,
		},
		{
			name: "relative symlink escaping destDir rejected",
			entries: []testTarEntry{
				{name: "evil", typeflag: tar.TypeSymlink, linkname: "../../etc"},
			},
			wantErr: true,
		},
		{
			name: "entry name escaping destDir rejected",
			entries: []testTarEntry{
				{name: "../escape.txt", typeflag: tar.TypeReg, content: "nope"},
			},
			wantErr: true,
		},
		{
			name: "valid internal symlink allowed",
			entries: []testTarEntry{
				{name: "pkg/", typeflag: tar.TypeDir},
				{name: "pkg/real.txt", typeflag: tar.TypeReg, content: "hello"},
				{name: "pkg/link.txt", typeflag: tar.TypeSymlink, linkname: "real.txt"},
			},
			wantErr: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			archivePath := filepath.Join(tmpDir, "test.tar.gz")
			extractDir := filepath.Join(tmpDir, "extracted")
			require.NoError(t, os.MkdirAll(extractDir, 0o755))

			createTestTarGz(t, archivePath, testCase.entries)

			err := tarball.Extract(archivePath, extractDir, tarball.CompressionGzip)

			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
		})
	}
}
