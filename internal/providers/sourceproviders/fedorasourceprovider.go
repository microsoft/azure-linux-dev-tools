// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
)

// FedoraSourcesProviderImpl implements [ComponentSourceProvider] for Git repositories.
type FedoraSourcesProviderImpl struct {
	fs               opctx.FS
	dryRunnable      opctx.DryRunnable
	gitProvider      git.GitProvider
	downloader       fedorasource.FedoraSourceDownloader
	distroGitBaseURI string
	distroGitBranch  string
	lookasideBaseURI string
	snapshotTime     string
	retryConfig      retry.Config
}

var _ ComponentSourceProvider = (*FedoraSourcesProviderImpl)(nil)

func NewFedoraSourcesProviderImpl(
	fs opctx.FS,
	dryRunnable opctx.DryRunnable,
	gitProvider git.GitProvider,
	downloader fedorasource.FedoraSourceDownloader,
	distro ResolvedDistro,
	retryCfg retry.Config,
) (*FedoraSourcesProviderImpl, error) {
	if fs == nil {
		return nil, errors.New("filesystem cannot be nil")
	}

	if dryRunnable == nil {
		return nil, errors.New("dryRunnable cannot be nil")
	}

	if gitProvider == nil {
		return nil, errors.New("git provider cannot be nil")
	}

	if downloader == nil {
		return nil, errors.New("downloader cannot be nil")
	}

	if distro.Definition.DistGitBaseURI == "" {
		return nil, errors.New("resolved distro must specify a dist-git base URI")
	}

	if distro.Version.DistGitBranch == "" {
		return nil, errors.New("resolved distro must specify a dist-git branch")
	}

	if distro.Definition.LookasideBaseURI == "" {
		return nil, errors.New("resolved distro must specify a lookaside base URI")
	}

	return &FedoraSourcesProviderImpl{
		fs:               fs,
		dryRunnable:      dryRunnable,
		gitProvider:      gitProvider,
		downloader:       downloader,
		distroGitBaseURI: distro.Definition.DistGitBaseURI,
		distroGitBranch:  distro.Version.DistGitBranch,
		lookasideBaseURI: distro.Definition.LookasideBaseURI,
		snapshotTime:     distro.Ref.Snapshot,
		retryConfig:      retryCfg,
	}, nil
}

func (g *FedoraSourcesProviderImpl) GetComponent(
	ctx context.Context, component components.Component, destDirPath string, opts ...FetchComponentOption,
) (err error) {
	resolved := resolveFetchComponentOptions(opts)

	componentName := component.GetName()
	if componentName == "" {
		return errors.New("component name cannot be empty")
	}

	upstreamNameToUse := componentName

	// Figure out if there's an override for the upstream name. This can happen when the derived
	// component name differs from the name used in the upstream distro.
	if upstreamNameOverride := component.GetConfig().Spec.UpstreamName; upstreamNameOverride != "" {
		upstreamNameToUse = upstreamNameOverride
	}

	if destDirPath == "" {
		return errors.New("destination path cannot be empty")
	}

	gitRepoURL := strings.ReplaceAll(g.distroGitBaseURI, "$pkg", upstreamNameToUse)

	slog.Info("Getting component from git repo",
		"component", componentName,
		"upstreamComponent", upstreamNameToUse,
		"branch", g.distroGitBranch,
		"upstreamCommit", component.GetConfig().Spec.UpstreamCommit,
		"snapshot", g.snapshotTime)

	// Clone to a temp directory first, then copy files to destination.
	tempDir, err := fileutils.MkdirTempInTempDir(g.fs, "azldev-clone-")
	if err != nil {
		return fmt.Errorf("failed to create temp directory for clone:\n%w", err)
	}

	defer fileutils.RemoveAllAndUpdateErrorIfNil(g.fs, tempDir, &err)

	// Clone the repository to temp directory with retry for transient network failures.
	err = retry.Do(ctx, g.retryConfig, func() error {
		// Clean up temp directory contents from any prior failed clone attempt.
		_ = g.fs.RemoveAll(tempDir)
		_ = fileutils.MkdirAll(g.fs, tempDir)

		return g.gitProvider.Clone(ctx, gitRepoURL, tempDir, git.WithGitBranch(g.distroGitBranch))
	})
	if err != nil {
		return fmt.Errorf("failed to clone git repository %#q:\n%w", gitRepoURL, err)
	}

	// Collect filenames from source-files config so the lookaside extractor can skip them.
	// These files were already fetched by FetchFiles and take precedence over upstream versions.
	sourceFiles := component.GetConfig().SourceFiles

	skipFileNames := make([]string, len(sourceFiles))
	for i := range sourceFiles {
		skipFileNames[i] = sourceFiles[i].Filename
	}

	// Process the cloned repo: checkout target commit, extract sources, copy to destination.
	return g.processClonedRepo(ctx, component.GetConfig().Spec.UpstreamCommit,
		tempDir, upstreamNameToUse, componentName, destDirPath, skipFileNames, resolved)
}

