// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"testing"
	"time"

	memfs "github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testFingerprintOld = "sha256:old"
	testFingerprintNew = "sha256:new"
	testSpecsDirSPECS  = "/SPECS"
)

// testRepoCommit represents the files added or updated in a single commit.
// Files from previous commits that are not listed here remain unchanged;
// no deletions are modeled.
type testRepoCommit struct {
	files map[string][]byte
}

// testRepoWithCommits creates an in-memory git repo with N sequential commits.
// Returns the repo and a slice of commit hashes (oldest first).
func testRepoWithCommits(
	t *testing.T,
	commits []testRepoCommit,
) (*gogit.Repository, []string) {
	t.Helper()

	require.NotEmpty(t, commits, "need at least one commit")

	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	hashes := make([]string, 0, len(commits))

	for idx, commit := range commits {
		sig := &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  baseTime.AddDate(0, idx, 0),
		}

		for path, content := range commit.files {
			f, fErr := memFS.Create(path)
			require.NoError(t, fErr)

			_, fErr = f.Write(content)
			require.NoError(t, fErr)
			require.NoError(t, f.Close())

			_, fErr = worktree.Add(path)
			require.NoError(t, fErr)
		}

		hash, commitErr := worktree.Commit(
			fmt.Sprintf("commit %d", idx),
			&gogit.CommitOptions{Author: sig, AllowEmptyCommits: true},
		)
		require.NoError(t, commitErr)

		hashes = append(hashes, hash.String())
	}

	return repo, hashes
}

// testRepoWithTwoCommits is a convenience wrapper around testRepoWithCommits.
func testRepoWithTwoCommits(
	t *testing.T,
	fromFiles, toFiles map[string][]byte,
) (*gogit.Repository, string, string) {
	t.Helper()

	repo, hashes := testRepoWithCommits(t, []testRepoCommit{
		{files: fromFiles},
		{files: toFiles},
	})

	return repo, hashes[0], hashes[1]
}

// marshalLock serializes a lock to TOML bytes for writing into test git repos.
func marshalLock(t *testing.T, lock *lockfile.ComponentLock) []byte {
	t.Helper()

	data, err := toml.Marshal(lock)
	require.NoError(t, err)

	return data
}

// makeLock creates a lock with the given fingerprint and optional upstream commit.
func makeLock(t *testing.T, fingerprint, upstreamCommit string) []byte {
	t.Helper()

	lock := lockfile.New()
	lock.InputFingerprint = fingerprint
	lock.UpstreamCommit = upstreamCommit

	return marshalLock(t, lock)
}

// --- classifyComponent tests ---

func TestClassifyComponent_Changed(t *testing.T) {
	fromLocks := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: testFingerprintOld},
	}
	toLocks := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: testFingerprintNew},
	}

	result := classifyComponent("curl", fromLocks, toLocks)
	assert.Equal(t, changeTypeChanged, result.ChangeType)
}

func TestClassifyComponent_Unchanged(t *testing.T) {
	locks := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: "sha256:same"},
	}

	result := classifyComponent("curl", locks, locks)
	assert.Equal(t, changeTypeUnchanged, result.ChangeType)
}

func TestClassifyComponent_Added(t *testing.T) {
	fromLocks := map[string]lockfile.ComponentLock{}
	toLocks := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: testFingerprintNew},
	}

	result := classifyComponent("curl", fromLocks, toLocks)
	assert.Equal(t, changeTypeAdded, result.ChangeType)
}

func TestClassifyComponent_Deleted(t *testing.T) {
	fromLocks := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: testFingerprintOld},
	}
	toLocks := map[string]lockfile.ComponentLock{}

	result := classifyComponent("curl", fromLocks, toLocks)
	assert.Equal(t, changeTypeDeleted, result.ChangeType)
}

func TestClassifyComponent_NeverExisted(t *testing.T) {
	empty := map[string]lockfile.ComponentLock{}

	result := classifyComponent("curl", empty, empty)
	assert.Equal(t, changeTypeUnchanged, result.ChangeType)
}

// --- haveMatchingFingerprints tests ---

