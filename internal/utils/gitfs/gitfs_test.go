// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package gitfs_test

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/gitfs"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile creates/overwrites a file in the in-memory worktree.
func writeFile(t *testing.T, fs billy.Filesystem, relPath, content string) {
	t.Helper()

	file, err := fs.Create(relPath)
	require.NoError(t, err)

	_, err = file.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

// commitAll stages everything and commits, returning the commit hash.
func commitAll(t *testing.T, repo *gogit.Repository, msg string) plumbing.Hash {
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

// newTestRepo builds an in-memory repo with a tiny project tree and returns the
// repo plus the single commit's hash.
func newTestRepo(t *testing.T) (*gogit.Repository, plumbing.Hash) {
	t.Helper()

	bfs := memfs.New()

	repo, err := gogit.Init(memory.NewStorage(), bfs)
	require.NoError(t, err)

	writeFile(t, bfs, "azldev.toml", "includes = [\"comps/**/*.toml\"]\n")
	writeFile(t, bfs, "comps/foo.toml", "name = \"foo\"\n")
	writeFile(t, bfs, "comps/sub/bar.toml", "name = \"bar\"\n")

	return repo, commitAll(t, repo, "init")
}

func TestOpenAndReadFile(t *testing.T) {
	repo, hash := newTestRepo(t)

	fs, err := gitfs.NewFromCommit(repo, hash)
	require.NoError(t, err)

	for _, name := range []string{"comps/foo.toml", "/comps/foo.toml", "./comps/foo.toml"} {
		file, openErr := fs.Open(name)
		require.NoError(t, openErr, "open %q", name)

		content, readErr := io.ReadAll(file)
		require.NoError(t, readErr)
		require.NoError(t, file.Close())

		assert.Equal(t, "name = \"foo\"\n", string(content), "content via %q", name)
	}
}

func TestStat(t *testing.T) {
	repo, hash := newTestRepo(t)

	gitFS, err := gitfs.NewFromCommit(repo, hash)
	require.NoError(t, err)

	fileInfo, err := gitFS.Stat("/comps/foo.toml")
	require.NoError(t, err)
	assert.False(t, fileInfo.IsDir())
	assert.Equal(t, int64(len("name = \"foo\"\n")), fileInfo.Size())

	dirInfo, err := gitFS.Stat("/comps")
	require.NoError(t, err)
	assert.True(t, dirInfo.IsDir())

	rootInfo, err := gitFS.Stat("/")
	require.NoError(t, err)
	assert.True(t, rootInfo.IsDir())
}

func TestStatMissing(t *testing.T) {
	repo, hash := newTestRepo(t)

	gitFS, err := gitfs.NewFromCommit(repo, hash)
	require.NoError(t, err)

	_, err = gitFS.Stat("/nope.toml")
	assert.True(t, os.IsNotExist(err), "expected not-exist, got %v", err)

	exists, err := afero.Exists(gitFS, "/nope.toml")
	require.NoError(t, err)
	assert.False(t, exists)

	exists, err = afero.Exists(gitFS, "/comps/foo.toml")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestReaddir(t *testing.T) {
	repo, hash := newTestRepo(t)

	fs, err := gitfs.NewFromCommit(repo, hash)
	require.NoError(t, err)

	dir, err := fs.Open("/comps")
	require.NoError(t, err)

	infos, err := dir.Readdir(-1)
	require.NoError(t, err)
	require.NoError(t, dir.Close())

	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name()
	}

	assert.ElementsMatch(t, []string{"foo.toml", "sub"}, names)
}

// TestGlobThroughDoublestar is the load-bearing test: it proves the config
// loader's include-resolution path (fileutils.Glob → afero.IOFS → doublestar)
// works against the git-backed filesystem with an absolute pattern, including
// the writable CopyOnWriteFs overlay the loader needs for scratch writes.
func TestGlobThroughDoublestar(t *testing.T) {
	repo, hash := newTestRepo(t)

	base, err := gitfs.NewFromCommit(repo, hash)
	require.NoError(t, err)

	fs := afero.NewCopyOnWriteFs(base, afero.NewMemMapFs())

	matches, err := fileutils.Glob(fs, "/comps/**/*.toml",
		doublestar.WithFailOnIOErrors(), doublestar.WithFilesOnly())
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"/comps/foo.toml", "/comps/sub/bar.toml"}, matches)
}

