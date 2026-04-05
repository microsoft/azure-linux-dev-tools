// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

const rpmautospecBinary = "rpmautospec"

// ProcessAutospecMacros runs rpmautospec process-distgit on a spec file to expand
// %autorelease and %autochangelog macros into concrete values. The spec must reside
// in a git directory with commit history (upstream + synthetic) for rpmautospec to
// read.
//
// For specs that don't use %autorelease or %autochangelog, rpmautospec is a no-op
// and the spec passes through unchanged.
//
// Returns an error if rpmautospec is not installed or fails.
func ProcessAutospecMacros(ctx context.Context, cmdFactory opctx.CmdFactory, specPath, outputPath string) error {
	if !cmdFactory.CommandInSearchPath(rpmautospecBinary) {
		return fmt.Errorf(
			"rpmautospec not found in PATH; install via: "+
				"dnf install mock-rpmautospec:\n%w", exec.ErrNotFound)
	}

	slog.Debug("Running rpmautospec process-distgit",
		"spec", specPath,
		"output", outputPath,
	)

	rawCmd := exec.CommandContext(ctx, rpmautospecBinary, "process-distgit", specPath, outputPath)

	var stderr strings.Builder

	rawCmd.Stderr = &stderr

	cmd, err := cmdFactory.Command(rawCmd)
	if err != nil {
		return fmt.Errorf("failed to wrap rpmautospec command:\n%w", err)
	}

	if _, err := cmd.RunAndGetOutput(ctx); err != nil {
		return fmt.Errorf("rpmautospec process-distgit failed for %#q:\n%s\n%w",
			specPath, stderr.String(), err)
	}

	slog.Debug("rpmautospec process-distgit complete",
		"spec", specPath,
		"output", outputPath,
	)

	return nil
}
