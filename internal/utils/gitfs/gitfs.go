// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package gitfs provides a read-only [afero.Fs] backed by a git tree at a
// specific commit. It lets code that already speaks [afero.Fs] (such as the
// project-config loader) read files as they existed at an arbitrary point in
// history, without checking anything out to disk.
//
// Paths are interpreted relative to the root of the git tree. Both absolute
// paths (e.g. "/base/comps/x.toml") and io/fs-style relative paths (e.g.
// "base/comps/x.toml") are accepted and normalized identically: the leading
// slash is stripped and the path is cleaned. This mirrors how an
// [afero.OsFs] rooted at the tree root would behave, which is what callers
// like the config loader (which pass absolute paths) and the doublestar glob
// adapter (which passes relative paths) expect.
//
// All mutating operations return an error: the filesystem is strictly
// read-only. To support callers that need to write scratch files (e.g. the
// loader copying embedded default configs to a temp dir), layer a writable
// filesystem on top with [afero.NewCopyOnWriteFs].
package gitfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/spf13/afero"
)

// errReadOnly is returned by all mutating operations.
var errReadOnly = errors.New("gitfs: read-only filesystem")

// Fs is a read-only [afero.Fs] backed by a git tree.
type Fs struct {
	repo *gogit.Repository
	tree *object.Tree
}

// Compile-time assurance that Fs implements afero.Fs.
var _ afero.Fs = (*Fs)(nil)

// NewFromCommit creates a read-only filesystem exposing the tree of the given
// commit.
func NewFromCommit(repo *gogit.Repository, commitHash plumbing.Hash) (*Fs, error) {
	commit, err := repo.CommitObject(commitHash)
	if err != nil {
		return nil, fmt.Errorf("gitfs: resolve commit %s:\n%w", commitHash, err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("gitfs: read tree for commit %s:\n%w", commitHash, err)
	}

	return &Fs{repo: repo, tree: tree}, nil
}

// normalize converts an incoming afero path (absolute or relative) to a
// clean, slash-separated, tree-relative path. The tree root is "".
func normalize(name string) string {
	cleaned := path.Clean("/" + filepath.ToSlash(name))

	// Strip the leading slash; the root becomes "".
	return cleaned[1:]
}

// notExist builds a PathError that reports as "does not exist" so that helpers
// like afero.Exists and os.IsNotExist behave correctly.
func notExist(op, name string) error {
	return &os.PathError{Op: op, Path: name, Err: os.ErrNotExist}
}

// Open opens the named file or directory for reading.
func (f *Fs) Open(name string) (afero.File, error) {
	rel := normalize(name)

	// Root of the tree is always a directory.
	if rel == "" {
		return newDirFile(f, name, "", f.tree), nil
	}

	entry, err := f.tree.FindEntry(rel)
	if err != nil {
		return nil, notExist("open", name)
	}

	if entry.Mode == filemode.Dir {
		subtree, subErr := f.tree.Tree(rel)
		if subErr != nil {
			return nil, notExist("open", name)
		}

		return newDirFile(f, name, rel, subtree), nil
	}

	content, err := f.blobContents(entry.Hash)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}

	return newRegularFile(name, entry, content), nil
}

// OpenFile opens the named file for reading. Any flag requesting write access
// is rejected.
func (f *Fs) OpenFile(name string, flag int, _ os.FileMode) (afero.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_APPEND|os.O_TRUNC) != 0 {
		return nil, &os.PathError{Op: "open", Path: name, Err: errReadOnly}
	}

	return f.Open(name)
}

// Stat returns file info for the named file or directory.
func (f *Fs) Stat(name string) (os.FileInfo, error) {
	rel := normalize(name)

	if rel == "" {
		return &fileInfo{name: ".", isDir: true, mode: os.ModeDir | fileperms.ReadOnlyExec}, nil
	}

	entry, err := f.tree.FindEntry(rel)
	if err != nil {
		return nil, notExist("stat", name)
	}

	return f.entryInfo(path.Base(rel), entry)
}

// Name identifies this filesystem implementation.
func (f *Fs) Name() string { return "gitfs" }

