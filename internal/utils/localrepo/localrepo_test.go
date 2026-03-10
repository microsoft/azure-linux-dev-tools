// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package localrepo_test

import (
	"context"
	"os/exec"
	"path"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/localrepo"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRepoPath = "/test/repo"

func TestNewPublisher(t *testing.T) {
	testFS := afero.NewMemMapFs()
	ctx := testctx.NewCtx(testctx.WithFS(testFS))

	publisher, err := localrepo.NewPublisher(ctx, testRepoPath, false)

	require.NoError(t, err)
	assert.NotNil(t, publisher)
	assert.Equal(t, testRepoPath, publisher.RepoPath())
}

func TestPublisher_EnsureRepoExists_CreatesDirectory(t *testing.T) {
	testFS := afero.NewMemMapFs()
	ctx := testctx.NewCtx(testctx.WithFS(testFS))

	publisher, err := localrepo.NewPublisher(ctx, testRepoPath, false)
	require.NoError(t, err)

	err = publisher.EnsureRepoExists()

	require.NoError(t, err)

	exists, err := afero.DirExists(testFS, testRepoPath)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestPublisher_EnsureRepoExists_ExistingDirectory(t *testing.T) {
	testFS := afero.NewMemMapFs()
	ctx := testctx.NewCtx(testctx.WithFS(testFS))

	require.NoError(t, testFS.MkdirAll(testRepoPath, 0o755))

	publisher, err := localrepo.NewPublisher(ctx, testRepoPath, false)
	require.NoError(t, err)

	err = publisher.EnsureRepoExists()

	require.NoError(t, err)
}

func TestPublisher_PublishRPMs_EmptyList(t *testing.T) {
	testFS := afero.NewMemMapFs()
	ctx := testctx.NewCtx(testctx.WithFS(testFS))

	publisher, err := localrepo.NewPublisher(ctx, testRepoPath, false)
	require.NoError(t, err)

	err = publisher.PublishRPMs(context.Background(), []string{})

	require.NoError(t, err)
}

func TestPublisher_EnsureRepoInitialized_CreatesRepoMetadata(t *testing.T) {
	testFS := afero.NewMemMapFs()
	ctx := testctx.NewCtx(testctx.WithFS(testFS))

	// Track commands executed.
	var commandsExecuted [][]string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		commandsExecuted = append(commandsExecuted, cmd.Args)

		return nil
	}

	publisher, err := localrepo.NewPublisher(ctx, testRepoPath, false)
	require.NoError(t, err)

	err = publisher.EnsureRepoInitialized(context.Background())

	require.NoError(t, err)

	// Verify directory was created.
	exists, err := afero.DirExists(testFS, testRepoPath)
	require.NoError(t, err)
	assert.True(t, exists)

	// Verify createrepo_c was called to initialize metadata.
	require.Len(t, commandsExecuted, 1)
	assert.Equal(t, "createrepo_c", commandsExecuted[0][0])
	assert.Contains(t, commandsExecuted[0], testRepoPath)
}

func TestPublisher_EnsureRepoInitialized_SkipsIfRepodataExists(t *testing.T) {
	testFS := afero.NewMemMapFs()
	ctx := testctx.NewCtx(testctx.WithFS(testFS))

	// Track commands executed.
	var commandsExecuted [][]string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		commandsExecuted = append(commandsExecuted, cmd.Args)

		return nil
	}

	// Pre-create the repo with repodata directory.
	require.NoError(t, testFS.MkdirAll(path.Join(testRepoPath, "repodata"), 0o755))

	publisher, err := localrepo.NewPublisher(ctx, testRepoPath, false)
	require.NoError(t, err)

	err = publisher.EnsureRepoInitialized(context.Background())

	require.NoError(t, err)

	// Verify createrepo_c was NOT called since repodata already exists.
	assert.Empty(t, commandsExecuted, "createrepo_c should not be called when repodata exists")
}

func TestPublisher_PublishRPMs_CopiesFilesAndRunsCreaterepoC(t *testing.T) {
	testFS := afero.NewMemMapFs()
	ctx := testctx.NewCtx(testctx.WithFS(testFS))

	// Track commands executed.
	var commandsExecuted [][]string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		commandsExecuted = append(commandsExecuted, cmd.Args)

		return nil
	}

	require.NoError(t, testFS.MkdirAll(testRepoPath, 0o755))

	// Create source RPM files.
	sourcePath := "/build/output"
	require.NoError(t, testFS.MkdirAll(sourcePath, 0o755))

	rpm1 := path.Join(sourcePath, "package-1.0-1.x86_64.rpm")
	rpm2 := path.Join(sourcePath, "package-devel-1.0-1.x86_64.rpm")

	require.NoError(t, fileutils.WriteFile(testFS, rpm1, []byte("rpm1 content"), 0o644))
	require.NoError(t, fileutils.WriteFile(testFS, rpm2, []byte("rpm2 content"), 0o644))

	publisher, err := localrepo.NewPublisher(ctx, testRepoPath, false)
	require.NoError(t, err)

	err = publisher.PublishRPMs(context.Background(), []string{rpm1, rpm2})

	require.NoError(t, err)

	// Verify files were copied.
	exists1, err := afero.Exists(testFS, path.Join(testRepoPath, "package-1.0-1.x86_64.rpm"))
	require.NoError(t, err)
	assert.True(t, exists1, "RPM 1 should be copied to repo")

	exists2, err := afero.Exists(testFS, path.Join(testRepoPath, "package-devel-1.0-1.x86_64.rpm"))
	require.NoError(t, err)
	assert.True(t, exists2, "RPM 2 should be copied to repo")

	// Verify createrepo_c was called.
	require.Len(t, commandsExecuted, 1)
	assert.Equal(t, "createrepo_c", commandsExecuted[0][0])
	assert.Contains(t, commandsExecuted[0], testRepoPath)
}
