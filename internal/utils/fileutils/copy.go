// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/spf13/afero"
)

// Options regarding file copying.
type CopyFileOptions struct {
	// Whether or not to preserve POSIX mode bits on files. Does *not* apply to directories.
	PreserveFileMode bool
}

// Copies the contents of an existing file to a new file at the given destination path.
// May preserve mode bits if "options" says so but does *not* preserve ownership, timestamps,
// or other metadata. *Does* follow symlinks. Is enlightened to skip copying in dry-run mode.
func CopyFile(dryRunnable opctx.DryRunnable, fs opctx.FS, sourcePath, destPath string, options CopyFileOptions) error {
	return CopyFileCrossFS(dryRunnable, fs, sourcePath, fs, destPath, options)
}

// Copies the contents of an existing file to a new file at the given destination path,
// potentially across two [opctx.FS] filesystem instances. May preserve mode bits if
// "options" says so but does *not* preserve ownership, timestamps, or other metadata.
// *Does* follow symlinks. Is *not* enlightened to skip copying in dry-run mode.
func CopyFileCrossFS(
	dryRunnable opctx.DryRunnable,
	sourceFS opctx.FS, sourcePath string,
	destFS opctx.FS, destPath string,
	options CopyFileOptions,
) (err error) {
	// Use a default read/write mode; umask will further restrict this as appropriate.
	const defaultPerms = os.FileMode(0o666)

	if dryRunnable.DryRun() {
		slog.Info("Dry run; would copy file", "source", sourcePath, "dest", destPath)

		return nil
	}

	destFileMode := defaultPerms

	if options.PreserveFileMode {
		stat, err := sourceFS.Stat(sourcePath)
		if err != nil {
			err = fmt.Errorf("failed to stat copy source '%s':\n%w", sourcePath, err)

			return err
		}

		destFileMode = stat.Mode()
	}

	sourceFile, err := sourceFS.Open(sourcePath)
	if err != nil {
		err = fmt.Errorf("failed to open copy source '%s':\n%w", sourcePath, err)

		return err
	}

	defer sourceFile.Close()

	destFile, err := destFS.OpenFile(destPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, destFileMode)
	if err != nil {
		err = fmt.Errorf("failed to create copy destination '%s':\n%w", destPath, err)

		return err
	}

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		err = fmt.Errorf("failed to copy '%s' to '%s':\n%w", sourcePath, destPath, err)

		return err
	}

	err = destFile.Close()
	if err != nil {
		err = fmt.Errorf("failed to close copy dest '%s':\n%w", destPath, err)

		return err
	}

	return nil
}

// Options regarding recursive directory copying.
type CopyDirOptions struct {
	CopyFileOptions

	// FileFilter is an optional callback invoked for each file before copying.
	// It receives the destination filesystem and the destination file path.
	// If it returns false, the file is skipped. If nil, all files are copied.
	FileFilter func(destFS opctx.FS, destPath string) (bool, error)
}

// SkipExistingFiles is a [CopyDirOptions.FileFilter] that skips files already present
// at the destination path, allowing pre-existing files to take precedence.
func SkipExistingFiles(destFS opctx.FS, destPath string) (bool, error) {
	exists, err := Exists(destFS, destPath)
	if err != nil {
		return false, fmt.Errorf("failed to check if file exists at '%s':\n%w", destPath, err)
	}

	if exists {
		slog.Debug("File already exists, skipping copy", "destPath", destPath)

		return false, nil
	}

	return true, nil
}

// Recursively copies a directory. May preserve mode bits if "options" says so but does *not*
// preserve ownership, timestamps, or other metadata. *Does* follow symlinks. Is enlightened
// to skip copying in dry-run mode.
func CopyDirRecursive(
	dryRunnable opctx.DryRunnable, fs opctx.FS,
	sourceDirPath, destDirPath string,
	options CopyDirOptions,
) (err error) {
	return CopyDirRecursiveCrossFS(dryRunnable, fs, sourceDirPath, fs, destDirPath, options)
}

