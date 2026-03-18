// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources_test

import (
	"fmt"
	"testing"
	"time"

	memfs "github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createInMemoryRepo creates an empty in-memory git repository.
func createInMemoryRepo(t *testing.T) *gogit.Repository {
	t.Helper()

	repo, err := gogit.Init(memory.NewStorage(), memfs.New())
	require.NoError(t, err)

	return repo
}

// addCommit creates a commit in the in-memory repository with the given message, author name,
// email, and timestamp. A dummy file change is added to ensure the commit is non-empty.
func addCommit(
	t *testing.T, repo *gogit.Repository, message, authorName, authorEmail string, when time.Time,
) {
	t.Helper()

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	fs := worktree.Filesystem

	// Write a unique file per commit to guarantee a non-empty diff.
	fileName := fmt.Sprintf("file-%d.txt", when.UnixNano())

	f, err := fs.Create(fileName)
	require.NoError(t, err)

	_, err = f.Write([]byte(message))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = worktree.Add(fileName)
	require.NoError(t, err)

	_, err = worktree.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  authorName,
			Email: authorEmail,
			When:  when,
		},
	})
	require.NoError(t, err)
}

func TestFindAffectsCommits(t *testing.T) {
	repo := createInMemoryRepo(t)

	// Three commits: two mention curl, one does not.
	addCommit(t, repo,
		"Initial setup",
		"Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	addCommit(t, repo,
		"Fix CVE-2025-1234\n\nAffects: curl",
		"Bob", "bob@example.com",
		time.Date(2025, 2, 1, 10, 0, 0, 0, time.UTC))

	addCommit(t, repo,
		"Bump release\n\nAffects: curl",
		"Charlie", "charlie@example.com",
		time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC))

	results, err := sources.FindAffectsCommits(repo, "curl")
	require.NoError(t, err)

	// Expect 2 matching commits, oldest first.
	require.Len(t, results, 2)

	assert.Equal(t, "Bob", results[0].Author)
	assert.Equal(t, "bob@example.com", results[0].AuthorEmail)
	assert.Contains(t, results[0].Message, "Fix CVE-2025-1234")

	assert.Equal(t, "Charlie", results[1].Author)
	assert.Equal(t, "charlie@example.com", results[1].AuthorEmail)
	assert.Contains(t, results[1].Message, "Bump release")

	// Chronological order: Bob's timestamp < Charlie's timestamp.
	assert.Less(t, results[0].Timestamp, results[1].Timestamp)
}

func TestFindAffectsCommits_NoMatches(t *testing.T) {
	repo := createInMemoryRepo(t)

	addCommit(t, repo,
		"Unrelated change",
		"Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	results, err := sources.FindAffectsCommits(repo, "curl")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestFindAffectsCommits_MultipleComponents(t *testing.T) {
	repo := createInMemoryRepo(t)

	addCommit(t, repo,
		"Fix curl issue\n\nAffects: curl",
		"Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	addCommit(t, repo,
		"Fix wget issue\n\nAffects: wget",
		"Bob", "bob@example.com",
		time.Date(2025, 2, 1, 10, 0, 0, 0, time.UTC))

	addCommit(t, repo,
		"Fix both\n\nAffects: curl\nAffects: wget",
		"Charlie", "charlie@example.com",
		time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC))

	curlResults, err := sources.FindAffectsCommits(repo, "curl")
	require.NoError(t, err)
	require.Len(t, curlResults, 2, "curl should match 2 commits")
	assert.Equal(t, "Alice", curlResults[0].Author)
	assert.Equal(t, "Charlie", curlResults[1].Author)

	wgetResults, err := sources.FindAffectsCommits(repo, "wget")
	require.NoError(t, err)
	require.Len(t, wgetResults, 2, "wget should match 2 commits")
	assert.Equal(t, "Bob", wgetResults[0].Author)
	assert.Equal(t, "Charlie", wgetResults[1].Author)
}

func TestFindAffectsCommits_SubstringMatch(t *testing.T) {
	repo := createInMemoryRepo(t)

	// "Affects: curl-minimal" contains "Affects: curl" as a substring.
	addCommit(t, repo,
		"Update curl-minimal\n\nAffects: curl-minimal",
		"Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	addCommit(t, repo,
		"Update curl itself\n\nAffects: curl",
		"Bob", "bob@example.com",
		time.Date(2025, 2, 1, 10, 0, 0, 0, time.UTC))

	// Searching for "curl" matches both because "Affects: curl-minimal" contains "Affects: curl".
	curlResults, err := sources.FindAffectsCommits(repo, "curl")
	require.NoError(t, err)
	assert.Len(t, curlResults, 2, "substring match includes curl-minimal commit")

	// Searching for "curl-minimal" matches only the first commit.
	minimalResults, err := sources.FindAffectsCommits(repo, "curl-minimal")
	require.NoError(t, err)
	require.Len(t, minimalResults, 1)
	assert.Equal(t, "Alice", minimalResults[0].Author)
}

