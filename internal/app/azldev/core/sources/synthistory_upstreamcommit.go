// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	toml "github.com/pelletier/go-toml/v2"
)

// This file implements synthetic-history discovery for lock-file-free mode. It
// derives the change history from the generated upstream-commit TOML that pins a
// component's commit, rather than from lock file fingerprint changes. Everything
// downstream of discovery (interleaving and replay) is shared with the default
// mode; see synthistory.go.

// FindUpstreamCommitChanges walks the git log for commits that changed a
// component's generated upstream-commit TOML. Results are chronological
// (oldest first).
func FindUpstreamCommitChanges(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	projectRepo *gogit.Repository,
	projectRepoDir string,
	configFileRelPath string,
	componentName string,
) ([]UpstreamCommitChange, error) {
	metas, err := gitLogFileMetadata(ctx, cmdFactory, projectRepoDir, configFileRelPath)
	if err != nil {
		return nil, err
	}

	if len(metas) == 0 {
		return nil, nil
	}

	type entry struct {
		upstreamCommit string
		meta           CommitMetadata
	}

	var entries []entry //nolint:prealloc // size not known ahead of time.

	for _, meta := range metas {
		upstreamCommit, err := upstreamCommitAtCommit(
			projectRepo, meta.Hash, configFileRelPath, componentName,
		)
		if errors.Is(err, object.ErrFileNotFound) || errors.Is(err, object.ErrDirectoryNotFound) {
			// The commit deleted the generated TOML; the pin it carried is the
			// one recorded by its parent.
			upstreamCommit, err = upstreamCommitBeforeCommit(
				projectRepo, meta.Hash, configFileRelPath, componentName,
			)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to read upstream commit TOML at commit %#q:\n%w",
				meta.Hash, err)
		}

		entries = append(entries, entry{upstreamCommit: upstreamCommit, meta: meta})
	}

	if len(entries) == 0 {
		return nil, nil
	}

	// Entries are newest-first (from git log order). Reverse to chronological.
	slices.Reverse(entries)

	changes := make([]UpstreamCommitChange, 0, len(entries))
	for _, change := range entries {
		changes = append(changes, UpstreamCommitChange{
			CommitMetadata: change.meta,
			UpstreamCommit: change.upstreamCommit,
		})
	}

	return changes, nil
}

// upstreamCommitBeforeCommit reads the component's pinned upstream commit from the
// first parent of commitHash.
func upstreamCommitBeforeCommit(
	repo *gogit.Repository,
	commitHash string,
	configFileRelPath string,
	componentName string,
) (string, error) {
	commit, err := repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		return "", fmt.Errorf("failed to resolve config deletion commit %#q:\n%w",
			commitHash, err)
	}

	parent, err := commit.Parent(0)
	if err != nil {
		return "", fmt.Errorf("failed to resolve parent of config deletion commit %#q:\n%w",
			commitHash, err)
	}

	return upstreamCommitAtCommit(repo, parent.Hash.String(), configFileRelPath, componentName)
}

// upstreamCommitAtCommit reads the component's pinned upstream commit from the
// generated TOML as it existed at commitHash.
func upstreamCommitAtCommit(
	repo *gogit.Repository,
	commitHash string,
	configFileRelPath string,
	componentName string,
) (string, error) {
	commit, err := repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		return "", fmt.Errorf("resolving commit %#q:\n%w", commitHash, err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("reading commit tree:\n%w", err)
	}

	file, err := tree.File(configFileRelPath)
	if err != nil {
		return "", fmt.Errorf("reading config file %#q:\n%w", configFileRelPath, err)
	}

	content, err := file.Contents()
	if err != nil {
		return "", fmt.Errorf("reading config contents %#q:\n%w", configFileRelPath, err)
	}

	var config projectconfig.ConfigFile
	if err := toml.Unmarshal([]byte(content), &config); err != nil {
		return "", fmt.Errorf("parsing config file:\n%w", err)
	}

	component, ok := config.Components[componentName]
	if !ok {
		return "", fmt.Errorf("config file does not define component %#q", componentName)
	}

	return component.Spec.UpstreamCommit, nil
}

// buildUpstreamCommitSyntheticCommits resolves the project repository from the
// config file that pinned the component's upstream commit and returns that file's
// upstream-commit changes chronologically. Returns (nil, nil) when there is no
// history to represent.
func buildUpstreamCommitSyntheticCommits(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	config *projectconfig.ComponentConfig,
	componentName string,
) ([]UpstreamCommitChange, error) {
	var configFileAbsPath string
	if configFile := config.UpstreamCommitConfigFile(); configFile != nil {
		configFileAbsPath = configFile.SourcePath()
	}

	projectRepo, projectRepoDir, err := openProjectRepoForConfigFile(configFileAbsPath, componentName)
	if err != nil {
		return nil, err
	}

	if projectRepo == nil {
		return nil, nil
	}

	configFileRelPath, err := filepath.Rel(projectRepoDir, configFileAbsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compute repo-relative config path for %#q:\n%w",
			configFileAbsPath, err)
	}

	if config.Spec.UpstreamCommit == "" {
		return nil, nil
	}

	changes, err := FindUpstreamCommitChanges(
		ctx, cmdFactory, projectRepo, projectRepoDir, configFileRelPath, componentName,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to find upstream commit changes for config file %#q:\n%w",
			configFileRelPath, err)
	}

	if len(changes) == 0 {
		shallowCommits, _ := projectRepo.Storer.Shallow()
		if len(shallowCommits) > 0 {
			return nil, fmt.Errorf(
				"upstream commit TOML %#q has no git history; a full clone is required",
				configFileRelPath)
		}

		slog.Warn("Upstream commit TOML has no changes; skipping synthetic history",
			"configFile", configFileRelPath)

		return nil, nil
	}

	return changes, nil
}
