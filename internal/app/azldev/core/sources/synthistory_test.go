// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources_test

import (
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

func TestCommitInterleavedHistory_LocalComponent(t *testing.T) {
	// Local components have no upstream commits — all fingerprint changes
	// have empty UpstreamCommit. The initial commit acts as the root and
	// all synthetic commits are appended on top.
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Create an initial commit (simulates initSourcesRepo).
	file, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = file.Write([]byte("Name: local-package\nVersion: 1.0\n"))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	_, err = worktree.Add("package.spec")
	require.NoError(t, err)

	_, err = worktree.Commit("Initial sources", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "azldev",
			Email: "azldev@localhost",
			When:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	// Simulate overlay modification in working tree.
	specFile, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: local-package\nVersion: 1.0\n# overlays applied\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

	// All changes have empty UpstreamCommit (local component).
	changes := []sources.FingerprintChange{
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "local-aaa",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
				Message:     "Add overlay config",
			},
			UpstreamCommit: "", // local — no upstream.
		},
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "local-bbb",
				Author:      "Bob",
				AuthorEmail: "bob@example.com",
				Timestamp:   time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC).Unix(),
				Message:     "Bump release",
			},
			UpstreamCommit: "", // local — no upstream.
		},
	}

	err = sources.CommitInterleavedHistory(repo, changes, "")
	require.NoError(t, err)

	// Verify: initial commit + 2 synthetic = 3 commits.
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

	require.Len(t, logCommits, 3, "should have initial + 2 synthetic commits")

	// Most recent (Bob's synthetic commit with overlay content).
	assert.Contains(t, logCommits[0].Message, "Bump release")
	assert.Equal(t, "Bob", logCommits[0].Author.Name)

	// Alice's synthetic commit (empty — only the last carries overlay tree).
	assert.Contains(t, logCommits[1].Message, "Add overlay config")
	assert.Equal(t, "Alice", logCommits[1].Author.Name)

	// Initial sources commit.
	assert.Equal(t, "Initial sources", logCommits[2].Message)

	// Verify the last synthetic commit has the overlay content.
	tree, err := logCommits[0].Tree()
	require.NoError(t, err)

	entry, err := tree.File("package.spec")
	require.NoError(t, err)

	content, err := entry.Contents()
	require.NoError(t, err)
	assert.Contains(t, content, "# overlays applied")
}

