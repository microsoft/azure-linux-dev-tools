// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lockfile_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validLockTOML = `version = 1
import-commit = "aaa111"
upstream-commit = "bbb222"
input-fingerprint = "fff000"
`

func TestShowAtCommit(t *testing.T) {
	const (
		testRepoDir     = "/repo"
		testCommitHash  = "abc123def456"
		testLockRelPath = "locks/curl.lock"
	)

	t.Run("success", func(t *testing.T) {
		ctx := testctx.NewCtx()
		ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
			return validLockTOML, nil
		}

		lock, err := lockfile.ShowAtCommit(
			context.Background(), ctx.CmdFactory, testRepoDir, testCommitHash, testLockRelPath,
		)

		require.NoError(t, err)
		assert.Equal(t, 1, lock.Version)
		assert.Equal(t, "aaa111", lock.ImportCommit)
		assert.Equal(t, "bbb222", lock.UpstreamCommit)
		assert.Equal(t, "fff000", lock.InputFingerprint)
	})

	t.Run("file not found propagates", func(t *testing.T) {
		ctx := testctx.NewCtx()
		ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
			fmt.Fprint(cmd.Stderr, "fatal: path 'locks/curl.lock' does not exist in 'abc123def456'\n")

			return "", errors.New("exit status 128")
		}

		_, err := lockfile.ShowAtCommit(
			context.Background(), ctx.CmdFactory, testRepoDir, testCommitHash, testLockRelPath,
		)

		require.Error(t, err)
		assert.ErrorIs(t, err, git.ErrFileNotFound)
	})

	t.Run("invalid TOML", func(t *testing.T) {
		ctx := testctx.NewCtx()
		ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
			return "this is not valid toml {{{", nil
		}

		_, err := lockfile.ShowAtCommit(
			context.Background(), ctx.CmdFactory, testRepoDir, testCommitHash, testLockRelPath,
		)

		require.Error(t, err)
		require.NotErrorIs(t, err, git.ErrFileNotFound)
		assert.Contains(t, err.Error(), "failed to parse lock file")
	})

	t.Run("wrong version", func(t *testing.T) {
		ctx := testctx.NewCtx()
		ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
			return "version = 99\n", nil
		}

		_, err := lockfile.ShowAtCommit(
			context.Background(), ctx.CmdFactory, testRepoDir, testCommitHash, testLockRelPath,
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported lock file version")
	})
}