func TestFindAffectsCommits_AffectsInSubject(t *testing.T) {
	repo := createInMemoryRepo(t)

	// Affects marker in the subject line (not just the body).
	addCommit(t, repo,
		"Affects: curl - fix build failure",
		"Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	results, err := sources.FindAffectsCommits(repo, "curl")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Alice", results[0].Author)
}

func TestIsRepoDirty_CleanRepo(t *testing.T) {
	repo := createInMemoryRepo(t)

	addCommit(t, repo, "initial", "Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	dirty, err := sources.IsRepoDirty(repo)
	require.NoError(t, err)
	assert.False(t, dirty, "repo with no uncommitted changes should be clean")
}

func TestIsRepoDirty_ModifiedFile(t *testing.T) {
	repo := createInMemoryRepo(t)

	addCommit(t, repo, "initial", "Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	// Modify a tracked file without committing.
	worktree, err := repo.Worktree()
	require.NoError(t, err)

	f, err := worktree.Filesystem.Create("file-946684800000000000.txt")
	require.NoError(t, err)

	_, err = f.Write([]byte("modified content"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	dirty, err := sources.IsRepoDirty(repo)
	require.NoError(t, err)
	assert.True(t, dirty, "repo with modified file should be dirty")
}

func TestIsRepoDirty_UntrackedFile(t *testing.T) {
	repo := createInMemoryRepo(t)

	addCommit(t, repo, "initial", "Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	// Create an untracked file.
	worktree, err := repo.Worktree()
	require.NoError(t, err)

	f, err := worktree.Filesystem.Create("untracked-new-file.txt")
	require.NoError(t, err)

	_, err = f.Write([]byte("new"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	dirty, err := sources.IsRepoDirty(repo)
	require.NoError(t, err)
	assert.True(t, dirty, "repo with untracked file should be dirty")
}

func TestCommitSyntheticHistory(t *testing.T) {
	// Create an in-memory repo with an initial commit (simulating upstream).
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Create an initial file.
	file, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = file.Write([]byte("Name: package\nVersion: 1.0\n"))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	_, err = worktree.Add("package.spec")
	require.NoError(t, err)

	_, err = worktree.Commit("upstream: initial", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Upstream",
			Email: "upstream@fedora.org",
			When:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	// Define overlay groups.
	groups := []sources.OverlayCommitGroup{
		{
			Commit: sources.CommitMetadata{
				Hash:        "abc123def456",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   time.Date(2025, 1, 10, 10, 0, 0, 0, time.UTC).Unix(),
				Message:     "Apply patch fix",
			},
			Overlays: []projectconfig.ComponentOverlay{
				{Type: projectconfig.ComponentOverlayAddPatch},
			},
		},
		{
			Commit: sources.CommitMetadata{
				Hash:        "789abc012def",
				Author:      "Bob",
				AuthorEmail: "bob@example.com",
				Timestamp:   time.Date(2025, 2, 20, 14, 0, 0, 0, time.UTC).Unix(),
				Message:     "Bump release",
			},
			Overlays: []projectconfig.ComponentOverlay{
				{Type: projectconfig.ComponentOverlaySetSpecTag},
			},
		},
	}

	// applyFn simulates overlay application by modifying the spec file.
	callCount := 0
	applyFn := func(overlays []projectconfig.ComponentOverlay) error {
		callCount++

		specFile, createErr := memFS.Create("package.spec")
		if createErr != nil {
			return createErr
		}

		// Write different content each call so the worktree has changes to commit.
		content := fmt.Sprintf("Name: package\nVersion: 1.0\n# overlay applied (call %d)\n", callCount)
		_, createErr = specFile.Write([]byte(content))

		closeErr := specFile.Close()

		if createErr != nil {
			return createErr
		}

		return closeErr
	}

	err = sources.CommitSyntheticHistory(repo, groups, applyFn)
	require.NoError(t, err)
	assert.Equal(t, 2, callCount, "applyFn should be called once per group")

	// Verify the commit log has 3 commits: upstream + 2 synthetic.
	head, err := repo.Head()
	require.NoError(t, err)

	commitIter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	require.NoError(t, err)

	var commits []*object.Commit

	err = commitIter.ForEach(func(c *object.Commit) error {
		commits = append(commits, c)

		return nil
	})
	require.NoError(t, err)

	assert.Len(t, commits, 3, "should have upstream + 2 synthetic commits")

	// Most recent commit (Bob's).
	assert.Contains(t, commits[0].Message, "Bump release")
	assert.Equal(t, "Bob", commits[0].Author.Name)
	assert.Equal(t, "bob@example.com", commits[0].Author.Email)

	// Second commit (Alice's).
	assert.Contains(t, commits[1].Message, "Apply patch fix")
	assert.Equal(t, "Alice", commits[1].Author.Name)

	// Original upstream commit.
	assert.Equal(t, "upstream: initial", commits[2].Message)
}

func TestCommitSyntheticHistory_EmptyGroups(t *testing.T) {
	repo := createInMemoryRepo(t)

	addCommit(t, repo, "initial", "Test", "test@example.com",
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	err := sources.CommitSyntheticHistory(repo, nil, nil)
	assert.ErrorIs(t, err, sources.ErrNoOverlaysToCommit)
}
