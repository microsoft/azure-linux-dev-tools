// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

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

// ValidateFilename ensures a filename is safe for use as a destination path.
// It rejects filenames that could escape the destination directory via path traversal.
func ValidateFilename(filename string) error {
	if filename == "" {
		return errors.New("filename cannot be empty")
	}

	// Reject special directory entries.
	if filename == "." || filename == ".." {
		return fmt.Errorf("filename %#q is not a valid file name", filename)
	}

	// Check for absolute paths
	if filepath.IsAbs(filename) {
		return fmt.Errorf("filename %#q cannot be an absolute path", filename)
	}

	cleaned := filepath.Clean(filename)
	if cleaned != filename {
		return fmt.Errorf("filename %#q contains path traversal elements", filename)
	}

	if filepath.Base(filename) != filename {
		return fmt.Errorf("filename %#q must be a simple filename without directory components", filename)
	}

	if strings.ContainsFunc(filename, unicode.IsSpace) {
		return fmt.Errorf("filename %#q must not contain whitespace", filename)
	}

	if strings.ContainsRune(filename, 0) {
		return fmt.Errorf("filename %#q must not contain null bytes", filename)
	}

	// Reject backslashes even on Linux where they are technically valid in
	// filenames. Component names travel across platform boundaries (e.g.,
	// mock chroots, JSON output) where backslashes act as path separators.
	if strings.ContainsRune(filename, '\\') {
		return fmt.Errorf("filename %#q must not contain backslashes", filename)
	}

	return nil
}
