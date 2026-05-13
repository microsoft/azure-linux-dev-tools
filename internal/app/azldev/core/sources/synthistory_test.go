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
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
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

	err = sources.CommitInterleavedHistory(repo, changes, "", "")
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

	err = sources.CommitInterleavedHistory(repo, changes, upstream1.String(), "")
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

	err = sources.CommitInterleavedHistory(repo, changes, "", "")
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

	err = sources.CommitInterleavedHistory(repo, changes, "", "")
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

func TestCommitInterleavedHistory_MergeCommitCrossBranch(t *testing.T) {
	// When a merge commit joins two branches, the committer-time-ordered
	// all-parent walk includes commits from both sides sorted by time.
	// This test verifies that commits from a merged-in branch are properly
	// included and interleaved in time order alongside the mainline.
	//
	// Graph (newest on top):
	//   M  ← merge commit (parents: [S, B])  ← HEAD
	//   |\
	//   S  B  ← S from merged branch, B from mainline
	//   |  |
	//   R  A  ← A is import-commit (root, mainline); R is root of merged branch
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// --- Mainline: A (root) → B ---

	// Commit A — import-commit, root of the mainline.
	specFile, err := memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: package\nVersion: 1.0\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

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

	// Commit B — on the mainline, child of A.
	specFile, err = memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: package\nVersion: 2.0\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

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

	// --- Merged branch: independent root R → S ---
	wrongRootAuthor := object.Signature{
		Name:  "Other",
		Email: "other@fedora.org",
		When:  time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
	}

	wrongRootCommit := &object.Commit{
		Author:    wrongRootAuthor,
		Committer: wrongRootAuthor,
		Message:   "merged-branch: root",
		TreeHash:  commitAObj.TreeHash,
	}

	wrongRootEncoded := repo.Storer.NewEncodedObject()
	err = wrongRootCommit.Encode(wrongRootEncoded)
	require.NoError(t, err)

	wrongRootHash, err := repo.Storer.SetEncodedObject(wrongRootEncoded)
	require.NoError(t, err)

	sideAuthor := object.Signature{
		Name:  "Other",
		Email: "other@fedora.org",
		When:  time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
	}

	sideCommitObj := &object.Commit{
		Author:       sideAuthor,
		Committer:    sideAuthor,
		Message:      "merged-branch: rebuild",
		TreeHash:     commitAObj.TreeHash,
		ParentHashes: []plumbing.Hash{wrongRootHash},
	}

	sideEncoded := repo.Storer.NewEncodedObject()
	err = sideCommitObj.Encode(sideEncoded)
	require.NoError(t, err)

	sideHash, err := repo.Storer.SetEncodedObject(sideEncoded)
	require.NoError(t, err)

	// --- Merge commit M: parents [S, B] ---
	mergeAuthor := object.Signature{
		Name:  "Upstream",
		Email: "upstream@fedora.org",
		When:  time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
	}

	mergeCommitObj := &object.Commit{
		Author:       mergeAuthor,
		Committer:    mergeAuthor,
		Message:      "Merge branch 'f44' into f43",
		TreeHash:     commitBObj.TreeHash,
		ParentHashes: []plumbing.Hash{sideHash, commitB},
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
	specFile, err = memFS.Create("package.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: package\nVersion: 2.0\n# overlay applied\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

	// Fingerprint change references the merge commit as upstream.
	changes := []sources.FingerprintChange{
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "proj-cross-branch",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
				Message:     "Fix for cross-branch merge",
			},
			UpstreamCommit: mergeHash.String(),
		},
	}

	err = sources.CommitInterleavedHistory(repo, changes, commitA.String(), "")
	require.NoError(t, err)

	// The CTime walk visits all parents sorted by committer time.
	// Expected first-parent chain (newest first) — 6 total:
	// 1. "Fix for cross-branch merge" (synthetic)
	// 2. "Merge branch 'f44' into f43" (replayed merge)
	// 3. "merged-branch: rebuild" (S, from merged branch)
	// 4. "upstream: v2.0" (B, from mainline)
	// 5. "merged-branch: root" (R, from merged branch)
	// 6. "upstream: v1.0" (A, import-commit)
	newHead, err := repo.Head()
	require.NoError(t, err)

	// Walk first-parent chain to verify the linearized replay.
	var logCommits []*object.Commit

	currentHash := newHead.Hash()

	for {
		commitObj, err := repo.CommitObject(currentHash)
		require.NoError(t, err)

		logCommits = append(logCommits, commitObj)

		if len(commitObj.ParentHashes) == 0 {
			break
		}

		currentHash = commitObj.ParentHashes[0]
	}

	require.Len(t, logCommits, 6,
		"should have 5 upstream (A, R, B, S, M) + 1 synthetic")

	assert.Contains(t, logCommits[0].Message, "Fix for cross-branch merge")
	assert.Contains(t, logCommits[5].Message, "upstream: v1.0") // import-commit

	// All commits should have exactly 1 parent (linearized), except the last (root).
	for i := range 5 {
		assert.Len(t, logCommits[i].ParentHashes, 1,
			"commit %d (%s) should have exactly 1 parent", i, logCommits[i].Message)
	}

	// Verify the synthetic commit carries overlay content.
	tr, err := logCommits[0].Tree()
	require.NoError(t, err)

	e, err := tr.File("package.spec")
	require.NoError(t, err)

	ct, err := e.Contents()
	require.NoError(t, err)
	assert.Contains(t, ct, "# overlay applied")
}

