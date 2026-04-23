// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources_test

import (
	"fmt"
	"testing"
	"time"

	memfs "github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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

func TestFindAffectsCommits_ShallowRepo(t *testing.T) {
	repo := createInMemoryRepo(t)

	addCommit(t, repo,
		"Fix CVE-2025-1234\n\nAffects: curl",
		"Alice", "alice@example.com",
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC))

	head, err := repo.Head()
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetShallow([]plumbing.Hash{head.Hash()}))

	results, err := sources.FindAffectsCommits(repo, "curl")
	require.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "shallow clone")
	assert.Contains(t, err.Error(), "git fetch --unshallow")
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
		name          string
		componentName string
		want          string
	}{
		{"simple name", "curl", "specs/c/curl/curl.lock"},
		{"hyphenated name", "curl-minimal", "specs/c/curl-minimal/curl-minimal.lock"},
		{"uppercase first letter", "Kernel", "specs/k/Kernel/Kernel.lock"},
		{"single char name", "a", "specs/a/a/a.lock"},
		{"long name", "golang-github-example", "specs/g/golang-github-example/golang-github-example.lock"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sources.LockFilePath(tt.componentName)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCommitInterleavedHistory_AllOnTop(t *testing.T) {
	// When all fingerprint changes reference the latest upstream commit,
	// all synthetic commits should be appended on top.
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Create an upstream commit.
	file, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = file.Write([]byte("Name: package\nVersion: 1.0\n"))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	_, err = worktree.Add("package.spec")
	require.NoError(t, err)

	upstreamCommit, err := worktree.Commit("upstream: initial", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Upstream",
			Email: "upstream@fedora.org",
			When:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	// Simulate overlay modification.
	specFile, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: package\nVersion: 1.0\n# overlays applied\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

	upstreamHash := upstreamCommit.String()

	changes := []sources.FingerprintChange{
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "abc123",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   time.Date(2025, 1, 10, 10, 0, 0, 0, time.UTC).Unix(),
				Message:     "Apply patch fix",
			},
			UpstreamCommit: upstreamHash,
		},
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "def456",
				Author:      "Bob",
				AuthorEmail: "bob@example.com",
				Timestamp:   time.Date(2025, 2, 20, 14, 0, 0, 0, time.UTC).Unix(),
				Message:     "Bump release",
			},
			UpstreamCommit: upstreamHash,
		},
	}

	err = sources.CommitInterleavedHistory(repo, changes, "")
	require.NoError(t, err)

	// Verify the commit log: upstream + 2 synthetic = 3 commits.
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

	// Most recent commit (Bob's) — this is the last synthetic commit.
	assert.Contains(t, logCommits[0].Message, "Bump release")
	assert.Equal(t, "Bob", logCommits[0].Author.Name)

	// Second commit (Alice's).
	assert.Contains(t, logCommits[1].Message, "Apply patch fix")
	assert.Equal(t, "Alice", logCommits[1].Author.Name)

	// Original upstream commit.
	assert.Equal(t, "upstream: initial", logCommits[2].Message)
}

