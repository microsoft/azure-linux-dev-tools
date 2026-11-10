// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mock_test

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testMockConfigPath = "/mock/config"
	testMockRootPath   = "/mock/root"
	testSpecPath       = "/sources/component.spec"
	testSourceDirPath  = "/sources"
	testOutputDirPath  = "/output"
	testSRPMPath       = "/output/component-1.0.0.src.rpm"
	testRPMPath        = "/output/component-1.0.0.rpm"
	testSudoBinary     = "sudo"
)

func newTestCtxWithMockPrereqsPresent() *testctx.TestCtx {
	ctx := testctx.NewCtx()
	ctx.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)

	return ctx
}

func TestConfigPath(t *testing.T) {
	ctx := testctx.NewCtx()

	runner := mock.NewRunner(ctx, testMockConfigPath)

	assert.Equal(t, testMockConfigPath, runner.ConfigPath())
}

func TestMockNotPresent(t *testing.T) {
	ctx := testctx.NewCtx()

	runner := mock.NewRunner(ctx, testMockConfigPath)

	_, err := runner.GetRootPath(ctx)
	require.Error(t, err)
}

func TestRunnerDefaults(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()
	runner := mock.NewRunner(ctx, testMockConfigPath)

	// Pin defaults.
	assert.Equal(t, testMockConfigPath, runner.ConfigPath())
	assert.Empty(t, runner.BindMounts())
	assert.False(t, runner.HasNetworkEnabled())
	assert.False(t, runner.HasNoPreClean())
	assert.False(t, runner.HasUnprivileged())
	assert.Empty(t, runner.BaseDir())
	assert.Empty(t, runner.RootDir())
	assert.Empty(t, runner.ConfigOpts())
}

func TestClone(t *testing.T) {
	const (
		testBaseDir   = "/base"
		testRootDir   = "/root"
		testHostPath  = "/host"
		testGuestPath = "/guest"
	)

	ctx := newTestCtxWithMockPrereqsPresent()
	runner := mock.NewRunner(ctx, testMockConfigPath)

	// Set a number of non-default options so we can confirm they propagate to the clone.
	runner.AddBindMount(testHostPath, testGuestPath)
	runner.WithBaseDir(testBaseDir)
	runner.WithRootDir(testRootDir)
	runner.WithNoPreClean()
	runner.EnableNetwork()
	runner.WithUnprivileged()
	runner.WithConfigOpts(map[string]string{"cleanup_on_success": "True", "cleanup_on_failure": "False"})

	clone := runner.Clone()

	// Confirm that the clone matches the original runner.
	assert.Equal(t, testMockConfigPath, clone.ConfigPath())
	assert.Equal(t, map[string]string{testHostPath: testGuestPath}, clone.BindMounts())
	assert.True(t, clone.HasNetworkEnabled())
	assert.True(t, clone.HasNoPreClean())
	assert.True(t, clone.HasUnprivileged())
	assert.Equal(t, testBaseDir, clone.BaseDir())
	assert.Equal(t, testRootDir, clone.RootDir())
	assert.Equal(t, map[string]string{"cleanup_on_success": "True", "cleanup_on_failure": "False"}, clone.ConfigOpts())
}

func TestGetRootPath(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		if filepath.Base(cmd.Path) == mock.MockBinary {
			return testMockRootPath, nil
		}

		return "", nil
	}

	runner := mock.NewRunner(ctx, testMockConfigPath)

	rootPath, err := runner.GetRootPath(ctx)
	require.NoError(t, err)
	assert.Equal(t, testMockRootPath, rootPath)
}