func TestReadOnly(t *testing.T) {
	repo, hash := newTestRepo(t)

	gitFS, err := gitfs.NewFromCommit(repo, hash)
	require.NoError(t, err)

	_, err = gitFS.Create("/x")
	require.Error(t, err)

	require.Error(t, gitFS.Mkdir("/d", 0o755))
	require.Error(t, gitFS.Remove("/comps/foo.toml"))
}

// storeBlob writes a blob object directly to the repo store and returns its hash.
func storeBlob(t *testing.T, repo *gogit.Repository, content string) plumbing.Hash {
	t.Helper()

	obj := repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)

	w, err := obj.Writer()
	require.NoError(t, err)

	_, err = w.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	hash, err := repo.Storer.SetEncodedObject(obj)
	require.NoError(t, err)

	return hash
}

// newRepoWithSubmodule builds a repo whose root tree contains a gitlink
// (submodule) entry alongside a regular file, and returns the commit hash. The
// submodule entry's hash points at a commit that does not exist as a blob in
// this repo, mirroring a real gitlink.
func newRepoWithSubmodule(t *testing.T) (*gogit.Repository, plumbing.Hash) {
	t.Helper()

	repo, err := gogit.Init(memory.NewStorage(), memfs.New())
	require.NoError(t, err)

	blobHash := storeBlob(t, repo, "name = \"foo\"\n")
	submoduleHash := plumbing.NewHash("0123456789abcdef0123456789abcdef01234567")

	tree := &object.Tree{Entries: []object.TreeEntry{
		{Name: "azldev.toml", Mode: filemode.Regular, Hash: blobHash},
		{Name: "sub", Mode: filemode.Submodule, Hash: submoduleHash},
	}}

	treeObj := repo.Storer.NewEncodedObject()
	require.NoError(t, tree.Encode(treeObj))

	treeHash, err := repo.Storer.SetEncodedObject(treeObj)
	require.NoError(t, err)

	commit := &object.Commit{
		Author:    object.Signature{Name: "t", Email: "t@t.com", When: time.Now()},
		Committer: object.Signature{Name: "t", Email: "t@t.com", When: time.Now()},
		Message:   "with submodule",
		TreeHash:  treeHash,
	}

	commitObj := repo.Storer.NewEncodedObject()
	require.NoError(t, commit.Encode(commitObj))

	commitHash, err := repo.Storer.SetEncodedObject(commitObj)
	require.NoError(t, err)

	return repo, commitHash
}

// TestSubmoduleEntry verifies that submodule (gitlink) entries are handled
// explicitly: Open reports a clear, stable submodule error instead of a
// confusing "read blob" failure, and Stat/Readdir classify the entry as
// non-regular without silently falling back to a zero-size blob.
func TestSubmoduleEntry(t *testing.T) {
	repo, hash := newRepoWithSubmodule(t)

	fs, err := gitfs.NewFromCommit(repo, hash)
	require.NoError(t, err)

	// Open must fail with the stable submodule sentinel, not a blob-read error.
	_, err = fs.Open("sub")
	require.Error(t, err)
	assert.ErrorIs(t, err, gitfs.ErrSubmodule)
	assert.NotContains(t, err.Error(), "read blob")

	// Stat must succeed and classify the gitlink as non-regular (no silent
	// zero-size blob fallback).
	info, err := fs.Stat("sub")
	require.NoError(t, err)
	assert.False(t, info.Mode().IsRegular(), "submodule entry must not look like a regular file")

	// Readdir must still list the submodule alongside the regular file.
	root, err := fs.Open("/")
	require.NoError(t, err)

	names, err := root.Readdirnames(-1)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"azldev.toml", "sub"}, names)
}

