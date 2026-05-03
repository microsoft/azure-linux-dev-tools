// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package git

import (
	gogit "github.com/go-git/go-git/v5"
)

// OpenProjectRepo opens a go-git repository from a path within the project,
// detecting the .git directory and supporting linked worktrees. All go-git
// repo opens in the codebase should use this to ensure consistent options.
func OpenProjectRepo(path string) (*gogit.Repository, error) {
	//nolint:wrapcheck // thin wrapper; callers add their own context.
	return gogit.PlainOpenWithOptions(path, &gogit.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
}