func TestScrubRoot(t *testing.T) {
	tests := []struct {
		name             string
		unique           bool
		bootstrapCache   bool
		baseDir          string
		missingBootstrap bool
	}{
		{name: "shared root"},
		{name: "shared private base", baseDir: "/private/mock"},
		{name: "unique modern mock", unique: true, bootstrapCache: true},
		{name: "unique legacy mock", unique: true},
		{name: "unique modern private base", unique: true, bootstrapCache: true, baseDir: "/private/mock"},
		{name: "unique legacy private base", unique: true, baseDir: "/private/mock"},
		{name: "unique missing bootstrap", unique: true, missingBootstrap: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := newTestCtxWithMockPrereqsPresent()
			ctx.Ctx = mock.WithUniqueRoots(ctx.Ctx, test.unique, "build")
			ctx.CmdFactory.RegisterCommandInSearchPath(testSudoBinary)
			runner := mock.NewRunner(ctx, testMockConfigPath).WithBaseDir(test.baseDir)
			runner.WithRootDir("/custom/root")
			runner.WithConfigOpts(map[string]string{"rootdir": "/configured/root"})

			baseDir := test.baseDir
			if baseDir == "" {
				baseDir = "/configured/mock"
			}

			rootName := "test"
			bootstrapName := "test-bootstrap"

			if test.unique {
				rootName += "-" + runner.UniqueExtension()
				bootstrapName += "-" + runner.UniqueExtension()
			}

			rootPath := filepath.Join(baseDir, rootName)
			bootstrapPath := filepath.Join(baseDir, bootstrapName)
			cachePath := "/var/cache/mock/test-bootstrap/root_cache/cache"
			otherRoot := filepath.Join(baseDir, "test-bootstrap-other")

			sharedRoot := filepath.Join(baseDir, "test-bootstrap")
			for _, path := range []string{rootPath, otherRoot, sharedRoot, filepath.Dir(cachePath)} {
				require.NoError(t, fileutils.MkdirAll(ctx.FS(), path))
			}

			if !test.missingBootstrap {
				require.NoError(t, fileutils.MkdirAll(ctx.FS(), bootstrapPath))
			}

			require.NoError(t, fileutils.WriteFile(ctx.FS(), cachePath, []byte("shared cache"), fileperms.PrivateFile))

			var actions []string

			ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				if slices.Contains(cmd.Args, "--help") {
					actions = append(actions, "help")

					assert.Equal(t, []string{"mock", "--help"}, cmd.Args)

					if test.bootstrapCache {
						return "--scrub=[all|bootstrap|chroot|cache|bootstrap-cache]", nil
					}

					return "--scrub=[all|bootstrap|chroot|cache]", nil
				}

				actions = append(actions, "path")

				assert.Contains(t, cmd.Args, "--print-root-path")
				assert.Contains(t, cmd.Args, "rootdir={{basedir}}/{{root}}")
				assert.NotContains(t, cmd.Args, "--rootdir")
				assert.NotContains(t, cmd.Args, "rootdir=/configured/root")

				return rootPath + "\n", nil
			}
			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				if cmd.Args[0] == testSudoBinary {
					actions = append(actions, "remove")

					assert.Equal(t, []string{
						testSudoBinary, "rm", "--recursive", "--force", "--one-file-system", "--", bootstrapPath,
					}, cmd.Args)

					return ctx.FS().RemoveAll(bootstrapPath)
				}

				actions = append(actions, "scrub")

				assert.Contains(t, cmd.Args, "--scrub")
				assert.NotContains(t, cmd.Args, "bootstrap-cache")
				assert.Equal(t, test.unique && test.bootstrapCache, slices.Contains(cmd.Args, "bootstrap"))
				assert.Equal(t, !test.unique && test.baseDir != "", slices.Contains(cmd.Args, "all"))
				require.NoError(t, ctx.FS().RemoveAll(rootPath))

				if test.unique && test.bootstrapCache {
					require.NoError(t, ctx.FS().RemoveAll(bootstrapPath))
				}

				return nil
			}

			require.NoError(t, runner.ScrubRoot(ctx))

			expectedActions := []string{"scrub"}
			if test.unique {
				expectedActions = []string{"help", "scrub"}
				if !test.bootstrapCache {
					expectedActions = []string{"help", "path", "scrub"}
					if !test.missingBootstrap {
						expectedActions = append(expectedActions, "remove")
					}
				}
			}

			assert.Equal(t, expectedActions, actions)

			if test.unique {
				exists, err := fileutils.Exists(ctx.FS(), bootstrapPath)
				require.NoError(t, err)
				assert.False(t, exists)
			}

			for _, path := range []string{otherRoot, sharedRoot, cachePath} {
				exists, err := fileutils.Exists(ctx.FS(), path)
				require.NoError(t, err)
				assert.True(t, exists, path)
			}
		})
	}
}