func TestCommitInterleavedHistory_UpstreamOnMergedBranch(t *testing.T) {
	// Regression test for the systemtap scenario: upstream-commit is a
	// plain commit on a merged-in branch (e.g. f44), while import-commit
	// is a merge commit on the target branch (f43) whose non-first-parent
	// is an ancestor of upstream-commit. HEAD is detached at the upstream
	// commit (simulating the clone + checkout flow).
	//
	// Real-world graph (systemtap f43):
	//   d06e77cc (f43 tip)  "Merge branch 'f44' into f43"
	//   ├─ 86f88495 (import) "Merge branch 'rawhide' into f43"
	//   │  ├─ 3c6c476  "Fix CI gating"      (f43 first-parent)
	//   │  └─ 6fe8d3d  "upstream release 5.4" (non-first-parent)
	//   └─ 58cfacab  "upstream release 5.5"
	//      └─ a5c5bd12 (upstream-commit)  "Rebuilt for Fedora 44"
	//         └─ 0eafb309  "Patched for GCC 16"
	//            └─ 070cdc17  "Rebuilt for Boost 1.90"
	//               └─ 6fe8d3d  ← shared with import's non-first-parent
	//
	// Test graph (simplified):
	//   branchTip  "Merge branch 'f44' into f43"  ← origin/f43
	//   ├─ import  "Merge branch 'rawhide' into f43"
	//   │  ├─ f43fix  "Fix CI gating"
	//   │  └─ shared  "upstream release 5.4"
	//   └─ f44tip  "upstream release 5.5"
	//      └─ upstream (upstream-commit)  "Rebuilt for Fedora 44"  ← HEAD
	//         └─ shared  "upstream release 5.4"
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// --- shared: "upstream release 5.4" (common ancestor) ---
	specFile, err := memFS.Create("systemtap.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: systemtap\nVersion: 5.4\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

	_, err = worktree.Add("systemtap.spec")
	require.NoError(t, err)

	sharedHash, err := worktree.Commit("upstream release 5.4", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Upstream",
			Email: "upstream@fedora.org",
			When:  time.Date(2025, 10, 31, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	sharedObj, err := repo.CommitObject(sharedHash)
	require.NoError(t, err)

	// --- f43fix: "Fix CI gating" (f43-only, child of shared) ---
	specFile, err = memFS.Create("systemtap.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: systemtap\nVersion: 5.4\n# CI fix\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

	_, err = worktree.Add("systemtap.spec")
	require.NoError(t, err)

	f43fixHash, err := worktree.Commit("Fix the CI gating setup", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Maintainer",
			Email: "maintainer@fedora.org",
			When:  time.Date(2025, 9, 22, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	f43fixObj, err := repo.CommitObject(f43fixHash)
	require.NoError(t, err)

	// --- import: "Merge branch 'rawhide' into f43" (parents: [f43fix, shared]) ---
	importAuthor := object.Signature{
		Name:  "Upstream",
		Email: "upstream@fedora.org",
		When:  time.Date(2025, 10, 31, 14, 0, 0, 0, time.UTC),
	}

	importMerge := &object.Commit{
		Author:       importAuthor,
		Committer:    importAuthor,
		Message:      "Merge branch 'rawhide' into f43",
		TreeHash:     f43fixObj.TreeHash,
		ParentHashes: []plumbing.Hash{f43fixHash, sharedHash},
	}

	importEncoded := repo.Storer.NewEncodedObject()
	err = importMerge.Encode(importEncoded)
	require.NoError(t, err)

	importHash, err := repo.Storer.SetEncodedObject(importEncoded)
	require.NoError(t, err)

	// --- f44 branch: upstream (child of shared, NOT of import) ---
	// "Rebuilt for Fedora 44" — this is the upstream-commit.
	upstreamAuthor := object.Signature{
		Name:  "RelEng",
		Email: "releng@fedora.org",
		When:  time.Date(2026, 1, 17, 0, 0, 0, 0, time.UTC),
	}

	upstreamObj := &object.Commit{
		Author:       upstreamAuthor,
		Committer:    upstreamAuthor,
		Message:      "Rebuilt for Fedora 44 Mass Rebuild",
		TreeHash:     sharedObj.TreeHash,
		ParentHashes: []plumbing.Hash{sharedHash},
	}

	upstreamEncoded := repo.Storer.NewEncodedObject()
	err = upstreamObj.Encode(upstreamEncoded)
	require.NoError(t, err)

	upstreamHash, err := repo.Storer.SetEncodedObject(upstreamEncoded)
	require.NoError(t, err)

	// --- branchTip: "Merge branch 'f44' into f43" (parents: [import, upstream]) ---
	tipAuthor := object.Signature{
		Name:  "Upstream",
		Email: "upstream@fedora.org",
		When:  time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}

	tipMerge := &object.Commit{
		Author:       tipAuthor,
		Committer:    tipAuthor,
		Message:      "Merge branch 'f44' into f43",
		TreeHash:     sharedObj.TreeHash,
		ParentHashes: []plumbing.Hash{importHash, upstreamHash},
	}

	tipEncoded := repo.Storer.NewEncodedObject()
	err = tipMerge.Encode(tipEncoded)
	require.NoError(t, err)

	tipHash, err := repo.Storer.SetEncodedObject(tipEncoded)
	require.NoError(t, err)

	// Set origin/f43 to the branch tip.
	err = repo.Storer.SetReference(plumbing.NewHashReference(
		plumbing.NewRemoteReferenceName("origin", "f43"), tipHash))
	require.NoError(t, err)

	// Detach HEAD at upstream-commit (simulates clone + checkout a5c5bd).
	head, err := repo.Storer.Reference(plumbing.HEAD)
	require.NoError(t, err)

	branchName := head.Target()
	err = repo.Storer.SetReference(plumbing.NewHashReference(branchName, upstreamHash))
	require.NoError(t, err)

	// Simulate overlay modification.
	specFile, err = memFS.Create("systemtap.spec")
	require.NoError(t, err)

	_, err = specFile.Write([]byte("Name: systemtap\nVersion: 5.4\n# overlay applied\n"))
	require.NoError(t, err)
	require.NoError(t, specFile.Close())

	changes := []sources.FingerprintChange{
		{
			CommitMetadata: sources.CommitMetadata{
				Hash:        "proj-systemtap",
				Author:      "Alice",
				AuthorEmail: "alice@example.com",
				Timestamp:   time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC).Unix(),
				Message:     "Update systemtap overlay",
			},
			UpstreamCommit: upstreamHash.String(),
		},
	}

	// This must NOT error — previously it failed with "import-commit not found".
	err = sources.CommitInterleavedHistory(repo, changes, importHash.String(), "f43")
	require.NoError(t, err, "must find import-commit even when upstream-commit "+
		"is on the non-first-parent side of import-commit's merge")

	// Verify the replayed history.
	newHead, err := repo.Head()
	require.NoError(t, err)

	var logCommits []*object.Commit

	currentHash := newHead.Hash()

	for {
		commitObj, err := repo.CommitObject(currentHash)
		require.NoError(t, err)

		logCommits = append(logCommits, commitObj)

		if len(commitObj.ParentHashes) == 0 {
			break
		}

		if commitObj.Hash == importHash {
			break
		}

		currentHash = commitObj.ParentHashes[0]
	}

	// Synthetic on top.
	assert.Contains(t, logCommits[0].Message, "Update systemtap overlay")

	// Import-commit at the bottom.
	assert.Contains(t, logCommits[len(logCommits)-1].Message,
		"Merge branch 'rawhide' into f43")

	// Upstream-commit must be present somewhere in the chain.
	foundUpstream := false

	for _, c := range logCommits {
		if c.Message == "Rebuilt for Fedora 44 Mass Rebuild" {
			foundUpstream = true

			break
		}
	}

	assert.True(t, foundUpstream,
		"upstream-commit must appear in the replayed history")

	// All replayed commits except import-commit should have 1 parent.
	for i := range len(logCommits) - 1 {
		assert.Len(t, logCommits[i].ParentHashes, 1,
			"commit %d (%s) should have exactly 1 parent", i, logCommits[i].Message)
	}

	// Verify overlay content on the synthetic commit.
	tr, err := logCommits[0].Tree()
	require.NoError(t, err)

	entry, err := tr.File("systemtap.spec")
	require.NoError(t, err)

	ct, err := entry.Contents()
	require.NoError(t, err)
	assert.Contains(t, ct, "# overlay applied")
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

func TestBuildDirtyChange(t *testing.T) {
	tests := []struct {
		name                  string
		currentFingerprint    string
		headLock              *lockfile.ComponentLock
		currentUpstreamCommit string
		wantNil               bool
		wantUpstream          string
	}{
		{
			name:                  "empty fingerprint disables detection",
			currentFingerprint:    "",
			headLock:              &lockfile.ComponentLock{InputFingerprint: "sha256:abc123", UpstreamCommit: "old"},
			currentUpstreamCommit: "new",
			wantNil:               true,
		},
		{
			name:                  "matching fingerprint returns nil",
			currentFingerprint:    "sha256:abc123",
			headLock:              &lockfile.ComponentLock{InputFingerprint: "sha256:abc123", UpstreamCommit: "old"},
			currentUpstreamCommit: "new",
			wantNil:               true,
		},
		{
			name:                  "nil headLock returns nil",
			currentFingerprint:    "sha256:abc123",
			headLock:              nil,
			currentUpstreamCommit: "new",
			wantNil:               true,
		},
		{
			name:                  "different fingerprint uses current upstream commit",
			currentFingerprint:    "sha256:new",
			headLock:              &lockfile.ComponentLock{InputFingerprint: "sha256:old", UpstreamCommit: "old-commit"},
			currentUpstreamCommit: "new-commit",
			wantUpstream:          "new-commit",
		},
		{
			name:                  "uses current upstream even when it matches head lock",
			currentFingerprint:    "sha256:new",
			headLock:              &lockfile.ComponentLock{InputFingerprint: "sha256:old", UpstreamCommit: "same-commit"},
			currentUpstreamCommit: "same-commit",
			wantUpstream:          "same-commit",
		},
		{
			name:                  "empty current upstream preserved for local components",
			currentFingerprint:    "sha256:new",
			headLock:              &lockfile.ComponentLock{InputFingerprint: "sha256:old", UpstreamCommit: ""},
			currentUpstreamCommit: "",
			wantUpstream:          "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := sources.BuildDirtyChange(test.currentFingerprint, test.headLock, test.currentUpstreamCommit)

			if test.wantNil {
				assert.Nil(t, result)

				return
			}

			require.NotNil(t, result)
			assert.Equal(t, "dirty", result.Hash)
			assert.Equal(t, "azldev", result.Author)
			assert.Equal(t, "azldev@local", result.AuthorEmail)
			assert.Equal(t, "Local changes (uncommitted)", result.Message)
			assert.Equal(t, test.wantUpstream, result.UpstreamCommit)
			assert.NotZero(t, result.Timestamp)
		})
	}
}