// Recursively copies a directory, potentially across two [opctx.FS] filesystem instances. May preserve
// mode bits if "options" says so but does *not* preserve ownership, timestamps, or other metadata.
// *Does* follow symlinks. Is *not* enlightened to skip copying in dry-run mode.
func CopyDirRecursiveCrossFS(
	dryRunnable opctx.DryRunnable,
	sourceFS opctx.FS, sourceDirPath string,
	destFS opctx.FS, destDirPath string,
	options CopyDirOptions,
) (err error) {
	// Use a default read/write/execute mode; umask will further restrict this as appropriate.
	const defaultDirPerms = os.FileMode(0o777)

	if dryRunnable.DryRun() {
		slog.Info("Dry run; would recursively copy dir", "source", sourceDirPath, "dest", destDirPath)

		return nil
	}

	var fileInfo os.FileInfo

	if fileInfo, err = sourceFS.Stat(sourceDirPath); err != nil {
		return fmt.Errorf("failed to stat source dir '%s':\n%w", sourceDirPath, err)
	}

	if !fileInfo.IsDir() {
		return fmt.Errorf("can't recursively copy non-directory path '%s'", sourceDirPath)
	}

	// Ensure dest dir exists.
	err = destFS.MkdirAll(destDirPath, defaultDirPerms)
	if err != nil {
		return fmt.Errorf("failed to ensure dest dir '%s' exists:\n%w", destDirPath, err)
	}

	var sourceDir opctx.File

	// Open the source dir.
	sourceDir, err = sourceFS.Open(sourceDirPath)
	if err != nil {
		return fmt.Errorf("failed to open source dir '%s':\n%w", sourceDirPath, err)
	}

	defer sourceDir.Close()

	var entries []os.FileInfo

	entries, err = sourceDir.Readdir(-1)
	if err != nil {
		return fmt.Errorf("failed to read source dir '%s':\n%w", sourceDirPath, err)
	}

	for _, entry := range entries {
		sourcePath := filepath.Join(sourceDirPath, entry.Name())
		destPath := filepath.Join(destDirPath, entry.Name())

		if entry.IsDir() {
			err = CopyDirRecursiveCrossFS(dryRunnable, sourceFS, sourcePath, destFS, destPath, options)
			if err != nil {
				return fmt.Errorf("failed to recursively copy dir '%s' to '%s':\n%w", sourcePath, destPath, err)
			}

			continue
		}

		if options.FileFilter != nil {
			shouldCopy, filterErr := options.FileFilter(destFS, destPath)
			if filterErr != nil {
				return fmt.Errorf("file filter error for '%s':\n%w", destPath, filterErr)
			}

			if !shouldCopy {
				continue
			}
		}

		err = CopyFileCrossFS(
			dryRunnable,
			sourceFS, sourcePath,
			destFS, destPath,
			options.CopyFileOptions,
		)
		if err != nil {
			return fmt.Errorf("failed to copy file '%s' to '%s':\n%w", sourcePath, destPath, err)
		}
	}

	return nil
}

// SymLinkOrCopy attempts to symlink a file, falling back to copy if symlinking
// is not supported or fails. Symlinks are only attempted on real OS filesystems
// (afero.OsFs). For other filesystem types (e.g., in-memory filesystems used in tests),
// this function logs a warning and falls back directly to copying.
func SymLinkOrCopy(
	dryRunnable opctx.DryRunnable,
	fs opctx.FS,
	sourcePath, destPath string,
	options CopyFileOptions,
) error {
	if dryRunnable.DryRun() {
		slog.Info("Dry run; would symlink or copy file", "source", sourcePath, "dest", destPath)

		return nil
	}

	// See if the filesystem supports symlinks.
	if linker, ok := fs.(afero.Linker); ok {
		err := linker.SymlinkIfPossible(sourcePath, destPath)
		if err == nil {
			return nil
		}

		// Symlink failed. Warn and fall back to copy.
		slog.Warn("Symlink failed, falling back to copy",
			"source", sourcePath,
			"dest", destPath,
			"error", err,
		)
	} else {
		// Not supported, skip symlink attempt.
		slog.Warn("Symlinking not supported on this filesystem, falling back to copy",
			"source", sourcePath,
			"dest", destPath,
			"fsType", fmt.Sprintf("%T", fs),
		)
	}

	return CopyFile(dryRunnable, fs, sourcePath, destPath, options)
}
