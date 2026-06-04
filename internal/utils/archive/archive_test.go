// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package archive_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/archive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectCompression(t *testing.T) {
	tests := []struct {
		filename string
		expected archive.Compression
		wantErr  bool
	}{
		{"pkg-1.0.tar.gz", archive.CompressionGzip, false},
		{"pkg-1.0.tgz", archive.CompressionGzip, false},
		{"pkg-1.0.tar.bz2", archive.CompressionNone, true},
		{"pkg-1.0.tar.xz", archive.CompressionXZ, false},
		{"pkg-1.0.txz", archive.CompressionXZ, false},
		{"pkg-1.0.tar.zst", archive.CompressionZstd, false},
		{"pkg-1.0.tzst", archive.CompressionZstd, false},
		{"pkg-1.0.tar", archive.CompressionNone, false},
		{"pkg-1.0.zip", archive.CompressionNone, true},
		{"PKG-1.0.TAR.GZ", archive.CompressionGzip, false},
	}

	for _, testCase := range tests {
		t.Run(testCase.filename, func(t *testing.T) {
			comp, err := archive.DetectCompression(testCase.filename)

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

	err := archive.Extract(archivePath, extractDir, archive.CompressionGzip)
	require.NoError(t, err)

	content, readErr := os.ReadFile(filepath.Join(extractDir, "pkg-1.0", "hello.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "hello world", string(content))

	repackPath := filepath.Join(tmpDir, "repacked.tar.gz")

	err = archive.CreateDeterministicArchive(repackPath, extractDir, archive.CompressionGzip)
	require.NoError(t, err)

	err = archive.Extract(repackPath, repackDir, archive.CompressionGzip)
	require.NoError(t, err)

	content, readErr = os.ReadFile(filepath.Join(repackDir, "pkg-1.0", "hello.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "hello world", string(content))

	// Repack twice and verify byte-for-byte identical output.
	repackPath2 := filepath.Join(tmpDir, "repacked2.tar.gz")

	err = archive.CreateDeterministicArchive(repackPath2, extractDir, archive.CompressionGzip)
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
		comp archive.Compression
	}{
		{"none", ".tar", archive.CompressionNone},
		{"gzip", ".tar.gz", archive.CompressionGzip},
		{"xz", ".tar.xz", archive.CompressionXZ},
		{"zstd", ".tar.zst", archive.CompressionZstd},
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

			require.NoError(t, archive.CreateDeterministicArchive(archivePath, sourceDir, testCase.comp))
			require.NoError(t, archive.Extract(archivePath, extractDir, testCase.comp))

			got, err := os.ReadFile(filepath.Join(extractDir, "sub", "b.txt"))
			require.NoError(t, err)
			assert.Equal(t, "beta", string(got))

			// Create a second archive over identical input and verify
			// byte-for-byte determinism across all compression modes.
			archivePath2 := filepath.Join(tmpDir, "archive2"+testCase.ext)
			require.NoError(t, archive.CreateDeterministicArchive(archivePath2, sourceDir, testCase.comp))

			data1, err := os.ReadFile(archivePath)
			require.NoError(t, err)
			data2, err := os.ReadFile(archivePath2)
			require.NoError(t, err)
			assert.Equal(t, data1, data2, "deterministic archive should produce identical output")
		})
	}
}

func TestUnsupportedCompression(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "archive.bin")
	require.NoError(t, os.WriteFile(archivePath, []byte("dummy"), 0o600))

	bogus := archive.Compression(99)

	err := archive.Extract(archivePath, tmpDir, bogus)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported compression type")

	err = archive.CreateDeterministicArchive(filepath.Join(tmpDir, "out.bin"), tmpDir, bogus)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported compression type")
}

func TestCreateDeterministicArchive_PreservesSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "src")
	externalDir := filepath.Join(tmpDir, "external")

	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.MkdirAll(externalDir, 0o755))

	// A regular file inside the source tree, plus an external file whose
	// contents must NOT end up embedded in the archive.
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "real.txt"), []byte("inside"), 0o600))

	const externalContent = "must-not-be-archived"

	externalPath := filepath.Join(externalDir, "secret.txt")
	require.NoError(t, os.WriteFile(externalPath, []byte(externalContent), 0o600))

	// Symlink staying inside the source tree (relative target).
	require.NoError(t, os.Symlink("real.txt", filepath.Join(sourceDir, "internal-link")))
	// Symlink pointing outside the source tree (absolute target).
	require.NoError(t, os.Symlink(externalPath, filepath.Join(sourceDir, "external-link")))

	archivePath := filepath.Join(tmpDir, "archive.tar")
	require.NoError(t, archive.CreateDeterministicArchive(archivePath, sourceDir, archive.CompressionNone))

	archiveBytes, err := os.ReadFile(archivePath)
	require.NoError(t, err)

	type entryInfo struct {
		header  *tar.Header
		content string
	}

	entriesByName := map[string]entryInfo{}

	reader := tar.NewReader(bytes.NewReader(archiveBytes))

	for {
		header, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}

		require.NoError(t, readErr)

		var content string

		if header.Typeflag == tar.TypeReg {
			body, bodyErr := io.ReadAll(reader)
			require.NoError(t, bodyErr)

			content = string(body)
		}

		entriesByName[header.Name] = entryInfo{header: header, content: content}
	}

	for name, entry := range entriesByName {
		if entry.header.Typeflag == tar.TypeReg {
			assert.NotContains(t, entry.content, externalContent,
				"regular file entry %#q must not contain external content", name)
		}
	}

	internalEntry, found := entriesByName["internal-link"]
	require.True(t, found, "internal symlink entry missing from archive")
	assert.Equal(t, byte(tar.TypeSymlink), internalEntry.header.Typeflag)
	assert.Equal(t, "real.txt", internalEntry.header.Linkname)
	assert.Zero(t, internalEntry.header.Size, "symlink entries must not carry payload bytes")

	externalEntry, found := entriesByName["external-link"]
	require.True(t, found, "external symlink entry missing from archive")
	assert.Equal(t, byte(tar.TypeSymlink), externalEntry.header.Typeflag)
	assert.Equal(t, externalPath, externalEntry.header.Linkname,
		"external symlink target must be recorded verbatim, not dereferenced")
	assert.Zero(t, externalEntry.header.Size)
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
		{
			name: "relative dotdot symlink staying inside root allowed",
			entries: []testTarEntry{
				{name: "src/", typeflag: tar.TypeDir},
				{name: "src/espeak-ng/", typeflag: tar.TypeDir},
				{name: "src/espeak-ng/speak_lib.h", typeflag: tar.TypeReg, content: "header"},
				{name: "src/espeak/", typeflag: tar.TypeDir},
				{name: "src/espeak/speak_lib.h", typeflag: tar.TypeSymlink, linkname: "../espeak-ng/speak_lib.h"},
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

			err := archive.Extract(archivePath, extractDir, archive.CompressionGzip)

			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
		})
	}
}
