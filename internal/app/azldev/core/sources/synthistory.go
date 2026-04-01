// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// affectsRegexPattern is the regex pattern prefix used to match an "Affects:" trailer
// line in a commit message. Each line must contain exactly one component name.
const affectsRegexPattern = `(?m)^[ \t]*Affects:[ \t]*`

// CommitMetadata holds full metadata for a commit in the project repository.
type CommitMetadata struct {
	Hash        string
	Author      string
	AuthorEmail string
	Timestamp   int64
	Message     string
}

// MessageAffectsComponent reports whether a commit message contains an "Affects:"
// trailer line naming the given component.
func MessageAffectsComponent(message, componentName string) bool {
	re := regexp.MustCompile(affectsRegexPattern + regexp.QuoteMeta(componentName) + `[ \t]*$`)

	return re.MatchString(message)
}

// FindAffectsCommits walks the git log from HEAD and returns metadata for all commits
// whose message contains an "Affects: <componentName>" trailer line. Results are sorted
// chronologically (oldest first).
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

	err = commitIter.ForEach(func(commit *object.Commit) error {
		if MessageAffectsComponent(commit.Message, componentName) {
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
// rpmautospec release numbering.
func CommitSyntheticHistory(
	repo *gogit.Repository,
	commits []CommitMetadata,
) error {
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

		message := fmt.Sprintf("%s\n\nProject commit: %s",
			commitMeta.Message, commitMeta.Hash)

		_, err := worktree.Commit(message, &gogit.CommitOptions{
			AllowEmptyCommits: true,
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
// matching commit metadata sorted chronologically. If no Affects commits are found, a
// single default overlay commit is returned instead.
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

	projectRepo, err := openProjectRepo(configFilePath)
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

	if len(affectsCommits) == 0 {
		slog.Info("No commits with Affects marker found; "+
			"creating default commit",
			"component", componentName)

		return []CommitMetadata{
			defaultOverlayCommit(projectRepo, componentName),
		}, nil
	}

	return affectsCommits, nil
}

// defaultOverlayCommit returns a single [CommitMetadata] entry that represents a generic
// commit when no Affects commits exist in the project history. The commit hash is
// set to the current HEAD of the project repository.
func defaultOverlayCommit(repo *gogit.Repository, componentName string) CommitMetadata {
	var (
		timestamp int64
		hash      string
	)

	if head, err := repo.Head(); err == nil {
		hash = head.Hash().String()
		if commit, commitErr := repo.CommitObject(head.Hash()); commitErr == nil {
			timestamp = commit.Author.When.Unix()
		}
	}

	return CommitMetadata{
		Hash:      hash,
		Author:    "azldev",
		Timestamp: timestamp,
		Message:   "Latest state for " + componentName,
	}
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

// openProjectRepo finds and opens the git repository containing configFilePath by
// walking up the directory tree.
func openProjectRepo(configFilePath string) (*gogit.Repository, error) {
	repo, err := gogit.PlainOpenWithOptions(filepath.Dir(configFilePath), &gogit.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to find project repository for config file %#q:\n%w",
			configFilePath, err)
	}

	return repo, nil
}

// unixToTime converts a Unix timestamp to a [time.Time] in UTC.
func unixToTime(unix int64) time.Time {
	return time.Unix(unix, 0).UTC()
}
