// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

const (
	MountRWOption   string = ":z"
	MountROOption   string = ":z,ro"
	PrivilegedFlag  string = "--privileged"
	InteractiveFlag string = "-i"
)

func validateDockerArgs(dockerArgs []string) error {
	foundPrivileged := false
	foundInteractive := false

	for _, arg := range dockerArgs {
		switch arg {
		case PrivilegedFlag:
			foundPrivileged = true
		case InteractiveFlag:
			foundInteractive = true
		}
	}

	// Ensure we get a TTY (not the default when run with --privileged)
	if foundPrivileged && !foundInteractive {
		return fmt.Errorf("the '%s' flag must be used together with the '%s' flag to create a TTY",
			PrivilegedFlag, InteractiveFlag)
	}

	return nil
}

// RunDocker runs a docker command with the specified arguments and returns the
// stdout output as a string. It can also capture output from a log file (logFile)
// in real-time, filter it (logFilter) and send it to the console.
func RunDocker(ctx context.Context, cmdFactory opctx.CmdFactory, dockerArgs []string,
	containerTag string, entryPointArgs []string, logFile string,
	logFilter func(context.Context, string),
) (string, error) {
	err := validateDockerArgs(dockerArgs)
	if err != nil {
		return "", err
	}

	var args []string

	args = append(args, dockerArgs...)
	args = append(args, containerTag)
	args = append(args, entryPointArgs...)

	var stderr strings.Builder

	var stdout strings.Builder

	cmdCtx := exec.CommandContext(ctx, "docker", args...)
	// When docker runs the container in privileged mode, it does not create a TTY
	// device automatically since the container can interact with the host's devices
	// in many ways. This leads to the azldev process being unable to capture the
	// stdout from the docker run command.
	// To avoid that, we need to provide a stdin so that a proper TTY device can be
	// created.
	cmdCtx.Stdin = os.Stdin
	cmdCtx.Stderr = &stderr
	cmdCtx.Stdout = &stdout

	cmd, err := cmdFactory.Command(cmdCtx)
	if err != nil {
		return "", fmt.Errorf("failed to wrap the 'docker' command:\n%w", err)
	}

	err = cmd.AddRealTimeFileListener(logFile, logFilter)
	if err != nil {
		return "", fmt.Errorf("failed to add docker log listeners: %w", err)
	}

	err = cmd.Run(ctx)
	if err != nil {
		return stdout.String(), fmt.Errorf("failed to execute 'docker' command:\n%w, stderr: %s", err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}
