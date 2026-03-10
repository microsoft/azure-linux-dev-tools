// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/spf13/afero"
)

var (
	// ErrReadOnlyEmbedFS is returned when a write operation is attempted on an embedded FS.
	ErrReadOnlyEmbedFS = errors.New("embed filesystem is read-only")

	// ErrUnimplementedForEmbedFS is returned when an operation is not implemented for an embedded FS.
	ErrUnimplementedForEmbedFS = errors.New("operation unimplemented for embed filesystem")
)

// WrapEmbedFS returns an [opctx.FS] that wraps an [embed.FS] instance.
func WrapEmbedFS(embedFS *embed.FS) opctx.FS {
	return &embedFSAdapter{fs: embedFS}
}

type embedFSAdapter struct {
	fs *embed.FS
}

var _ opctx.FS = &embedFSAdapter{}

type embedFileAdapter struct {
	fs *embed.FS

	openPath string
	file     fs.File
}

var _ opctx.File = &embedFileAdapter{}

func (e *embedFSAdapter) Chmod(name string, mode os.FileMode) error {
	return ErrReadOnlyEmbedFS
}

func (e *embedFSAdapter) Chown(name string, uid int, gid int) error {
	return ErrReadOnlyEmbedFS
}

func (e *embedFSAdapter) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return ErrReadOnlyEmbedFS
}

func (e *embedFSAdapter) Create(name string) (afero.File, error) {
	return nil, ErrReadOnlyEmbedFS
}

func (e *embedFSAdapter) Mkdir(name string, perm os.FileMode) error {
	return ErrReadOnlyEmbedFS
}

func (e *embedFSAdapter) MkdirAll(path string, perm os.FileMode) error {
	return ErrReadOnlyEmbedFS
}

func (e *embedFSAdapter) Name() string {
	return "embed"
}

func (e *embedFSAdapter) Open(name string) (openedFile afero.File, err error) {
	var file fs.File

	// Remove leading slashes because embed.FS doesn't like them.
	embedPath := strings.TrimPrefix(name, "/")

	file, err = e.fs.Open(embedPath)
	if err != nil {
		//nolint:wrapcheck // We are intentionally a pass-through.
		return nil, err
	}

	return &embedFileAdapter{fs: e.fs, openPath: name, file: file}, nil
}

func (e *embedFSAdapter) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	// NOTE: We ignore flags and permissions.
	return e.Open(name)
}

func (e *embedFSAdapter) Remove(name string) error {
	return ErrReadOnlyEmbedFS
}

func (e *embedFSAdapter) RemoveAll(path string) error {
	return ErrReadOnlyEmbedFS
}

func (e *embedFSAdapter) Rename(oldname string, newname string) error {
	return ErrReadOnlyEmbedFS
}

func (e *embedFSAdapter) Stat(name string) (os.FileInfo, error) {
	// Remove leading slashes because embed.FS doesn't like them.
	embedPath := strings.TrimPrefix(name, "/")

	file, err := e.fs.Open(embedPath)
	if err != nil {
		//nolint:wrapcheck // We are intentionally a pass-through.
		return nil, err
	}

	//nolint:wrapcheck // We are intentionally a pass-through.
	return file.Stat()
}

func (e *embedFileAdapter) Close() error {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return e.file.Close()
}

func (e *embedFileAdapter) Name() string {
	return e.openPath
}

func (e *embedFileAdapter) Read(p []byte) (n int, err error) {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return e.file.Read(p)
}

func (e *embedFileAdapter) ReadAt(p []byte, off int64) (n int, err error) {
	return 0, ErrUnimplementedForEmbedFS
}

func (e *embedFileAdapter) Readdir(count int) ([]os.FileInfo, error) {
	// NOTE: Our expected use cases don't perform partial/incremental reads; we do not
	// implement support for `count` being a positive integer. If the provided count is
	// negative or zero, we are expected to read all entries.
	if count > 0 {
		return nil, ErrUnimplementedForEmbedFS
	}

	embedPath := strings.TrimPrefix(e.openPath, "/")

	entries, err := e.fs.ReadDir(embedPath)
	if err != nil {
		//nolint:wrapcheck // We are intentionally a pass-through.
		return []os.FileInfo{}, err
	}

	info := make([]os.FileInfo, 0, len(entries))

	for _, entry := range entries {
		fileInfo, err := entry.Info()
		if err != nil {
			//nolint:wrapcheck // We are intentionally a pass-through.
			return []os.FileInfo{}, err
		}

		info = append(info, fileInfo)
	}

	return info, nil
}

func (e *embedFileAdapter) Readdirnames(n int) ([]string, error) {
	info, err := e.Readdir(n)
	if err != nil {
		return []string{}, err
	}

	names := make([]string, len(info))
	for i, entry := range info {
		names[i] = entry.Name()
	}

	return names, nil
}

func (e *embedFileAdapter) Seek(offset int64, whence int) (int64, error) {
	return 0, ErrUnimplementedForEmbedFS
}

func (e *embedFileAdapter) Stat() (os.FileInfo, error) {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return e.file.Stat()
}

func (e *embedFileAdapter) Sync() error {
	return ErrReadOnlyEmbedFS
}

func (e *embedFileAdapter) Truncate(size int64) error {
	return ErrReadOnlyEmbedFS
}

func (e *embedFileAdapter) Write(p []byte) (n int, err error) {
	return 0, ErrReadOnlyEmbedFS
}

func (e *embedFileAdapter) WriteAt(p []byte, off int64) (n int, err error) {
	return 0, ErrReadOnlyEmbedFS
}

func (e *embedFileAdapter) WriteString(s string) (ret int, err error) {
	return 0, ErrReadOnlyEmbedFS
}
