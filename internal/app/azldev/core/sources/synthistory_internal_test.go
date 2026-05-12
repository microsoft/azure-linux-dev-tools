// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"testing"
	"time"

	memfs "github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initRepoWithLockFile creates an in-memory git repo with a committed lock file.
// Returns the repo and its worktree root.
func initRepoWithLockFile(t *testing.T, lockRelPath, lockContent string) *gogit.Repository {
	t.Helper()

	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	// Write the lock file.
	file, err := memFS.Create(lockRelPath)
	require.NoError(t, err)

	_, err = file.Write([]byte(lockContent))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	// Stage and commit.
	worktree, err := repo.Worktree()
	require.NoError(t, err)

	_, err = worktree.Add(lockRelPath)
	require.NoError(t, err)

	_, err = worktree.Commit("Add lock file", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "test",
			Email: "test@test.com",
			When:  time.Unix(1000000, 0),
		},
	})
	require.NoError(t, err)

	return repo
}

func TestReadLockFileAtHEAD_Found(t *testing.T) {
	lockContent := `version = 1
upstream-commit = "abc123"
input-fingerprint = "sha256:test-fp"
`
	repo := initRepoWithLockFile(t, "locks/curl.lock", lockContent)

	lock, err := readLockFileAtHEAD(repo, "locks/curl.lock")
	require.NoError(t, err)
	require.NotNil(t, lock)
	assert.Equal(t, "abc123", lock.UpstreamCommit)
	assert.Equal(t, "sha256:test-fp", lock.InputFingerprint)
}

func TestReadLockFileAtHEAD_Missing(t *testing.T) {
	// Create repo with a different file — lock is missing.
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	file, err := memFS.Create("README.md")
	require.NoError(t, err)

	_, err = file.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	_, err = worktree.Add("README.md")
	require.NoError(t, err)

	_, err = worktree.Commit("Initial", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "t@t", When: time.Unix(1, 0)},
	})
	require.NoError(t, err)

	lock, err := readLockFileAtHEAD(repo, "locks/curl.lock")
	require.NoError(t, err)
	assert.Nil(t, lock, "missing lock should return nil without error")
}

func TestInitialImportFromDisk_WithLockedData(t *testing.T) {
	config := projectConfigWithLock("test-comp", &lockfile.ComponentLock{
		UpstreamCommit:   "abc123",
		ImportCommit:     "import456",
		InputFingerprint: "sha256:test-fp",
	})

	changes, importCommit, err := initialImportFromDisk(
		&config,
		"locks/test-comp.lock",
	)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	assert.Equal(t, "initial", changes[0].Hash)
	assert.Equal(t, "abc123", changes[0].UpstreamCommit)
	assert.Equal(t, "import456", importCommit)
}

func TestInitialImportFromDisk_NoLockedData(t *testing.T) {
	config := projectConfigWithLock("test-comp", nil)

	changes, importCommit, err := initialImportFromDisk(
		&config,
		"locks/test-comp.lock",
	)
	require.NoError(t, err)
	assert.Nil(t, changes)
	assert.Empty(t, importCommit)
}

// projectConfigWithLock creates a minimal ComponentConfig with optional lock data.
func projectConfigWithLock(name string, lock *lockfile.ComponentLock) projectconfig.ComponentConfig {
	config := projectconfig.ComponentConfig{Name: name}
	if lock != nil {
		config.Locked = &projectconfig.ComponentLockData{
			UpstreamCommit:   lock.UpstreamCommit,
			ImportCommit:     lock.ImportCommit,
			InputFingerprint: lock.InputFingerprint,
		}
	}

	return config
}

// --- handleMissingHeadLock tests ---

func TestHandleMissingHeadLock_AllowDirty_WithLock(t *testing.T) {
	config := projectConfigWithLock("curl", &lockfile.ComponentLock{
		UpstreamCommit:   "abc123",
		ImportCommit:     "import456",
		InputFingerprint: "sha256:test-fp",
	})

	var hints []string

	changes, importCommit, err := handleMissingHeadLock(
		&config, "curl", "locks/curl.lock", true, &hints,
	)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	assert.Equal(t, "initial", changes[0].Hash)
	assert.Equal(t, "abc123", changes[0].UpstreamCommit)
	assert.Equal(t, "import456", importCommit)
	assert.Empty(t, hints, "no hints when allow-dirty is true")
}

func TestHandleMissingHeadLock_NoDirty_WithLock_Errors(t *testing.T) {
	config := projectConfigWithLock("curl", &lockfile.ComponentLock{
		UpstreamCommit:   "abc123",
		InputFingerprint: "sha256:test-fp",
	})

	var hints []string

	_, _, err := handleMissingHeadLock(
		&config, "curl", "locks/curl.lock", false, &hints,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uncommitted lock file")
	require.Len(t, hints, 1)
	assert.Contains(t, hints[0], "--allow-dirty")
}

func TestHandleMissingHeadLock_NoDirty_NoLock_Skips(t *testing.T) {
	config := projectConfigWithLock("curl", nil)

	var hints []string

	changes, importCommit, err := handleMissingHeadLock(
		&config, "curl", "locks/curl.lock", false, &hints,
	)
	require.NoError(t, err)
	assert.Nil(t, changes)
	assert.Empty(t, importCommit)
	assert.Empty(t, hints, "no hints when no lock data exists")
}

// --- handleDirtyState tests ---

func TestHandleDirtyState_AllowDirty_ReturnsDirtyEntry(t *testing.T) {
	headLock := &lockfile.ComponentLock{
		UpstreamCommit:   "committed-abc",
		InputFingerprint: "sha256:old-fp",
	}
	config := projectConfigWithLock("curl", &lockfile.ComponentLock{
		UpstreamCommit: "new-commit",
	})

	var hints []string

	entry, err := handleDirtyState(
		"sha256:new-fp", headLock, &config, "curl", "locks/curl.lock", true, &hints,
	)
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "dirty", entry.Hash)
	assert.Empty(t, hints, "no hints when allow-dirty is true")
}

func TestHandleDirtyState_NoDirty_Errors(t *testing.T) {
	headLock := &lockfile.ComponentLock{
		UpstreamCommit:   "committed-abc",
		InputFingerprint: "sha256:old-fp",
	}
	config := projectConfigWithLock("curl", nil)

	var hints []string

	entry, err := handleDirtyState(
		"sha256:new-fp", headLock, &config, "curl", "locks/curl.lock", false, &hints,
	)
	require.Error(t, err)
	assert.Nil(t, entry)
	assert.Contains(t, err.Error(), "uncommitted changes")
	require.Len(t, hints, 1)
	assert.Contains(t, hints[0], "--allow-dirty")
}

func TestHandleDirtyState_NoMismatch_NoError(t *testing.T) {
	headLock := &lockfile.ComponentLock{
		UpstreamCommit:   "committed-abc",
		InputFingerprint: "sha256:same-fp",
	}
	config := projectConfigWithLock("curl", nil)

	var hints []string

	entry, err := handleDirtyState(
		"sha256:same-fp", headLock, &config, "curl", "locks/curl.lock", false, &hints,
	)
	require.NoError(t, err)
	assert.Nil(t, entry, "matching fingerprints should not produce dirty entry")
	assert.Empty(t, hints)
}
