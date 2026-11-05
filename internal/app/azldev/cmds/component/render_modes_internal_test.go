// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestReadLocklessComponents(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		lines   []string
		missing bool
		want    map[string]bool
	}{
		{name: "missing file", missing: true, want: map[string]bool{}},
		{name: "empty file", want: map[string]bool{}},
		{
			name:  "exact names with whitespace and duplicates",
			lines: []string{" rust-asyncband \r", "", "\tcurl\t", "rust-asyncband", "Curl"},
			want:  map[string]bool{"rust-asyncband": true, "curl": true, "Curl": true},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			require.NoError(t, fileutils.WriteFile(
				env.TestFS, "/project/subdir/lockless-components.txt", []byte("wrong-component"), fileperms.PublicFile,
			))
			require.NoError(t, env.TestOSEnv.Chdir("/project/subdir"))

			if !testCase.missing {
				require.NoError(t, fileutils.WriteFile(
					env.TestFS, "/project/lockless-components.txt",
					[]byte(strings.Join(testCase.lines, "\n")), fileperms.PublicFile,
				))
			}

			names, err := projectconfig.ReadLocklessComponents(env.TestFS, env.Env.ProjectDir())
			require.NoError(t, err)
			assert.Equal(t, testCase.want, names)
		})
	}

	t.Run("read errors are not treated as an empty list", func(t *testing.T) {
		env := testutils.NewTestEnv(t)
		require.NoError(t, fileutils.MkdirAll(env.TestFS, "/project/lockless-components.txt"))

		_, err := projectconfig.ReadLocklessComponents(env.TestFS, env.Env.ProjectDir())
		require.ErrorContains(t, err, "lockless-components.txt")
	})
}

type renderModeObservingPreparer struct {
	sources.SourcePreparer
	observe func(components.Component)
	err     error
	prepare func(components.Component, string) error
}

func (p *renderModeObservingPreparer) PrepareSources(
	_ context.Context, component components.Component, dir string, _ bool,
) error {
	p.observe(component)

	if p.prepare != nil {
		return p.prepare(component, dir)
	}

	return p.err
}

func TestRenderComponentsRequiresCleanLocklessUpstreamRepository(t *testing.T) {
	statusErr := errors.New("git status failed")

	for _, testCase := range []struct {
		name, status, wantError              string
		allow, checkOnly, noGitCommit, mixed bool
		statusErr                            error
	}{
		{name: "clean"},
		{name: "unstaged tracked", status: " M tracked\n", wantError: "uncommitted or untracked files"},
		{name: "staged tracked", status: "M  tracked\n", wantError: "uncommitted or untracked files"},
		{name: "untracked", status: "?? scratch\n", wantError: "uncommitted or untracked files"},
		{name: "explicit override", status: "?? scratch\n", allow: true},
		{
			name: "check-only still checks", status: " M tracked\n",
			checkOnly: true, wantError: "uncommitted or untracked files",
		},
		{
			name: "no-git-commit still checks", status: " M tracked\n",
			noGitCommit: true, wantError: "uncommitted or untracked files",
		},
		{
			name: "mixed selection stops before preparation", status: "?? scratch\n",
			mixed: true, wantError: "uncommitted or untracked files",
		},
		{name: "status failure", statusErr: statusErr, wantError: "checking project repository"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnvWithoutLockfile(t)
			if testCase.mixed {
				env = testutils.NewTestEnv(t)
				env.AddUpstreamComponent(t, "bash")
				env.Config.LocklessComponents = map[string]bool{"curl": true}
			}

			env.Config.Components["curl"] = projectconfig.ComponentConfig{
				Name: "curl",
				Spec: projectconfig.SpecSource{
					SourceType: projectconfig.SpecSourceTypeUpstream, UpstreamCommit: "configured-commit",
				},
			}
			queried := false
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				queried = true

				assert.Equal(t, []string{
					"git", "-C", "/project", "status", "--porcelain=v1",
					"--untracked-files=all", "--ignore-submodules=none",
				}, cmd.Args)

				return testCase.status, testCase.statusErr
			}
			started := false
			original := newSourcePreparer
			newSourcePreparer = func(
				_ sourceproviders.SourceManager, _ opctx.FS, _ opctx.EventListener, _ opctx.DryRunnable,
				_ ...sources.PreparerOption,
			) (sources.SourcePreparer, error) {
				started = true

				return &renderModeObservingPreparer{
					observe: func(_ components.Component) {},
					err:     errors.New("stop after preflight"),
				}, nil
			}

			t.Cleanup(func() { newSourcePreparer = original })

			results, err := RenderComponents(env.Env, &RenderOptions{
				ComponentFilter:   components.ComponentFilter{IncludeAllComponents: true},
				OutputDir:         "/project/SPECS",
				OutputDirExplicit: true,
				AllowUncommitted:  testCase.allow,
				CheckOnly:         testCase.checkOnly,
				NoGitCommit:       testCase.noGitCommit,
			})
			assert.Equal(t, !testCase.allow, queried)

			if testCase.wantError != "" {
				require.ErrorContains(t, err, testCase.wantError)
				assert.Empty(t, results)
				assert.False(t, started, "refuse before fetching any selected component")

				if testCase.statusErr != nil {
					require.ErrorIs(t, err, statusErr)
				} else {
					assert.Contains(t, err.Error(), "--allow-uncommitted")
				}
			} else {
				require.NoError(t, err)
				assert.True(t, started)
				require.Len(t, results, 1)
				assert.Contains(t, results[0].Error, "stop after preflight")
			}
		})
	}
}