func TestHaveMatchingFingerprints(t *testing.T) {
	t.Parallel()

	const fingerprint = "sha256:abc"

	fromHas := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: fingerprint},
	}
	toHas := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: fingerprint},
	}
	toDifferent := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: "sha256:def"},
	}
	empty := map[string]lockfile.ComponentLock{}
	emptyFingerprint := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: ""},
	}

	tests := []struct {
		name string
		from map[string]lockfile.ComponentLock
		to   map[string]lockfile.ComponentLock
		want bool
	}{
		{"both refs have lock with same fingerprint", fromHas, toHas, true},
		{"both refs have lock but fingerprints differ", fromHas, toDifferent, false},
		{"only from has a lock", fromHas, empty, false},
		{"only to has a lock", empty, toHas, false},
		{"neither ref has a lock (regression: must NOT report violation)", empty, empty, false},
		{
			"both refs have lock but fingerprint field is empty (regression: must NOT report violation)",
			emptyFingerprint, emptyFingerprint, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, haveMatchingFingerprints("curl", tt.from, tt.to))
		})
	}
}

// --- compareSources tests ---

func TestCompareSources_Changed(t *testing.T) {
	repo, fromRef, toRef := testRepoWithTwoCommits(t,
		map[string][]byte{
			"SPECS/c/curl/sources": []byte("SHA512 (curl-8.0.tar.gz) = oldsha"),
		},
		map[string][]byte{
			"SPECS/c/curl/sources": []byte("SHA512 (curl-8.1.tar.gz) = newsha"),
		},
	)

	fromTree, err := resolveTree(repo, fromRef)
	require.NoError(t, err)

	toTree, err := resolveTree(repo, toRef)
	require.NoError(t, err)

	result, err := compareSources("/", fromTree, toTree, testSpecsDirSPECS, "curl")
	require.NoError(t, err)
	assert.True(t, result)
}

func TestCompareSources_Unchanged(t *testing.T) {
	sourcesContent := []byte("SHA512 (curl-8.0.tar.gz) = samehash")

	repo, fromRef, toRef := testRepoWithTwoCommits(t,
		map[string][]byte{"SPECS/c/curl/sources": sourcesContent},
		map[string][]byte{"SPECS/c/curl/sources": sourcesContent},
	)

	fromTree, err := resolveTree(repo, fromRef)
	require.NoError(t, err)

	toTree, err := resolveTree(repo, toRef)
	require.NoError(t, err)

	result, err := compareSources("/", fromTree, toTree, testSpecsDirSPECS, "curl")
	require.NoError(t, err)
	assert.False(t, result)
}

func TestCompareSources_Appeared(t *testing.T) {
	repo, fromRef, toRef := testRepoWithTwoCommits(t,
		map[string][]byte{"placeholder": []byte("x")},
		map[string][]byte{
			"placeholder":          []byte("x"),
			"SPECS/c/curl/sources": []byte("SHA512 (curl-8.0.tar.gz) = hash"),
		},
	)

	fromTree, err := resolveTree(repo, fromRef)
	require.NoError(t, err)

	toTree, err := resolveTree(repo, toRef)
	require.NoError(t, err)

	result, err := compareSources("/", fromTree, toTree, testSpecsDirSPECS, "curl")
	require.NoError(t, err)
	assert.True(t, result, "sources file appeared")
}

func TestCompareSources_NoSourcesAtEitherRef(t *testing.T) {
	repo, fromRef, toRef := testRepoWithTwoCommits(t,
		map[string][]byte{"placeholder": []byte("x")},
		map[string][]byte{"placeholder": []byte("x")},
	)

	fromTree, err := resolveTree(repo, fromRef)
	require.NoError(t, err)

	toTree, err := resolveTree(repo, toRef)
	require.NoError(t, err)

	result, err := compareSources("/", fromTree, toTree, testSpecsDirSPECS, "curl")
	require.NoError(t, err)
	assert.False(t, result, "no sources at either ref")
}

// --- Multi-component batch test ---