func TestScrubRootDryRun(t *testing.T) {
	for _, baseDir := range []string{"", "/private/mock"} {
		t.Run("base="+baseDir, func(t *testing.T) {
			ctx := newTestCtxWithMockPrereqsPresent()
			ctx.DryRunValue = true
			ctx.Ctx = mock.WithUniqueRoots(ctx.Ctx, true, "build")
			runner := mock.NewRunner(ctx, testMockConfigPath).WithBaseDir(baseDir)

			ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				t.Errorf("unexpected discovery command during dry run: %v", cmd.Args)

				return "", nil
			}

			var commands [][]string

			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				commands = append(commands, cmd.Args)

				return nil
			}

			for _, candidate := range []*mock.Runner{runner, runner.Clone()} {
				require.NoError(t, candidate.ScrubRoot(ctx))
			}

			require.Len(t, commands, 2)

			for _, args := range commands {
				assert.Equal(t, "mock", args[0])
				assert.Contains(t, args, "--uniqueext")
				assert.Contains(t, args, runner.UniqueExtension())
				assert.Equal(t, []string{"--scrub", "chroot", "--scrub", "bootstrap"}, args[len(args)-4:])
				assert.NotContains(t, args, "all")
				assert.NotContains(t, args, "bootstrap-cache")
			}
		})
	}
}

func TestScrubRootRejectsUnexpectedPaths(t *testing.T) {
	for _, path := range []string{
		"", "/", "/var/lib/mock/test", "relative-EXTENSION",
		"/var/lib/mock/-EXTENSION", "/var/lib/mock/unsafe\nroot-EXTENSION",
	} {
		t.Run(path, func(t *testing.T) {
			ctx := newTestCtxWithMockPrereqsPresent()
			ctx.Ctx = mock.WithUniqueRoots(ctx.Ctx, true, "build")
			runner := mock.NewRunner(ctx, testMockConfigPath)
			ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				if slices.Contains(cmd.Args, "--help") {
					return "--scrub=[bootstrap|chroot]", nil
				}

				return strings.ReplaceAll(path, "EXTENSION", runner.UniqueExtension()), nil
			}
			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				t.Fatalf("unexpected destructive command: %v", cmd.Args)

				return nil
			}

			err := runner.ScrubRoot(ctx)
			require.ErrorContains(t, err, "refusing to derive bootstrap root")
		})
	}
}

func TestScrubRootFailures(t *testing.T) {
	for _, stage := range []string{"help", "path", "scrub", "remove", "missing sudo"} {
		t.Run(stage, func(t *testing.T) {
			ctx := newTestCtxWithMockPrereqsPresent()

			ctx.Ctx = mock.WithUniqueRoots(ctx.Ctx, true, "build")
			if stage != "missing sudo" {
				ctx.CmdFactory.RegisterCommandInSearchPath(testSudoBinary)
			}

			runner := mock.NewRunner(ctx, testMockConfigPath)
			rootPath := "/var/lib/mock/test-" + runner.UniqueExtension()
			bootstrapPath := "/var/lib/mock/test-bootstrap-" + runner.UniqueExtension()
			require.NoError(t, fileutils.MkdirAll(ctx.FS(), bootstrapPath))

			testError := errors.New("injected cleanup failure")
			ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				if slices.Contains(cmd.Args, "--help") {
					if stage == "help" {
						return "", testError
					}

					return "--scrub=[bootstrap|chroot]", nil
				}

				if stage == "path" {
					return "", testError
				}

				return rootPath, nil
			}
			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				if cmd.Args[0] == testSudoBinary {
					assert.Equal(t, "remove", stage)
				} else {
					assert.Contains(t, []string{"scrub", "remove", "missing sudo"}, stage)
					assert.NotContains(t, cmd.Args, "bootstrap")
				}

				if stage == "scrub" || stage == "remove" && cmd.Args[0] == testSudoBinary {
					return testError
				}

				return nil
			}

			err := runner.ScrubRoot(ctx)
			if stage == "missing sudo" {
				require.ErrorContains(t, err, "sudo required")
			} else {
				require.ErrorIs(t, err, testError)
			}

			exists, err := fileutils.Exists(ctx.FS(), bootstrapPath)
			require.NoError(t, err)
			assert.True(t, exists)
		})
	}
}

