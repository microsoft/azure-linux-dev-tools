// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestBuildSyntheticCommits_SymlinkedProjectPath(t *testing.T) {
	const (
		componentName  = "curl"
		importCommit   = "1111111111111111111111111111111111111111"
		upstreamCommit = "2222222222222222222222222222222222222222"
	)

	tempDir := t.TempDir()
	realParentDir := filepath.Join(tempDir, "real")
	realProjectDir := filepath.Join(realParentDir, "project")
	linkedProjectDir := filepath.Join(tempDir, "link", "project")

	osFS := afero.NewOsFs()

	require.NoError(t, fileutils.MkdirAll(osFS, filepath.Join(realProjectDir, "locks")))
	require.NoError(t, os.Symlink(realParentDir, filepath.Join(tempDir, "link")))

	require.NoError(t, fileutils.WriteFile(osFS,
		filepath.Join(realProjectDir, projectconfig.DefaultConfigFileName),
		[]byte("[components."+componentName+"]\n"), fileperms.PublicFile))

	require.NoError(t, fileutils.WriteFile(osFS,
		filepath.Join(realProjectDir, "locks", componentName+".lock"),
		[]byte(strings.Join([]string{
			"version = 1",
			`import-commit = "` + importCommit + `"`,
			`upstream-commit = "` + upstreamCommit + `"`,
			`input-fingerprint = "sha256:abc"`,
		}, "\n")+"\n"), fileperms.PublicFile))

	repo, err := gogit.PlainInit(realProjectDir, false)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, worktree.AddWithOptions(&gogit.AddOptions{All: true}))

	commitHash, err := worktree.Commit("add curl lock", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "azldev",
			Email: "azldev@local",
			When:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	lockPath := filepath.Join(realProjectDir, "locks", componentName+".lock")
	lockContent, err := fileutils.ReadFile(osFS, lockPath)
	require.NoError(t, err)
	require.NoError(t, fileutils.WriteFile(osFS, lockPath,
		[]byte(strings.ReplaceAll(string(lockContent), "sha256:abc", "sha256:def")), fileperms.PublicFile))
	require.NoError(t, worktree.AddWithOptions(&gogit.AddOptions{All: true}))
	secondHash, err := worktree.Commit("update curl overlay", &gogit.CommitOptions{
		Author: &object.Signature{
			Name: "azldev", Email: "azldev@local",
			When: time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	_, config, err := projectconfig.LoadProjectConfig(
		osFS, testctx.NewTestOSEnv(), linkedProjectDir, true, t.TempDir(),
		nil, false, false,
	)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(linkedProjectDir, "locks"), config.Project.LockDir,
		"precondition: the loaded config keeps the unresolved path")

	component, ok := config.Components[componentName]
	require.True(t, ok)

	for _, declarationFree := range []bool{false, true} {
		t.Run(fmt.Sprintf("declaration-free=%t", declarationFree), func(t *testing.T) {
			component := component
			if declarationFree {
				component.SourceConfigFile = nil
				component.Locked = &projectconfig.ComponentLockData{
					UpstreamCommit: upstreamCommit,
					ImportCommit:   importCommit,
				}
			}

			var loggedPaths []string

			cmdFactory := testctx.NewTestCmdFactory()
			cmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				loggedPaths = append(loggedPaths, cmd.Args[len(cmd.Args)-1])

				return strings.Join([]string{
					secondHash.String(), "azldev", "azldev@local", "1717286400", "update curl overlay",
				}, "\x00") + "\x01" + strings.Join([]string{
					commitHash.String(), "azldev", "azldev@local", "1717200000", "add curl lock",
				}, "\x00") + "\x01", nil
			}

			changes, gotImportCommit, err := buildSyntheticCommits(
				t.Context(), cmdFactory, &component, componentName, config.Project.LockDir, "",
			)
			require.NoError(t, err)
			assert.Equal(t, []string{filepath.Join("locks", componentName+".lock")}, loggedPaths)
			assert.Equal(t, importCommit, gotImportCommit)
			require.Len(t, changes, 2)
			assert.Equal(t, commitHash.String(), changes[0].Hash)
			assert.Equal(t, secondHash.String(), changes[1].Hash)
			assert.Equal(t, upstreamCommit, changes[1].UpstreamCommit)

			ctx := testctx.NewCtx()
			specPath := writeStaticReleaseTestSpec(t, ctx.FS(), componentName, "1%{?dist}")
			component.Release.Calculation = projectconfig.ReleaseCalculationStatic
			comp := mockComponent(gomock.NewController(t), componentName, &component)
			preparer := newTestPreparer(ctx.FS())
			require.NoError(t, preparer.tryBumpRelease(
				t.Context(), comp, filepath.Dir(specPath), changes))
			release, err := GetReleaseTagValue(ctx.FS(), specPath)
			require.NoError(t, err)
			assert.Equal(t, "3%{?dist}", release)
		})
	}

	lockedConfig := &projectconfig.ComponentConfig{Locked: &projectconfig.ComponentLockData{}}
	_, _, err = openProjectRepo(lockedConfig, componentName, tempDir)
	require.ErrorContains(t, err, "failed to find project repository")
	_, _, err = openProjectRepo(lockedConfig, componentName, "")
	require.ErrorContains(t, err, "absolute lock directory required")
}

func TestBuildSyntheticCommits_UndeclaredComponentWithoutLock(t *testing.T) {
	config := &projectconfig.ComponentConfig{
		Name: "local",
		Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal},
	}
	changes, importCommit, err := buildSyntheticCommits(
		t.Context(), testctx.NewTestCmdFactory(), config, config.Name, "/not-a-project/locks", "",
	)
	require.NoError(t, err)
	assert.Empty(t, changes)
	assert.Empty(t, importCommit)
}
