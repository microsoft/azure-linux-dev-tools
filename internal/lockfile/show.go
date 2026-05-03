// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lockfile

import (
	"errors"
	"fmt"
	gopath "path"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ShowAtCommit reads and parses a lock file at a specific commit hash
// using go-git and parses it into a [ComponentLock].
func ShowAtCommit(
	repo *gogit.Repository,
	commitHash string,
	lockFileRelPath string,
) (ComponentLock, error) {
	hash := plumbing.NewHash(commitHash)

	commitObj, err := repo.CommitObject(hash)
	if err != nil {
		return ComponentLock{}, fmt.Errorf("failed to resolve commit %#q:\n%w", commitHash, err)
	}

	tree, err := commitObj.Tree()
	if err != nil {
		return ComponentLock{}, fmt.Errorf("failed to get tree for commit %#q:\n%w", commitHash, err)
	}

	file, err := tree.File(lockFileRelPath)
	if err != nil {
		return ComponentLock{}, fmt.Errorf("failed to read %#q at commit %#q:\n%w",
			lockFileRelPath, commitHash, err)
	}

	content, err := file.Contents()
	if err != nil {
		return ComponentLock{}, fmt.Errorf("failed to read contents of %#q at commit %#q:\n%w",
			lockFileRelPath, commitHash, err)
	}

	lock, err := Parse([]byte(content))
	if err != nil {
		return ComponentLock{}, fmt.Errorf("failed to parse lock file %#q at commit %#q:\n%w",
			lockFileRelPath, commitHash, err)
	}

	return *lock, nil
}

// ReadAllAtCommit reads and parses all lock files from a directory at a specific
// commit. Returns a map of component name → [ComponentLock]. The lockRelDir must
// be a POSIX-style repo-relative path (e.g., "locks"). Use "" or "." for root.
// Absolute paths and ".." escapes are rejected.
//
// Returns an empty map (not an error) when the lock directory does not exist in
// the commit tree. Returns an error if any individual lock file fails to parse.
func ReadAllAtCommit(
	repo *gogit.Repository,
	commitHash string,
	lockRelDir string,
) (map[string]ComponentLock, error) {
	lockRelDir, err := cleanLockRelDir(lockRelDir)
	if err != nil {
		return nil, err
	}

	lockSubtree, err := resolveLockSubtree(repo, commitHash, lockRelDir)
	if err != nil {
		return nil, err
	}

	if lockSubtree == nil {
		return make(map[string]ComponentLock), nil
	}

	return parseLockEntries(lockSubtree, lockRelDir, commitHash)
}

// cleanLockRelDir normalizes and validates a repo-relative lock directory path.
func cleanLockRelDir(lockRelDir string) (string, error) {
	lockRelDir = gopath.Clean(lockRelDir)

	if gopath.IsAbs(lockRelDir) {
		return "", fmt.Errorf("lockRelDir must be repo-relative, got absolute path %#q", lockRelDir)
	}

	if strings.HasPrefix(lockRelDir, "..") {
		return "", fmt.Errorf("lockRelDir %#q escapes repository root", lockRelDir)
	}

	return lockRelDir, nil
}

// resolveLockSubtree resolves the lock directory tree at a specific commit.
// Returns nil when the directory does not exist (not an error).
func resolveLockSubtree(
	repo *gogit.Repository, commitHash, lockRelDir string,
) (*object.Tree, error) {
	hash := plumbing.NewHash(commitHash)

	commitObj, err := repo.CommitObject(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve commit %#q:\n%w", commitHash, err)
	}

	tree, err := commitObj.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree for commit %#q:\n%w", commitHash, err)
	}

	// "." means root tree — no subtree lookup needed.
	if lockRelDir == "" || lockRelDir == "." {
		return tree, nil
	}

	lockSubtree, err := tree.Tree(lockRelDir)
	if err != nil {
		if errors.Is(err, object.ErrDirectoryNotFound) || errors.Is(err, object.ErrEntryNotFound) {
			return nil, nil //nolint:nilnil // nil tree signals "directory absent".
		}

		return nil, fmt.Errorf("failed to read lock directory %#q at commit %#q:\n%w",
			lockRelDir, commitHash, err)
	}

	return lockSubtree, nil
}

// parseLockEntries reads all lock files from a resolved tree.
func parseLockEntries(
	lockSubtree *object.Tree, lockRelDir, commitHash string,
) (map[string]ComponentLock, error) {
	locks := make(map[string]ComponentLock)

	for _, entry := range lockSubtree.Entries {
		if !entry.Mode.IsFile() ||
			strings.HasPrefix(entry.Name, ".") ||
			!strings.HasSuffix(entry.Name, lockFileExtension) {
			continue
		}

		name := strings.TrimSuffix(entry.Name, lockFileExtension)
		entryPath := gopath.Join(lockRelDir, entry.Name)

		file, fileErr := lockSubtree.File(entry.Name)
		if fileErr != nil {
			return nil, fmt.Errorf("failed to read lock file %#q at commit %#q:\n%w",
				entryPath, commitHash, fileErr)
		}

		content, contentErr := file.Contents()
		if contentErr != nil {
			return nil, fmt.Errorf("failed to read contents of %#q at commit %#q:\n%w",
				entryPath, commitHash, contentErr)
		}

		lock, parseErr := Parse([]byte(content))
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse lock file %#q at commit %#q:\n%w",
				entryPath, commitHash, parseErr)
		}

		locks[name] = *lock
	}

	return locks, nil
}
