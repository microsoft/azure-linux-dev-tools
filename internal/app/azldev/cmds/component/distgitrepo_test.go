// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	distGitTestOutput     = "/exports/component"
	distGitTestProject    = "/project"
	distGitTestRevParse   = "rev-parse"
	distGitTestCommonDir  = "--git-common-dir"
	distGitTestClone      = "clone"
	distGitTestFilterRepo = "filter-repo"
	distGitTestMetadata   = "/project/.git"
)

func newDistGitRepoTestEnv(t *testing.T, name string) *testutils.TestEnv {
	t.Helper()

	env := testutils.NewTestEnv(t)
	env.Config.Components[name] = projectconfig.ComponentConfig{Name: name}
	env.Config.Project.RenderedSpecsDir = "/project/specs"
	env.CmdFactory.RegisterCommandInSearchPath("git")

	return env
}

func TestDistGitRepoCmd_Arguments(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"curl", "vim", "-o", distGitTestOutput},
		{"curl"},
		{"curl", "-o", ""},
		{"unknown-component", "-o", distGitTestOutput},
		{"cu*", "-o", distGitTestOutput},
	} {
		t.Run(filepath.Join(args...), func(t *testing.T) {
			env := newDistGitRepoTestEnv(t, "curl")
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				t.Fatalf("unexpected command: %v", cmd.Args)

				return "", nil
			}
			cmd := componentcmds.NewDistGitRepoCmd()
			cmd.SetArgs(args)
			require.Error(t, cmd.ExecuteContext(env.Env))
		})
	}
}

func TestCreateComponentDistGitRepo(t *testing.T) {
	for _, testCase := range []struct {
		name, component, specDir, wantSubdir string
		force                                bool
	}{
		{name: "default path", component: "curl", wantSubdir: "specs/c/curl"},
		{name: "custom relative path", component: "curl", specDir: "build/rendered", wantSubdir: "build/rendered/c/curl"},
		{name: "uppercase component", component: "SLOF", specDir: "/project/rendered", wantSubdir: "rendered/s/SLOF"},
		{name: "replace existing directory", component: "curl", wantSubdir: "specs/c/curl", force: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := newDistGitRepoTestEnv(t, testCase.component)

			env.Config.Project.RenderedSpecsDir = testCase.specDir
			if testCase.force {
				require.NoError(t, fileutils.MkdirAll(env.TestFS, distGitTestOutput))
				require.NoError(t, fileutils.WriteFile(env.TestFS, distGitTestOutput+"/stale", []byte("keep"),
					fileperms.PublicFile))
			}

			var commands [][]string

			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				commands = append(commands, cmd.Args)
				switch cmd.Args[3] {
				case distGitTestRevParse:
					if cmd.Args[4] == distGitTestCommonDir {
						return distGitTestMetadata, nil
					}

					return distGitTestProject, nil
				case distGitTestClone:
					exists, err := fileutils.Exists(env.TestFS, distGitTestOutput)
					require.NoError(t, err)
					assert.False(t, exists, "clone must receive a fresh destination")
					require.NoError(t, fileutils.MkdirAll(env.TestFS, distGitTestOutput))
				}

				return "", nil
			}
			result, err := componentcmds.CreateComponentDistGitRepo(env.Env, &componentcmds.DistGitRepoOptions{
				Component: testCase.component, OutputDir: distGitTestOutput, Force: testCase.force,
			})
			require.NoError(t, err)
			assert.Equal(t, testCase.component, result.Component)
			assert.Equal(t, distGitTestOutput, result.OutputDir)
			assert.Equal(t, [][]string{
				{"git", "-C", distGitTestProject, distGitTestFilterRepo, "--version"},
				{"git", "-C", distGitTestProject, distGitTestRevParse, "--show-toplevel"},
				{"git", "-C", distGitTestProject, distGitTestRevParse, distGitTestCommonDir},
				{
					"git", "-C", distGitTestProject, distGitTestClone, "--single-branch", "--no-local", "--",
					distGitTestProject, distGitTestOutput,
				},
				{"git", "-C", distGitTestOutput, distGitTestFilterRepo, "--subdirectory-filter", testCase.wantSubdir},
			}, commands)
		})
	}
}

func TestCreateComponentDistGitRepo_ExistingOutput(t *testing.T) {
	for _, nonempty := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "nonempty"}[nonempty], func(t *testing.T) {
			env := newDistGitRepoTestEnv(t, "curl")
			require.NoError(t, fileutils.MkdirAll(env.TestFS, distGitTestOutput))

			if nonempty {
				require.NoError(t, fileutils.WriteFile(env.TestFS, distGitTestOutput+"/keep", []byte("keep"),
					fileperms.PublicFile))
			}

			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				t.Fatalf("unexpected command: %v", cmd.Args)

				return "", nil
			}
			_, err := componentcmds.CreateComponentDistGitRepo(env.Env, &componentcmds.DistGitRepoOptions{
				Component: "curl", OutputDir: distGitTestOutput,
			})
			require.ErrorContains(t, err, "already exists")
			require.ErrorContains(t, err, "--force")
			exists, err := fileutils.DirExists(env.TestFS, distGitTestOutput)
			require.NoError(t, err)
			assert.True(t, exists)
		})
	}
}

