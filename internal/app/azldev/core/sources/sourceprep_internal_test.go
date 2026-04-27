// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoveSubmoduleEntries_StripsGitlinks(t *testing.T) {
	const repoDir = "/fakerepo"

	memFS := afero.NewMemMapFs()
	storer := memory.NewStorage()

	// Initialize a repo with in-memory storage and a real working tree path.
	repo, err := gogit.Init(storer, nil)
	require.NoError(t, err)

	// Create an initial commit so HEAD exists.
	_, err = repo.CommitObject(plumbing.ZeroHash)
	require.Error(t, err) // expected — no commits yet

	// Manually build an index with a normal file entry and a submodule entry.
	idx := &index.Index{
		Version: 2,
		Entries: []*index.Entry{
			{
				Name: "regular-file.spec",
				Mode: filemode.Regular,
				Hash: plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			},
			{
				Name: "tests/at",
				Mode: filemode.Submodule,
				Hash: plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			},
		},
	}

	require.NoError(t, storer.SetIndex(idx))

	// Create the empty directory that the bogus submodule leaves behind.
	submoduleDir := filepath.Join(repoDir, "tests/at")
	require.NoError(t, memFS.MkdirAll(submoduleDir, 0o755))

	// Verify the directory exists before calling removeSubmoduleEntries.
	dirExists, err := fileutils.Exists(memFS, submoduleDir)
	require.NoError(t, err)
	require.True(t, dirExists, "submodule directory should exist before removal")

	// Act
	err = removeSubmoduleEntries(memFS, repo, repoDir)
	require.NoError(t, err)

	// Assert: index should only have the regular file.
	updatedIdx, err := storer.Index()
	require.NoError(t, err)
	require.Len(t, updatedIdx.Entries, 1)
	assert.Equal(t, "regular-file.spec", updatedIdx.Entries[0].Name)
	assert.Equal(t, filemode.Regular, updatedIdx.Entries[0].Mode)

	// Assert: empty directory was removed.
	dirExists, err = fileutils.Exists(memFS, submoduleDir)
	require.NoError(t, err)
	assert.False(t, dirExists, "submodule directory should be removed")
}

func TestRemoveSubmoduleEntries_NoOpWithoutSubmodules(t *testing.T) {
	const repoDir = "/fakerepo"

	memFS := afero.NewMemMapFs()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, nil)
	require.NoError(t, err)

	// Index with only normal entries.
	idx := &index.Index{
		Version: 2,
		Entries: []*index.Entry{
			{
				Name: "file-a.spec",
				Mode: filemode.Regular,
				Hash: plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			},
			{
				Name: "file-b.patch",
				Mode: filemode.Regular,
				Hash: plumbing.NewHash("cccccccccccccccccccccccccccccccccccccccc"),
			},
		},
	}

	require.NoError(t, storer.SetIndex(idx))

	err = removeSubmoduleEntries(memFS, repo, repoDir)
	require.NoError(t, err)

	// Index should be untouched.
	updatedIdx, err := storer.Index()
	require.NoError(t, err)
	require.Len(t, updatedIdx.Entries, 2)
}

func TestRemoveSubmoduleEntries_PreservesNormalEntriesWithMixedModes(t *testing.T) {
	const repoDir = "/fakerepo"

	memFS := afero.NewMemMapFs()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, nil)
	require.NoError(t, err)

	// Mix of regular files, executable, and submodule entries.
	idx := &index.Index{
		Version: 2,
		Entries: []*index.Entry{
			{
				Name: "build.sh",
				Mode: filemode.Executable,
				Hash: plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			},
			{
				Name: "tests/submod1",
				Mode: filemode.Submodule,
				Hash: plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			},
			{
				Name: "pkg.spec",
				Mode: filemode.Regular,
				Hash: plumbing.NewHash("cccccccccccccccccccccccccccccccccccccccc"),
			},
			{
				Name: "tests/submod2",
				Mode: filemode.Submodule,
				Hash: plumbing.NewHash("dddddddddddddddddddddddddddddddddddddd"),
			},
		},
	}

	require.NoError(t, storer.SetIndex(idx))

	// Create empty dirs for both submodules.
	require.NoError(t, memFS.MkdirAll(filepath.Join(repoDir, "tests/submod1"), 0o755))
	require.NoError(t, memFS.MkdirAll(filepath.Join(repoDir, "tests/submod2"), 0o755))

	err = removeSubmoduleEntries(memFS, repo, repoDir)
	require.NoError(t, err)

	updatedIdx, err := storer.Index()
	require.NoError(t, err)
	require.Len(t, updatedIdx.Entries, 2)
	assert.Equal(t, "build.sh", updatedIdx.Entries[0].Name)
	assert.Equal(t, "pkg.spec", updatedIdx.Entries[1].Name)
}
