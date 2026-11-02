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

func TestCommitInterleavedHistory_EquivalentUpstreamBranch(t *testing.T) {
	tests := []struct {
		name             string
		changedFile      string
		message          string
		changeAuthor     bool
		changeTimestamp  bool
		duplicate        bool
		preserveExpected bool
	}{
		{name: "identical packaging", preserveExpected: true},
		{name: "different ignored tarballs", changedFile: ".gitignore", preserveExpected: true},
		{name: "different spec", changedFile: "package.spec"},
		{name: "different source checksum", changedFile: "sources"},
		{name: "different patch", changedFile: "fix.patch"},
		{name: "different attributes", changedFile: ".gitattributes"},
		{name: "different message", message: "Another upstream change"},
		{name: "different author", changeAuthor: true},
		{name: "different author timestamp", changeTimestamp: true},
		{name: "ambiguous equivalents", duplicate: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			memFS := memfs.New()
			repo, err := gogit.Init(memory.NewStorage(), memFS)
			require.NoError(t, err)
			worktree, err := repo.Worktree()
			require.NoError(t, err)

			write := func(name, content string) {
				t.Helper()

				file, err := memFS.Create(name)
				require.NoError(t, err)
				_, err = file.Write([]byte(content))
				require.NoError(t, err)
				require.NoError(t, file.Close())

				_, err = worktree.Add(name)
				require.NoError(t, err)
			}
			author := object.Signature{
				Name: "Upstream", Email: "upstream@example.com",
				When: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			}
			commit := func(message string, signature object.Signature, parents []plumbing.Hash) plumbing.Hash {
				t.Helper()

				hash, err := worktree.Commit(message, &gogit.CommitOptions{
					Author: &signature, Committer: &signature,
					Parents: parents, AllowEmptyCommits: true,
				})
				require.NoError(t, err)

				return hash
			}

			write("package.spec", "Name: package\nVersion: 0.9\nRelease: %autorelease\n")

			initial := commit("Initial import", author, nil)

			write("package.spec", "Name: package\nVersion: 1.0\nRelease: %autorelease\n")
			write("sources", "original source checksum\n")
			write("fix.patch", "original patch\n")
			write(".gitattributes", "original attributes\n")
			write(".gitignore", "package-1.0.tar.gz\n")

			original := commit("Update to 1.0", author, []plumbing.Hash{initial})

			if testCase.changedFile != "" {
				write(testCase.changedFile, "different branch contents\n")
			}

			message := "Update to 1.0"
			if testCase.message != "" {
				message = testCase.message
			}

			equivalentAuthor := author
			if testCase.changeAuthor {
				equivalentAuthor.Email = "other@example.com"
			}

			if testCase.changeTimestamp {
				equivalentAuthor.When = equivalentAuthor.When.Add(time.Hour)
			}
			// A different parent makes the upstream commit a distinct branch
			// object even when its metadata and packaging tree are identical.
			write("branch-point", "another branch\n")

			branchPoint := commit("Branch point", author, []plumbing.Hash{initial})
			_, err = worktree.Remove("branch-point")
			require.NoError(t, err)

			equivalent := commit(message, equivalentAuthor, []plumbing.Hash{branchPoint})
			if testCase.duplicate {
				equivalent = commit(message, equivalentAuthor, []plumbing.Hash{equivalent})
			}

			write("package.spec", "Name: package\nVersion: 2.0\nRelease: %autorelease\n")

			latest := commit("Update to 2.0", author, []plumbing.Hash{equivalent})

			write("package.spec", "Name: package\nVersion: 2.0\nRelease: %autorelease\n# overlays\n")

			changes := []sources.FingerprintChange{
				{
					CommitMetadata: sources.CommitMetadata{
						Hash: "project-old", Author: "Dan", AuthorEmail: "dan@example.com",
						Timestamp: author.When.Add(time.Hour).Unix(), Message: "Historical distro change",
					},
					UpstreamCommit: original.String(),
				},
				{
					CommitMetadata: sources.CommitMetadata{
						Hash: "project-new", Author: "New author", AuthorEmail: "new@example.com",
						Timestamp: author.When.Add(2 * time.Hour).Unix(), Message: "Current upgrade",
					},
					UpstreamCommit: latest.String(),
				},
			}
			require.NoError(t, sources.CommitInterleavedHistory(repo, changes, initial.String()))
			assert.Equal(t, original.String(), changes[0].UpstreamCommit, "input history must not be mutated")

			head, err := repo.Head()
			require.NoError(t, err)
			iter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
			require.NoError(t, err)

			var history []*object.Commit
			require.NoError(t, iter.ForEach(func(c *object.Commit) error {
				history = append(history, c)

				return nil
			}))
			assert.Contains(t, history[0].Message, "Current upgrade")
			currentSpec, err := history[0].File("package.spec")
			require.NoError(t, err)
			content, err := currentSpec.Contents()
			require.NoError(t, err)
			assert.Contains(t, content, "Version: 2.0")
			assert.Contains(t, content, "# overlays")

			var historical []*object.Commit

			for _, c := range history {
				if c.Author.Name == "Dan" {
					historical = append(historical, c)
				}
			}

			if !testCase.preserveExpected {
				assert.Empty(t, historical)

				return
			}

			require.Len(t, historical, 1)
			assert.Equal(t, "dan@example.com", historical[0].Author.Email)
			assert.Equal(t, changes[0].Timestamp, historical[0].Author.When.Unix())
			assert.Contains(t, historical[0].Message, "Historical distro change")
			oldSpec, err := historical[0].File("package.spec")
			require.NoError(t, err)
			content, err = oldSpec.Contents()
			require.NoError(t, err)
			assert.Equal(t, "Name: package\nVersion: 1.0\nRelease: %autorelease\n", content)
		})
	}
}
