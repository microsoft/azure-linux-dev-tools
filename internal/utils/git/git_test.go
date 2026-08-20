// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/externalcmd"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

//
// These tests use real git commands where possible to verify actual cloning behavior.
// Constructor and validation tests use mocks for focused unit testing.
//

const (
	testRepoURL            = "https://github.com/octocat/Hello-World.git"
	testRepoBranch         = "test"
	testRepoSubDir         = "repo"
	testRepoReadmeFile     = "README"
	testRepoContribFile    = "CONTRIBUTING.md"
	testGitDir             = ".git"
	nonExistentRepoURL     = "https://github.com/nonexistent/repository-does-not-exist-12345.git"
	errMsgEmptyURL         = "repository URL cannot be empty"
	errMsgEmptyDestination = "destination directory cannot be empty"
	errMsgCloneFailed      = "failed to clone repository"
)

func TestNewGitProviderImpl(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockEventListener := opctx_test.NewMockEventListener(ctrl)
	mockCmdFactory := opctx_test.NewMockCmdFactory(ctrl)

	provider, err := git.NewGitProviderImpl(mockEventListener, mockCmdFactory)

	require.NoError(t, err)
	require.NotNil(t, provider)
}

func TestNewGitProviderImplFailsForNilParams(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, err := git.NewGitProviderImpl(nil, opctx_test.NewMockCmdFactory(ctrl))
	require.Error(t, err)

	_, err = git.NewGitProviderImpl(opctx_test.NewMockEventListener(ctrl), nil)
	require.Error(t, err)
}

