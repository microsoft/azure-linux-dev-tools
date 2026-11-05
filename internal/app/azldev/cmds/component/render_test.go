// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"os/exec"
	"slices"
	"testing"

	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRenderCmd(t *testing.T) {
	cmd := componentcmds.NewRenderCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "render", cmd.Use)
	assert.NotNil(t, cmd.RunE)
}

func TestNewRenderCmd_Flags(t *testing.T) {
	cmd := componentcmds.NewRenderCmd()

	outputDirFlag := cmd.Flags().Lookup("output-dir")
	require.NotNil(t, outputDirFlag, "output-dir flag should be registered")
	assert.Equal(t, "o", outputDirFlag.Shorthand)
	assert.Empty(t, outputDirFlag.DefValue)

	allFlag := cmd.Flags().Lookup("all-components")
	require.NotNil(t, allFlag, "all-components flag should be registered")

	componentFlag := cmd.Flags().Lookup("component")
	require.NotNil(t, componentFlag, "component flag should be registered")

	failOnErrorFlag := cmd.Flags().Lookup("fail-on-error")
	require.NotNil(t, failOnErrorFlag, "fail-on-error flag should be registered")
	assert.Equal(t, "false", failOnErrorFlag.DefValue)

	forceFlag := cmd.Flags().Lookup("force")
	require.NotNil(t, forceFlag, "force flag should be registered")
	assert.Equal(t, "false", forceFlag.DefValue)

	cleanStaleFlag := cmd.Flags().Lookup("clean-stale")
	require.NotNil(t, cleanStaleFlag, "clean-stale flag should be registered")
	assert.Equal(t, "false", cleanStaleFlag.DefValue)

	checkOnlyFlag := cmd.Flags().Lookup("check-only")
	require.NotNil(t, checkOnlyFlag, "check-only flag should be registered")
	assert.Equal(t, "false", checkOnlyFlag.DefValue)

	noChangeRebuildFlag := cmd.Flags().Lookup("no-change-rebuild")
	require.NotNil(t, noChangeRebuildFlag)
	require.NoError(t, cmd.ParseFlags([]string{"--no-change-rebuild"}))
	assert.Equal(t, "true", noChangeRebuildFlag.Value.String())

	commentFlag := cmd.Flags().Lookup("changelog-comment")
	require.NotNil(t, commentFlag)

	noGitCommitFlag := cmd.Flags().Lookup("no-git-commit")
	require.NotNil(t, noGitCommitFlag)
	require.NoError(t, cmd.ParseFlags([]string{"--changelog-comment", "Fix curl patches", "--no-git-commit"}))
	assert.Equal(t, "Fix curl patches", commentFlag.Value.String())
	assert.Equal(t, "true", noGitCommitFlag.Value.String())

	allowUncommittedFlag := cmd.Flags().Lookup("allow-uncommitted")
	require.NotNil(t, allowUncommittedFlag)
	assert.Equal(t, "false", allowUncommittedFlag.DefValue)
	require.NoError(t, cmd.ParseFlags([]string{"--allow-uncommitted"}))
	assert.Equal(t, "true", allowUncommittedFlag.Value.String())

	rpmDevBumpspecFlag := cmd.Flags().Lookup("rpmdev-bumpspec")
	require.NotNil(t, rpmDevBumpspecFlag)
	assert.Equal(t, "false", rpmDevBumpspecFlag.DefValue)
	assert.Equal(t, "Use rpmdev-bumpspec instead of the legacy static release calculation", rpmDevBumpspecFlag.Usage)

	assert.Nil(t, componentcmds.NewDiffSourcesCmd().Flags().Lookup("rpmdev-bumpspec"))
}

func TestRenderCmd_NoComponents(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := componentcmds.NewRenderCmd()
	cmd.SetArgs([]string{
		"-o", "SPECS", "--no-change-rebuild", "--no-git-commit",
		"--changelog-comment", "Fix patches", "nonexistent-component",
	})

	err := cmd.ExecuteContext(testEnv.Env)

	// We expect an error because no components match (not the output-dir error).
	require.Error(t, err)
	assert.Contains(t, err.Error(), "component not found")
}

func TestRenderCmd_AllowUncommitted(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		allow bool
	}{
		{name: "dirty repository refused"},
		{name: "override skips status", allow: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			env.AddUpstreamComponent(t, "curl")
			env.Config.LocklessComponents = map[string]bool{"curl": true}
			config := env.Config.Components["curl"]
			config.Spec.UpstreamCommit = "configured-commit"
			env.Config.Components["curl"] = config
			queried := false
			original := env.CmdFactory.RunAndGetOutputHandler
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				if slices.Contains(cmd.Args, "--porcelain=v1") {
					queried = true

					return "?? scratch\n", nil
				}

				return original(cmd)
			}
			cmd := componentcmds.NewRenderCmd()

			args := []string{"-p", "curl", "-o", "SPECS", "--fail-on-error"}
			if testCase.allow {
				args = append(args, "--allow-uncommitted")
			}

			cmd.SetArgs(args)
			err := cmd.ExecuteContext(env.Env)

			assert.Equal(t, !testCase.allow, queried)
			require.Error(t, err)

			if !testCase.allow {
				assert.Contains(t, err.Error(), "uncommitted or untracked files")
			} else {
				assert.NotContains(t, err.Error(), "uncommitted or untracked files")
			}
		})
	}
}

func TestRenderCmd_ChangelogCommentRequiresValue(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	cmd := componentcmds.NewRenderCmd()
	cmd.SetArgs([]string{"--changelog-comment"})
	require.ErrorContains(t, cmd.ExecuteContext(testEnv.Env), "flag needs an argument")
}

func TestRenderCmd_NoOutputDir(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := componentcmds.NewRenderCmd()
	cmd.SetArgs([]string{"-a"})

	err := cmd.ExecuteContext(testEnv.Env)

	// Without config rendered-specs-dir or -o, render should fail.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no output directory configured")
}

func TestRenderCmd_CleanStaleRequiresAll(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := componentcmds.NewRenderCmd()
	cmd.SetArgs([]string{"-o", "SPECS", "--clean-stale", "some-component"})

	err := cmd.ExecuteContext(testEnv.Env)

	// --clean-stale without -a should fail.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--clean-stale requires -a")
}

func TestRenderCmd_CleanStaleCustomOutDirRequiresForce(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := componentcmds.NewRenderCmd()
	cmd.SetArgs([]string{"-a", "-o", "SPECS", "--clean-stale"})

	err := cmd.ExecuteContext(testEnv.Env)

	// --clean-stale with -o but without -f should fail.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--clean-stale with --output-dir requires --force")
}
