// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package buildenv_testutils

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/microsoft/azure-linux-dev-tools/internal/buildenv"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

type TestBuildEnv struct {
	ctx opctx.Ctx
}

func NewTestBuildEnv(ctx opctx.Ctx) *TestBuildEnv {
	return &TestBuildEnv{
		ctx: ctx,
	}
}

// CreateCmd builds a new [exec.Cmd] for running the given command within the build environment.
func (t *TestBuildEnv) CreateCmd(
	ctx context.Context, args []string, options buildenv.RunOptions,
) (cmd opctx.Cmd, err error) {
	cmd, err = t.ctx.Command(exec.CommandContext(ctx, "mock", args...))
	if err != nil {
		return nil, fmt.Errorf("failed to create command in test build environment:\n%w", err)
	}

	return cmd, nil
}

// GetInfo returns a [buildenv.BuildEnvironmentInfo] structure that contains information about the build environment.
func (t *TestBuildEnv) GetInfo() buildenv.BuildEnvInfo {
	return buildenv.BuildEnvInfo{
		Type: "test build environment",
		Name: "test1",
	}
}

// Destroy permanently (and irreversibly) destroys the build environment, removing its files from the filesystem.
func (t *TestBuildEnv) Destroy(ctx opctx.Ctx) error {
	return nil
}
