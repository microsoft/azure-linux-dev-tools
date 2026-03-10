// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils

import (
	"fmt"
	"os"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/spf13/afero"
)

// NOTE: The sole point of this function is to provide a single place to decide
// the correct permissions for the directory.
func MkdirAll(fs opctx.FS, path string) error {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return fs.MkdirAll(path, fileperms.PublicDir)
}

// Wrapper that calls [MkdirTemp] with the default temp dir.
func MkdirTempInTempDir(fs opctx.FS, pattern string) (string, error) {
	return MkdirTemp(fs, afero.GetTempDir(fs, ""), pattern)
}

// Adaptation of [os.TempDir] that works with [opctx.FS].
func MkdirTemp(fs opctx.FS, dir, pattern string) (string, error) {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return afero.TempDir(fs, dir, pattern)
}

// Utility function intended for use in defer statements; calls opctx.FS.RemoveAll
// and, on error, updates the error value pointed to by errorToUpdate. Intentionally
// does *not* update the error pointed to by errorToUpdate on success cases, to avoid
// clobbering an error that may already be stored there.
func RemoveAllAndUpdateErrorIfNil(fs opctx.FS, path string, errorToUpdate *error) {
	removeErr := fs.RemoveAll(path)
	if removeErr == nil {
		return
	}

	if *errorToUpdate != nil {
		return
	}

	*errorToUpdate = fmt.Errorf("failed to remove:\n%w", removeErr)
}

// Readdir is an adaptation of [os.ReadDir] that works with [opctx.FS]; notably, it returns
// a slice of [os.FileInfo] instead of [fs.DirEntry].
func ReadDir(fs opctx.FS, name string) (entries []os.FileInfo, err error) {
	dir, err := fs.Open(name)
	if err != nil {
		//nolint:wrapcheck // We are intentionally a pass-through.
		return []os.FileInfo{}, err
	}

	defer defers.HandleDeferError(dir.Close, &err)

	// Pass -1 to read *all* entries in the dir.
	//nolint:wrapcheck // We are intentionally a pass-through.
	return dir.Readdir(-1)
}

// IsDirEmpty checks if a directory exists and is empty.
func IsDirEmpty(fs opctx.FS, path string) (bool, error) {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return afero.IsEmpty(fs, path)
}
