// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/upstreamcommit"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const makeLocklessTestPath = "/project/lockless-components.txt"

func TestNewMakeLocklessCmd(t *testing.T) {
	cmd := componentcmds.NewMakeLocklessCmd()
	assert.Equal(t, "make-lockless", cmd.Use)
	assert.NotNil(t, cmd.RunE)
	assert.NotNil(t, cmd.Flags().Lookup("component"))
	assert.NotNil(t, cmd.Flags().Lookup("all-components"))
	assert.NotNil(t, cmd.Flags().Lookup("component-group"))
	assert.Contains(t, cmd.Annotations, azldev.CommandAnnotationPermissiveConfig)
}

func TestMakeLocklessCmd_NoComponents(t *testing.T) {
	env := testutils.NewTestEnv(t)
	cmd := componentcmds.NewMakeLocklessCmd()
	cmd.SetArgs([]string{"nonexistent-component"})
	require.ErrorContains(t, cmd.ExecuteContext(env.Env), "component not found")
	exists, err := fileutils.Exists(env.TestFS, makeLocklessTestPath)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestMakeComponentsLockless_SelectsAndRefreshes(t *testing.T) {
	for _, testCase := range []struct {
		name, existing, want string
	}{
		{name: "missing file", want: "curl\nrust-asyncband\n"},
		{name: "missing newline", existing: "rust-asyncband", want: "rust-asyncband\ncurl\n"},
		{name: "existing whitespace", existing: "\t rust-asyncband \r\n\n", want: "\t rust-asyncband \r\n\ncurl\n"},
		{name: "already selected", existing: "curl\nrust-asyncband\n", want: "curl\nrust-asyncband\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)

			gitCalls := setupMockGitWithCounter(env, "new-upstream-commit")
			for _, name := range []string{"curl", "rust-asyncband", "vim"} {
				addRefreshUpstreamComponent(env, name)
			}

			lock := lockfile.New()
			lock.UpstreamCommit = "old-locked-commit"
			env.WriteLock(t, "curl", lock)
			previousLock, err := fileutils.ReadFile(env.TestFS, "/project/locks/curl.lock")
			require.NoError(t, err)

			if testCase.existing != "" {
				require.NoError(t, fileutils.WriteFile(env.TestFS, makeLocklessTestPath,
					[]byte(testCase.existing), fileperms.PublicFile))
			}

			store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
			require.NoError(t, store.Save("vim", "unselected-pin"))

			filter := &components.ComponentFilter{ComponentNamePatterns: []string{"rust-*", "curl"}}
			_, err = componentcmds.MakeComponentsLockless(env.Env, filter)
			require.NoError(t, err)
			assert.Positive(t, gitCalls.Load())

			for _, name := range []string{"curl", "rust-asyncband"} {
				assert.True(t, env.Env.ComponentWithoutLockfile(name))
				pin, exists, err := store.Get(name)
				require.NoError(t, err)
				assert.True(t, exists)
				assert.Equal(t, "new-upstream-commit", pin)
			}

			pin, exists, err := store.Get("vim")
			require.NoError(t, err)
			assert.True(t, exists)
			assert.Equal(t, "unselected-pin", pin)

			content, err := fileutils.ReadFile(env.TestFS, makeLocklessTestPath)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, string(content))

			currentLock, err := fileutils.ReadFile(env.TestFS, "/project/locks/curl.lock")
			require.NoError(t, err)
			assert.Equal(t, previousLock, currentLock)

			gitCalls.Store(0)

			_, err = componentcmds.MakeComponentsLockless(env.Env, filter)
			require.NoError(t, err)
			assert.Positive(t, gitCalls.Load(), "already-listed components must still refresh")

			content, err = fileutils.ReadFile(env.TestFS, makeLocklessTestPath)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, string(content))
		})
	}
}

func TestMakeComponentsLockless_NonUpstream(t *testing.T) {
	env := testutils.NewTestEnv(t)
	env.Config.Components["local-component"] = projectconfig.ComponentConfig{
		Name: "local-component",
		Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal, Path: "local-component.spec"},
	}
	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	require.NoError(t, store.Save("local-component", "obsolete-pin"))

	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		t.Fatalf("unexpected upstream command: %v", cmd.Args)

		return "", nil
	}
	cmd := componentcmds.NewMakeLocklessCmd()
	cmd.SetArgs([]string{"-p", "local-component"})
	require.NoError(t, cmd.ExecuteContext(env.Env))
	content, err := fileutils.ReadFile(env.TestFS, makeLocklessTestPath)
	require.NoError(t, err)
	assert.Equal(t, "local-component\n", string(content))

	_, exists, err := store.Get("local-component")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestMakeComponentsLockless_RefreshFailureKeepsSelection(t *testing.T) {
	env := testutils.NewTestEnv(t)
	addRefreshUpstreamComponent(env, "curl")
	setupMockGit(env, "unused")
	env.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
		return "", errors.New("upstream unavailable")
	}
	_, err := componentcmds.MakeComponentsLockless(env.Env,
		&components.ComponentFilter{ComponentNamePatterns: []string{"curl"}})
	require.Error(t, err)
	content, err := fileutils.ReadFile(env.TestFS, makeLocklessTestPath)
	require.NoError(t, err)
	assert.Equal(t, "curl\n", string(content))
	assert.True(t, env.Env.ComponentWithoutLockfile("curl"))
}

func TestMakeComponentsLockless_InvalidSelectionFile(t *testing.T) {
	env := testutils.NewTestEnv(t)
	addRefreshUpstreamComponent(env, "curl")
	require.NoError(t, fileutils.MkdirAll(env.TestFS, makeLocklessTestPath))
	_, err := componentcmds.MakeComponentsLockless(env.Env,
		&components.ComponentFilter{ComponentNamePatterns: []string{"curl"}})
	require.ErrorContains(t, err, "expected a file")
	assert.False(t, env.Env.ComponentWithoutLockfile("curl"))
}

func TestMakeComponentsLockless_DryRun(t *testing.T) {
	env := testutils.NewTestEnv(t)
	addRefreshUpstreamComponent(env, "curl")
	dryEnv := azldev.NewEnv(t.Context(), azldev.EnvOptions{
		ProjectDir: "/project", Config: env.Config, Interfaces: env.TestInterfaces,
		EventListener: env.EventListener, DryRunnable: azldev.NewAppDryRunnable(true),
	})
	_, err := componentcmds.MakeComponentsLockless(dryEnv,
		&components.ComponentFilter{ComponentNamePatterns: []string{"curl"}})
	require.NoError(t, err)
	exists, err := fileutils.Exists(env.TestFS, makeLocklessTestPath)
	require.NoError(t, err)
	assert.False(t, exists)
	assert.False(t, dryEnv.ComponentWithoutLockfile("curl"))
}
