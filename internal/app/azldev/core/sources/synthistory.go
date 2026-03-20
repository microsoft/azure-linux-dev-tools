// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// affectsRegexPattern is the regex pattern prefix used to match an [AffectsPrefix] marker
// line in a commit message. It is combined with a quoted component name to form the full
// match expression.
const affectsRegexPattern = `(?m)^\s*Affects:\s*`

var (
	// ErrNoGitRepository is returned when no enclosing git repository can be found.
	ErrNoGitRepository = errors.New("no git repository found")

	// ErrNoOverlaysToCommit is returned when there are no synthetic commits to create.
	ErrNoOverlaysToCommit = errors.New("no synthetic commits to create")
)

// IsRepoDirty reports whether the given go-git repository has staged changes
// in its index. Unstaged modifications and untracked files are intentionally
// ignored so the developer must explicitly stage changes to trigger an extra
// synthetic commit.
func IsRepoDirty(repo *gogit.Repository) (bool, error) {
	worktree, err := repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("failed to get worktree:\n%w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return false, fmt.Errorf("failed to get worktree status:\n%w", err)
	}

	for _, fileStatus := range status {
		if fileStatus.Staging != gogit.Unmodified && fileStatus.Staging != gogit.Untracked {
			return true, nil
		}
	}

	return false, nil
}

// CommitMetadata holds full metadata for a commit in the project repository.
type CommitMetadata struct {
	Hash        string
	Author      string
	AuthorEmail string
	Timestamp   int64
	Message     string
}

// FindAffectsCommits walks the git log from HEAD and returns metadata for all commits
// whose message contains "Affects: <componentName>". Results are sorted chronologically
// (oldest first).
func FindAffectsCommits(repo *gogit.Repository, componentName string) ([]CommitMetadata, error) {
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD reference:\n%w", err)
	}

	commitIter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate commit log:\n%w", err)
	}

	var matches []CommitMetadata

	re := regexp.MustCompile(affectsRegexPattern + regexp.QuoteMeta(componentName) + `\b`)

	err = commitIter.ForEach(func(commit *object.Commit) error {
		if re.MatchString(commit.Message) {
			matches = append(matches, CommitMetadata{
				Hash:        commit.Hash.String(),
				Author:      commit.Author.Name,
				AuthorEmail: commit.Author.Email,
				Timestamp:   commit.Author.When.Unix(),
				Message:     strings.TrimSpace(commit.Message),
			})
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk commit log:\n%w", err)
	}

	// Log iteration returns newest-first; reverse to get chronological order.
	slices.Reverse(matches)

	return matches, nil
}

// CommitSyntheticHistory stages all pending working tree changes and creates synthetic
// commits in the provided git repository. The first commit captures all file changes;
// subsequent commits are created as empty commits to preserve the commit count for
// rpmautospec release numbering. Overlay application must happen before calling this
// function — it only handles the git history.
func CommitSyntheticHistory(
	repo *gogit.Repository,
	commits []CommitMetadata,
) error {
	if len(commits) == 0 {
		return ErrNoOverlaysToCommit
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree:\n%w", err)
	}

	// Stage all working tree changes once — overlays have already been applied.
	if err := worktree.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		return fmt.Errorf("failed to stage changes:\n%w", err)
	}

	for commitIdx, commitMeta := range commits {
		slog.Info("Creating synthetic commit",
			"commit", commitIdx+1,
			"total", len(commits),
			"projectHash", commitMeta.Hash,
		)

		message := fmt.Sprintf("[azldev] %s\n\nProject commit: %s",
			commitMeta.Message, commitMeta.Hash)

		_, err := worktree.Commit(message, &gogit.CommitOptions{
			AllowEmptyCommits: commitIdx > 0,
			Author: &object.Signature{
				Name:  commitMeta.Author,
				Email: commitMeta.AuthorEmail,
				When:  unixToTime(commitMeta.Timestamp),
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create synthetic commit %d:\n%w", commitIdx+1, err)
		}
	}

	slog.Info("Synthetic history generation complete",
		"commitsCreated", len(commits))

	return nil
}

// buildSyntheticCommits resolves the project repository from the component's config file,
// walks the git log for commits containing "Affects: <componentName>", and returns the
// matching commit metadata sorted chronologically.
//
// Returns nil when no matching commits are found.
func buildSyntheticCommits(
	config *projectconfig.ComponentConfig, componentName string,
) ([]CommitMetadata, error) {
	configFilePath, err := resolveConfigFilePath(config, componentName)
	if err != nil {
		// No config file reference means this component can't have Affects commits.
		slog.Debug("Cannot resolve config file for synthetic commits; skipping",
			"component", componentName, "error", err)

		return nil, nil
	}

	projectRepo, _, err := openProjectRepo(configFilePath)
	if err != nil {
		return nil, err
	}

	affectsCommits, err := FindAffectsCommits(projectRepo, componentName)
	if err != nil {
		return nil, fmt.Errorf("failed to find Affects commits for component %#q:\n%w", componentName, err)
	}

	slog.Info("Found commits affecting component",
		"component", componentName,
		"commitCount", len(affectsCommits))

	commits := make([]CommitMetadata, 0, len(affectsCommits))

	// Create one synthetic commit per Affects commit, preserving each commit's
	// original message and author attribution in the upstream history.
	commits = append(commits, affectsCommits...)

	if len(commits) == 0 {
		slog.Warn("No commits with Affects marker found; "+
			"falling back to standard overlay processing",
			"component", componentName)

		return nil, nil
	}

	return commits, nil
}

// resolveConfigFilePath extracts and validates the source config file path from the component config.
func resolveConfigFilePath(config *projectconfig.ComponentConfig, componentName string) (string, error) {
	configFile := config.SourceConfigFile
	if configFile == nil {
		return "", fmt.Errorf("component %#q has no source config file reference", componentName)
	}

	configFilePath := configFile.SourcePath()
	if configFilePath == "" {
		return "", fmt.Errorf("component %#q source config file has no path", componentName)
	}

	return configFilePath, nil
}

// openProjectRepo finds the git repository root containing configFilePath, opens it, and
// returns the repository handle along with the config file path relative to the repo root.
func openProjectRepo(configFilePath string) (*gogit.Repository, string, error) {
	projectRepoPath, err := findRepoRoot(filepath.Dir(configFilePath))
	if err != nil {
		return nil, "", fmt.Errorf("failed to find project repository for config file %#q:\n%w",
			configFilePath, err)
	}

	projectRepo, err := gogit.PlainOpen(projectRepoPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open project repository at %#q:\n%w", projectRepoPath, err)
	}

	relConfigPath, err := filepath.Rel(projectRepoPath, configFilePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to compute relative config path:\n%w", err)
	}

	return projectRepo, relConfigPath, nil
}

// findRepoRoot walks up the directory tree from startDir to find a directory containing
// a .git directory or file (for worktrees).
func findRepoRoot(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path for %#q:\n%w", startDir, err)
	}

	for {
		gitPath := filepath.Join(dir, ".git")

		if info, statErr := os.Stat(gitPath); statErr == nil {
			// Accept both .git directories and .git files (for git worktrees).
			if info.IsDir() || info.Mode().IsRegular() {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%w: searched from %#q to filesystem root", ErrNoGitRepository, startDir)
		}

		dir = parent
	}
}

// unixToTime converts a Unix timestamp to a [time.Time] in UTC.
func unixToTime(unix int64) time.Time {
	return time.Unix(unix, 0).UTC()
}
