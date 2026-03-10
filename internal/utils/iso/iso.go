// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package iso provides utilities for creating ISO images.
package iso

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
)

const (
	// GenisoimageBinary is the name of the genisoimage executable.
	GenisoimageBinary = "genisoimage"
)

// Runner encapsulates options and dependencies for creating ISO images.
type Runner struct {
	cmdFactory    opctx.CmdFactory
	eventListener opctx.EventListener
	verbose       bool
}

// NewRunner constructs a new [Runner] that can be used to create ISO images.
func NewRunner(ctx opctx.Ctx) *Runner {
	return &Runner{
		cmdFactory:    ctx,
		eventListener: ctx,
		verbose:       ctx.Verbose(),
	}
}

// CreateISOOptions contains configuration for creating an ISO image.
type CreateISOOptions struct {
	// OutputPath is the path where the ISO will be created.
	OutputPath string
	// VolumeID is the volume identifier for the ISO.
	VolumeID string
	// InputFiles is the list of files to include in the ISO.
	InputFiles []string
	// UseJoliet enables Joliet extensions for longer filenames.
	UseJoliet bool
	// UseRockRidge enables Rock Ridge extensions for POSIX attributes.
	UseRockRidge bool
	// Description is an optional human-readable description for progress display.
	Description string
}

// CreateISO creates an ISO image from the specified input files.
func (r *Runner) CreateISO(ctx context.Context, options CreateISOOptions) error {
	args := []string{
		"-output", options.OutputPath,
		"-volid", options.VolumeID,
	}

	if options.UseJoliet {
		args = append(args, "-joliet")
	}

	if options.UseRockRidge {
		args = append(args, "-rock")
	}

	args = append(args, options.InputFiles...)

	isoCmd := exec.CommandContext(ctx, GenisoimageBinary, args...)

	cmd, err := r.cmdFactory.Command(isoCmd)
	if err != nil {
		return fmt.Errorf("failed to create genisoimage command:\n%w", err)
	}

	description := options.Description
	if description == "" {
		description = "Creating ISO image"
	}

	err = cmd.SetDescription(description).Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to create ISO image:\n%w", err)
	}

	return nil
}

// CheckPrerequisites verifies that genisoimage is available.
func CheckPrerequisites(ctx opctx.Ctx) error {
	if err := prereqs.RequireExecutable(ctx, GenisoimageBinary, &prereqs.PackagePrereq{
		AzureLinuxPackages: []string{"cdrkit"},
		FedoraPackages:     []string{"genisoimage"},
	}); err != nil {
		return fmt.Errorf("genisoimage prerequisite check failed:\n%w", err)
	}

	return nil
}
