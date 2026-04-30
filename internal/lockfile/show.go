// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lockfile

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
)

// ShowAtCommit reads and parses a lock file at a specific git revision using the
// CLI ('git show'). This is more efficient than go-git for large repositories.
//
// Returns [git.ErrFileNotFound] (wrapped) when the lock file does not exist in
// the revision — callers can check with [errors.Is].
func ShowAtCommit(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	repoDir string,
	revision string,
	lockFileRelPath string,
) (ComponentLock, error) {
	content, err := git.ShowFileAtCommit(ctx, cmdFactory, repoDir, revision, lockFileRelPath)
	if err != nil {
		return ComponentLock{}, fmt.Errorf("failed to read lock file %#q at revision %#q:\n%w",
			lockFileRelPath, revision, err)
	}

	lock, err := Parse([]byte(content))
	if err != nil {
		return ComponentLock{}, fmt.Errorf("failed to parse lock file %#q at revision %#q:\n%w",
			lockFileRelPath, revision, err)
	}

	return *lock, nil
}