// processClonedRepo handles the post-clone steps: checking out the target commit,
// extracting lookaside sources, renaming spec files, and copying to the destination.
func (g *FedoraSourcesProviderImpl) processClonedRepo(
	ctx context.Context,
	upstreamCommit string,
	tempDir, upstreamName, componentName, destDirPath string,
	skipFilenames []string,
	opts FetchComponentOptions,
) error {
	// Checkout the appropriate commit based on component/distro config
	if err := g.checkoutTargetCommit(ctx, upstreamCommit, tempDir); err != nil {
		return fmt.Errorf("failed to checkout target commit:\n%w", err)
	}

	// Delete the .git directory so it's not copied to destination, unless the caller
	// requested that it be preserved (e.g., for synthetic history generation).
	if !opts.PreserveGitDir {
		if err := g.fs.RemoveAll(filepath.Join(tempDir, ".git")); err != nil {
			return fmt.Errorf("failed to remove .git directory from cloned repository at %#q:\n%w",
				tempDir, err)
		}
	}

	// Extract sources from repo (downloads lookaside files into the temp dir).
	// Files in skipFilenames are not downloaded — they were already fetched by FetchFiles.
	err := g.downloader.ExtractSourcesFromRepo(
		ctx, tempDir, upstreamName, g.lookasideBaseURI, skipFilenames,
	)
	if err != nil {
		return fmt.Errorf("failed to extract sources from git repository:\n%w", err)
	}

	// If the upstream name differs from the component name, rename the spec in temp dir.
	if err := g.renameSpecIfNeeded(tempDir, upstreamName, componentName); err != nil {
		return err
	}

	// Copy files from temp dir to destination, skipping files that already exist.
	// This preserves any files downloaded by FetchFiles, giving them precedence.
	copyOptions := fileutils.CopyDirOptions{
		CopyFileOptions: fileutils.CopyFileOptions{
			PreserveFileMode: true,
		},
		FileFilter: fileutils.SkipExistingFiles,
	}

	if err := fileutils.CopyDirRecursive(g.dryRunnable, g.fs, tempDir, destDirPath, copyOptions); err != nil {
		return fmt.Errorf("failed to copy files to destination:\n%w", err)
	}

	return nil
}

// renameSpecIfNeeded renames the spec file in the given directory if the upstream name
// differs from the desired component name.
func (g *FedoraSourcesProviderImpl) renameSpecIfNeeded(dir, upstreamName, componentName string) error {
	if upstreamName == componentName {
		return nil
	}

	downloadedSpecPath := filepath.Join(dir, upstreamName+".spec")
	desiredSpecPath := filepath.Join(dir, componentName+".spec")

	err := g.fs.Rename(downloadedSpecPath, desiredSpecPath)
	if err != nil {
		return fmt.Errorf("failed to rename fetched spec file from %#q to %#q:\n%w",
			downloadedSpecPath, desiredSpecPath, err)
	}

	return nil
}

// checkoutTargetCommit determines the appropriate commit to use and checks it out.
// Priority order:
//  1. Explicit upstream commit hash - specified per-component via upstream-commit
//  2. Upstream distro snapshot - snapshot time from the provider's resolved distro
//  3. Default - use current HEAD (no checkout needed)
func (g *FedoraSourcesProviderImpl) checkoutTargetCommit(
	ctx context.Context,
	upstreamCommit string,
	repoDir string,
) error {
	// Case 1: Explicit upstream commit hash specified per-component
	if upstreamCommit != "" {
		slog.Info("Using explicit upstream commit hash",
			"commitHash", upstreamCommit)

		if err := g.gitProvider.Checkout(ctx, repoDir, upstreamCommit); err != nil {
			return fmt.Errorf("failed to checkout upstream commit %#q:\n%w", upstreamCommit, err)
		}

		return nil
	}

	// Case 2: Provider has a snapshot time configured from the resolved distro
	if g.snapshotTime != "" {
		snapshotDateTime, err := time.Parse(time.RFC3339, g.snapshotTime)
		if err != nil {
			return fmt.Errorf("invalid snapshot time %#q:\n%w", g.snapshotTime, err)
		}

		commitHash, err := g.gitProvider.GetCommitHashBeforeDate(ctx, repoDir, snapshotDateTime)
		if err != nil {
			return fmt.Errorf("failed to get commit hash for snapshot time %s:\n%w",
				snapshotDateTime.Format(time.RFC3339), err)
		}

		slog.Info("Using upstream distro snapshot time",
			"snapshotDateTime", snapshotDateTime.Format(time.RFC3339),
			"commitHash", commitHash)

		if err := g.gitProvider.Checkout(ctx, repoDir, commitHash); err != nil {
			return fmt.Errorf("failed to checkout snapshot commit %#q:\n%w", commitHash, err)
		}

		return nil
	}

	// Case 3: Default - use current HEAD (already checked out by clone)
	slog.Info("Using current HEAD (no snapshot time configured)")

	return nil
}