func TestCreateComponentDistGitRepo_FailuresPreserveOutput(t *testing.T) {
	for _, step := range []string{distGitTestFilterRepo, distGitTestRevParse} {
		t.Run(step, func(t *testing.T) {
			env := newDistGitRepoTestEnv(t, "curl")
			require.NoError(t, fileutils.MkdirAll(env.TestFS, distGitTestOutput))

			const sentinel = distGitTestOutput + "/keep"
			require.NoError(t, fileutils.WriteFile(env.TestFS, sentinel, []byte("original"), fileperms.PublicFile))

			failure := errors.New("preflight failed")
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.NotEqual(t, distGitTestClone, cmd.Args[3])

				if cmd.Args[3] == step {
					return "", failure
				}

				return distGitTestProject, nil
			}
			_, err := componentcmds.CreateComponentDistGitRepo(env.Env, &componentcmds.DistGitRepoOptions{
				Component: "curl", OutputDir: distGitTestOutput, Force: true,
			})
			require.ErrorIs(t, err, failure)

			if step == distGitTestFilterRepo {
				require.ErrorContains(t, err, "git-filter-repo must be installed to use the dist-git-repo command")
			}

			content, err := fileutils.ReadFile(env.TestFS, sentinel)
			require.NoError(t, err)
			assert.Equal(t, "original", string(content))
		})
	}
}

func TestCreateComponentDistGitRepo_UnsafeOutput(t *testing.T) {
	for _, output := range []string{
		"/", "/project", "/project/..", "/project/.git", "/project/.git/objects",
		"/shared", "/shared/git", "/shared/git/objects",
	} {
		t.Run(output, func(t *testing.T) {
			env := newDistGitRepoTestEnv(t, "curl")
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.NotEqual(t, distGitTestClone, cmd.Args[3])

				if cmd.Args[3] == distGitTestRevParse {
					if cmd.Args[4] == distGitTestCommonDir {
						return "/shared/git", nil
					}

					return distGitTestProject, nil
				}

				return "", nil
			}
			_, err := componentcmds.CreateComponentDistGitRepo(env.Env, &componentcmds.DistGitRepoOptions{
				Component: "curl", OutputDir: output, Force: true,
			})
			require.ErrorContains(t, err, "output directory")
			exists, err := fileutils.Exists(env.TestFS, "/project/azldev.toml")
			require.NoError(t, err)
			assert.True(t, exists, "source project must remain intact")
		})
	}
}

func TestCreateComponentDistGitRepo_CommandFailures(t *testing.T) {
	for _, step := range []string{distGitTestClone, "filter"} {
		t.Run(step, func(t *testing.T) {
			env := newDistGitRepoTestEnv(t, "curl")
			failure := errors.New("export failed")

			var cloned bool

			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				switch cmd.Args[3] {
				case distGitTestRevParse:
					if cmd.Args[4] == distGitTestCommonDir {
						return "/project/.git", nil
					}

					return distGitTestProject, nil
				case distGitTestClone:
					if step == distGitTestClone {
						return "", failure
					}

					cloned = true
				case distGitTestFilterRepo:
					if cmd.Args[4] == "--subdirectory-filter" {
						assert.True(t, cloned)

						return "", failure
					}
				}

				return "", nil
			}
			_, err := componentcmds.CreateComponentDistGitRepo(env.Env, &componentcmds.DistGitRepoOptions{
				Component: "curl", OutputDir: distGitTestOutput,
			})
			require.ErrorIs(t, err, failure)
		})
	}
}

func TestCreateComponentDistGitRepo_DryRun(t *testing.T) {
	env := newDistGitRepoTestEnv(t, "curl")
	dryEnv := azldev.NewEnv(t.Context(), azldev.EnvOptions{
		ProjectDir: "/project", Config: env.Config, Interfaces: env.TestInterfaces,
		EventListener: env.EventListener, DryRunnable: azldev.NewAppDryRunnable(true),
	})
	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		t.Fatalf("unexpected command: %v", cmd.Args)

		return "", nil
	}
	_, err := componentcmds.CreateComponentDistGitRepo(dryEnv, &componentcmds.DistGitRepoOptions{
		Component: "curl", OutputDir: distGitTestOutput,
	})
	require.NoError(t, err)
	exists, err := fileutils.Exists(env.TestFS, distGitTestOutput)
	require.NoError(t, err)
	assert.False(t, exists)
}