func TestBuildSRPM(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()
	executedCmds := []string{}

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		executedCmds = append(executedCmds, strings.Join(cmd.Args, " "))

		require.NoError(t, fileutils.WriteFile(ctx.FS(), testSRPMPath, []byte{}, fileperms.PrivateFile))

		return nil
	}

	runner := mock.NewRunner(ctx, testMockConfigPath)
	options := mock.SRPMBuildOptions{}

	err := runner.BuildSRPM(ctx, testSpecPath, testSourceDirPath, testOutputDirPath, options)
	require.NoError(t, err)

	// Confirm that we invoked mock, and didn't do so under sudo.
	mockCmd := executedCmds[0]
	assert.Regexp(t, `^mock`, mockCmd)

	// Confirm that we invoked mock with the expected arguments.
	assert.Contains(t, mockCmd, "-r "+testMockConfigPath)
}

func TestBuildSRPM_MockFails(t *testing.T) {
	testError := errors.New("injected mock failure")

	ctx := newTestCtxWithMockPrereqsPresent()
	ctx.CmdFactory.RunHandler = func(_ *exec.Cmd) error { return testError }

	runner := mock.NewRunner(ctx, testMockConfigPath)
	options := mock.SRPMBuildOptions{}

	err := runner.BuildSRPM(ctx, testSpecPath, testSourceDirPath, testOutputDirPath, options)
	require.ErrorIs(t, err, testError)
}

func TestBuildRPM(t *testing.T) {
	const (
		withoutFeature = "without-feature"
		withFeature    = "with-feature"
		macroName      = "macro-name"
		macroValue     = "macro-value"
	)

	ctx := newTestCtxWithMockPrereqsPresent()
	executedCmds := []string{}

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		executedCmds = append(executedCmds, strings.Join(cmd.Args, " "))

		require.NoError(t, fileutils.WriteFile(ctx.FS(), testRPMPath, []byte{}, fileperms.PrivateFile))

		return nil
	}

	runner := mock.NewRunner(ctx, testMockConfigPath)
	mockOptions := mock.RPMBuildOptions{
		CommonBuildOptions: mock.CommonBuildOptions{
			Without: []string{withoutFeature},
			With:    []string{withFeature},
			Defines: map[string]string{macroName: macroValue},
		},
	}

	err := runner.BuildRPM(ctx, testSRPMPath, testOutputDirPath, mockOptions)
	require.NoError(t, err)

	// Confirm that mock was invoked once.
	assert.Len(t, executedCmds, 1)

	// Confirm that we invoked mock, and didn't do so under sudo.
	mockCmd := executedCmds[0]
	assert.Regexp(t, `^mock`, mockCmd)

	// Confirm that we invoked mock with the expected arguments.
	assert.Contains(t, mockCmd, "-r "+testMockConfigPath)
	assert.Contains(t, mockCmd, "--without "+withoutFeature)
	assert.Contains(t, mockCmd, "--with "+withFeature)
	assert.Contains(t, mockCmd, fmt.Sprintf("--define %s %s", macroName, macroValue))
}

func TestBuildRPM_VerboseArgs(t *testing.T) {
	t.Run("NonVerbose", func(t *testing.T) {
		ctx := newTestCtxWithMockPrereqsPresent()
		ctx.VerboseValue = false

		mockCmd := captureBuildRPMCmd(t, ctx)

		// Non-verbose runs should ask mock to be quiet and must not pass --verbose.
		assert.Contains(t, mockCmd, "--quiet")
		assert.NotContains(t, mockCmd, "--verbose")
	})

	t.Run("Verbose", func(t *testing.T) {
		ctx := newTestCtxWithMockPrereqsPresent()
		ctx.VerboseValue = true

		mockCmd := captureBuildRPMCmd(t, ctx)

		// Verbose runs should pass --verbose so mock streams its full output, and must not be quiet.
		assert.Contains(t, mockCmd, "--verbose")
		assert.NotContains(t, mockCmd, "--quiet")
	})
}

// captureBuildRPMCmd runs a successful BuildRPM and returns the joined mock command line.
func captureBuildRPMCmd(t *testing.T, ctx *testctx.TestCtx) string {
	t.Helper()

	var mockCmd string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		mockCmd = strings.Join(cmd.Args, " ")

		require.NoError(t, fileutils.WriteFile(ctx.FS(), testRPMPath, []byte{}, fileperms.PrivateFile))

		return nil
	}

	runner := mock.NewRunner(ctx, testMockConfigPath)

	err := runner.BuildRPM(ctx, testSRPMPath, testOutputDirPath, mock.RPMBuildOptions{})
	require.NoError(t, err)

	return mockCmd
}

