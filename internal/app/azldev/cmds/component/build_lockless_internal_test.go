// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/require"
)

func TestLocklessBuildRejectsSyntheticHistoryFlags(t *testing.T) {
	for _, options := range []ComponentBuildOptions{{WithoutGitRepo: true}, {RPMDevBumpspec: true}} {
		env := testutils.NewTestEnv(t)
		env.Config.LocklessComponents = map[string]bool{"curl": true}
		env.Config.Components["curl"] = projectconfig.ComponentConfig{Name: "curl"}
		comp := resolveComponents(t, env, "curl")[0]
		_, err := BuildComponent(env.Env, comp, nil, &options)
		require.ErrorContains(t, err, "does not support '--without-git' or '--rpmdev-bumpspec'")
	}
}

func TestLocklessBuildUsesDistGitAndCleansUp(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "build failure"}[fail], func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			env.Config.Project.RenderedSpecsDir = "/project/specs"
			env.Config.LocklessComponents = map[string]bool{"curl": true}
			env.Config.Components["curl"] = projectconfig.ComponentConfig{Name: "curl"}
			env.CmdFactory.RegisterCommandInSearchPath("git")
			env.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)

			var cloneDir string

			sentinel := errors.New("injected SRPM failure")
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				if cmd.Args[0] != "git" {
					return "/mock/root", nil
				}

				switch cmd.Args[3] {
				case "rev-parse":
					if cmd.Args[4] == "--git-common-dir" {
						return filepath.Join("/project", ".git"), nil
					}

					return "/project", nil
				case "clone":
					cloneDir = cmd.Args[len(cmd.Args)-1]
					require.Equal(t, []string{"--single-branch", "--no-local", "--"}, cmd.Args[4:7])
					require.NoError(t, fileutils.MkdirAll(env.FS(), cloneDir))
				case "filter-repo":
					if slices.Contains(cmd.Args, "--subdirectory-filter") {
						require.Equal(t, "specs/c/curl", cmd.Args[len(cmd.Args)-1])
						require.NoError(t, fileutils.WriteFile(env.FS(), filepath.Join(cloneDir, "curl.spec"),
							[]byte("committed spec"), fileperms.PublicFile))
					}
				}

				return "", nil
			}
			built := false
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				require.NotContains(t, strings.Join(cmd.Args, " "), "calculate-release")

				if !slices.Contains(cmd.Args, "--buildsrpm") {
					return nil
				}

				built = true

				require.Contains(t, strings.Join(cmd.Args, " "), "--enable-plugin rpmautospec")
				require.Equal(t, cloneDir, cmd.Args[slices.Index(cmd.Args, "--sources")+1])

				if fail {
					return sentinel
				}

				out := cmd.Args[slices.Index(cmd.Args, "--resultdir")+1]

				return fileutils.WriteFile(env.FS(), filepath.Join(out, "curl.src.rpm"), []byte("srpm"), fileperms.PublicFile)
			}
			factory, err := workdir.NewFactory(env.FS(), env.Env.WorkDir(), env.Env.ConstructionTime())
			require.NoError(t, err)
			comp := resolveComponents(t, env, "curl")[0]

			results, err := BuildComponent(env.Env, comp, factory, &ComponentBuildOptions{SourcePackageOnly: true})
			if fail {
				require.ErrorIs(t, err, sentinel)
			} else {
				require.NoError(t, err)
				require.Len(t, results.SRPMPaths, 1)
			}

			require.True(t, built)

			exists, err := fileutils.Exists(env.FS(), cloneDir)
			require.NoError(t, err)
			require.False(t, exists)
		})
	}
}
