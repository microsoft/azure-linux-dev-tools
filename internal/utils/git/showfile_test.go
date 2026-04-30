// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package git_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/externalcmd"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestShowFileAtCommit(t *testing.T) {
	const (
		testRepoDir   = "/repo"
		testCommit    = "abc123def456"
		testRelPath   = "locks/curl.lock"
		testContent   = "version = 1\nimport-commit = \"aaa\"\n"
		testObjectRef = testCommit + ":" + testRelPath
		gitShowArg    = "show"
	)

	t.Run("success", func(t *testing.T) {
		ctx := testctx.NewCtx()
		ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
			assert.Contains(t, cmd.Args, gitShowArg)
			assert.Contains(t, cmd.Args, testObjectRef)

			return testContent, nil
		}

		content, err := git.ShowFileAtCommit(context.Background(), ctx.CmdFactory, testRepoDir, testCommit, testRelPath)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)
	})

	t.Run("file not found", func(t *testing.T) {
		ctx := testctx.NewCtx()
		ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
			// Simulate git writing "does not exist in" to stderr, then returning an error.
			fmt.Fprint(cmd.Stderr, "fatal: path 'locks/curl.lock' does not exist in 'abc123def456'\n")

			return "", errors.New("exit status 128")
		}

		_, err := git.ShowFileAtCommit(context.Background(), ctx.CmdFactory, testRepoDir, testCommit, testRelPath)
		require.Error(t, err)
		assert.ErrorIs(t, err, git.ErrFileNotFound)
	})

	t.Run("file exists on disk but not in commit", func(t *testing.T) {
		ctx := testctx.NewCtx()
		ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
			// Git emits this when a file is on disk but not yet committed.
			fmt.Fprint(cmd.Stderr, "fatal: path 'locks/curl.lock' exists on disk, but not in 'HEAD'\n")

			return "", errors.New("exit status 128")
		}

		_, err := git.ShowFileAtCommit(context.Background(), ctx.CmdFactory, testRepoDir, "HEAD", testRelPath)
		require.Error(t, err)
		assert.ErrorIs(t, err, git.ErrFileNotFound)
	})

	t.Run("other git error", func(t *testing.T) {
		ctx := testctx.NewCtx()
		ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
			fmt.Fprint(cmd.Stderr, "fatal: not a git repository\n")

			return "", errors.New("exit status 128")
		}

		_, err := git.ShowFileAtCommit(context.Background(), ctx.CmdFactory, testRepoDir, testCommit, testRelPath)
		require.Error(t, err)
		require.NotErrorIs(t, err, git.ErrFileNotFound)
		assert.Contains(t, err.Error(), "not a git repository")
	})
}

// testCommit pairs file content with the resulting commit hash.
type testCommit struct {
	Content    string
	CommitHash string
}

// initTestRepo creates a local git repo in a temp dir and commits each version
// of the file at relPath in order. Returns the repo dir and a slice of
// [testCommit] (one per version, oldest first).
//
// Callers must set GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM via t.Setenv before
// calling to isolate from the developer's global git config.
func initTestRepo(t *testing.T, relPath string, versions []string) (repoDir string, commits []testCommit) {
	t.Helper()

	require.NotEmpty(t, versions, "need at least one version")

	repoDir = t.TempDir()
	ctx := context.Background()

	// git init + minimal config so commit works in CI.
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoDir}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", args[0], out)
	}

	absPath := filepath.Join(repoDir, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absPath), fileperms.PublicDir))

	for i, content := range versions {
		require.NoError(t, os.WriteFile(absPath, []byte(content), fileperms.PrivateFile))

		for _, args := range [][]string{
			{"add", relPath},
			{"commit", "-m", fmt.Sprintf("version %d", i)},
		} {
			cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoDir}, args...)...)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "git %s: %s", args[0], out)
		}

		cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD")
		out, err := cmd.Output()
		require.NoError(t, err)

		commits = append(commits, testCommit{
			Content:    content,
			CommitHash: strings.TrimSpace(string(out)),
		})
	}

	return repoDir, commits
}

// newRealCmdFactory creates a real [opctx.CmdFactory] backed by exec.
func newRealCmdFactory(t *testing.T) opctx.CmdFactory {
	t.Helper()

	ctrl := gomock.NewController(t)

	cmdFactory, err := externalcmd.NewCmdFactory(
		opctx_test.NewNoOpMockDryRunnable(ctrl),
		opctx_test.NewNoOpMockEventListener(ctrl),
	)
	require.NoError(t, err)

	return cmdFactory
}

// TestShowFileAtCommitE2E uses real git to verify our stderr-parsing contract
// matches actual git output. This complements the mock-based unit tests above.
func TestShowFileAtCommitE2E(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Isolate from developer's global git config (commit.gpgsign, init.defaultBranch, etc.).
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	const testRelPath = "locks/curl.lock"

	versions := []string{
		"version = 1\nimport-commit = \"aaa111\"\n",
		"version = 1\nimport-commit = \"bbb222\"\n",
		"version = 1\nimport-commit = \"ccc333\"\n",
	}

	repoDir, commits := initTestRepo(t, testRelPath, versions)
	cmdFactory := newRealCmdFactory(t)

	t.Run("reads each committed version", func(t *testing.T) {
		for _, tc := range commits {
			content, err := git.ShowFileAtCommit(
				context.Background(), cmdFactory, repoDir, tc.CommitHash, testRelPath,
			)
			require.NoError(t, err)
			assert.Equal(t, tc.Content, content)
		}
	})

	t.Run("HEAD resolves to latest version", func(t *testing.T) {
		content, err := git.ShowFileAtCommit(
			context.Background(), cmdFactory, repoDir, "HEAD", testRelPath,
		)
		require.NoError(t, err)
		assert.Equal(t, versions[len(versions)-1], content)
	})

	t.Run("file not found at commit", func(t *testing.T) {
		_, err := git.ShowFileAtCommit(
			context.Background(), cmdFactory, repoDir, commits[0].CommitHash, "nonexistent/file.txt",
		)
		require.Error(t, err)
		assert.ErrorIs(t, err, git.ErrFileNotFound)
	})

	t.Run("invalid revision", func(t *testing.T) {
		_, err := git.ShowFileAtCommit(
			context.Background(), cmdFactory, repoDir, "not_a_real_ref", testRelPath,
		)
		require.Error(t, err)
		// Invalid revision is NOT a file-not-found — it's a different git error.
		require.NotErrorIs(t, err, git.ErrFileNotFound)
	})

	t.Run("file exists on disk but not in commit", func(t *testing.T) {
		// Write a new file that is NOT committed — git show HEAD:path should
		// produce "exists on disk, but not in" and map to ErrFileNotFound.
		untrackedPath := filepath.Join(repoDir, "locks", "untracked.lock")
		require.NoError(t, os.WriteFile(untrackedPath, []byte("data"), fileperms.PrivateFile))

		_, err := git.ShowFileAtCommit(
			context.Background(), cmdFactory, repoDir, "HEAD", "locks/untracked.lock",
		)
		require.Error(t, err)
		assert.ErrorIs(t, err, git.ErrFileNotFound)
	})
}
