// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package dirdiff_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/dirdiff"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffResult_String_EmptyResult(t *testing.T) {
	result := &dirdiff.DiffResult{}
	assert.Empty(t, result.String())
}

func TestDiffResult_ColorString_EmptyResult(t *testing.T) {
	result := &dirdiff.DiffResult{}
	assert.Empty(t, result.ColorString())
}

func TestDiffResult_ColorString_ContainsANSICodes(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	require.NoError(t, fileutils.WriteFile(testFS, "/a/file.txt", []byte("old line\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/file.txt", []byte("new line\n"), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	colored := result.ColorString()

	// Colorized output should contain ANSI escape codes (\x1b[).
	assert.Contains(t, colored, "\x1b[", "expected ANSI escape codes in colorized output")

	// Plain String() should NOT contain ANSI escape codes.
	plain := result.String()
	assert.NotContains(t, plain, "\x1b[", "plain output should not contain ANSI escape codes")

	// Both should contain the actual diff content (file paths, changed lines).
	assert.Contains(t, colored, "file.txt")
	assert.Contains(t, colored, "old line")
	assert.Contains(t, colored, "new line")
}

func TestDiffResult_ColorString_BinaryFile(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	binaryContent := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x00, 0x00, 0x01}

	require.NoError(t, fileutils.WriteFile(testFS, "/a/image.png", binaryContent, fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/image.png", append(binaryContent, 0x02), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	colored := result.ColorString()

	// The "Binary files" line should be styled (contains ANSI codes).
	assert.Contains(t, colored, "\x1b[", "expected ANSI escape codes in binary file colorized output")
	assert.Contains(t, colored, "Binary files")
}