// ResolveSourceIdentity implements [SourceIdentityProvider] by resolving the upstream
// commit hash for the component. Resolution priority matches [checkoutTargetCommit]:
//  1. Explicit upstream commit hash (pinned per-component) — returned directly.
//  2. Snapshot time — perform a metadata-only clone of the dist-git branch and use the
//     local git history to find the commit immediately before the snapshot date.
//  3. Default — perform a metadata-only clone of the dist-git branch and use its current HEAD.
func (g *FedoraSourcesProviderImpl) ResolveSourceIdentity(
	ctx context.Context,
	component components.Component,
) (string, error) {
	if component.GetName() == "" {
		return "", errors.New("component name cannot be empty")
	}

	// Case 1: Explicit upstream commit hash — no network call needed.
	if pinnedCommit := component.GetConfig().Spec.UpstreamCommit; pinnedCommit != "" {
		slog.Debug("Using pinned upstream commit for identity",
			"component", component.GetName(),
			"commit", pinnedCommit)

		return pinnedCommit, nil
	}

	// Case 2: Need to resolve the commit for the snapshot time or current HEAD
	upstreamName := component.GetConfig().Spec.UpstreamName
	if upstreamName == "" {
		upstreamName = component.GetName()
	}

	gitRepoURL := strings.ReplaceAll(g.distroGitBaseURI, "$pkg", upstreamName)

	return g.resolveCommit(ctx, gitRepoURL, upstreamName)
}

// resolveCommit clones the branch and determines the effective commit, either
// at the snapshot time, or at the latest commit if no snapshot time is configured.
func (g *FedoraSourcesProviderImpl) resolveCommit(
	ctx context.Context, gitRepoURL string, upstreamName string,
) (string, error) {
	tempDir, err := fileutils.MkdirTempInTempDir(g.fs, "azldev-identity-snapshot-")
	if err != nil {
		return "", fmt.Errorf("creating temp directory for snapshot clone:\n%w", err)
	}

	defer func() {
		if removeErr := g.fs.RemoveAll(tempDir); removeErr != nil {
			slog.Debug("Failed to clean up snapshot clone temp directory",
				"path", tempDir, "error", removeErr)
		}
	}()

	// Clone a single branch to resolve the snapshot commit. We use a full
	// (non-shallow) clone because not all git servers support --shallow-since
	// (e.g., Pagure returns "the remote end hung up unexpectedly").
	err = retry.Do(ctx, g.retryConfig, func() error {
		_ = g.fs.RemoveAll(tempDir)
		_ = fileutils.MkdirAll(g.fs, tempDir)

		return g.gitProvider.Clone(ctx, gitRepoURL, tempDir,
			git.WithGitBranch(g.distroGitBranch),
			git.WithMetadataOnly(),
			git.WithQuiet(),
		)
	})
	if err != nil {
		return "", fmt.Errorf("partial clone for identity of %#q:\n%w", upstreamName, err)
	}

	var commitHash string

	if g.snapshotTime != "" {
		snapshotDateTime, parseErr := time.Parse(time.RFC3339, g.snapshotTime)
		if parseErr != nil {
			return "", fmt.Errorf("invalid snapshot time %#q:\n%w", g.snapshotTime, parseErr)
		}

		commitHash, err = g.gitProvider.GetCommitHashBeforeDate(ctx, tempDir, snapshotDateTime)
		if err != nil {
			return "", fmt.Errorf("resolving snapshot commit for %#q at %s:\n%w",
				upstreamName, snapshotDateTime.Format(time.RFC3339), err)
		}
	} else {
		commitHash, err = g.gitProvider.GetCurrentCommit(ctx, tempDir)
		if err != nil {
			return "", fmt.Errorf("resolving current commit for %#q:\n%w", upstreamName, err)
		}
	}

	slog.Debug("Resolved snapshot commit for identity",
		"component", upstreamName,
		"snapshot", g.snapshotTime,
		"commit", commitHash)

	return commitHash, nil
}
