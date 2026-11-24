// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

// ErrPathNotFound reports that a path is absent from a git tree, or does not
// have the requested object type.
var ErrPathNotFound = errors.New("path not found in git tree")

const (
	objectTypeBlob = "blob"
	objectTypeTree = "tree"
)

// Repo reads committed objects from a local repository by running the git
// program installed on the system.
type Repo struct {
	cmdFactory opctx.CmdFactory
	root       string
}

// Tree is a git tree object in a [Repo].
type Tree struct {
	repo *Repo
	hash string
}

// treeEntry is one entry from 'git ls-tree' output.
type treeEntry struct {
	objectType string
	hash       string
	path       string
}

// OpenRepo opens the repository whose worktree contains path.
func OpenRepo(ctx context.Context, cmdFactory opctx.CmdFactory, path string) (*Repo, error) {
	root, err := RunInDir(ctx, cmdFactory, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("finding git worktree root for %#q:\n%w", path, err)
	}

	if root == "" {
		return nil, fmt.Errorf("no git worktree root found for %#q", path)
	}

	return &Repo{cmdFactory: cmdFactory, root: root}, nil
}

// Root returns the repository's worktree root directory.
func (r *Repo) Root() string {
	return r.root
}

// ResolveCommit resolves rev to a full commit hash.
func (r *Repo) ResolveCommit(ctx context.Context, rev string) (string, error) {
	return r.revParse(ctx, rev+"^{commit}")
}

// Head returns the commit hash at HEAD, or "" when HEAD is an unborn branch.
func (r *Repo) Head(ctx context.Context) (string, error) {
	hash, err := RunInDir(ctx, r.cmdFactory, r.root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")

	// '--verify --quiet' exits 1 without output when HEAD has no commit.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("resolving HEAD:\n%w", err)
	}

	return hash, nil
}

// Tree returns the root tree of commit.
func (r *Repo) Tree(ctx context.Context, commit string) (*Tree, error) {
	hash, err := r.revParse(ctx, commit+"^{tree}")
	if err != nil {
		return nil, err
	}

	return &Tree{repo: r, hash: hash}, nil
}

func (r *Repo) revParse(ctx context.Context, rev string) (string, error) {
	hash, err := RunInDir(ctx, r.cmdFactory, r.root, "rev-parse", "--verify", "--end-of-options", rev)
	if err != nil {
		return "", fmt.Errorf("resolving %#q:\n%w", rev, err)
	}

	if hash == "" {
		return "", fmt.Errorf("resolving %#q: no object found", rev)
	}

	return hash, nil
}

// Hash returns the tree object hash.
func (t *Tree) Hash() string {
	return t.hash
}

// Subtree returns the tree at the slash-separated relative path. It returns
// [ErrPathNotFound] when the path is absent or is not a directory.
func (t *Tree) Subtree(ctx context.Context, relPath string) (*Tree, error) {
	if cleanTreePath(relPath) == "" {
		return t, nil
	}

	entry, err := t.entry(ctx, relPath, objectTypeTree)
	if err != nil {
		return nil, err
	}

	return &Tree{repo: t.repo, hash: entry.hash}, nil
}

// ReadFile returns the contents of the file at the slash-separated relative
// path; a symlink's contents are its target. It returns [ErrPathNotFound]
// when the path is absent or is not a file.
func (t *Tree) ReadFile(ctx context.Context, relPath string) ([]byte, error) {
	entry, err := t.entry(ctx, relPath, objectTypeBlob)
	if err != nil {
		return nil, err
	}

	content, err := runInDirRaw(ctx, t.repo.cmdFactory, t.repo.root, nil, "cat-file", objectTypeBlob, entry.hash)
	if err != nil {
		return nil, fmt.Errorf("reading %#q from git tree:\n%w", relPath, err)
	}

	return []byte(content), nil
}

