// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package pythonenv

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

// Program is the Python interpreter used to create and run virtual environments.
const Program = "python3"

// Create creates a Python virtual environment at venvDir.
func Create(ctx opctx.Ctx, venvDir string) error {
	slog.Info("Creating Python venv", slog.String("path", venvDir))

	venvCmd := exec.CommandContext(ctx, Program, "-m", "venv", venvDir)
	venvCmd.Stdout = os.Stdout
	venvCmd.Stderr = os.Stderr

	cmd, err := ctx.Command(venvCmd)
	if err != nil {
		return fmt.Errorf("failed to create venv command:\n%w", err)
	}

	if err := cmd.Run(ctx); err != nil {
		return fmt.Errorf("failed to create Python venv at %#q:\n%w", venvDir, err)
	}

	return nil
}
