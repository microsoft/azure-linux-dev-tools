// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lockfile

import (
	"fmt"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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