func TestMultiComponentBatch(t *testing.T) {
	curlFrom := lockfile.ComponentLock{InputFingerprint: "sha256:curl-v1"}
	curlTo := lockfile.ComponentLock{InputFingerprint: "sha256:curl-v2"}
	bashLock := lockfile.ComponentLock{InputFingerprint: "sha256:bash-v1"}
	sedFrom := lockfile.ComponentLock{InputFingerprint: "sha256:sed-v1"}
	sedTo := lockfile.ComponentLock{InputFingerprint: "sha256:sed-v2"}

	fromLocks := map[string]lockfile.ComponentLock{
		"curl": curlFrom,
		"bash": bashLock,
		"sed":  sedFrom,
	}

	toLocks := map[string]lockfile.ComponentLock{
		"curl": curlTo,
		"bash": bashLock,
		"sed":  sedTo,
	}

	curlResult := classifyComponent("curl", fromLocks, toLocks)
	assert.Equal(t, changeTypeChanged, curlResult.ChangeType)

	bashResult := classifyComponent("bash", fromLocks, toLocks)
	assert.Equal(t, changeTypeUnchanged, bashResult.ChangeType)

	sedResult := classifyComponent("sed", fromLocks, toLocks)
	assert.Equal(t, changeTypeChanged, sedResult.ChangeType)
}

// --- Incremental updates test ---

func TestIncrementalUpdates(t *testing.T) {
	lockV1 := makeLock(t, "sha256:v1", "aaa")
	lockV2 := makeLock(t, "sha256:v2", "bbb")
	lockV3 := makeLock(t, "sha256:v3", "ccc")

	repo, hashes := testRepoWithCommits(t, []testRepoCommit{
		{files: map[string][]byte{
			"locks/curl.lock":      lockV1,
			"SPECS/c/curl/sources": []byte("SHA512 (curl-7.0.tar.gz) = old"),
		}},
		{files: map[string][]byte{
			"locks/curl.lock":      lockV2,
			"SPECS/c/curl/sources": []byte("SHA512 (curl-8.0.tar.gz) = mid"),
		}},
		{files: map[string][]byte{
			"locks/curl.lock":      lockV3,
			"SPECS/c/curl/sources": []byte("SHA512 (curl-8.1.tar.gz) = new"),
		}},
	})

	tests := []struct {
		name          string
		fromIdx       int
		toIdx         int
		changeType    string
		sourcesChange bool
	}{
		{"v1-v2", 0, 1, changeTypeChanged, true},
		{"v2-v3", 1, 2, changeTypeChanged, true},
		{"v1-v3 skip middle", 0, 2, changeTypeChanged, true},
		{"v2-v2 same ref", 1, 1, changeTypeUnchanged, false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fromLocks, readErr := lockfile.ReadAllAtCommit(repo, hashes[testCase.fromIdx], "locks")
			require.NoError(t, readErr)

			toLocks, readErr := lockfile.ReadAllAtCommit(repo, hashes[testCase.toIdx], "locks")
			require.NoError(t, readErr)

			result := classifyComponent("curl", fromLocks, toLocks)
			assert.Equal(t, testCase.changeType, result.ChangeType, "changeType")

			fromTree, treeErr := resolveTree(repo, hashes[testCase.fromIdx])
			require.NoError(t, treeErr)

			toTree, treeErr := resolveTree(repo, hashes[testCase.toIdx])
			require.NoError(t, treeErr)

			srcChange, srcErr := compareSources("/", fromTree, toTree, testSpecsDirSPECS, "curl")
			require.NoError(t, srcErr)
			assert.Equal(t, testCase.sourcesChange, srcChange, "sourcesChange")
		})
	}
}

// --- Config-only change (rebuild without re-upload) ---

func TestConfigOnlyChange(t *testing.T) {
	fromLocks := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: "sha256:config-v1"},
	}
	toLocks := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: "sha256:config-v2"},
	}

	result := classifyComponent("curl", fromLocks, toLocks)
	assert.Equal(t, changeTypeChanged, result.ChangeType, "fingerprint changed")
}

// --- Manual bump (fingerprint changes, sources same) ---

func TestManualBumpOnly(t *testing.T) {
	fromLocks := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: "sha256:bump0"},
	}
	toLocks := map[string]lockfile.ComponentLock{
		"curl": {InputFingerprint: "sha256:bump1"},
	}

	result := classifyComponent("curl", fromLocks, toLocks)
	assert.Equal(t, changeTypeChanged, result.ChangeType, "manual bump changes fingerprint")
}