// ForEachFile calls visit with the slash-separated path and contents of each
// file in the tree, recursively. Symlinks are files whose contents are their
// targets; submodule entries are skipped, as are paths for which skip (when
// non-nil) returns true.
func (t *Tree) ForEachFile(
	ctx context.Context, skip func(relPath string) bool, visit func(relPath string, content []byte) error,
) error {
	listing, err := runInDirRaw(ctx, t.repo.cmdFactory, t.repo.root, nil, "ls-tree", "-r", "-z", "--full-tree", t.hash)
	if err != nil {
		return fmt.Errorf("listing git tree %#q:\n%w", t.hash, err)
	}

	entries, err := parseTreeEntries(listing)
	if err != nil {
		return err
	}

	files := make([]treeEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.objectType == objectTypeBlob && (skip == nil || !skip(entry.path)) {
			files = append(files, entry)
		}
	}

	if len(files) == 0 {
		return nil
	}

	var request strings.Builder
	for _, file := range files {
		request.WriteString(file.hash + "\n")
	}

	output, err := runInDirRaw(ctx, t.repo.cmdFactory, t.repo.root, strings.NewReader(request.String()),
		"cat-file", "--batch")
	if err != nil {
		return fmt.Errorf("reading files from git tree %#q:\n%w", t.hash, err)
	}

	for _, file := range files {
		var content []byte

		content, output, err = nextBatchObject(output, file.hash)
		if err != nil {
			return fmt.Errorf("reading %#q from git tree:\n%w", file.path, err)
		}

		if err := visit(file.path, content); err != nil {
			return err
		}
	}

	return nil
}

// entry returns the entry at relPath, which must have objectType.
func (t *Tree) entry(ctx context.Context, relPath, objectType string) (treeEntry, error) {
	cleaned := cleanTreePath(relPath)
	if cleaned == "" {
		return treeEntry{}, fmt.Errorf("%w: %#q", ErrPathNotFound, relPath)
	}

	listing, err := runInDirRaw(ctx, t.repo.cmdFactory, t.repo.root, nil,
		"--literal-pathspecs", "ls-tree", "-z", "--full-tree", t.hash, "--", cleaned)
	if err != nil {
		return treeEntry{}, fmt.Errorf("looking up %#q in git tree %#q:\n%w", relPath, t.hash, err)
	}

	entries, err := parseTreeEntries(listing)
	if err != nil {
		return treeEntry{}, err
	}

	for _, entry := range entries {
		if entry.path == cleaned && entry.objectType == objectType {
			return entry, nil
		}
	}

	return treeEntry{}, fmt.Errorf("%w: %#q", ErrPathNotFound, relPath)
}

// cleanTreePath returns the cleaned slash-separated relative path, or "" for
// the tree root.
func cleanTreePath(relPath string) string {
	cleaned := strings.TrimPrefix(path.Clean("/"+relPath), "/")

	return cleaned
}

// parseTreeEntries parses 'git ls-tree -z' output.
func parseTreeEntries(listing string) ([]treeEntry, error) {
	records := strings.Split(listing, "\x00")
	entries := make([]treeEntry, 0, len(records))

	for _, record := range records {
		if record == "" {
			continue
		}

		info, entryPath, found := strings.Cut(record, "\t")
		fields := strings.Fields(info)

		if !found || len(fields) != 3 {
			return nil, fmt.Errorf("parsing git tree entry %#q", record)
		}

		entries = append(entries, treeEntry{objectType: fields[1], hash: fields[2], path: entryPath})
	}

	return entries, nil
}

// nextBatchObject splits the next object, which must be hash, from
// 'git cat-file --batch' output.
func nextBatchObject(output, hash string) (content []byte, rest string, err error) {
	header, rest, found := strings.Cut(output, "\n")
	if !found {
		return nil, "", fmt.Errorf("truncated output for object %#q", hash)
	}

	fields := strings.Fields(header)

	if len(fields) != 3 || fields[0] != hash {
		return nil, "", fmt.Errorf("unexpected object header %#q for %#q", header, hash)
	}

	size, err := strconv.Atoi(fields[2])
	if err != nil || size < 0 || len(rest) < size+1 || rest[size] != '\n' {
		return nil, "", fmt.Errorf("invalid object data for %#q", hash)
	}

	return []byte(rest[:size]), rest[size+1:], nil
}
