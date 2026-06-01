// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeWorktreeFile(t *testing.T, fs billy.Filesystem, content string) {
	t.Helper()

	file, err := fs.Create("azldev.toml")
	require.NoError(t, err)

	_, err = file.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

func commitWorktree(t *testing.T, repo *gogit.Repository, msg string) plumbing.Hash {
	t.Helper()

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, worktree.AddGlob("."))

	hash, err := worktree.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t.com", When: time.Now()},
	})
	require.NoError(t, err)

	return hash
}

// TestLoadProjectConfigAtCommit verifies that a component's overlays defined in
// azldev.toml are recovered when loading the project config as of a historical
// commit, reading purely from the git tree (no checkout).
func TestLoadProjectConfigAtCommit(t *testing.T) {
	bfs := memfs.New()

	repo, err := gogit.Init(memory.NewStorage(), bfs)
	require.NoError(t, err)

	writeWorktreeFile(t, bfs, `
[components.foo]
[[components.foo.overlays]]
type = "spec-search-replace"
regex = "1\\.0\\.0"
replacement = "2.0.0"
`)

	hash := commitWorktree(t, repo, "add foo overlay")

	projectDir, config, err := projectconfig.LoadProjectConfigAtCommit(repo, hash, "/", false)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "/", projectDir)

	comp, ok := config.Components["foo"]
	require.True(t, ok, "component foo should be present")
	require.Len(t, comp.Overlays, 1)
	assert.Equal(t, projectconfig.ComponentOverlaySearchAndReplaceInSpec, comp.Overlays[0].Type)
	assert.Equal(t, "2.0.0", comp.Overlays[0].Replacement)
}

// TestResolveComponentOverlaysAtCommit verifies that overlays inherited from a
// component group default are merged with the component's own overlays when
// resolving historically.
func TestResolveComponentOverlaysAtCommit(t *testing.T) {
	bfs := memfs.New()

	repo, err := gogit.Init(memory.NewStorage(), bfs)
	require.NoError(t, err)

	writeWorktreeFile(t, bfs, `
[component-groups.shared]
components = ["foo"]
[[component-groups.shared.default-component-config.overlays]]
type = "spec-search-replace"
regex = "from-group"
replacement = "group-applied"

[components.foo]
[[components.foo.overlays]]
type = "spec-search-replace"
regex = "from-comp"
replacement = "comp-applied"
`)

	hash := commitWorktree(t, repo, "add group + component overlays")

	overlays, err := projectconfig.ResolveComponentOverlaysAtCommit(repo, hash, "/", "foo", false)
	require.NoError(t, err)
	require.Len(t, overlays, 2)

	replacements := []string{overlays[0].Replacement, overlays[1].Replacement}
	assert.Contains(t, replacements, "group-applied")
	assert.Contains(t, replacements, "comp-applied")
}

// TestResolveComponentOverlaysAtCommit_MissingComponent verifies that a request
// for a component absent at the commit returns nil overlays without error.
func TestResolveComponentOverlaysAtCommit_MissingComponent(t *testing.T) {
	bfs := memfs.New()

	repo, err := gogit.Init(memory.NewStorage(), bfs)
	require.NoError(t, err)

	writeWorktreeFile(t, bfs, "[components.foo]\n")

	hash := commitWorktree(t, repo, "add foo")

	overlays, err := projectconfig.ResolveComponentOverlaysAtCommit(repo, hash, "/", "absent", false)
	require.NoError(t, err)
	assert.Nil(t, overlays)
}

// TestResolveComponentOverlaysAtCommit_TracksHistory verifies that resolving
// overlays at an OLDER commit returns the overlay value as it existed at THAT
// commit — not the latest value. This is the core guarantee historical overlay
// replay relies on: each synthetic commit must see the version it actually
// carried at that point in history. If resolution leaked HEAD's config, every
// historic entry would show the current version.
func TestResolveComponentOverlaysAtCommit_TracksHistory(t *testing.T) {
	bfs := memfs.New()

	repo, err := gogit.Init(memory.NewStorage(), bfs)
	require.NoError(t, err)

	// Commit A: overlay replacement is 2.0.0.
	writeWorktreeFile(t, bfs, `
[components.foo]
[[components.foo.overlays]]
type = "spec-search-replace"
regex = "VERSION"
replacement = "2.0.0"
`)
	hashA := commitWorktree(t, repo, "foo -> 2.0.0")

	// Commit B: same overlay, replacement bumped to 3.0.0.
	writeWorktreeFile(t, bfs, `
[components.foo]
[[components.foo.overlays]]
type = "spec-search-replace"
regex = "VERSION"
replacement = "3.0.0"
`)
	hashB := commitWorktree(t, repo, "foo -> 3.0.0")

	overlaysA, err := projectconfig.ResolveComponentOverlaysAtCommit(repo, hashA, "/", "foo", false)
	require.NoError(t, err)
	require.Len(t, overlaysA, 1)
	assert.Equal(t, "2.0.0", overlaysA[0].Replacement, "commit A must resolve its own (older) overlay value")

	overlaysB, err := projectconfig.ResolveComponentOverlaysAtCommit(repo, hashB, "/", "foo", false)
	require.NoError(t, err)
	require.Len(t, overlaysB, 1)
	assert.Equal(t, "3.0.0", overlaysB[0].Replacement, "commit B must resolve its own (newer) overlay value")
}

// TestResolveComponentOverlaysAtCommit_PermissiveToleratesUndefinedRef verifies
// that with permissive parsing enabled, a config whose component group references
// an undefined component still loads, so the target component's overlays can be
// recovered. Historical commits may legitimately reference components that were
// only defined in a later revision; a strict load would fail the entire resolve
// and mis-attribute the version for that commit.
func TestResolveComponentOverlaysAtCommit_PermissiveToleratesUndefinedRef(t *testing.T) {
	bfs := memfs.New()

	repo, err := gogit.Init(memory.NewStorage(), bfs)
	require.NoError(t, err)

	// "shared" group references "not-yet-defined", which has no [components] entry.
	writeWorktreeFile(t, bfs, `
[component-groups.shared]
components = ["foo", "not-yet-defined"]

[components.foo]
[[components.foo.overlays]]
type = "spec-search-replace"
regex = "VERSION"
replacement = "2.0.0"
`)
	hash := commitWorktree(t, repo, "foo defined, dangling group ref")

	// Strict load fails on the undefined component reference.
	_, err = projectconfig.ResolveComponentOverlaysAtCommit(repo, hash, "/", "foo", false)
	require.Error(t, err)

	// Permissive load tolerates it and still returns foo's overlays.
	overlays, err := projectconfig.ResolveComponentOverlaysAtCommit(repo, hash, "/", "foo", true)
	require.NoError(t, err)
	require.Len(t, overlays, 1)
	assert.Equal(t, "2.0.0", overlays[0].Replacement)
}
