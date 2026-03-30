// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package git_test

import (
	"context"
	"path/filepath"
	"testing"

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
