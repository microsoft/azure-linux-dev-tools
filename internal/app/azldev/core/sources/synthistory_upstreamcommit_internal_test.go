// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"
	memfs "github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeGeneratedCommit commits a generated upstream-commit TOML holding the given
// pin and returns the resulting commit hash.
func writeGeneratedCommit(
	t *testing.T, repo *gogit.Repository, memFS billy.Filesystem, relPath, upstreamCommit string,
) string {
	t.Helper()

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	file, err := memFS.Create(relPath)
	require.NoError(t, err)

	_, err = file.Write([]byte("[components.curl.spec]\nupstream-commit = \"" + upstreamCommit + "\"\n"))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	_, err = worktree.Add(relPath)
	require.NoError(t, err)

	hash, err := worktree.Commit("pin "+upstreamCommit, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "azldev",
			Email: "azldev@local",
			When:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	return hash.String()
}

func TestUpstreamCommitAtCommit(t *testing.T) {
	memFS := memfs.New()

	repo, err := gogit.Init(memory.NewStorage(), memFS)
	require.NoError(t, err)

	const relPath = "base/upstream-commits/curl.toml"

	first := writeGeneratedCommit(t, repo, memFS, relPath, "aaa1111")
	second := writeGeneratedCommit(t, repo, memFS, relPath, "bbb2222")

	firstPin, err := upstreamCommitAtCommit(repo, first, relPath, "curl")
	require.NoError(t, err)
	assert.Equal(t, "aaa1111", firstPin)

	secondPin, err := upstreamCommitAtCommit(repo, second, relPath, "curl")
	require.NoError(t, err)
	assert.Equal(t, "bbb2222", secondPin)

	// The pin recorded before a commit comes from that commit's first parent.
	previousPin, err := upstreamCommitBeforeCommit(repo, second, relPath, "curl")
	require.NoError(t, err)
	assert.Equal(t, "aaa1111", previousPin)
}

func TestUpstreamCommitAtCommit_UnknownComponent(t *testing.T) {
	memFS := memfs.New()

	repo, err := gogit.Init(memory.NewStorage(), memFS)
	require.NoError(t, err)

	const relPath = "base/upstream-commits/curl.toml"

	hash := writeGeneratedCommit(t, repo, memFS, relPath, "aaa1111")

	_, err = upstreamCommitAtCommit(repo, hash, relPath, "openssl")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "openssl")
}

func TestFindSyntheticChanges_WithoutLockfile_NoConfigFile(t *testing.T) {
	// A component whose upstream commit is not pinned by any config file has no
	// generated history to replay, so discovery is skipped rather than failing.
	preparer := &sourcePreparerImpl{withoutLockfile: true}
	config := &projectconfig.ComponentConfig{Name: "curl"}

	changes, importCommit, err := preparer.findSyntheticChanges(t.Context(), config, config, "curl")
	require.NoError(t, err)
	assert.Empty(t, changes)
	assert.Empty(t, importCommit)
}
