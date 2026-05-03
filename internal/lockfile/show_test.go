// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lockfile_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validLockTOML = `version = 1
import-commit = "aaa111"
upstream-commit = "bbb222"
input-fingerprint = "fff000"
`

// commitFile creates or overwrites a file in the in-memory repo and commits it.
// Returns the commit hash as a string.
func commitFile(t *testing.T, repo *gogit.Repository, fs billy.Filesystem, relPath, content, msg string) string {
	t.Helper()

	file, err := fs.Create(relPath)
	require.NoError(t, err)

	_, err = file.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	_, err = worktree.Add(relPath)
	require.NoError(t, err)

	hash, err := worktree.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)

	return hash.String()
}

func TestShowAtCommit(t *testing.T) {
	const testLockRelPath = "locks/curl.lock"

	t.Run("success", func(t *testing.T) {
		fs := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), fs)
		require.NoError(t, err)

		commitHash := commitFile(t, repo, fs, testLockRelPath, validLockTOML, "add lock")

		lock, err := lockfile.ShowAtCommit(repo, commitHash, testLockRelPath)

		require.NoError(t, err)
		assert.Equal(t, 1, lock.Version)
		assert.Equal(t, "aaa111", lock.ImportCommit)
		assert.Equal(t, "bbb222", lock.UpstreamCommit)
		assert.Equal(t, "fff000", lock.InputFingerprint)
	})

	t.Run("reads correct version at each commit", func(t *testing.T) {
		testFS := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), testFS)
		require.NoError(t, err)

		versions := []string{
			"version = 1\nimport-commit = \"aaa\"\n",
			"version = 1\nimport-commit = \"bbb\"\n",
			"version = 1\nimport-commit = \"ccc\"\n",
		}

		var commits []string

		for i, content := range versions {
			hash := commitFile(t, repo, testFS, testLockRelPath, content, fmt.Sprintf("v%d", i))
			commits = append(commits, hash)
		}

		for i, hash := range commits {
			lock, err := lockfile.ShowAtCommit(repo, hash, testLockRelPath)
			require.NoError(t, err)

			expected := []string{"aaa", "bbb", "ccc"}
			assert.Equal(t, expected[i], lock.ImportCommit)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		fs := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), fs)
		require.NoError(t, err)

		commitHash := commitFile(t, repo, fs, "other/file.txt", "data", "init")

		_, err = lockfile.ShowAtCommit(repo, commitHash, testLockRelPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read")
	})

	t.Run("invalid TOML", func(t *testing.T) {
		fs := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), fs)
		require.NoError(t, err)

		commitHash := commitFile(t, repo, fs, testLockRelPath, "not valid toml {{{", "bad lock")

		_, err = lockfile.ShowAtCommit(repo, commitHash, testLockRelPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse lock file")
	})

	t.Run("wrong version", func(t *testing.T) {
		fs := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), fs)
		require.NoError(t, err)

		commitHash := commitFile(t, repo, fs, testLockRelPath, "version = 99\n", "bad version")

		_, err = lockfile.ShowAtCommit(repo, commitHash, testLockRelPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported lock file version")
	})

	t.Run("bad commit hash", func(t *testing.T) {
		fs := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), fs)
		require.NoError(t, err)

		commitFile(t, repo, fs, testLockRelPath, validLockTOML, "init")

		_, err = lockfile.ShowAtCommit(repo, "0000000000000000000000000000000000000000", testLockRelPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to resolve commit")
	})
}

func TestReadAllAtCommit(t *testing.T) {
	const lockDir = "locks"

	t.Run("reads all locks", func(t *testing.T) {
		testFS := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), testFS)
		require.NoError(t, err)

		commitFile(t, repo, testFS, "locks/curl.lock", validLockTOML, "add curl lock")
		commitHash := commitFile(t, repo, testFS,
			"locks/bash.lock",
			"version = 1\nimport-commit = \"bash111\"\nupstream-commit = \"bash222\"\ninput-fingerprint = \"bashfp\"\n",
			"add bash lock",
		)

		locks, readErr := lockfile.ReadAllAtCommit(repo, commitHash, lockDir)
		require.NoError(t, readErr)
		assert.Len(t, locks, 2)
		assert.Equal(t, "fff000", locks["curl"].InputFingerprint)
		assert.Equal(t, "bashfp", locks["bash"].InputFingerprint)
	})

	t.Run("empty when no lock dir", func(t *testing.T) {
		testFS := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), testFS)
		require.NoError(t, err)

		commitHash := commitFile(t, repo, testFS, "other/file.txt", "data", "init")

		locks, readErr := lockfile.ReadAllAtCommit(repo, commitHash, lockDir)
		require.NoError(t, readErr)
		assert.Empty(t, locks)
	})

	t.Run("skips non-lock files", func(t *testing.T) {
		testFS := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), testFS)
		require.NoError(t, err)

		commitFile(t, repo, testFS, "locks/curl.lock", validLockTOML, "add lock")
		commitHash := commitFile(t, repo, testFS, "locks/README.md", "# Locks", "add readme")

		locks, readErr := lockfile.ReadAllAtCommit(repo, commitHash, lockDir)
		require.NoError(t, readErr)
		assert.Len(t, locks, 1)
		assert.Contains(t, locks, "curl")
	})

	t.Run("skips dot-prefixed lock files", func(t *testing.T) {
		testFS := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), testFS)
		require.NoError(t, err)

		commitFile(t, repo, testFS, "locks/curl.lock", validLockTOML, "add lock")
		commitHash := commitFile(t, repo, testFS, "locks/.hidden.lock", validLockTOML, "add hidden")

		locks, readErr := lockfile.ReadAllAtCommit(repo, commitHash, lockDir)
		require.NoError(t, readErr)
		assert.Len(t, locks, 1)
		assert.Contains(t, locks, "curl")
	})

	t.Run("error on unparseable lock", func(t *testing.T) {
		testFS := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), testFS)
		require.NoError(t, err)

		commitHash := commitFile(t, repo, testFS, "locks/bad.lock", "not valid {{{", "add bad")

		_, readErr := lockfile.ReadAllAtCommit(repo, commitHash, lockDir)
		require.Error(t, readErr)
		assert.Contains(t, readErr.Error(), "failed to parse")
	})

	t.Run("reads root-level locks with dot dir", func(t *testing.T) {
		testFS := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), testFS)
		require.NoError(t, err)

		commitHash := commitFile(t, repo, testFS, "curl.lock", validLockTOML, "add root lock")

		locks, readErr := lockfile.ReadAllAtCommit(repo, commitHash, ".")
		require.NoError(t, readErr)
		assert.Len(t, locks, 1)
		assert.Equal(t, "fff000", locks["curl"].InputFingerprint)
	})

	t.Run("normalizes ./locks to locks", func(t *testing.T) {
		testFS := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), testFS)
		require.NoError(t, err)

		commitHash := commitFile(t, repo, testFS, "locks/curl.lock", validLockTOML, "add lock")

		locks, readErr := lockfile.ReadAllAtCommit(repo, commitHash, "./locks")
		require.NoError(t, readErr)
		assert.Len(t, locks, 1)
	})

	t.Run("rejects absolute path", func(t *testing.T) {
		testFS := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), testFS)
		require.NoError(t, err)

		commitFile(t, repo, testFS, "locks/curl.lock", validLockTOML, "add lock")

		_, readErr := lockfile.ReadAllAtCommit(repo, "dummy", "/abs/locks")
		require.Error(t, readErr)
		assert.Contains(t, readErr.Error(), "repo-relative")
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		testFS := memfs.New()
		repo, err := gogit.Init(memory.NewStorage(), testFS)
		require.NoError(t, err)

		commitFile(t, repo, testFS, "locks/curl.lock", validLockTOML, "add lock")

		_, readErr := lockfile.ReadAllAtCommit(repo, "dummy", "../escape")
		require.Error(t, readErr)
		assert.Contains(t, readErr.Error(), "escapes repository root")
	})
}