func TestCommitInterleavedHistory_Interleaved(t *testing.T) {
	// Two upstream commits, one synthetic change for the first (older) upstream
	// commit and one for the second (latest). The interleaved commit should
	// appear between the two upstream commits.
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Upstream commit 1.
	file1, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = file1.Write([]byte("Name: package\nVersion: 1.0\n"))
	require.NoError(t, err)
	require.NoError(t, file1.Close())

	_, err = worktree.Add("package.spec")
	require.NoError(t, err)

	upstream1, err := worktree.Commit("upstream: v1.0", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Upstream",
			Email: "upstream@fedora.org",
			When:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	// Upstream commit 2.
	file2, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = file2.Write([]byte("Name: package\nVersion: 2.0\n"))
	require.NoError(t, err)
	require.NoError(t, file2.Close())

	_, err = worktree.Add("package.spec")
	require.NoError(t, err)

	upstream2, err := worktree.Commit("upstream: v2.0", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Upstream",
			Email: "upstream@fedora.org",
			When:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	// Simulate overlay modification in working tree.
	specFile, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: package\nVersion: 2.0\n# overlays\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

	changes := []sources.FingerprintChange{
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "proj-aaa",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC).Unix(),
				Message:     "Fix for v1.0",
			},
			UpstreamCommit: upstream1.String(), // references older upstream.
		},
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "proj-bbb",
				Author:      "Bob",
				AuthorEmail: "bob@example.com",
				Timestamp:   time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC).Unix(),
				Message:     "Fix for v2.0",
			},
			UpstreamCommit: upstream2.String(), // references latest upstream.
		},
	}

	err = sources.CommitInterleavedHistory(repo, changes, upstream1.String())
	require.NoError(t, err)

	// Expected order (newest first):
	// 1. "Fix for v2.0" (synthetic, on top — latest upstream, with overlay)
	// 2. "upstream: v2.0" (replayed with new parent)
	// 3. "Fix for v1.0" (synthetic, interleaved after upstream v1.0)
	// 4. "upstream: v1.0" (import-commit, kept as-is)
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

	require.Len(t, logCommits, 4, "should have 2 upstream + 2 synthetic commits")

	assert.Contains(t, logCommits[0].Message, "Fix for v2.0")   // top synthetic (latest)
	assert.Contains(t, logCommits[1].Message, "upstream: v2.0") // replayed upstream 2
	assert.Contains(t, logCommits[2].Message, "Fix for v1.0")   // interleaved synthetic
	assert.Contains(t, logCommits[3].Message, "upstream: v1.0") // import-commit (kept)
}

func TestCommitInterleavedHistory_SingleCommit(t *testing.T) {
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

	upstream, err := worktree.Commit("upstream: initial", &gogit.CommitOptions{
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

	changes := []sources.FingerprintChange{
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "abc123",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   time.Date(2025, 1, 10, 10, 0, 0, 0, time.UTC).Unix(),
				Message:     "Fix build",
			},
			UpstreamCommit: upstream.String(),
		},
	}

	err = sources.CommitInterleavedHistory(repo, changes, "")
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

func TestCommitInterleavedHistory_OrphanUpstreamCommit(t *testing.T) {
	// When a fingerprint change references an upstream commit that doesn't
	// exist in the dist-git history, it should be dropped (not appended).
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

	upstream, err := worktree.Commit("upstream: initial", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Upstream",
			Email: "upstream@fedora.org",
			When:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	changes := []sources.FingerprintChange{
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "proj-orphan",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC).Unix(),
				Message:     "Fix for unknown upstream",
			},
			UpstreamCommit: "deadbeefdeadbeef", // not in dist-git history.
		},
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "proj-latest",
				Author:      "Bob",
				AuthorEmail: "bob@example.com",
				Timestamp:   time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC).Unix(),
				Message:     "Latest fix",
			},
			UpstreamCommit: upstream.String(), // latest.
		},
	}

	err = sources.CommitInterleavedHistory(repo, changes, "")
	require.NoError(t, err)

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

	// Only the latest-upstream synthetic commit is included; orphan is dropped.
	require.Len(t, logCommits, 2)
	assert.Contains(t, logCommits[0].Message, "Latest fix")
	assert.Equal(t, "upstream: initial", logCommits[1].Message)
}

func TestParseCommitMetadata(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    sources.CommitMetadata
		wantErr bool
	}{
		{
			name:  "valid output",
			input: "abc123def456\nAlice\nalice@example.com\n1706100000\nFix CVE-2025-1234",
			want: sources.CommitMetadata{
				Hash:        "abc123def456",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   1706100000,
				Message:     "Fix CVE-2025-1234",
			},
		},
		{
			name:    "too few lines",
			input:   "abc123\nAlice\nalice@example.com",
			wantErr: true,
		},
		{
			name:    "invalid timestamp",
			input:   "abc123\nAlice\nalice@example.com\nnot-a-number\nFix bug",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := sources.ParseCommitMetadata(test.input)
			if test.wantErr {
				assert.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}
