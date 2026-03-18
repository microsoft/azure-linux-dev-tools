// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

var (
	// ErrNoGitRepository is returned when no enclosing git repository can be found.
	ErrNoGitRepository = errors.New("no git repository found")

	// ErrNoOverlaysToCommit is returned when there are no overlay groups to commit.
	ErrNoOverlaysToCommit = errors.New("no overlays to commit")
)

// IsRepoDirty reports whether the given go-git repository has uncommitted changes
// (modified, added, deleted, or untracked files) in its working tree.
func IsRepoDirty(repo *gogit.Repository) (bool, error) {
	worktree, err := repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("failed to get worktree:\n%w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return false, fmt.Errorf("failed to get worktree status:\n%w", err)
	}

	return !status.IsClean(), nil
}

// CommitMetadata holds full metadata for a commit in the project repository.
type CommitMetadata struct {
	Hash        string
	Author      string
	AuthorEmail string
	Timestamp   int64
	Message     string
}

// OverlayCommitGroup groups overlays that originate from the same git commit in the project
// configuration repository. During synthetic history generation, all overlays in a group are
// applied together and recorded as a single commit.
type OverlayCommitGroup struct {
	// Commit holds metadata from the originating commit in the project repository.
	Commit CommitMetadata
	// Overlays contains the overlay definitions to apply as part of this synthetic commit.
	Overlays []projectconfig.ComponentOverlay
}

// OverlayApplyFunc is a callback that applies a batch of overlays to the component sources.
// It is called once per [OverlayCommitGroup] during synthetic history generation.
type OverlayApplyFunc func(overlays []projectconfig.ComponentOverlay) error

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

	marker := "Affects: " + componentName

	var matches []CommitMetadata

	err = commitIter.ForEach(func(commit *object.Commit) error {
		if strings.Contains(commit.Message, marker) {
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

// CommitSyntheticHistory creates synthetic commits in the provided git repository, one per
// [OverlayCommitGroup]. For each group the applyFn callback is invoked to mutate the working
// tree, then all changes are staged and committed with the group's metadata.
func CommitSyntheticHistory(
	repo *gogit.Repository,
	groups []OverlayCommitGroup,
	applyFn OverlayApplyFunc,
) error {
	if len(groups) == 0 {
		return ErrNoOverlaysToCommit
	}

	if applyFn == nil {
		return errors.New("applyFn callback is required")
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree:\n%w", err)
	}

	for groupIdx, group := range groups {
		slog.Info("Creating synthetic commit",
			"commit", groupIdx+1,
			"total", len(groups),
			"originalHash", group.Commit.Hash,
			"overlayCount", len(group.Overlays),
		)

		// Apply the overlay batch to the working tree.
		if err := applyFn(group.Overlays); err != nil {
			return fmt.Errorf("failed to apply overlays for synthetic commit %d (original %s):\n%w",
				groupIdx+1, group.Commit.Hash, err)
		}

		// Stage all changes (modified, added, and deleted files).
		if err := worktree.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
			return fmt.Errorf("failed to stage changes for synthetic commit %d:\n%w", groupIdx+1, err)
		}

		// Create the synthetic commit preserving author attribution from the project repo.
		message := fmt.Sprintf("[azldev] %s\n\nOriginal commit: %s",
			group.Commit.Message, group.Commit.Hash)

		_, err := worktree.Commit(message, &gogit.CommitOptions{
			Author: &object.Signature{
				Name:  group.Commit.Author,
				Email: group.Commit.AuthorEmail,
				When:  unixToTime(group.Commit.Timestamp),
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create synthetic commit %d:\n%w", groupIdx+1, err)
		}
	}

	slog.Info("Synthetic history generation complete",
		"commitsCreated", len(groups))

	return nil
}

// buildOverlayGroups resolves the project repository from the component's config file, walks
// the git log for commits containing "Affects: <componentName>", and produces a single
// [OverlayCommitGroup] that applies all overlays under the most recent matching commit's
// attribution. Returns nil groups when no matching commits are found.
func buildOverlayGroups(
	config *projectconfig.ComponentConfig, componentName string,
) ([]OverlayCommitGroup, error) {
	configFilePath, err := resolveConfigFilePath(config, componentName)
	if err != nil {
		return nil, err
	}

	projectRepo, _, err := openProjectRepo(configFilePath)
	if err != nil {
		return nil, err
	}

	affectsCommits, err := FindAffectsCommits(projectRepo, componentName)
	if err != nil {
		return nil, fmt.Errorf("failed to find Affects commits for component %#q:\n%w", componentName, err)
	}

	if len(affectsCommits) == 0 {
		slog.Warn("No commits with Affects marker found for component; "+
			"falling back to standard overlay processing",
			"component", componentName)

		return nil, nil
	}

	// Use the most recent Affects commit for the synthetic commit's author attribution.
	latestCommit := affectsCommits[len(affectsCommits)-1]

	// Build a commit message listing all matching commits for traceability.
	var hashList strings.Builder

	for _, c := range affectsCommits {
		fmt.Fprintf(&hashList, "\n- %s %s", c.Hash, firstLine(c.Message))
	}

	syntheticMeta := CommitMetadata{
		Hash:        latestCommit.Hash,
		Author:      latestCommit.Author,
		AuthorEmail: latestCommit.AuthorEmail,
		Timestamp:   latestCommit.Timestamp,
		Message: fmt.Sprintf("Apply overlays for %s\n\nDerived from %d project commit(s):%s",
			componentName, len(affectsCommits), hashList.String()),
	}

	groups := []OverlayCommitGroup{
		{
			Commit:   syntheticMeta,
			Overlays: config.Overlays,
		},
	}

	// When the project repo has uncommitted changes the developer is iterating
	// locally. Append an extra overlay commit so rpmautospec sees a new commit
	// and assigns a fresh release number instead of colliding with the last build.
	dirty, err := IsRepoDirty(projectRepo)
	if err != nil {
		slog.Warn("Could not determine project repo dirty state; skipping local-changes commit",
			"error", err)
	} else if dirty {
		slog.Info("Project repo has uncommitted changes; adding local-changes synthetic commit",
			"component", componentName)

		groups = append(groups, OverlayCommitGroup{
			Commit: CommitMetadata{
				Hash:        "local",
				Author:      "local",
				AuthorEmail: "local@dev",
				Timestamp:   time.Now().Unix(),
				Message:     "Local uncommitted changes for " + componentName,
			},
			Overlays: config.Overlays,
		})
	}

	return groups, nil
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

// firstLine returns the first line of s (the subject line of a commit message).
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}

	return s
}
