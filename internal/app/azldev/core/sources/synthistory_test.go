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
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestRepo creates an in-memory git repository with a single file committed.
// Returns the repo, the commit hash, and the billy filesystem.
func createTestRepo(t *testing.T, fileName, fileContent, commitMsg string) (*gogit.Repository, plumbing.Hash) {
	t.Helper()

	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Create the file.
	file, err := memFS.Create(fileName)
	require.NoError(t, err)

	_, err = file.Write([]byte(fileContent))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	_, err = worktree.Add(fileName)
	require.NoError(t, err)

	hash, err := worktree.Commit(commitMsg, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test Author",
			Email: "test@example.com",
			When:  time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	return repo, hash
}

func TestBlameFile(t *testing.T) {
	const (
		fileName    = "config.toml"
		fileContent = "[project]\ndescription = \"test\"\n"
		commitMsg   = "initial commit"
	)

	repo, commitHash := createTestRepo(t, fileName, fileContent, commitMsg)

	result, err := sources.BlameFile(repo, fileName)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Len(t, result.Entries, 2, "should have one entry per line")
	assert.Equal(t, commitHash.String(), result.Entries[0].CommitHash)
	assert.Equal(t, "Test Author", result.Entries[0].Author)
	assert.Equal(t, 1, result.Entries[0].Line)
	assert.Equal(t, "[project]", result.Entries[0].Content)
	assert.Equal(t, 2, result.Entries[1].Line)
	assert.Contains(t, result.Entries[1].Content, "description")
}

func TestBlameFile_NonexistentFile(t *testing.T) {
	repo, _ := createTestRepo(t, "other.toml", "content", "init")

	result, err := sources.BlameFile(repo, "missing.toml")
	require.Error(t, err)
	assert.Nil(t, result)
}

func TestFindOverlayLineRanges(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		componentName string
		expected      []struct {
			startLine int
			index     int
		}
	}{
		{
			name: "single overlay",
			content: `[components.curl]
spec = { type = "upstream" }

[[components.curl.overlays]]
type = "patch-add"
source = "patches/fix.patch"
`,
			componentName: "curl",
			expected: []struct {
				startLine int
				index     int
			}{
				{startLine: 4, index: 0},
			},
		},
		{
			name: "multiple overlays",
			content: `[components.curl]
spec = { type = "upstream" }

[[components.curl.overlays]]
type = "patch-add"
source = "patches/fix.patch"

[[components.curl.overlays]]
type = "spec-set-tag"
tag = "Release"
value = "2%{?dist}"
`,
			componentName: "curl",
			expected: []struct {
				startLine int
				index     int
			}{
				{startLine: 4, index: 0},
				{startLine: 8, index: 1},
			},
		},
		{
			name: "no overlays for component",
			content: `[components.curl]
spec = { type = "upstream" }
`,
			componentName: "curl",
			expected:      nil,
		},
		{
			name: "wrong component name",
			content: `[[components.wget.overlays]]
type = "patch-add"
`,
			componentName: "curl",
			expected:      nil,
		},
		{
			name: "mixed components",
			content: `[[components.curl.overlays]]
type = "patch-add"
source = "a.patch"

[[components.wget.overlays]]
type = "patch-add"
source = "b.patch"

[[components.curl.overlays]]
type = "spec-set-tag"
tag = "Release"
`,
			componentName: "curl",
			expected: []struct {
				startLine int
				index     int
			}{
				{startLine: 1, index: 0},
				{startLine: 9, index: 1},
			},
		},
		{
			name: "quoted component name",
			content: `[[components."my-pkg".overlays]]
type = "patch-add"
source = "fix.patch"
`,
			componentName: "my-pkg",
			expected: []struct {
				startLine int
				index     int
			}{
				{startLine: 1, index: 0},
			},
		},
		{
			name: "inline array single entry",
			content: `[components.shim]
spec = { type = "upstream" }
overlays = [
    { type = "spec-search-replace", regex = 'foo', replacement = "bar" },
]
`,
			componentName: "shim",
			expected: []struct {
				startLine int
				index     int
			}{
				{startLine: 4, index: 0},
			},
		},
		{
			name: "inline array multiple entries",
			content: `[components.shim]
spec = { type = "upstream" }
overlays = [
    { type = "spec-search-replace", regex = 'foo', replacement = "bar" },
    { type = "spec-append-lines", section = "%prep", lines = ["echo hello"] },
    { type = "patch-add", source = "patches/fix.patch" },
]
`,
			componentName: "shim",
			expected: []struct {
				startLine int
				index     int
			}{
				{startLine: 4, index: 0},
				{startLine: 5, index: 1},
				{startLine: 6, index: 2},
			},
		},
		{
			name: "inline array multiline entries",
			content: `[components.shim]
overlays = [
    {
        type = "spec-search-replace",
        regex = 'foo',
        replacement = "bar",
    },
    {
        type = "patch-add",
        source = "fix.patch",
    },
]
`,
			componentName: "shim",
			expected: []struct {
				startLine int
				index     int
			}{
				{startLine: 3, index: 0},
				{startLine: 8, index: 1},
			},
		},
		{
			name: "inline array wrong component",
			content: `[components.curl]
overlays = [
    { type = "patch-add", source = "fix.patch" },
]
`,
			componentName: "shim",
			expected:      nil,
		},
		{
			name: "inline array with no overlays key",
			content: `[components.shim]
spec = { type = "upstream" }
`,
			componentName: "shim",
			expected:      nil,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ranges := sources.FindOverlayLineRanges(testCase.content, testCase.componentName)

			if testCase.expected == nil {
				assert.Empty(t, ranges)

				return
			}

			require.Len(t, ranges, len(testCase.expected))

			for i, exp := range testCase.expected {
				assert.Equal(t, exp.startLine, ranges[i].StartLine, "range %d startLine", i)
				assert.Equal(t, exp.index, ranges[i].Index, "range %d index", i)
			}
		})
	}
}

