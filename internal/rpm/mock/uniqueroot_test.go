// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mock_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUniqueExtensionLabels(t *testing.T) {
	tests := []struct {
		name       string
		action     string
		components []string
		prefix     string
	}{
		{name: "shell without packages", action: "shell", prefix: "shl-"},
		{name: "shell with packages", action: "shell", components: []string{"curl"}, prefix: "shl-curl-"},
		{name: "build without components", action: "build", prefix: "bld-"},
		{name: "build with one component", action: "build", components: []string{"curl"}, prefix: "bld-curl-"},
		{name: "advanced build", action: "build-rpms", components: []string{"curl"}, prefix: "bld-curl-"},
		{name: "render without components", action: "render", prefix: "rndr-"},
		{name: "render with one component", action: "render", components: []string{"curl"}, prefix: "rndr-curl-"},
		{name: "query without components", action: "query", prefix: "qry-"},
		{name: "query with one component", action: "query", components: []string{"curl"}, prefix: "qry-curl-"},
		{
			name: "exact middle field and total limits", action: "render",
			components: []string{"abcdefghijkl"}, prefix: "rndr-abcdefghijkl-",
		},
		{
			name: "middle field over limit", action: "render",
			components: []string{"abcdefghijklm"}, prefix: "rndr-abcdefghijkl-",
		},
		{
			name: "middle field capped with shorter action", action: "build",
			components: []string{"abcdefghijklm"}, prefix: "bld-abcdefghijkl-",
		},
		{
			name: "multiple components", action: "render",
			components: []string{"curl", "zlib"}, prefix: "rndr-c_2-",
		},
		{
			name: "large batch", action: "render",
			components: append([]string{"curl"}, make([]string, 999)...), prefix: "rndr-c_1000-",
		},
		{
			name: "large shell package list", action: "shell",
			components: append([]string{"curl"}, make([]string, 999)...), prefix: "shl-c_1000-",
		},
		{
			name: "two digit batch count", action: "build",
			components: append([]string{"curl"}, make([]string, 9)...), prefix: "bld-c_10-",
		},
		{
			name: "three digit batch count", action: "build",
			components: append([]string{"curl"}, make([]string, 99)...), prefix: "bld-c_100-",
		},
		{
			name: "long component label", action: "query",
			components: []string{strings.Repeat("rust-component", 10)}, prefix: "qry-rust-compone-",
		},
		{name: "long action without components", action: "build-source-rpms", prefix: "build-so-"},
		{name: "allowed character boundaries", action: "AZ_az-09", prefix: "az_az-09-"},
		{
			name: "unsafe characters", action: "BUILD", components: []string{"a/b \t\u00e9"},
			prefix: "bld-a-b-",
		},
		{name: "empty action", prefix: "mock-"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := newTestCtxWithMockPrereqsPresent()
			ctx.Ctx = mock.WithUniqueRoots(ctx.Ctx, true, test.action)
			ctx.Ctx = mock.WithRootComponents(ctx.Ctx, test.components...)

			runner := mock.NewRunner(ctx, testMockConfigPath)
			extension := runner.UniqueExtension()

			assert.LessOrEqual(t, len(extension), 24)
			assert.Regexp(t, `^[a-z0-9_-]+$`, extension)
			assert.True(t, strings.HasPrefix(extension, test.prefix), extension)
			assert.Regexp(t, `^[a-z2-7]{6}$`, strings.TrimPrefix(extension, test.prefix))
		})
	}
}

func TestUniqueExtensionLifecycle(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()
	ctx.Ctx = mock.WithUniqueRoots(ctx.Ctx, true, "build")
	ctx.Ctx = mock.WithRootComponents(ctx.Ctx, "curl")

	var extensions []string

	capture := func(cmd *exec.Cmd) {
		t.Helper()

		index := slices.Index(cmd.Args, "--uniqueext")
		require.Positive(t, index)
		require.Less(t, index+1, len(cmd.Args))
		extensions = append(extensions, cmd.Args[index+1])
	}
	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		capture(cmd)

		return nil
	}
	ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		capture(cmd)

		return testMockRootPath, nil
	}

	runner := mock.NewRunner(ctx, testMockConfigPath)
	require.NoError(t, runner.InitRoot(ctx))
	require.NoError(t, runner.InstallPackages(ctx, []string{"rpm-build"}))
	require.NoError(t, runner.BuildSRPM(ctx, testSpecPath, testSourceDirPath, testOutputDirPath, mock.SRPMBuildOptions{}))
	require.NoError(t, runner.BuildRPM(ctx, testSRPMPath, testOutputDirPath, mock.RPMBuildOptions{}))
	_, err := runner.GetRootPath(ctx)
	require.NoError(t, err)

	cmd, err := runner.Clone().CmdInChroot(ctx, []string{"true"}, false)
	require.NoError(t, err)
	require.NoError(t, cmd.Run(ctx))
	require.NoError(t, runner.Clone().ScrubRoot(ctx))

	require.Len(t, extensions, 7)

	for _, extension := range extensions {
		assert.Equal(t, runner.UniqueExtension(), extension)
	}

	assert.NotEqual(t, runner.UniqueExtension(), mock.NewRunner(ctx, testMockConfigPath).UniqueExtension())
}

func TestUniqueExtensionDisabled(t *testing.T) {
	for name, explicitlyDisabled := range map[string]bool{"default": false, "explicit": true} {
		t.Run(name, func(t *testing.T) {
			ctx := newTestCtxWithMockPrereqsPresent()
			if explicitlyDisabled {
				ctx.Ctx = mock.WithUniqueRoots(ctx.Ctx, false, "build")
			}

			ctx.Ctx = mock.WithRootComponents(ctx.Ctx, "curl")
			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				assert.NotContains(t, cmd.Args, "--uniqueext")

				return nil
			}

			runner := mock.NewRunner(ctx, testMockConfigPath)
			assert.Empty(t, runner.UniqueExtension())
			require.NoError(t, runner.InitRoot(ctx))
		})
	}
}
