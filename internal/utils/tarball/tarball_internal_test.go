// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package tarball

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtract_OversizedEntryRejected(t *testing.T) {
	previous := maxEntryBytes
	maxEntryBytes = 5

	t.Cleanup(func() { maxEntryBytes = previous })

	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "big.tar.gz")
	extractDir := filepath.Join(tmpDir, "out")
	require.NoError(t, os.MkdirAll(extractDir, 0o755))

	var buf bytes.Buffer

	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)

	const content = "hello world" // 11 bytes > 5

	require.NoError(t, tarWriter.WriteHeader(&tar.Header{
		Name:     "pkg/huge.bin",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}))
	_, err := tarWriter.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzWriter.Close())
	require.NoError(t, os.WriteFile(archivePath, buf.Bytes(), 0o600))

	err = Extract(archivePath, extractDir, CompressionGzip)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds max of 5")

	_, statErr := os.Stat(filepath.Join(extractDir, "pkg", "huge.bin"))
	assert.True(t, os.IsNotExist(statErr), "oversized entry must not leave a partial file (stat err=%v)", statErr)
}
