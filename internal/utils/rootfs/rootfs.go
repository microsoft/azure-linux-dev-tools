// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package rootfs provides an [afero.Fs] implementation backed by Go's [os.Root] API.
// This confines all filesystem operations to a root directory with atomic
// protection against symlink-based escape attacks via openat2/RESOLVE_BENEATH.
package rootfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/spf13/afero"
)

// RootFs is an [afero.Fs] implementation backed by [os.Root].
// All operations are confined to the root directory using Go's [os.Root] API,
// which provides atomic protection against symlink-based escape attacks
// via the kernel's openat2/RESOLVE_BENEATH on Linux.
//
// Security model:
//   - All paths are treated as relative to the root directory
//   - Leading slashes are stripped (e.g., "/foo" becomes "foo")
//   - Symlinks within the root that resolve within the root are allowed
//   - Symlinks that would escape the root are rejected atomically by the kernel
//   - No TOCTOU vulnerabilities: symlink checks happen at the kernel level
//     during file open, not as separate stat+open operations
type RootFs struct {
	root *os.Root
}

// New creates a new [RootFs] rooted at the given directory path.
// The path must exist and be a directory.
func New(rootPath string) (*RootFs, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("opening root at %#q:\n%w", rootPath, err)
	}

	return &RootFs{root: root}, nil
}

// Close releases resources held by the [RootFs].
func (r *RootFs) Close() error {
	if err := r.root.Close(); err != nil {
		return fmt.Errorf("closing root:\n%w", err)
	}

	return nil
}

// Name returns the name of this filesystem.
func (r *RootFs) Name() string {
	return "RootFs"
}

// Create creates a file in the filesystem, returning the file and any error.
func (r *RootFs) Create(name string) (afero.File, error) {
	name = r.resolvePath(name)

	file, err := r.root.Create(name)
	if err != nil {
		return nil, fmt.Errorf("creating file %#q:\n%w", name, err)
	}

	return file, nil
}

// Mkdir creates a directory in the filesystem.
func (r *RootFs) Mkdir(name string, perm os.FileMode) error {
	name = r.resolvePath(name)

	if err := r.root.Mkdir(name, perm); err != nil {
		return wrapFsError(err, "creating directory %#q", name)
	}

	return nil
}

// MkdirAll creates a directory path and all parents that do not exist yet.
func (r *RootFs) MkdirAll(path string, perm os.FileMode) error {
	path = r.resolvePath(path)

	if path == "." {
		return nil
	}

	if err := r.root.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("creating directory %#q:\n%w", path, err)
	}

	return nil
}

// Open opens a file, returning it or an error.
func (r *RootFs) Open(name string) (afero.File, error) {
	name = r.resolvePath(name)

	file, err := r.root.Open(name)
	if err != nil {
		return nil, wrapFsError(err, "opening file %#q", name)
	}

	return file, nil
}

// OpenFile opens a file using the given flags and mode.
func (r *RootFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	name = r.resolvePath(name)

	file, err := r.root.OpenFile(name, flag, perm)
	if err != nil {
		return nil, wrapFsError(err, "opening file %#q with flags %d", name, flag)
	}

	return file, nil
}

// Remove removes a file or empty directory.
func (r *RootFs) Remove(name string) error {
	name = r.resolvePath(name)

	if err := r.root.Remove(name); err != nil {
		return wrapFsError(err, "removing %#q", name)
	}

	return nil
}

// RemoveAll removes a directory path and all children it contains.
func (r *RootFs) RemoveAll(path string) error {
	path = r.resolvePath(path)

	if err := r.root.RemoveAll(path); err != nil {
		return fmt.Errorf("removing %#q:\n%w", path, err)
	}

	return nil
}

// Rename renames a file or directory within the root atomically.
func (r *RootFs) Rename(oldname, newname string) error {
	oldname = r.resolvePath(oldname)
	newname = r.resolvePath(newname)

	if err := r.root.Rename(oldname, newname); err != nil {
		return fmt.Errorf("renaming %#q to %#q:\n%w", oldname, newname, err)
	}

	return nil
}

// Stat returns file info for the named file.
func (r *RootFs) Stat(name string) (os.FileInfo, error) {
	name = r.resolvePath(name)

	info, err := r.root.Stat(name)
	if err != nil {
		return nil, wrapFsError(err, "stat %#q", name)
	}

	return info, nil
}

// Chmod changes the mode of the named file.
func (r *RootFs) Chmod(name string, mode os.FileMode) error {
	name = r.resolvePath(name)

	if err := r.root.Chmod(name, mode); err != nil {
		return fmt.Errorf("chmod %#q:\n%w", name, err)
	}

	return nil
}

// Chown changes the uid and gid of the named file.
func (r *RootFs) Chown(name string, uid, gid int) error {
	name = r.resolvePath(name)

	if err := r.root.Chown(name, uid, gid); err != nil {
		return fmt.Errorf("chown %#q:\n%w", name, err)
	}

	return nil
}

// Chtimes changes the access and modification times of the named file.
func (r *RootFs) Chtimes(name string, atime time.Time, mtime time.Time) error {
	name = r.resolvePath(name)

	if err := r.root.Chtimes(name, atime, mtime); err != nil {
		return fmt.Errorf("chtimes %#q:\n%w", name, err)
	}

	return nil
}

// resolvePath normalizes a path for use with [os.Root].
// All paths are treated as relative to root. Leading slashes are stripped.
// Security note: The actual confinement is enforced by [os.Root] at the kernel level.
func (r *RootFs) resolvePath(path string) string {
	path = filepath.Clean(path)

	// os.Root expects paths relative to the root, without leading slashes.
	path = strings.TrimPrefix(path, string(os.PathSeparator))

	if path == "" {
		return "."
	}

	return path
}

// wrapFsError wraps filesystem errors but preserves sentinel errors like fs.ErrNotExist
// and fs.ErrExist unwrapped to maintain compatibility with os.IsNotExist() and os.IsExist().
// This is required because afero's utility functions (Exists, DirExists, TempFile, TempDir)
// use the os.Is* functions which don't support error wrapping like errors.Is() does.
func wrapFsError(err error, format string, args ...any) error {
	// Return unwrapped for sentinel errors that callers check with os.Is* functions.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrExist) {
		return err
	}

	return fmt.Errorf(format+":\n%w", append(args, err)...)
}

// Verify [RootFs] implements [afero.Fs] and [opctx.FS] at compile time.
var (
	_ afero.Fs = (*RootFs)(nil)
	_ opctx.FS = (*RootFs)(nil)
)
