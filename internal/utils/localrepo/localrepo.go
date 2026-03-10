// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package localrepo provides utilities for managing local RPM repositories.
package localrepo

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
)

const (
	// CreaterepoCBinary is the name of the createrepo_c executable.
	CreaterepoCBinary = "createrepo_c"
)

// createrepoCPrereq returns the prerequisite for the createrepo_c tool.
func createrepoCPrereq() *prereqs.PackagePrereq {
	return &prereqs.PackagePrereq{
		AzureLinuxPackages: []string{"createrepo_c"},
	}
}

// Publisher handles publishing RPMs to a local repository.
type Publisher struct {
	// repoPath is the absolute path to the repository directory.
	repoPath      string
	fs            opctx.FS
	cmdFactory    opctx.CmdFactory
	dryRunnable   opctx.DryRunnable
	eventListener opctx.EventListener
}

// NewPublisher creates a new [Publisher] for the given repository path.
// The path is converted to an absolute path to ensure consistent behavior
// regardless of working directory changes during command execution.
//
// If initialize is true, the repository will be initialized with metadata
// (via createrepo_c) so it can be used as a dependency source. This requires
// createrepo_c to be available and will check for it.
func NewPublisher(
	ctx opctx.Ctx,
	repoPath string,
	initialize bool,
) (*Publisher, error) {
	// If we need to initialize, check that createrepo_c is available first (fail fast).
	if initialize {
		if err := RequireCreaterepoC(ctx); err != nil {
			return nil, fmt.Errorf("createrepo_c required for repository initialization:\n%w", err)
		}
	}

	// Convert to absolute path to avoid issues with relative paths
	// when external commands run in different directories.
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for %#q:\n%w", repoPath, err)
	}

	publisher := &Publisher{
		repoPath:      absPath,
		fs:            ctx.FS(),
		cmdFactory:    ctx,
		dryRunnable:   ctx,
		eventListener: ctx,
	}

	// Initialize the repository if requested.
	if initialize {
		if err := publisher.EnsureRepoInitialized(ctx); err != nil {
			return nil, fmt.Errorf("failed to initialize repository %#q:\n%w", repoPath, err)
		}
	}

	return publisher, nil
}

// RepoPath returns the absolute path to the repository.
func (p *Publisher) RepoPath() string {
	return p.repoPath
}

// EnsureRepoExists ensures the repository directory exists, creating it if necessary.
func (p *Publisher) EnsureRepoExists() error {
	err := fileutils.MkdirAll(p.fs, p.repoPath)
	if err != nil {
		return fmt.Errorf("failed to create local repo directory %#q:\n%w", p.repoPath, err)
	}

	return nil
}

// EnsureRepoInitialized ensures the repository directory exists and has valid repo metadata.
// This is required before the repo can be used as a dependency source (e.g., via mock --addrepo).
func (p *Publisher) EnsureRepoInitialized(ctx context.Context) error {
	if err := p.EnsureRepoExists(); err != nil {
		return err
	}

	// Check if repodata already exists.
	repodataPath := path.Join(p.repoPath, "repodata")

	exists, err := fileutils.DirExists(p.fs, repodataPath)
	if err != nil {
		return fmt.Errorf("failed to check if repodata exists at %#q:\n%w", repodataPath, err)
	}

	if exists {
		return nil
	}

	// Initialize the repo metadata.
	return p.updateRepoMetadata(ctx)
}

// PublishRPMs copies the given RPM files to the repository and updates the repository metadata.
// If an RPM file is already in the repository directory, the copy is skipped.
func (p *Publisher) PublishRPMs(ctx context.Context, rpmPaths []string) error {
	if len(rpmPaths) == 0 {
		return nil
	}

	// Copy each RPM to the repository (unless it's already there).
	for _, rpmPath := range rpmPaths {
		destPath := path.Join(p.repoPath, path.Base(rpmPath))

		// Convert source to absolute path to compare with destination.
		absRpmPath, err := filepath.Abs(rpmPath)
		if err != nil {
			return fmt.Errorf("failed to resolve absolute path for %#q:\n%w", rpmPath, err)
		}

		// Skip copy if source and destination are the same file.
		// This happens when the local repo path is the same as the build output directory.
		if absRpmPath == destPath {
			continue
		}

		err = fileutils.CopyFile(p.dryRunnable, p.fs, rpmPath, destPath, fileutils.CopyFileOptions{})
		if err != nil {
			return fmt.Errorf("failed to copy RPM %#q to local repo:\n%w", rpmPath, err)
		}
	}

	// Update the repository metadata.
	err := p.updateRepoMetadata(ctx)
	if err != nil {
		return fmt.Errorf("failed to update local repo metadata:\n%w", err)
	}

	return nil
}

// updateRepoMetadata runs createrepo_c to update the repository metadata.
func (p *Publisher) updateRepoMetadata(ctx context.Context) error {
	event := p.eventListener.StartEvent("Updating local repo metadata", "repo", p.repoPath)
	defer event.End()

	// Run createrepo_c to regenerate repo metadata.
	// We don't use --update because files may have been replaced with the same name
	// (e.g., rebuilding a package without bumping version/release), and --update
	// might not detect content changes if mtime didn't change.
	cmd := exec.CommandContext(ctx, CreaterepoCBinary, p.repoPath)

	extCmd, err := p.cmdFactory.Command(cmd)
	if err != nil {
		return fmt.Errorf("failed to create createrepo_c command:\n%w", err)
	}

	err = extCmd.Run(ctx)
	if err != nil {
		return fmt.Errorf("createrepo_c failed:\n%w", err)
	}

	return nil
}

// RequireCreaterepoC checks that createrepo_c is available, offering to install it if not.
func RequireCreaterepoC(ctx opctx.Ctx) error {
	err := prereqs.RequireExecutable(ctx, CreaterepoCBinary, createrepoCPrereq())
	if err != nil {
		return fmt.Errorf("checking for createrepo_c:\n%w", err)
	}

	return nil
}