// TestSymlinkEntry verifies that symlink entries are not silently read as file
// content (git stores the link target as the blob body). Open must report a
// clear, stable symlink error rather than handing the target-path string to the
// caller, and Stat must classify the entry as a symlink.
func TestSymlinkEntry(t *testing.T) {
	repo, hash := commitTreeWithSymlink(t)

	fs, err := gitfs.NewFromCommit(repo, hash)
	require.NoError(t, err)

	_, err = fs.Open("link")
	require.Error(t, err)
	assert.ErrorIs(t, err, gitfs.ErrSymlink)
	assert.NotContains(t, err.Error(), "azldev.toml", "must not leak the link target as content")

	info, err := fs.Stat("link")
	require.NoError(t, err)
	assert.False(t, info.Mode().IsRegular(), "symlink must not look like a regular file")
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "symlink mode bit must be set")
}

// commitTreeWithSymlink builds a repo whose root tree contains a symlink entry
// (blob body = target path) next to the regular file it points at.
func commitTreeWithSymlink(t *testing.T) (*gogit.Repository, plumbing.Hash) {
	t.Helper()

	repo, err := gogit.Init(memory.NewStorage(), memfs.New())
	require.NoError(t, err)

	targetBlob := storeBlob(t, repo, "name = \"foo\"\n")
	linkBlob := storeBlob(t, repo, "azldev.toml") // symlink body is the target path

	entries := []object.TreeEntry{
		{Name: "azldev.toml", Mode: filemode.Regular, Hash: targetBlob},
		{Name: "link", Mode: filemode.Symlink, Hash: linkBlob},
	}

	tree := &object.Tree{Entries: entries}

	treeObj := repo.Storer.NewEncodedObject()
	require.NoError(t, tree.Encode(treeObj))

	treeHash, err := repo.Storer.SetEncodedObject(treeObj)
	require.NoError(t, err)

	commit := &object.Commit{
		Author:    object.Signature{Name: "t", Email: "t@t.com", When: time.Now()},
		Committer: object.Signature{Name: "t", Email: "t@t.com", When: time.Now()},
		Message:   "with symlink",
		TreeHash:  treeHash,
	}

	commitObj := repo.Storer.NewEncodedObject()
	require.NoError(t, commit.Encode(commitObj))

	commitHash, err := repo.Storer.SetEncodedObject(commitObj)
	require.NoError(t, err)

	return repo, commitHash
}

// TestDirSeekResetsOffset verifies that seeking a directory handle back to the
// start lets a subsequent Readdir return the full listing again, rather than an
// empty result because the internal offset was never reset.
func TestDirSeekResetsOffset(t *testing.T) {
	repo, hash := newTestRepo(t)

	fs, err := gitfs.NewFromCommit(repo, hash)
	require.NoError(t, err)

	dir, err := fs.Open("/comps")
	require.NoError(t, err)

	first, err := dir.Readdir(-1)
	require.NoError(t, err)
	require.NotEmpty(t, first)

	_, err = dir.Seek(0, io.SeekStart)
	require.NoError(t, err)

	again, err := dir.Readdir(-1)
	require.NoError(t, err)
	assert.Len(t, again, len(first), "rewound directory must list all entries again")
}

// TestReadOnlyErrorIsExported verifies callers can identify read-only failures
// via errors.Is against the exported sentinel.
func TestReadOnlyErrorIsExported(t *testing.T) {
	repo, hash := newTestRepo(t)

	fs, err := gitfs.NewFromCommit(repo, hash)
	require.NoError(t, err)

	_, err = fs.Create("/x")
	require.Error(t, err)
	assert.ErrorIs(t, err, gitfs.ErrReadOnly)
}