func TestLocklessRenderWorktreeCheckScope(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		source   projectconfig.SpecSourceType
		lockless bool
		checked  bool
	}{
		{name: "locked upstream", source: projectconfig.SpecSourceTypeUpstream},
		{name: "locked local", source: projectconfig.SpecSourceTypeLocal},
		{name: "lockless local", source: projectconfig.SpecSourceTypeLocal, lockless: true},
		{name: "lockless upstream", source: projectconfig.SpecSourceTypeUpstream, lockless: true, checked: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			env.Config.LocklessComponents = map[string]bool{"curl": testCase.lockless, "rust-asyncband": true}
			comp := components_testutils.NewMockComponent(gomock.NewController(t))
			comp.EXPECT().GetName().AnyTimes().Return("curl")
			comp.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				Spec: projectconfig.SpecSource{SourceType: testCase.source},
			})

			queried := false
			env.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
				queried = true

				return "?? scratch\n", nil
			}
			err := validateRenderComponents(env.Env, []components.Component{comp}, false)

			assert.Equal(t, testCase.checked, queried)

			if testCase.checked {
				require.ErrorContains(t, err, "uncommitted or untracked files")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestLocklessRenderPreservesAllFilesWithoutMock(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)
	version := env.Config.Distros["test-distro"].Versions["1.0"]
	version.MockConfigPath = ""
	env.Config.Distros["test-distro"].Versions["1.0"] = version
	env.Config.Components["curl"] = projectconfig.ComponentConfig{
		Name: "curl",
		Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal},
	}
	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		t.Fatalf("unexpected query during lockless rendering: %v", cmd.Args)

		return "", nil
	}
	env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		t.Fatalf("unexpected command during host-only rendering: %v", cmd.Args)

		return nil
	}

	const (
		rawSpec   = "Name: curl\nVersion: 1\nRelease: %autorelease\n\n%changelog\n%autochangelog\n"
		changelog = "* Old entry\n- Keep unchanged\n"
	)

	files := map[string]string{
		"curl.spec": rawSpec, "changelog": changelog, "fix.patch": "patch",
		"unreferenced": "keep", ".gitignore": "upstream ignore rules",
		"extras/nested.txt": "unreferenced nested file",
	}

	original := newSourcePreparer
	newSourcePreparer = func(
		_ sourceproviders.SourceManager, fs opctx.FS, _ opctx.EventListener, _ opctx.DryRunnable,
		_ ...sources.PreparerOption,
	) (sources.SourcePreparer, error) {
		return &renderModeObservingPreparer{
			observe: func(_ components.Component) {},
			prepare: func(_ components.Component, dir string) error {
				for name, content := range files {
					require.NoError(t, fileutils.WriteFile(fs, filepath.Join(dir, name),
						[]byte(content), fileperms.PublicFile))
				}

				return fileutils.WriteFile(fs, filepath.Join(dir, ".git/HEAD"),
					[]byte("temporary history"), fileperms.PublicFile)
			},
		}, nil
	}

	t.Cleanup(func() { newSourcePreparer = original })

	results, err := RenderComponents(env.Env, &RenderOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		OutputDir:       "/rendered", OutputDirExplicit: true, FailOnError: true,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, renderStatusOK, results[0].Status)

	for name, want := range files {
		content, err := fileutils.ReadFile(env.TestFS, filepath.Join("/rendered/c/curl", name))
		require.NoError(t, err)
		assert.Equal(t, want, string(content))
	}

	exists, err := fileutils.Exists(env.TestFS, "/rendered/c/curl/.git")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestRenderComponentsSelectsLockModeFromFile(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		withoutLockfile bool
	}{
		{name: "project component list"},
		{name: "internal whole-project override", withoutLockfile: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			if testCase.withoutLockfile {
				env = testutils.NewTestEnvWithoutLockfile(t)
				env.Config.Project.LockDir = "/project/locks"
			}

			for _, name := range []string{"curl", "rust-asyncband"} {
				env.Config.Components[name] = projectconfig.ComponentConfig{
					Name: name,
					Spec: projectconfig.SpecSource{
						SourceType:     projectconfig.SpecSourceTypeUpstream,
						UpstreamCommit: "configured-commit",
					},
				}
			}

			lock := lockfile.New()
			lock.UpstreamCommit = "configured-commit"
			env.WriteLock(t, "curl", lock)
			require.NoError(t, fileutils.WriteFile(
				env.TestFS, "/project/locks/rust-asyncband.lock", []byte("corrupt lock"), fileperms.PublicFile,
			))
			require.NoError(t, fileutils.WriteFile(
				env.TestFS, "/project/lockless-components.txt", []byte("rust-asyncband\n"), fileperms.PublicFile,
			))
			names, err := projectconfig.ReadLocklessComponents(env.TestFS, env.Env.ProjectDir())
			require.NoError(t, err)

			env.Config.LocklessComponents = names

			var observedMutex sync.Mutex

			observed := make(map[string]*projectconfig.ComponentLockData)
			observedErr := errors.New("stop after observing render mode")
			original := newSourcePreparer
			newSourcePreparer = func(
				_ sourceproviders.SourceManager, _ opctx.FS, _ opctx.EventListener, _ opctx.DryRunnable,
				_ ...sources.PreparerOption,
			) (sources.SourcePreparer, error) {
				return &renderModeObservingPreparer{
					observe: func(comp components.Component) {
						observedMutex.Lock()
						defer observedMutex.Unlock()

						observed[comp.GetName()] = comp.GetConfig().Locked
					},
					err: observedErr,
				}, nil
			}

			t.Cleanup(func() { newSourcePreparer = original })

			results, err := RenderComponents(env.Env, &RenderOptions{
				ComponentFilter:   components.ComponentFilter{IncludeAllComponents: true},
				OutputDir:         "/rendered",
				OutputDirExplicit: true,
			})
			require.NoError(t, err)
			require.Len(t, results, 2)
			require.Len(t, observed, 2)

			if testCase.withoutLockfile {
				assert.Nil(t, observed["curl"], "the internal override applies to every component")
			} else {
				require.NotNil(t, observed["curl"])
				assert.Equal(t, "configured-commit", observed["curl"].UpstreamCommit)
			}

			assert.Nil(t, observed["rust-asyncband"])
			assert.Equal(t, testCase.withoutLockfile, env.Env.WithoutLockfile())
		})
	}
}

func TestRenderComponentsRequiresLocksForUnlistedComponents(t *testing.T) {
	env := testutils.NewTestEnv(t)
	env.AddUpstreamComponent(t, "curl")
	env.Config.Components["rust-asyncband"] = projectconfig.ComponentConfig{
		Name: "rust-asyncband",
		Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream},
	}
	require.NoError(t, fileutils.WriteFile(
		env.TestFS, filepath.Join(env.Env.ProjectDir(), "lockless-components.txt"), []byte("curl\n"), fileperms.PublicFile,
	))
	env.Config.LocklessComponents = map[string]bool{"curl": true}

	_, err := RenderComponents(env.Env, &RenderOptions{
		ComponentFilter:   components.ComponentFilter{IncludeAllComponents: true},
		OutputDir:         "/rendered",
		OutputDirExplicit: true,
	})
	require.ErrorContains(t, err, "lock file validation failed")
}
