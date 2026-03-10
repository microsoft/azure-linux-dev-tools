// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package prereqs_test

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/acobaugh/osrelease"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestCtx() *testctx.TestCtx {
	ctx := testctx.NewCtx()

	// We set dry-run mode to avoid actually executing commands.
	// This is necessary because the [prereqs] package doesn't call through
	// the [opctx] package to execute commands.
	ctx.DryRunValue = true

	return ctx
}

func setupOSReleaseFile(t *testing.T, ctx opctx.Ctx, osID string) {
	t.Helper()

	var osReleaseContents string

	if osID != "" {
		osReleaseContents = fmt.Sprintf("ID=%s\n", osID)
	}

	require.NoError(t, fileutils.WriteFile(ctx.FS(), osrelease.EtcOsRelease,
		[]byte(osReleaseContents), fileperms.PublicFile))
}

func TestRequireExecutable_NonExistent(t *testing.T) {
	const testProgramName = "test-program"

	prereq := prereqs.PackagePrereq{
		AzureLinuxPackages: []string{"test-package"},
	}

	t.Run("no prereq", func(t *testing.T) {
		ctx := newTestCtx()

		err := prereqs.RequireExecutable(ctx, testProgramName, nil)
		require.Error(t, err)
	})

	t.Run("prereq but disallowed", func(t *testing.T) {
		ctx := newTestCtx()
		ctx.AllPromptsAcceptedValue = false
		ctx.PromptsAllowedValue = false

		err := prereqs.RequireExecutable(ctx, testProgramName, &prereq)
		require.ErrorIs(t, err, prereqs.ErrMissingExecutable)
	})

	t.Run("auto-install but program missing", func(t *testing.T) {
		ctx := newTestCtx()
		ctx.AllPromptsAcceptedValue = true
		ctx.PromptsAllowedValue = false

		setupOSReleaseFile(t, ctx, prereqs.OSIDAzureLinux)

		err := prereqs.RequireExecutable(ctx, testProgramName, &prereq)
		require.Error(t, err)
	})
}

func TestRequireExecutable_Existent(t *testing.T) {
	const testProgramName = "test-program"

	ctx := newTestCtx()
	ctx.CmdFactory.RegisterCommandInSearchPath(testProgramName)

	err := prereqs.RequireExecutable(ctx, testProgramName, nil)
	require.NoError(t, err)
}

func TestPrereqInstall_UnsupportedHost(t *testing.T) {
	prereq := &prereqs.PackagePrereq{
		AzureLinuxPackages: []string{"test-package"},
	}

	t.Run("no os-release file", func(t *testing.T) {
		ctx := newTestCtx()

		err := prereq.Install(ctx)
		require.Error(t, err)
	})

	t.Run("missing host OS ID", func(t *testing.T) {
		ctx := newTestCtx()

		setupOSReleaseFile(t, ctx, "")

		err := prereq.Install(ctx)
		require.Error(t, err)
	})

	t.Run("unsupported host", func(t *testing.T) {
		ctx := newTestCtx()

		setupOSReleaseFile(t, ctx, "unknown")

		err := prereq.Install(ctx)
		require.Error(t, err)
	})
}

func TestPrereqInstall_SupportedHost(t *testing.T) {
	const testPackageName = "test-package"

	prereq := &prereqs.PackagePrereq{
		AzureLinuxPackages: []string{testPackageName},
	}

	ctx := newTestCtx()
	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		args := append([]string{cmd.Path}, cmd.Args...)

		assert.ElementsMatch(t, []string{"sudo", "tdnf", "install", "-y", testPackageName}, args)

		return nil
	}

	setupOSReleaseFile(t, ctx, prereqs.OSIDAzureLinux)

	err := prereq.Install(ctx)
	require.NoError(t, err)
}
