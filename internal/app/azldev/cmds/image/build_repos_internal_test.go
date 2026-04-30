// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/kiwi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const addRepoFlag = "--add-repo"

// captureKiwiAddRepoArgs builds a kiwi.Runner against the given setup function and
// returns the list of `--add-repo <arg>` values that the kiwi command would receive.
func captureKiwiAddRepoArgs(t *testing.T, setup func(*kiwi.Runner) error) []string {
	t.Helper()

	ctx := testctx.NewCtx()

	var captured []string
	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		captured = cmd.Args
		return nil
	}

	runner := kiwi.NewRunner(ctx, "/description").WithTargetDir("/output")

	require.NoError(t, setup(runner))
	require.NoError(t, runner.Build(context.Background()))

	var args []string
	for i, a := range captured {
		if a == addRepoFlag && i+1 < len(captured) {
			args = append(args, captured[i+1])
		}
	}
	return args
}

func TestAddKiwiRepoFromResource_BaseURI(t *testing.T) {
	t.Parallel()

	args := captureKiwiAddRepoArgs(t, func(r *kiwi.Runner) error {
		return addKiwiRepoFromResource(r, "test-repo", &projectconfig.RpmRepoResource{
			BaseURI:         "https://example.com/repo/$basearch",
			DisableGPGCheck: true,
		})
	})

	require.Len(t, args, 1)
	parts := strings.Split(args[0], ",")

	// Positional fields per kiwi: source,rpm-md,alias,priority,imageinclude,
	// package_gpgcheck,signing_keys,components,distribution,repo_gpgcheck,repo_sourcetype.
	assert.Equal(t, "https://example.com/repo/$basearch", parts[0])
	assert.Equal(t, "rpm-md", parts[1])
	assert.Equal(t, "test-repo", parts[2], "alias must be the repo name (used as-is for kiwi)")
	// Priority 50 = remote default.
	assert.Equal(t, "50", parts[3])

	// disable-gpg-check=true must turn OFF *both* package_gpgcheck (field 6, index 5)
	// and repo_gpgcheck (field 10, index 9) — they correspond to fields[1] and fields[5]
	// after the first 4 positional fields.
	assert.Equal(t, "false", parts[5], "package_gpgcheck must be false when disable-gpg-check=true (field 6 / index 5)")
	require.GreaterOrEqual(t, len(parts), 10, "expected at least 10 fields when disable-gpg-check=true: %v", parts)
	assert.Equal(t, "false", parts[9], "repo_gpgcheck must be false when disable-gpg-check=true (field 10 / index 9)")
}

func TestAddKiwiRepoFromResource_GPGEnabled_BothChecksOn(t *testing.T) {
	t.Parallel()

	args := captureKiwiAddRepoArgs(t, func(r *kiwi.Runner) error {
		return addKiwiRepoFromResource(r, "signed", &projectconfig.RpmRepoResource{
			BaseURI: "https://example.com/repo",
			GPGKey:  "https://example.com/key.gpg",
		})
	})

	require.Len(t, args, 1)
	// The "false" sentinel for either gpgcheck field must NOT appear when GPG is enabled.
	// (kiwi defaults both to true; we only emit "false" overrides.)
	assert.NotContains(t, args[0], "false",
		"no GPG-disable override should appear when DisableGPGCheck=false (got %q)", args[0])
	// Signing key must be wrapped in {} braces.
	assert.Contains(t, args[0], "{https://example.com/key.gpg}",
		"signing key must be projected as `{key}` in field 7 (got %q)", args[0])
}

func TestAddKiwiRepoFromResource_Metalink(t *testing.T) {
	t.Parallel()

	args := captureKiwiAddRepoArgs(t, func(r *kiwi.Runner) error {
		return addKiwiRepoFromResource(r, "ml", &projectconfig.RpmRepoResource{
			Metalink:        "https://mirrors.example.com/metalink?repo=foo",
			DisableGPGCheck: true,
		})
	})

	require.Len(t, args, 1)
	assert.True(t, strings.HasPrefix(args[0], "https://mirrors.example.com/metalink?repo=foo,rpm-md,ml,"),
		"metalink must be used as the source (got %q)", args[0])
	assert.True(t, strings.HasSuffix(args[0], ",metalink"),
		"sourcetype trailing field must be `metalink` (got %q)", args[0])
}

func TestAddKiwiRepoFromResource_NoSourceErrors(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()
	runner := kiwi.NewRunner(ctx, "/description")
	err := addKiwiRepoFromResource(runner, "broken", &projectconfig.RpmRepoResource{
		DisableGPGCheck: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "neither base-uri nor metalink")
}
