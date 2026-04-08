// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
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

	gitRepoURL, err := fedorasource.BuildDistGitURL(g.distroGitBaseURI, upstreamNameToUse)
	if err != nil {
		return fmt.Errorf("failed to build dist-git URL for %#q:\n%w", upstreamNameToUse, err)
	}

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
	// Skip this step entirely when SkipLookaside is set (e.g., during rendering).
	if !opts.SkipLookaside {
		err := g.downloader.ExtractSourcesFromRepo(
			ctx, tempDir, upstreamName, g.lookasideBaseURI, skipFilenames,
		)
		if err != nil {
			return fmt.Errorf("failed to extract sources from git repository:\n%w", err)
		}
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

// checkoutTargetCommit resolves the effective commit via [resolveEffectiveCommitHash]
// and checks it out in the cloned repository.
func (g *FedoraSourcesProviderImpl) checkoutTargetCommit(
	ctx context.Context,
	upstreamCommit string,
	repoDir string,
) error {
	commitHash, err := g.resolveEffectiveCommitHash(ctx, repoDir, upstreamCommit, slog.LevelInfo)
	if err != nil {
		return err
	}

	if err := g.gitProvider.Checkout(ctx, repoDir, commitHash); err != nil {
		return fmt.Errorf("failed to checkout commit %#q:\n%w", commitHash, err)
	}

	return nil
}

// ResolveIdentity implements [SourceIdentityProvider] by resolving the upstream
// commit hash for the component. All resolution priority logic is in
// [resolveEffectiveCommitHash], called via [resolveCommit].
func (g *FedoraSourcesProviderImpl) ResolveIdentity(
	ctx context.Context,
	component components.Component,
) (string, error) {
	if component.GetName() == "" {
		return "", errors.New("component name cannot be empty")
	}

	upstreamName := component.GetConfig().Spec.UpstreamName
	if upstreamName == "" {
		upstreamName = component.GetName()
	}

	gitRepoURL, err := fedorasource.BuildDistGitURL(g.distroGitBaseURI, upstreamName)
	if err != nil {
		return "", fmt.Errorf("failed to build dist-git URL for %#q:\n%w", upstreamName, err)
	}

	return g.resolveCommit(ctx, gitRepoURL, upstreamName, component.GetConfig().Spec.UpstreamCommit)
}

// resolveCommit determines the effective commit via [resolveEffectiveCommitHash].
// For pinned commits (case 1), it returns immediately without cloning. For snapshot
// and HEAD cases, it performs a metadata-only clone to resolve the commit hash.
func (g *FedoraSourcesProviderImpl) resolveCommit(
	ctx context.Context, gitRepoURL string, upstreamName string, upstreamCommit string,
) (string, error) {
	// Case 1: Explicit upstream commit hash specified per-component
	if upstreamCommit != "" {
		return g.resolveEffectiveCommitHash(ctx, "", upstreamCommit, slog.LevelDebug)
	}

	// Cases 2 & 3: need a metadata-only clone to resolve snapshot or HEAD commit.
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

	commitHash, err := g.resolveEffectiveCommitHash(ctx, tempDir, "", slog.LevelDebug)
	if err != nil {
		return "", fmt.Errorf("resolving commit for %#q:\n%w", upstreamName, err)
	}

	return commitHash, nil
}

// resolveEffectiveCommitHash is the single source of truth for which commit a
// component should use from a cloned repository.
//
// Priority:
//  1. Explicit upstream commit hash (pinned per-component).
//  2. Snapshot time — commit immediately before the snapshot date.
//  3. Default — current HEAD.
func (g *FedoraSourcesProviderImpl) resolveEffectiveCommitHash(
	ctx context.Context,
	repoDir string,
	upstreamCommit string,
	logLevel slog.Level,
) (string, error) {
	// Case 1: Explicit upstream commit hash specified per-component.
	if upstreamCommit != "" {
		slog.Log(ctx, logLevel, "Using explicit upstream commit hash", "commitHash", upstreamCommit)

		return upstreamCommit, nil
	}

	// Case 2: Provider has a snapshot time configured from the resolved distro.
	if g.snapshotTime != "" {
		snapshotDateTime, err := time.Parse(time.RFC3339, g.snapshotTime)
		if err != nil {
			return "", fmt.Errorf("invalid snapshot time %#q:\n%w", g.snapshotTime, err)
		}

		commitHash, err := g.gitProvider.GetCommitHashBeforeDate(ctx, repoDir, snapshotDateTime)
		if err != nil {
			return "", fmt.Errorf("resolving commit for snapshot time %s:\n%w",
				snapshotDateTime.Format(time.RFC3339), err)
		}

		slog.Log(ctx, logLevel, "Using upstream distro snapshot time",
			"snapshotDateTime", snapshotDateTime.Format(time.RFC3339),
			"commitHash", commitHash)

		return commitHash, nil
	}

	// Case 3: Default — use current HEAD.
	commitHash, err := g.gitProvider.GetCurrentCommit(ctx, repoDir)
	if err != nil {
		return "", fmt.Errorf("resolving current HEAD commit:\n%w", err)
	}

	slog.Log(ctx, logLevel, "Using current HEAD", "commitHash", commitHash)

	return commitHash, nil
}
