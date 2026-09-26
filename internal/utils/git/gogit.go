// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package git

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
)

// OpenProjectRepo opens a go-git repository from a path within the project,
// detecting the .git directory and supporting linked worktrees. Use this for
// opening the project-config repo (not source checkouts, which may have
// different options).
func OpenProjectRepo(path string) (*gogit.Repository, error) {
	//nolint:wrapcheck // thin wrapper; callers add their own context.
	return gogit.PlainOpenWithOptions(path, &gogit.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
}

func RepoRelPath(repoRoot, absPath string) (string, error) {
	resolvedRoot, err := resolveSymlinks(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolving repository root %#q:\n%w", repoRoot, err)
	}

	resolvedPath, err := resolveSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolving path %#q:\n%w", absPath, err)
	}

	relPath, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return "", fmt.Errorf("computing relative path from %#q to %#q:\n%w", repoRoot, absPath, err)
	}

	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %#q escapes repository root %#q", absPath, repoRoot)
	}

	return relPath, nil
}

func resolveSymlinks(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("evaluating symlinks in %#q:\n%w", path, err)
	}

	parent := filepath.Dir(path)
	if parent == path {
		return path, nil
	}

	resolvedParent, err := resolveSymlinks(parent)
	if err != nil {
		return "", err
	}

	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}