func TestCloneValidation(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, err := git.NewGitProviderImpl(
		opctx_test.NewMockEventListener(ctrl),
		opctx_test.NewMockCmdFactory(ctrl))
	require.NoError(t, err)

	tests := []struct {
		name        string
		repoURL     string
		destDir     string
		expectedErr string
	}{
		{"empty URL", "", "/tmp/dest", errMsgEmptyURL},
		{"empty destination", "https://github.com/example/repo.git", "", errMsgEmptyDestination},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := provider.Clone(context.Background(), tt.repoURL, tt.destDir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestClone(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Use real command factory for actual git execution
	cmdFactory, err := externalcmd.NewCmdFactory(
		opctx_test.NewNoOpMockDryRunnable(ctrl),
		opctx_test.NewNoOpMockEventListener(ctrl),
	)
	require.NoError(t, err)

	provider, err := git.NewGitProviderImpl(opctx_test.NewNoOpMockEventListener(ctrl), cmdFactory)
	require.NoError(t, err)

	destDir := filepath.Join(t.TempDir(), testRepoSubDir)

	// Clone a small, well-known public repository
	err = provider.Clone(
		context.Background(),
		testRepoURL,
		destDir,
	)

	require.NoError(t, err)
	// Verify the repository was actually cloned
	assert.DirExists(t, destDir)
	assert.DirExists(t, filepath.Join(destDir, testGitDir))
	assert.FileExists(t, filepath.Join(destDir, testRepoReadmeFile))
}

func TestCloneWithBranch(t *testing.T) {
	ctrl := gomock.NewController(t)

	cmdFactory, err := externalcmd.NewCmdFactory(
		opctx_test.NewNoOpMockDryRunnable(ctrl),
		opctx_test.NewNoOpMockEventListener(ctrl),
	)
	require.NoError(t, err)

	provider, err := git.NewGitProviderImpl(opctx_test.NewNoOpMockEventListener(ctrl), cmdFactory)
	require.NoError(t, err)

	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, testRepoSubDir)

	err = provider.Clone(
		context.Background(),
		testRepoURL,
		destDir,
		git.WithGitBranch(testRepoBranch),
	)

	require.NoError(t, err)
	assert.DirExists(t, destDir)
	assert.DirExists(t, filepath.Join(destDir, testGitDir))
	assert.FileExists(t, filepath.Join(destDir, testRepoContribFile))
}

func TestCloneNonExistentRepo(t *testing.T) {
	ctrl := gomock.NewController(t)

	cmdFactory, err := externalcmd.NewCmdFactory(
		opctx_test.NewNoOpMockDryRunnable(ctrl),
		opctx_test.NewNoOpMockEventListener(ctrl),
	)
	require.NoError(t, err)

	provider, err := git.NewGitProviderImpl(opctx_test.NewNoOpMockEventListener(ctrl), cmdFactory)
	require.NoError(t, err)

	destDir := filepath.Join(t.TempDir(), testRepoSubDir)

	err = provider.Clone(
		context.Background(),
		nonExistentRepoURL,
		destDir,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), errMsgCloneFailed)
}

func TestGetCurrentCommit(t *testing.T) {
	ctrl := gomock.NewController(t)

	cmdFactory, err := externalcmd.NewCmdFactory(
		opctx_test.NewNoOpMockDryRunnable(ctrl),
		opctx_test.NewNoOpMockEventListener(ctrl),
	)
	require.NoError(t, err)

	provider, err := git.NewGitProviderImpl(opctx_test.NewNoOpMockEventListener(ctrl), cmdFactory)
	require.NoError(t, err)

	destDir := filepath.Join(t.TempDir(), testRepoSubDir)

	err = provider.Clone(context.Background(), testRepoURL, destDir)
	require.NoError(t, err)

	commitHash, err := provider.GetCurrentCommit(t.Context(), destDir)
	require.NoError(t, err)

	// A full SHA-1 hash is 40 hex characters.
	assert.Len(t, commitHash, 40)
	assert.Regexp(t, `^[0-9a-f]{40}$`, commitHash)
}

func TestGetCurrentCommitEmptyRepoDir(t *testing.T) {
	ctrl := gomock.NewController(t)

	provider, err := git.NewGitProviderImpl(
		opctx_test.NewMockEventListener(ctrl),
		opctx_test.NewMockCmdFactory(ctrl),
	)
	require.NoError(t, err)

	_, err = provider.GetCurrentCommit(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repository directory cannot be empty")
}

func TestCloneWithMetadataOnly(t *testing.T) {
	ctrl := gomock.NewController(t)

	cmdFactory, err := externalcmd.NewCmdFactory(
		opctx_test.NewNoOpMockDryRunnable(ctrl),
		opctx_test.NewNoOpMockEventListener(ctrl),
	)
	require.NoError(t, err)

	provider, err := git.NewGitProviderImpl(opctx_test.NewNoOpMockEventListener(ctrl), cmdFactory)
	require.NoError(t, err)

	destDir := filepath.Join(t.TempDir(), testRepoSubDir)

	err = provider.Clone(
		t.Context(),
		testRepoURL,
		destDir,
		git.WithMetadataOnly(),
	)

	require.NoError(t, err)

	// Git metadata should exist.
	assert.DirExists(t, filepath.Join(destDir, testGitDir))
	// --no-checkout means the working tree file should NOT be present.
	assert.NoFileExists(t, filepath.Join(destDir, testRepoReadmeFile))
}

func TestGetCommitHashBeforeDate_FirstParentOnly(t *testing.T) {
	// Verify that GetCommitHashBeforeDate returns a first-parent commit
	// even when a side-branch commit has a more recent timestamp that
	// still falls before the snapshot date.
	//
	// Graph:
	//   M  ← merge (2024-05-01), parents: [B, S]  ← HEAD (main)
	//   |\
	//   B  S ← S on side branch (2024-04-01), B on main (2024-02-01)
	//   |  |
	//   A  R ← R side-branch root (2024-03-01), A main root (2024-01-01)
	//
	// With snapshot date 2024-04-15:
	//   All-parent walk would pick S (2024-04-01, closest before snapshot)
	//   First-parent walk should pick B (2024-02-01, latest first-parent before snapshot)
	repoDir := t.TempDir()

	// Helper to run git commands in the repo.
	runGit := func(args ...string) string {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repoDir}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, out)

		return string(out)
	}

	// Helper to commit with both author and committer dates set.
	commitWithDate := func(msg, date string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", "-C", repoDir, "commit", "-m", msg, "--date="+date)
		cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date)

		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git commit failed: %s", out)
	}

	// Init repo.
	runGit("init", "--initial-branch=main")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "user.name", "Test")

	// Commit A — main root (2024-01-01).
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("a"), 0o600))

	runGit("add", ".")
	commitWithDate("A: main root", "2024-01-01T00:00:00Z")

	// Commit B — main second commit (2024-02-01).
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("b"), 0o600))

	runGit("add", ".")
	commitWithDate("B: main update", "2024-02-01T00:00:00Z")

	mainCommitB := strings.TrimSpace(runGit("rev-parse", "HEAD"))

	// Create side branch from A.
	runGit("checkout", "-b", "side", "HEAD~1")

	// Commit R — side-branch root (2024-03-01).
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "side.txt"), []byte("r"), 0o600))

	runGit("add", ".")
	commitWithDate("R: side root", "2024-03-01T00:00:00Z")

	// Commit S — side-branch tip (2024-04-01), newer than B.
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "side.txt"), []byte("s"), 0o600))

	runGit("add", ".")
	commitWithDate("S: side update", "2024-04-01T00:00:00Z")

	// Back to main, merge side branch with date 2024-05-01.
	runGit("checkout", "main")

	mergeCmd := exec.CommandContext(t.Context(), "git", "-C", repoDir, "merge", "side", "--no-ff",
		"-m", "M: merge side into main")

	mergeCmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE=2024-05-01T00:00:00Z")

	mergeOut, mergeErr := mergeCmd.CombinedOutput()
	require.NoError(t, mergeErr, "merge failed: %s", mergeOut)

	// Now test: snapshot at 2024-04-15 should pick B (first-parent),
	// not S (side-branch, even though S is newer and before snapshot).
	ctrl := gomock.NewController(t)

	cmdFactory, err := externalcmd.NewCmdFactory(
		opctx_test.NewNoOpMockDryRunnable(ctrl),
		opctx_test.NewNoOpMockEventListener(ctrl),
	)
	require.NoError(t, err)

	provider, err := git.NewGitProviderImpl(opctx_test.NewNoOpMockEventListener(ctrl), cmdFactory)
	require.NoError(t, err)

	snapshotDate := time.Date(2024, 4, 15, 0, 0, 0, 0, time.UTC)

	resolved, err := provider.GetCommitHashBeforeDate(t.Context(), repoDir, snapshotDate)
	require.NoError(t, err)

	assert.Equal(t, mainCommitB, resolved,
		"should resolve to first-parent commit B, not side-branch commit S")
}
