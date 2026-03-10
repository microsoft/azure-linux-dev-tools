// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:wrapcheck // This package is meant to be transparent to users, so we don't wrap errors.
package allowedroots

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/afero"
)

// ErrAccessDenied is returned when access to a file or directory is denied by AllowedRootsFS policy.
// We use syscall.ENOENT (No such file or directory) instead of syscall.EACCES so that CopyOnWriteFs will correctly
// fall back to the base filesystem when paths are outside allowed roots.
var ErrAccessDenied = syscall.ENOENT

// AllowedRootsFS filters files and directories by checking if they are within
// allowed root directories. Only files within the specified root directories
// will be allowed, all others get an access denied error.
//
// Note: This filesystem is intended to be a best-effort check and cannot provide any security guarantees.
type AllowedRootsFS struct {
	allowedRoots []string
	source       afero.Fs
}

// NewAllowedRootsFS creates a new AllowedRootsFS that restricts access to files within the specified root directories.
func NewAllowedRootsFS(source afero.Fs, allowedRoots []string) afero.Fs {
	// Clean and normalize all root paths
	cleanRoots := make([]string, len(allowedRoots))
	for i, root := range allowedRoots {
		cleanRoots[i] = filepath.Clean(root)
	}

	return &AllowedRootsFS{allowedRoots: cleanRoots, source: source}
}

// isPathAllowed checks if the given path is within any of the allowed root directories.
func (s *AllowedRootsFS) isPathAllowed(path string) bool {
	cleanPath := filepath.Clean(path)

	for _, root := range s.allowedRoots {
		// Check if the path is the root itself
		if cleanPath == root {
			return true
		}

		// Special case for root directory "/"
		if root == "/" {
			return true // Everything is under root
		}

		// Check if path is a subdirectory of root
		if strings.HasPrefix(cleanPath, root+string(filepath.Separator)) {
			return true
		}
	}

	return false
}

// pathAllowedOrError returns nil if the path is allowed, otherwise returns ErrAccessDenied.
func (s *AllowedRootsFS) pathAllowedOrError(path string) error {
	if s.isPathAllowed(path) {
		return nil
	}

	return ErrAccessDenied
}

// dirOrAllowed checks if a path is allowed (regardless of whether it's a file or directory).
func (s *AllowedRootsFS) dirOrAllowed(name string) error {
	return s.pathAllowedOrError(name)
}

func (s *AllowedRootsFS) Chtimes(name string, atime, mtime time.Time) error {
	err := s.dirOrAllowed(name)
	if err != nil {
		return err
	}

	return s.source.Chtimes(name, atime, mtime)
}

func (s *AllowedRootsFS) Chmod(name string, mode os.FileMode) error {
	err := s.dirOrAllowed(name)
	if err != nil {
		return err
	}

	return s.source.Chmod(name, mode)
}

func (s *AllowedRootsFS) Chown(name string, uid, gid int) error {
	err := s.dirOrAllowed(name)
	if err != nil {
		return err
	}

	return s.source.Chown(name, uid, gid)
}

func (s *AllowedRootsFS) Name() string {
	return "AllowedRootsFS"
}

func (s *AllowedRootsFS) Stat(name string) (os.FileInfo, error) {
	err := s.dirOrAllowed(name)
	if err != nil {
		return nil, err
	}

	return s.source.Stat(name)
}

func (s *AllowedRootsFS) Rename(oldname, newname string) error {
	err := s.pathAllowedOrError(oldname)
	if err != nil {
		return err
	}

	err = s.pathAllowedOrError(newname)
	if err != nil {
		return err
	}

	return s.source.Rename(oldname, newname)
}

func (s *AllowedRootsFS) Remove(name string) error {
	err := s.dirOrAllowed(name)
	if err != nil {
		return err
	}

	return s.source.Remove(name)
}

func (s *AllowedRootsFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	err := s.dirOrAllowed(name)
	if err != nil {
		return nil, err
	}

	return s.source.OpenFile(name, flag, perm)
}

func (s *AllowedRootsFS) Open(name string) (afero.File, error) {
	err := s.pathAllowedOrError(name)
	if err != nil {
		return nil, err
	}

	return s.source.Open(name)
}

func (s *AllowedRootsFS) Mkdir(name string, perm os.FileMode) error {
	err := s.pathAllowedOrError(name)
	if err != nil {
		return err
	}

	return s.source.Mkdir(name, perm)
}

// allParentPathsAllowedOrError checks if all parent directories of the given path either exist or are allowed to
// be created. If any parent directory is both missing and not allowed, it returns an error.
func (s *AllowedRootsFS) allParentPathsAllowedOrError(path string) error {
	err := s.pathAllowedOrError(path)
	if err != nil {
		return err
	}

	cleanPath := filepath.Clean(path)

	var pathsToCreate []string

	for current := cleanPath; current != "/" && current != "."; current = filepath.Dir(current) {
		if exists, _ := afero.Exists(s.source, current); !exists {
			// Directory doesn't exist, might need to be created
			pathsToCreate = append(pathsToCreate, current)
		} else {
			// Directory exists, stop here
			break
		}
	}

	// Validate that all directories that would be created are allowed
	for _, path := range pathsToCreate {
		err := s.pathAllowedOrError(path)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *AllowedRootsFS) MkdirAll(name string, perm os.FileMode) error {
	err := s.allParentPathsAllowedOrError(name)
	if err != nil {
		return err
	}

	return s.source.MkdirAll(name, perm)
}

func (s *AllowedRootsFS) RemoveAll(path string) error {
	err := s.pathAllowedOrError(path)
	if err != nil {
		return err
	}

	return s.source.RemoveAll(path)
}

func (s *AllowedRootsFS) Create(name string) (afero.File, error) {
	err := s.pathAllowedOrError(name)
	if err != nil {
		return nil, err
	}

	return s.source.Create(name)
}
