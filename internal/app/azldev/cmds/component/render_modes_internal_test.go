// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
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
}

func (p *renderModeObservingPreparer) PrepareSources(
	_ context.Context, component components.Component, _ string, _ bool,
) error {
	p.observe(component)

	return p.err
}

func TestRenderComponentsSelectsLockModeFromFile(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		withoutLockfile bool
	}{
		{name: "default global mode"},
		{name: "lockless global mode", withoutLockfile: true},
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
				assert.Nil(t, observed["curl"], "the global flag overrides the component list")
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
