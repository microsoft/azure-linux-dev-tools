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
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
)

// DistroResolver is a function type that resolves a distro reference to its definition and version.
type DistroResolver func(distroRef projectconfig.DistroReference) (
	projectconfig.DistroDefinition, projectconfig.DistroVersionDefinition, error,
)

// FedoraSourcesProviderImpl implements ComponentSourceProvider for Git repositories.
type FedoraSourcesProviderImpl struct {
	fs             opctx.FS
	dryRunnable    opctx.DryRunnable
	gitProvider    git.GitProvider
	downloader     fedorasource.FedoraSourceDownloader
	distroResolver DistroResolver
	retryConfig    retry.Config
}

var _ ComponentSourceProvider = (*FedoraSourcesProviderImpl)(nil)

func NewFedoraSourcesProviderImpl(
	fs opctx.FS,
	dryRunnable opctx.DryRunnable,
	gitProvider git.GitProvider,
	downloader fedorasource.FedoraSourceDownloader,
	distroResolver DistroResolver,
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

	if distroResolver == nil {
		return nil, errors.New("distro resolver cannot be nil")
	}

	return &FedoraSourcesProviderImpl{
		fs:             fs,
		dryRunnable:    dryRunnable,
		gitProvider:    gitProvider,
		downloader:     downloader,
		distroResolver: distroResolver,
		retryConfig:    retryCfg,
	}, nil
}

func (g *FedoraSourcesProviderImpl) GetComponent(
	ctx context.Context, component components.Component, destDirPath string,
) (err error) {
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

	// Resolve the distro configuration for this component
	effectiveDistroRef, distroGitBaseURI, distroGitBranch, lookasideBaseURI, err := g.resolveDistroConfig(component)
	if err != nil {
		return fmt.Errorf("failed to resolve distro configuration for component %#q:\n%w", componentName, err)
	}

	gitRepoURL := strings.ReplaceAll(distroGitBaseURI, "$pkg", upstreamNameToUse)

	slog.Info("Getting component from git repo",
		"component", componentName,
		"upstreamComponent", upstreamNameToUse,
		"branch", distroGitBranch,
		"upstreamCommit", component.GetConfig().Spec.UpstreamCommit,
		"snapshot", effectiveDistroRef.Snapshot)

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

		return g.gitProvider.Clone(ctx, gitRepoURL, tempDir, git.WithGitBranch(distroGitBranch))
	})
	if err != nil {
		return fmt.Errorf("failed to clone git repository %#q:\n%w", gitRepoURL, err)
	}

	// Process the cloned repo: checkout target commit, extract sources, copy to destination.
	return g.processClonedRepo(ctx, effectiveDistroRef, component.GetConfig().Spec.UpstreamCommit,
		tempDir, upstreamNameToUse, componentName, lookasideBaseURI, destDirPath)
}

// processClonedRepo handles the post-clone steps: checking out the target commit,
// extracting lookaside sources, renaming spec files, and copying to the destination.
func (g *FedoraSourcesProviderImpl) processClonedRepo(
	ctx context.Context,
	effectiveDistroRef projectconfig.DistroReference,
	upstreamCommit string,
	tempDir, upstreamName, componentName, lookasideBaseURI, destDirPath string,
) error {
	// Checkout the appropriate commit based on component/distro config
	if err := g.checkoutTargetCommit(ctx, effectiveDistroRef, upstreamCommit, tempDir); err != nil {
		return fmt.Errorf("failed to checkout target commit:\n%w", err)
	}

	// Delete the .git directory so it's not copied to destination.
	if err := g.fs.RemoveAll(filepath.Join(tempDir, ".git")); err != nil {
		return fmt.Errorf("failed to remove .git directory from cloned repository at %#q:\n%w",
			tempDir, err)
	}

	// Extract sources from repo (downloads lookaside files into the temp dir)
	if err := g.downloader.ExtractSourcesFromRepo(ctx, tempDir, upstreamName, lookasideBaseURI); err != nil {
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

// resolveDistroConfig resolves the distro configuration for a component.
// The component's upstream-distro reference is used directly, as it already has
// defaults applied from the distro's default-component-config.
// Returns the distro reference plus resolved URIs and branch.
func (g *FedoraSourcesProviderImpl) resolveDistroConfig(
	component components.Component,
) (distroRef projectconfig.DistroReference, distroGitBaseURI, distroGitBranch, lookasideBaseURI string, err error) {
	distroRef = component.GetConfig().Spec.UpstreamDistro

	slog.Debug("Resolving distro configuration for component",
		"component", component.GetName(),
		"distroRef", distroRef,
	)

	// Resolve the distro reference
	distroDef, distroVersionDef, err := g.distroResolver(distroRef)
	if err != nil {
		return distroRef, "", "", "", fmt.Errorf("failed to resolve distro reference %#q:\n%w", distroRef.Name, err)
	}

	return distroRef, distroDef.DistGitBaseURI, distroVersionDef.DistGitBranch, distroDef.LookasideBaseURI, nil
}

// checkoutTargetCommit determines the appropriate commit to use and checks it out.
// Priority order:
//  1. Explicit upstream commit hash - specified per-component via upstream-commit
//  2. Upstream distro snapshot - snapshot time from the effective distro reference
//  3. Default - use current HEAD (no checkout needed)
func (g *FedoraSourcesProviderImpl) checkoutTargetCommit(
	ctx context.Context,
	effectiveDistroRef projectconfig.DistroReference,
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

	// Case 2: Effective distro reference has snapshot time configured
	if snapshotStr := effectiveDistroRef.Snapshot; snapshotStr != "" {
		snapshotDateTime, err := time.Parse(time.RFC3339, snapshotStr)
		if err != nil {
			return fmt.Errorf("invalid snapshot time %#q:\n%w", snapshotStr, err)
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