func TestCommitInterleavedHistory_MergeCommitInUpstream(t *testing.T) {
	// When the upstream dist-git contains merge commits, the replay should
	// linearize them: follow only first parents and preserve the merge
	// commit's tree content.
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Upstream commit A (root).
	fileA, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = fileA.Write([]byte("Name: package\nVersion: 1.0\n"))
	require.NoError(t, err)
	require.NoError(t, fileA.Close())

	_, err = worktree.Add("package.spec")
	require.NoError(t, err)

	commitA, err := worktree.Commit("upstream: v1.0", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Upstream",
			Email: "upstream@fedora.org",
			When:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	commitAObj, err := repo.CommitObject(commitA)
	require.NoError(t, err)

	// Upstream commit B (child of A, on main branch).
	fileB, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = fileB.Write([]byte("Name: package\nVersion: 2.0\n"))
	require.NoError(t, err)
	require.NoError(t, fileB.Close())

	_, err = worktree.Add("package.spec")
	require.NoError(t, err)

	commitB, err := worktree.Commit("upstream: v2.0", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Upstream",
			Email: "upstream@fedora.org",
			When:  time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	commitBObj, err := repo.CommitObject(commitB)
	require.NoError(t, err)

	// Create a side-branch commit F (parent: A) to serve as second parent of merge.
	featureAuthor := object.Signature{
		Name:  "Feature",
		Email: "feature@fedora.org",
		When:  time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
	}

	featureCommitObj := &object.Commit{
		Author:       featureAuthor,
		Committer:    featureAuthor,
		Message:      "feature: add widget",
		TreeHash:     commitAObj.TreeHash,
		ParentHashes: []plumbing.Hash{commitA},
	}

	featureEncoded := repo.Storer.NewEncodedObject()
	err = featureCommitObj.Encode(featureEncoded)
	require.NoError(t, err)

	featureHash, err := repo.Storer.SetEncodedObject(featureEncoded)
	require.NoError(t, err)

	// Create merge commit M (parents: [B, F], tree: B's tree).
	mergeAuthor := object.Signature{
		Name:  "Upstream",
		Email: "upstream@fedora.org",
		When:  time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
	}

	mergeCommitObj := &object.Commit{
		Author:       mergeAuthor,
		Committer:    mergeAuthor,
		Message:      "Merge branch 'feature'",
		TreeHash:     commitBObj.TreeHash,
		ParentHashes: []plumbing.Hash{commitB, featureHash},
	}

	mergeEncoded := repo.Storer.NewEncodedObject()
	err = mergeCommitObj.Encode(mergeEncoded)
	require.NoError(t, err)

	mergeHash, err := repo.Storer.SetEncodedObject(mergeEncoded)
	require.NoError(t, err)

	// Update HEAD to point to the merge commit.
	head, err := repo.Storer.Reference(plumbing.HEAD)
	require.NoError(t, err)

	branchName := head.Target()
	err = repo.Storer.SetReference(plumbing.NewHashReference(branchName, mergeHash))
	require.NoError(t, err)

	// Simulate overlay modification in working tree.
	specFile, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: package\nVersion: 2.0\n# overlay applied\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

	// Fingerprint change references the merge commit as upstream.
	changes := []sources.FingerprintChange{
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "proj-merge",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
				Message:     "Fix for merged version",
			},
			UpstreamCommit: mergeHash.String(),
		},
	}

	err = sources.CommitInterleavedHistory(repo, changes, commitA.String())
	require.NoError(t, err)

	// Expected order (newest first):
	// 1. "Fix for merged version" (synthetic, with overlay content)
	// 2. "Merge branch 'feature'" (replayed merge, linearized)
	// 3. "upstream: v2.0" (replayed)
	// 4. "upstream: v1.0" (import-commit, kept as-is)
	// The side-branch commit F should NOT appear.
	newHead, err := repo.Head()
	require.NoError(t, err)

	commitIter, err := repo.Log(&gogit.LogOptions{From: newHead.Hash()})
	require.NoError(t, err)

	var logCommits []*object.Commit

	err = commitIter.ForEach(func(c *object.Commit) error {
		logCommits = append(logCommits, c)

		return nil
	})
	require.NoError(t, err)

	require.Len(t, logCommits, 4, "should have 3 upstream (A, B, M linearized) + 1 synthetic")

	assert.Contains(t, logCommits[0].Message, "Fix for merged version") // synthetic
	assert.Contains(t, logCommits[1].Message, "Merge branch 'feature'") // linearized merge
	assert.Contains(t, logCommits[2].Message, "upstream: v2.0")         // replayed
	assert.Contains(t, logCommits[3].Message, "upstream: v1.0")         // import-commit

	// All replayed commits should have exactly 1 parent (linearized).
	for i := range 3 {
		assert.Len(t, logCommits[i].ParentHashes, 1,
			"commit %d (%s) should have exactly 1 parent", i, logCommits[i].Message)
	}

	// Verify the synthetic commit carries overlay content.
	tree, err := logCommits[0].Tree()
	require.NoError(t, err)

	entry, err := tree.File("package.spec")
	require.NoError(t, err)

	content, err := entry.Contents()
	require.NoError(t, err)
	assert.Contains(t, content, "# overlay applied")
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
			input: "abc123def456\x00Alice\x00alice@example.com\x001706100000\x00Fix CVE-2025-1234",
			want: sources.CommitMetadata{
				Hash:        "abc123def456",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   1706100000,
				Message:     "Fix CVE-2025-1234",
			},
		},
		{
			name:    "too few fields",
			input:   "abc123\x00Alice\x00alice@example.com",
			wantErr: true,
		},
		{
			name:    "invalid timestamp",
			input:   "abc123\x00Alice\x00alice@example.com\x00not-a-number\x00Fix bug",
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