func TestMapOverlaysToCommits(t *testing.T) {
	// Create an in-memory repo with two commits to different sections of a TOML file.
	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// First commit: add the component and first overlay.
	firstContent := `[components.curl]
spec = { type = "upstream" }

[[components.curl.overlays]]
type = "patch-add"
source = "fix.patch"
`
	file, err := memFS.Create("azldev.toml")
	require.NoError(t, err)

	_, err = file.Write([]byte(firstContent))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	_, err = worktree.Add("azldev.toml")
	require.NoError(t, err)

	firstHash, err := worktree.Commit("Add curl with first overlay", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Alice",
			Email: "alice@example.com",
			When:  time.Date(2025, 1, 10, 10, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	// Second commit: add a second overlay.
	secondContent := `[components.curl]
spec = { type = "upstream" }

[[components.curl.overlays]]
type = "patch-add"
source = "fix.patch"

[[components.curl.overlays]]
type = "spec-set-tag"
tag = "Release"
value = "2%{?dist}"
`
	file, err = memFS.Create("azldev.toml")
	require.NoError(t, err)

	_, err = file.Write([]byte(secondContent))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	_, err = worktree.Add("azldev.toml")
	require.NoError(t, err)

	secondHash, err := worktree.Commit("Add second overlay to curl", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Bob",
			Email: "bob@example.com",
			When:  time.Date(2025, 2, 20, 14, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	// Now blame and map.
	blame, err := sources.BlameFile(repo, "azldev.toml")
	require.NoError(t, err)

	lineRanges := sources.FindOverlayLineRanges(secondContent, "curl")
	require.Len(t, lineRanges, 2)

	overlays := []projectconfig.ComponentOverlay{
		{Type: projectconfig.ComponentOverlayAddPatch, Source: "fix.patch"},
		{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Release", Value: "2%{?dist}"},
	}

	groups, err := sources.MapOverlaysToCommits(repo, overlays, lineRanges, blame)
	require.NoError(t, err)

	// Expect two groups: one from Alice (first overlay), one from Bob (second overlay).
	// Groups should be sorted chronologically (Alice first).
	require.Len(t, groups, 2)

	assert.Equal(t, firstHash.String(), groups[0].Commit.Hash)
	assert.Equal(t, "Alice", groups[0].Commit.Author)
	assert.Equal(t, "alice@example.com", groups[0].Commit.AuthorEmail)
	assert.Len(t, groups[0].Overlays, 1)
	assert.Equal(t, projectconfig.ComponentOverlayAddPatch, groups[0].Overlays[0].Type)

	assert.Equal(t, secondHash.String(), groups[1].Commit.Hash)
	assert.Equal(t, "Bob", groups[1].Commit.Author)
	assert.Equal(t, "bob@example.com", groups[1].Commit.AuthorEmail)
	assert.Len(t, groups[1].Overlays, 1)
	assert.Equal(t, projectconfig.ComponentOverlaySetSpecTag, groups[1].Overlays[0].Type)
}

func TestMapOverlaysToCommits_MismatchedCounts(t *testing.T) {
	repo, _ := createTestRepo(t, "config.toml", "content", "init")

	overlays := []projectconfig.ComponentOverlay{
		{Type: projectconfig.ComponentOverlayAddPatch},
		{Type: projectconfig.ComponentOverlaySetSpecTag},
	}

	// Only one line range for two overlays.
	lineRanges := []sources.OverlayLineRange{
		{StartLine: 1, EndLine: 3, Index: 0},
	}

	_, err := sources.MapOverlaysToCommits(repo, overlays, lineRanges, &sources.ConfigBlameResult{})
	require.Error(t, err)
	assert.ErrorIs(t, err, sources.ErrLineRangeOverlayMismatch)
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
	repo, _ := createTestRepo(t, "file.txt", "content", "init")
	err := sources.CommitSyntheticHistory(repo, nil, nil)
	assert.ErrorIs(t, err, sources.ErrNoOverlaysToCommit)
}
