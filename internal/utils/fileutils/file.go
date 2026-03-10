// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils

import (
	"os"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/spf13/afero"
)

// Adaptation of [os.WriteFile] that works with [opctx.FS].
func WriteFile(fs opctx.FS, path string, data []byte, perm os.FileMode) error {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return afero.WriteFile(fs, path, data, perm)
}

// Adaptation of [os.ReadFile] that works with [opctx.FS].
func ReadFile(fs opctx.FS, filename string) ([]byte, error) {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return afero.ReadFile(fs, filename)
}

// Adaptation of [filepath.Glob] that works with [opctx.FS] and also uses the
// [doublestar] package to supports standard double-star globs ('**').
func Glob(fs opctx.FS, pattern string, opts ...doublestar.GlobOption) ([]string, error) {
	adaptedFS := afero.NewIOFS(fs)

	//nolint:wrapcheck // We are intentionally a pass-through.
	return doublestar.Glob(
		adaptedFS,
		pattern,
		opts...,
	)
}

// Adaptation of [afero.Exists] that works with [opctx.FS].
// Checks if a file or directory exists at the given path.
func Exists(fs opctx.FS, path string) (bool, error) {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return afero.Exists(fs, path)
}

// Adaptation of [afero.DirExists] that works with [opctx.FS].
// Checks if a directory exists at the given path.
func DirExists(fs opctx.FS, path string) (bool, error) {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return afero.DirExists(fs, path)
}