// blobContents reads the full contents of a blob.
func (f *Fs) blobContents(hash plumbing.Hash) ([]byte, error) {
	blob, err := f.repo.BlobObject(hash)
	if err != nil {
		return nil, fmt.Errorf("read blob %s:\n%w", hash, err)
	}

	reader, err := blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("open blob %s:\n%w", hash, err)
	}

	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read blob %s:\n%w", hash, err)
	}

	return content, nil
}

// entryInfo builds a FileInfo for a tree entry, fetching the blob size for
// regular files.
func (f *Fs) entryInfo(name string, entry *object.TreeEntry) (os.FileInfo, error) {
	if entry.Mode == filemode.Dir {
		return &fileInfo{name: name, isDir: true, mode: os.ModeDir | fileperms.ReadOnlyExec}, nil
	}

	var size int64

	if blob, err := f.repo.BlobObject(entry.Hash); err == nil {
		size = blob.Size
	}

	return &fileInfo{name: name, size: size, mode: entryFileMode(entry.Mode)}, nil
}

// entryFileMode maps a git filemode to an os.FileMode for non-directory entries.
func entryFileMode(mode filemode.FileMode) os.FileMode {
	switch mode {
	case filemode.Executable:
		return fileperms.ReadOnlyExec
	case filemode.Symlink:
		return os.ModeSymlink | fileperms.ReadOnlyExec
	case filemode.Empty, filemode.Dir, filemode.Regular, filemode.Deprecated, filemode.Submodule:
		return fileperms.ReadOnlyFile
	default:
		return fileperms.ReadOnlyFile
	}
}

//
// Mutating operations — all unsupported.
//

func (f *Fs) Create(name string) (afero.File, error) {
	return nil, &os.PathError{Op: "create", Path: name, Err: errReadOnly}
}

func (f *Fs) Mkdir(name string, _ os.FileMode) error {
	return &os.PathError{Op: "mkdir", Path: name, Err: errReadOnly}
}

func (f *Fs) MkdirAll(path string, _ os.FileMode) error {
	return &os.PathError{Op: "mkdir", Path: path, Err: errReadOnly}
}

func (f *Fs) Remove(name string) error {
	return &os.PathError{Op: "remove", Path: name, Err: errReadOnly}
}

func (f *Fs) RemoveAll(path string) error {
	return &os.PathError{Op: "removeall", Path: path, Err: errReadOnly}
}

func (f *Fs) Rename(oldname, _ string) error {
	return &os.PathError{Op: "rename", Path: oldname, Err: errReadOnly}
}

func (f *Fs) Chmod(name string, _ os.FileMode) error {
	return &os.PathError{Op: "chmod", Path: name, Err: errReadOnly}
}

func (f *Fs) Chown(name string, _, _ int) error {
	return &os.PathError{Op: "chown", Path: name, Err: errReadOnly}
}

func (f *Fs) Chtimes(name string, _, _ time.Time) error {
	return &os.PathError{Op: "chtimes", Path: name, Err: errReadOnly}
}

//
// fileInfo
//

type fileInfo struct {
	name  string
	size  int64
	mode  os.FileMode
	isDir bool
}

func (i *fileInfo) Name() string       { return i.name }
func (i *fileInfo) Size() int64        { return i.size }
func (i *fileInfo) Mode() os.FileMode  { return i.mode }
func (i *fileInfo) ModTime() time.Time { return time.Time{} }
func (i *fileInfo) IsDir() bool        { return i.isDir }
func (i *fileInfo) Sys() any           { return nil }

//
// regularFile — a read-only view over blob contents.
//

type regularFile struct {
	name   string
	info   os.FileInfo
	reader *bytes.Reader
}

var _ afero.File = (*regularFile)(nil)

func newRegularFile(name string, entry *object.TreeEntry, content []byte) *regularFile {
	return &regularFile{
		name: name,
		info: &fileInfo{
			name: path.Base(normalize(name)),
			size: int64(len(content)),
			mode: entryFileMode(entry.Mode),
		},
		reader: bytes.NewReader(content),
	}
}

func (f *regularFile) Close() error               { return nil }
func (f *regularFile) Name() string               { return f.name }
func (f *regularFile) Stat() (os.FileInfo, error) { return f.info, nil }
func (f *regularFile) Sync() error                { return nil }

