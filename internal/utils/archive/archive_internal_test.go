// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

func TestNewCompressor_GzipOutputIsPinned(t *testing.T) {
	var input bytes.Buffer
	for index := range 20000 {
		fmt.Fprintf(&input, "entry %d value %d\n", index, index*index%7919)
	}

	var output bytes.Buffer

	compressor, closer, err := newCompressor(&output, CompressionGzip)
	require.NoError(t, err)

	_, err = compressor.Write(input.Bytes())
	require.NoError(t, err)
	require.NoError(t, closer.Close())

	sum := sha256.Sum256(output.Bytes())
	assert.Equal(t, "1eaa59dd773566d71c8f53b8760cfa2372cb260e0bd328fc35dd742dbf7afc3c", hex.EncodeToString(sum[:]))
}