func TestBuildRPM_MockFails(t *testing.T) {
	testError := errors.New("injected mock failure")

	ctx := newTestCtxWithMockPrereqsPresent()
	ctx.CmdFactory.RunHandler = func(_ *exec.Cmd) error { return testError }

	runner := mock.NewRunner(ctx, testMockConfigPath)
	options := mock.RPMBuildOptions{}

	err := runner.BuildRPM(ctx, testSRPMPath, testOutputDirPath, options)
	require.ErrorIs(t, err, testError)
}

func TestCmdInChroot_MockNotPresent(t *testing.T) {
	ctx := testctx.NewCtx()

	runner := mock.NewRunner(ctx, testMockConfigPath)

	_, err := runner.CmdInChroot(ctx, []string{"arg1", "arg2"}, true /*interactive*/)
	require.Error(t, err)
}

func TestCmdInChroot_Success(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)

	cmd, err := runner.CmdInChroot(ctx, []string{"arg1", "arg2 with spaces"}, true /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Make sure our args end up in the command line (with quoting as needed).
	assert.Contains(t, cmd.GetArgs(), "arg1 'arg2 with spaces'")

	// Ensure we *don't* see unexpected options.
	assert.NotContains(t, cmd.GetArgs(), "--no-clean")
	assert.NotContains(t, cmd.GetArgs(), "--plugin-option=bind_mount")
}

func TestCmdInChroot_BindMount(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.AddBindMount("/host-path", "/mock-path")
	assert.Equal(t, map[string]string{"/host-path": "/mock-path"}, runner.BindMounts())

	cmd, err := runner.CmdInChroot(ctx, []string{"arg"}, false /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Look for bind-mount arg.
	assert.Contains(t, cmd.GetArgs(), `--plugin-option=bind_mount:dirs=[("/host-path", "/mock-path")]`)
}

func TestCmdInChroot_NoPreClean(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.WithNoPreClean()
	assert.True(t, runner.HasNoPreClean())

	cmd, err := runner.CmdInChroot(ctx, []string{"arg"}, false /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Look for no-clean arg.
	assert.Contains(t, cmd.GetArgs(), "--no-clean")
}

func TestCmdInChroot_EnableNetworking(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.EnableNetwork()
	assert.True(t, runner.HasNetworkEnabled())

	cmd, err := runner.CmdInChroot(ctx, []string{"arg"}, false /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Look for enable-network arg.
	assert.Contains(t, cmd.GetArgs(), "--enable-network")
}

func TestCmdInChroot_ConfigOpts(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.WithConfigOpts(map[string]string{"cleanup_on_success": "True", "cleanup_on_failure": "False"})
	assert.Equal(t, map[string]string{"cleanup_on_success": "True", "cleanup_on_failure": "False"}, runner.ConfigOpts())

	cmd, err := runner.CmdInChroot(ctx, []string{"arg"}, false /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Look for config-opts args (keys should be sorted deterministically).
	cmdArgs := cmd.GetArgs()
	assert.Contains(t, cmdArgs, "--config-opts")
	assert.Contains(t, cmdArgs, "cleanup_on_failure=False")
	assert.Contains(t, cmdArgs, "cleanup_on_success=True")
}

func TestCmdInChroot_Unprivileged(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.WithUnprivileged()

	cmd, err := runner.CmdInChroot(ctx, []string{"arg"}, false /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Look for unpriv arg.
	assert.Contains(t, cmd.GetArgs(), "--unpriv")
}

func TestBuildRPM_WithConfigOpts(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()
	executedCmds := []string{}

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		executedCmds = append(executedCmds, strings.Join(cmd.Args, " "))

		require.NoError(t, fileutils.WriteFile(ctx.FS(), testRPMPath, []byte{}, fileperms.PrivateFile))

		return nil
	}

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.WithConfigOpts(map[string]string{"cleanup_on_success": "True"})

	mockOptions := mock.RPMBuildOptions{}

	err := runner.BuildRPM(ctx, testSRPMPath, testOutputDirPath, mockOptions)
	require.NoError(t, err)

	// Confirm that mock was invoked once.
	assert.Len(t, executedCmds, 1)

	// Confirm the config-opts flag was passed through.
	mockCmd := executedCmds[0]
	assert.Contains(t, mockCmd, "--config-opts cleanup_on_success=True")
}
