// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFileUpdateWriter(t *testing.T) {
	ctx := testctx.NewCtx(testctx.WithHostFS())
	testFilePath := filepath.Join(t.TempDir(), "testfile.txt")

	writer, err := fileutils.NewFileUpdateWriter(ctx.FS(), testFilePath)
	require.NoError(t, err)
	require.NotNil(t, writer)

	// We didn't commit, so the file shouldn't exist.
	assert.NoFileExists(t, testFilePath)
}

func TestNewFileUpdateWriter_Commit(t *testing.T) {
	ctx := testctx.NewCtx(testctx.WithHostFS())
	testFilePath := filepath.Join(t.TempDir(), "testfile.txt")

	writer, err := fileutils.NewFileUpdateWriter(ctx.FS(), testFilePath)
	require.NoError(t, err)

	err = writer.Commit()
	require.NoError(t, err)
	assert.FileExists(t, testFilePath)
}

func TestNewFileUpdateWriter_MultipleCommitCalls(t *testing.T) {
	ctx := testctx.NewCtx(testctx.WithHostFS())
	testFilePath := filepath.Join(t.TempDir(), "testfile.txt")

	writer, err := fileutils.NewFileUpdateWriter(ctx.FS(), testFilePath)
	require.NoError(t, err)

	err = writer.Commit()
	require.NoError(t, err)

	// Commit should fail, returning a standard "already closed" error.
	err = writer.Commit()
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestNewFileUpdateWriter_Write(t *testing.T) {
	const testContent = "Hello, World!"

	ctx := testctx.NewCtx(testctx.WithHostFS())
	testFilePath := filepath.Join(t.TempDir(), "testfile.txt")

	writer, err := fileutils.NewFileUpdateWriter(ctx.FS(), testFilePath)
	require.NoError(t, err)

	// Copy the test content into the writer.
	reader := strings.NewReader(testContent)
	written, err := io.Copy(writer, reader)

	// Make sure we wrote it all.
	require.NoError(t, err)
	require.Equal(t, int64(len(testContent)), written)

	// Commit, so we can check that everything happened correctly.
	err = writer.Commit()
	require.NoError(t, err)

	// Read back the file we just wrote, and confirm its contents are what we tried to write.
	bytes, err := os.ReadFile(testFilePath)
	require.NoError(t, err)
	require.Equal(t, testContent, string(bytes))
}
