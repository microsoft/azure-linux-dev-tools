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

func TestFindAffectsCommits_NoSubstringMatch(t *testing.T) {
	repo := createInMemoryRepo(t)

	// "Affects: curl-minimal" should NOT match when searching for "curl".
	addCommit(t, repo,
		"Update curl-minimal\n\nAffects: curl-minimal",
		"Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	addCommit(t, repo,
		"Update curl itself\n\nAffects: curl",
		"Bob", "bob@example.com",
		time.Date(2025, 2, 1, 10, 0, 0, 0, time.UTC))

	// Searching for "curl" matches only Bob's commit (exact component name).
	curlResults, err := sources.FindAffectsCommits(repo, "curl")
	require.NoError(t, err)
	require.Len(t, curlResults, 1, "exact match should not include curl-minimal commit")
	assert.Equal(t, "Bob", curlResults[0].Author)

	// Searching for "curl-minimal" matches only Alice's commit.
	minimalResults, err := sources.FindAffectsCommits(repo, "curl-minimal")
	require.NoError(t, err)
	require.Len(t, minimalResults, 1)
	assert.Equal(t, "Alice", minimalResults[0].Author)
}

func TestFindAffectsCommits_AffectsInSubject(t *testing.T) {
	repo := createInMemoryRepo(t)

	// Affects marker in the subject line (not just the body).
	addCommit(t, repo,
		"Affects: curl",
		"Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	results, err := sources.FindAffectsCommits(repo, "curl")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Alice", results[0].Author)
}

func TestFindAffectsCommits_CaseSensitive(t *testing.T) {
	repo := createInMemoryRepo(t)

	addCommit(t, repo,
		"Bump release\n\nAffects: Kernel",
		"Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	addCommit(t, repo,
		"Fix CVE\n\nAFFECTS: KERNEL",
		"Bob", "bob@example.com",
		time.Date(2025, 2, 1, 10, 0, 0, 0, time.UTC))

	addCommit(t, repo,
		"Upstream fix\n\nAffects: kernel",
		"Charlie", "charlie@example.com",
		time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC))

	// Matching is case-sensitive: searching for "kernel" only matches the exact-case commit.
	results, err := sources.FindAffectsCommits(repo, "kernel")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Charlie", results[0].Author)

	// Searching for "Kernel" matches only Alice's commit (exact case on component name).
	results, err = sources.FindAffectsCommits(repo, "Kernel")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Alice", results[0].Author)
}

func TestMessageAffectsComponent(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		component string
		want      bool
	}{
		// Positive matches.
		{"exact match in body", "Fix bug\n\nAffects: curl", "curl", true},
		{"trailing whitespace", "Fix bug\n\nAffects: curl  ", "curl", true},
		{"leading whitespace on line", "Fix bug\n\n  Affects: curl", "curl", true},
		{"subject line only", "Affects: curl", "curl", true},

		// Negative matches.
		{"different component", "Fix bug\n\nAffects: wget", "curl", false},
		{"no substring match", "Fix bug\n\nAffects: curl-minimal", "curl", false},
		{"comma separated", "Fix bug\n\nAffects: curl, wget", "curl", false},
		{"extra text after name", "Affects: curl - fix build failure", "curl", false},
		{"case sensitive", "Fix bug\n\nAffects: Curl", "curl", false},
		{"no match across newlines", "Fix bug\n\nAffects:\ncurl", "curl", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sources.MessageAffectsComponent(tt.message, tt.component)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCommitSyntheticHistory(t *testing.T) {
	// Create an in-memory repo with an initial commit (simulating upstream).
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Create an initial file (upstream).
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

	// Simulate overlay application by modifying the working tree before committing.
	specFile, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: package\nVersion: 1.0\n# overlays applied\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

	// Define synthetic commits.
	commits := []sources.CommitMetadata{
		{
			Hash:        "abc123def456",
			Author:      "Alice",
			AuthorEmail: "alice@example.com",
			Timestamp:   time.Date(2025, 1, 10, 10, 0, 0, 0, time.UTC).Unix(),
			Message:     "Apply patch fix",
		},
		{
			Hash:        "789abc012def",
			Author:      "Bob",
			AuthorEmail: "bob@example.com",
			Timestamp:   time.Date(2025, 2, 20, 14, 0, 0, 0, time.UTC).Unix(),
			Message:     "Bump release",
		},
	}

	err = sources.CommitSyntheticHistory(repo, commits)
	require.NoError(t, err)

	// Verify the commit log has 3 commits: upstream + 2 synthetic.
	head, err := repo.Head()
	require.NoError(t, err)

	commitIter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	require.NoError(t, err)

	var logCommits []*object.Commit

	err = commitIter.ForEach(func(c *object.Commit) error {
		logCommits = append(logCommits, c)

		return nil
	})
	require.NoError(t, err)

	require.Len(t, logCommits, 3, "should have upstream + 2 synthetic commits")

	// Most recent commit (Bob's) — empty commit.
	assert.Contains(t, logCommits[0].Message, "Bump release")
	assert.Equal(t, "Bob", logCommits[0].Author.Name)
	assert.Equal(t, "bob@example.com", logCommits[0].Author.Email)

	// Second commit (Alice's) — has the actual file changes.
	assert.Contains(t, logCommits[1].Message, "Apply patch fix")
	assert.Equal(t, "Alice", logCommits[1].Author.Name)

	// Original upstream commit.
	assert.Equal(t, "upstream: initial", logCommits[2].Message)
}

func TestCommitSyntheticHistory_SingleCommit(t *testing.T) {
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	file, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = file.Write([]byte("Name: package\n"))
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

	// Modify working tree (simulates overlay application).
	specFile, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: package\n# modified\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

	commits := []sources.CommitMetadata{
		{
			Hash:        "abc123",
			Author:      "Alice",
			AuthorEmail: "alice@example.com",
			Timestamp:   time.Date(2025, 1, 10, 10, 0, 0, 0, time.UTC).Unix(),
			Message:     "Fix build",
		},
	}

	err = sources.CommitSyntheticHistory(repo, commits)
	require.NoError(t, err)

	// Verify working tree changes are in the single synthetic commit.
	head, err := repo.Head()
	require.NoError(t, err)

	headCommit, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)

	assert.Contains(t, headCommit.Message, "Fix build")
	assert.Equal(t, "Alice", headCommit.Author.Name)

	// Verify file content was committed.
	tree, err := headCommit.Tree()
	require.NoError(t, err)

	entry, err := tree.File("package.spec")
	require.NoError(t, err)

	content, err := entry.Contents()
	require.NoError(t, err)
	assert.Contains(t, content, "# modified")
}
