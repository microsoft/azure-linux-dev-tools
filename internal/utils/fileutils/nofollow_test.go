// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/sys/unix"
)

func TestOpenRegularFileNoFollow(t *testing.T) {
	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/regular", []byte("contents"), fileperms.PublicFile))
	file, err := fileutils.OpenRegularFileNoFollow(ctx.FS(), "/regular")
	require.NoError(t, err)

	defer file.Close()

	data, err := io.ReadAll(file)
	require.NoError(t, err)
	assert.Equal(t, "contents", string(data))

	missing, err := fileutils.OpenRegularFileNoFollow(ctx.FS(), "/missing")
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Nil(t, missing)
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), "/directory"))
	directory, err := fileutils.OpenRegularFileNoFollow(ctx.FS(), "/directory")
	require.ErrorContains(t, err, "not a regular file")
	assert.Nil(t, directory)
}

func TestOpenRegularFileNoFollowFlagsAndStatErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockFS := opctx_test.NewMockFS(ctrl)
	flags := os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK
	mockFS.EXPECT().OpenFile("/denied", flags, os.FileMode(0)).Return(nil, os.ErrPermission)
	file, err := fileutils.OpenRegularFileNoFollow(mockFS, "/denied")
	require.ErrorIs(t, err, os.ErrPermission)
	assert.Nil(t, file)

	opened := opctx_test.NewMockFile(ctrl)
	mockFS.EXPECT().OpenFile("/bad-stat", flags, os.FileMode(0)).Return(opened, nil)
	opened.EXPECT().Stat().Return(nil, os.ErrPermission)
	opened.EXPECT().Close().Return(nil)

	file, err = fileutils.OpenRegularFileNoFollow(mockFS, "/bad-stat")
	require.ErrorIs(t, err, os.ErrPermission)
	assert.Nil(t, file)
}

// Kernel-enforced symlink rejection requires a real filesystem.
func TestCopyFileNoFollowRejectsSymlinks(t *testing.T) {
	ctx := testctx.NewCtx(testctx.WithHostFS())
	root := t.TempDir()
	target, link, output := filepath.Join(root, "target"), filepath.Join(root, "link"), filepath.Join(root, "output")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), target, []byte("host-only contents"), fileperms.PublicFile))
	require.NoError(t, os.Symlink(target, link))
	err := fileutils.CopyFile(ctx, ctx.FS(), link, output, fileutils.CopyFileOptions{NoFollow: true})
	require.ErrorContains(t, err, "without following symlinks")
	exists, err := fileutils.Exists(ctx.FS(), output)
	require.NoError(t, err)
	assert.False(t, exists)
}