func (f *regularFile) Read(p []byte) (int, error) {
	n, err := f.reader.Read(p)

	return n, err //nolint:wrapcheck // pass through bytes.Reader io semantics (incl. io.EOF) unchanged
}

func (f *regularFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := f.reader.ReadAt(p, off)

	return n, err //nolint:wrapcheck // pass through bytes.Reader io semantics unchanged
}

func (f *regularFile) Seek(off int64, whence int) (int64, error) {
	pos, err := f.reader.Seek(off, whence)

	return pos, err //nolint:wrapcheck // pass through bytes.Reader io semantics unchanged
}

func (f *regularFile) Readdir(int) ([]os.FileInfo, error) {
	return nil, &os.PathError{Op: "readdir", Path: f.name, Err: errors.New("not a directory")}
}

func (f *regularFile) Readdirnames(int) ([]string, error) {
	return nil, &os.PathError{Op: "readdir", Path: f.name, Err: errors.New("not a directory")}
}

func (f *regularFile) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: f.name, Err: errReadOnly}
}

func (f *regularFile) WriteAt([]byte, int64) (int, error) {
	return 0, &os.PathError{Op: "write", Path: f.name, Err: errReadOnly}
}

func (f *regularFile) WriteString(string) (int, error) {
	return 0, &os.PathError{Op: "write", Path: f.name, Err: errReadOnly}
}

func (f *regularFile) Truncate(int64) error {
	return &os.PathError{Op: "truncate", Path: f.name, Err: errReadOnly}
}

//
// dirFile — a read-only view over a tree's immediate entries.
//

type dirFile struct {
	fs      *Fs
	name    string
	relPath string
	tree    *object.Tree
	offset  int
}

var _ afero.File = (*dirFile)(nil)

func newDirFile(fs *Fs, name, relPath string, tree *object.Tree) *dirFile {
	return &dirFile{fs: fs, name: name, relPath: relPath, tree: tree}
}

func (d *dirFile) Close() error                      { return nil }
func (d *dirFile) Read([]byte) (int, error)          { return 0, io.EOF }
func (d *dirFile) ReadAt([]byte, int64) (int, error) { return 0, io.EOF }
func (d *dirFile) Seek(int64, int) (int64, error)    { return 0, nil }
func (d *dirFile) Name() string                      { return d.name }
func (d *dirFile) Sync() error                       { return nil }

func (d *dirFile) Stat() (os.FileInfo, error) {
	base := "."
	if d.relPath != "" {
		base = path.Base(d.relPath)
	}

	return &fileInfo{name: base, isDir: true, mode: os.ModeDir | fileperms.ReadOnlyExec}, nil
}

// Readdir returns the immediate children of the directory, sorted by name.
// It honors the offset/count semantics of os.File.Readdir.
func (d *dirFile) Readdir(count int) ([]os.FileInfo, error) {
	entries := d.tree.Entries

	infos := make([]os.FileInfo, 0, len(entries))

	for i := range entries {
		entry := entries[i]

		info, err := d.fs.entryInfo(entry.Name, &entry)
		if err != nil {
			return nil, err
		}

		infos = append(infos, info)
	}

	sort.Slice(infos, func(a, b int) bool { return infos[a].Name() < infos[b].Name() })

	if d.offset >= len(infos) {
		if count > 0 {
			return nil, io.EOF
		}

		return []os.FileInfo{}, nil
	}

	infos = infos[d.offset:]

	if count > 0 && count < len(infos) {
		infos = infos[:count]
	}

	d.offset += len(infos)

	return infos, nil
}

func (d *dirFile) Readdirnames(n int) ([]string, error) {
	infos, err := d.Readdir(n)
	if err != nil {
		return nil, err
	}

	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name()
	}

	return names, nil
}

func (d *dirFile) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: d.name, Err: errReadOnly}
}

func (d *dirFile) WriteAt([]byte, int64) (int, error) {
	return 0, &os.PathError{Op: "write", Path: d.name, Err: errReadOnly}
}

func (d *dirFile) WriteString(string) (int, error) {
	return 0, &os.PathError{Op: "write", Path: d.name, Err: errReadOnly}
}

func (d *dirFile) Truncate(int64) error {
	return &os.PathError{Op: "truncate", Path: d.name, Err: errReadOnly}
}
