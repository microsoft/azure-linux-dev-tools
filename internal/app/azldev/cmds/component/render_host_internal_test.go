// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestHostRenderAutoreleaseCommands(t *testing.T) {
	for _, operation := range []string{"changelog", "base", "provenance"} {
		t.Run(operation, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			env.CmdFactory.RegisterCommandInSearchPath("rpmautospec")
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.Equal(t, "/working", cmd.Dir)

				switch operation {
				case "changelog":
					assert.Equal(t, []string{"rpmautospec", "generate-changelog", "curl.spec"}, cmd.Args)
				case "base":
					assert.Equal(t, []string{"rpmautospec", "calculate-release", "--number-only", "curl.spec"}, cmd.Args)
				case "provenance":
					assert.Equal(t, []string{"rpmautospec", "calculate-release", "--complete-release", "curl.spec"}, cmd.Args)
				}

				return "unchanged output\n", nil
			}
			processor := hostRenderProcessor{env: env.Env}

			var (
				out string
				err error
			)

			switch operation {
			case "changelog":
				out, err = processor.GenerateChangelog(env.Env, "/working", "curl.spec")
			case "base":
				out, err = processor.CalculateBaseRelease(env.Env, "/working", "curl.spec")
			case "provenance":
				out, err = processor.CalculateRelease(env.Env, "/working", "curl.spec")
			}

			require.NoError(t, err)
			assert.Equal(t, "unchanged output\n", out)
		})
	}
}

func TestHostRenderExpandRelease(t *testing.T) {
	const missingTool = "missing tool"

	for _, phase := range []string{"diagnostics", "missing release", "query failure", missingTool} {
		t.Run(phase, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			if phase != missingTool {
				env.CmdFactory.RegisterCommandInSearchPath("rpmspec")
			}

			queryErr := errors.New("query failed")
			called := false
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				called = true

				assert.Equal(t, "/committed", cmd.Dir)
				assert.Equal(t, []string{
					"rpmspec", "-q", "--srpm", "--queryformat", "release=%{RELEASE}\n",
					"--define", "_sourcedir /committed", "--define", "_specdir /committed", "curl.spec",
				}, cmd.Args)

				switch phase {
				case "query failure":
					return "", queryErr
				case "missing release":
					return "error: %changelog not in descending chronological order\nrelease=\n", nil
				default:
					return "error: %changelog not in descending chronological order\n" +
						"release=9.fc43\nwarning: trailing diagnostic\n", nil
				}
			}
			out, err := (hostRenderProcessor{env: env.Env}).ExpandRelease(env.Env, "/committed", "curl.spec")

			assert.Equal(t, phase != missingTool, called)

			switch phase {
			case missingTool:
				require.ErrorContains(t, err, "requires host program")
			case "query failure":
				require.ErrorIs(t, err, queryErr)
			case "missing release":
				require.ErrorContains(t, err, "no Release field")
			default:
				require.NoError(t, err)
				assert.Equal(t, "9.fc43", out)
			}
		})
	}
}

func TestHostRenderSpecFiles(t *testing.T) {
	env := testutils.NewTestEnv(t)
	env.CmdFactory.RegisterCommandInSearchPath("spectool")
	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		assert.Equal(t, "/working", cmd.Dir)
		assert.Equal(t, []string{
			"spectool", "--define", "_sourcedir /working", "-l", "-a", "curl.spec",
		}, cmd.Args)

		return "Source0: https://example.com/curl-1.tar.gz\nPatch0: fixes/fix.patch\n", nil
	}
	files, err := (hostRenderProcessor{env: env.Env}).specFiles(env.Env, "/working", "curl.spec")
	require.NoError(t, err)
	assert.Equal(t, []string{"curl-1.tar.gz", "fixes/fix.patch", "changelog"}, files)
}

func TestMixedRenderRoutesOnlyLockedComponentsThroughMock(t *testing.T) {
	const (
		hostFailure = "host failure"
		mockFailure = "mock failure"
		skipFilter  = "skip host filter"
	)

	for _, phase := range []string{"success", hostFailure, mockFailure, skipFilter} {
		t.Run(phase, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			env.CmdFactory.RegisterCommandInSearchPath("spectool")
			env.CmdFactory.RegisterCommandInSearchPath("mock")

			ctrl := gomock.NewController(t)
			locked := components_testutils.NewMockComponent(ctrl)
			locked.EXPECT().GetName().AnyTimes().Return("curl")

			host := components_testutils.NewMockComponent(ctrl)
			host.EXPECT().GetName().AnyTimes().Return("rust-asyncband")
			host.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				Render: projectconfig.ComponentRenderConfig{SkipFileFilter: phase == skipFilter},
			})

			env.Config.LocklessComponents = map[string]bool{"rust-asyncband": true}
			processor, err := renderMockProcessor(env.Env, []components.Component{host, locked})
			require.NoError(t, err)
			require.NotNil(t, processor)

			queryErr := errors.New("host query failed")
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.NotEqual(t, skipFilter, phase)
				assert.Equal(t, "spectool", cmd.Args[0])
				assert.Equal(t, "/stage/rust-asyncband", cmd.Dir)

				if phase == hostFailure {
					return "", queryErr
				}

				return "Patch0: host.patch\n", nil
			}
			batched := false
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				assert.Equal(t, "mock", cmd.Args[0])

				if phase == mockFailure {
					return errors.New("mock unavailable")
				}

				if strings.Contains(strings.Join(cmd.Args, " "), "render_process.py") {
					batched = true
					inputs, err := fileutils.ReadFile(env.TestFS, "/stage/inputs.json")
					require.NoError(t, err)
					assert.JSONEq(t, `[{"name":"curl","specFilename":"curl.spec"}]`, string(inputs))
					require.NoError(t, fileutils.WriteFile(env.TestFS, "/stage/results.json",
						[]byte(`[{"name":"curl","specFiles":"Patch0: locked.patch\n","error":null}]`),
						fileperms.PublicFile))
				}

				return nil
			}
			results := processRenderSources(env.Env, processor,
				"/stage", []*preparedComponent{
					{comp: locked, specFilename: "curl.spec"},
					{comp: host, specFilename: "rust-asyncband.spec", lockless: true},
				})
			require.Contains(t, results, "rust-asyncband")
			assert.Equal(t, phase != mockFailure, batched)

			if phase == hostFailure {
				require.ErrorIs(t, results["rust-asyncband"].Error, queryErr)
			} else {
				require.NoError(t, results["rust-asyncband"].Error)

				if phase != skipFilter {
					assert.Equal(t, []string{"host.patch", "changelog"}, results["rust-asyncband"].SpecFiles)
				}
			}

			if phase == mockFailure {
				assert.NotContains(t, results, "curl")
			} else {
				require.Contains(t, results, "curl")
				assert.Equal(t, []string{"locked.patch"}, results["curl"].SpecFiles)
			}
		})
	}
}