// --- resolveTree / readFileFromTree / helpers ---

func TestResolveTree(t *testing.T) {
	repo, fromRef, _ := testRepoWithTwoCommits(t,
		map[string][]byte{"file.txt": []byte("hello")},
		map[string][]byte{"file.txt": []byte("world")},
	)

	tree, err := resolveTree(repo, fromRef)
	require.NoError(t, err)
	require.NotNil(t, tree)

	content, err := readFileFromTree(tree, "file.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), content)
}

func TestResolveTree_InvalidRef(t *testing.T) {
	repo, _, _ := testRepoWithTwoCommits(t,
		map[string][]byte{"file.txt": []byte("hello")},
		map[string][]byte{"file.txt": []byte("world")},
	)

	_, err := resolveCommitHash(repo, "nonexistent-ref")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving ref")
}

func TestReadFileFromTree(t *testing.T) {
	repo, ref, _ := testRepoWithTwoCommits(t,
		map[string][]byte{"SPECS/c/curl/sources": []byte("SHA512 (file) = hash")},
		map[string][]byte{"SPECS/c/curl/sources": []byte("SHA512 (file) = hash")},
	)

	tree, err := resolveTree(repo, ref)
	require.NoError(t, err)

	content, err := readFileFromTree(tree, "SPECS/c/curl/sources")
	require.NoError(t, err)
	assert.Equal(t, []byte("SHA512 (file) = hash"), content)
}

func TestReadFileFromTree_Missing(t *testing.T) {
	repo, ref, _ := testRepoWithTwoCommits(t,
		map[string][]byte{"placeholder": []byte("x")},
		map[string][]byte{"placeholder": []byte("x")},
	)

	tree, err := resolveTree(repo, ref)
	require.NoError(t, err)

	_, err = readFileFromTree(tree, "nonexistent/path")
	require.Error(t, err)
}

func TestReadFileFromTreeSafe_NotFound(t *testing.T) {
	repo, ref, _ := testRepoWithTwoCommits(t,
		map[string][]byte{"placeholder": []byte("x")},
		map[string][]byte{"placeholder": []byte("x")},
	)

	tree, err := resolveTree(repo, ref)
	require.NoError(t, err)

	_, notFound, readErr := readFileFromTreeSafe(tree, "nonexistent/path")
	require.NoError(t, readErr)
	assert.True(t, notFound)
}

// --- classifyComponent table-driven ---

func TestClassifyComponent_TableDriven(t *testing.T) {
	lockA := lockfile.ComponentLock{InputFingerprint: "sha256:aaa"}
	lockB := lockfile.ComponentLock{InputFingerprint: "sha256:bbb"}

	tests := []struct {
		name           string
		fromLocks      map[string]lockfile.ComponentLock
		toLocks        map[string]lockfile.ComponentLock
		wantChangeType string
	}{
		{
			name:           "both present, fingerprint changed",
			fromLocks:      map[string]lockfile.ComponentLock{"curl": lockA},
			toLocks:        map[string]lockfile.ComponentLock{"curl": lockB},
			wantChangeType: changeTypeChanged,
		},
		{
			name:           "both present, fingerprint same",
			fromLocks:      map[string]lockfile.ComponentLock{"curl": lockA},
			toLocks:        map[string]lockfile.ComponentLock{"curl": lockA},
			wantChangeType: changeTypeUnchanged,
		},
		{
			name:           "added",
			fromLocks:      map[string]lockfile.ComponentLock{},
			toLocks:        map[string]lockfile.ComponentLock{"curl": lockA},
			wantChangeType: changeTypeAdded,
		},
		{
			name:           "deleted",
			fromLocks:      map[string]lockfile.ComponentLock{"curl": lockA},
			toLocks:        map[string]lockfile.ComponentLock{},
			wantChangeType: changeTypeDeleted,
		},
		{
			name:           "never existed",
			fromLocks:      map[string]lockfile.ComponentLock{},
			toLocks:        map[string]lockfile.ComponentLock{},
			wantChangeType: changeTypeUnchanged,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result := classifyComponent("curl", testCase.fromLocks, testCase.toLocks)
			assert.Equal(t, testCase.wantChangeType, result.ChangeType)
		})
	}
}
