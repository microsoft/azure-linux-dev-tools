// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package kiwi_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/acobaugh/osrelease"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/kiwi"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const addRepoFlag = "--add-repo"

func TestNewRunner(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	runner := kiwi.NewRunner(ctx, "/path/to/description")

	require.NotNil(t, runner)
	assert.Equal(t, "/path/to/description", runner.DescriptionDir())
	assert.Empty(t, runner.TargetDir())
}

func TestRunner_Clone(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	original := kiwi.NewRunner(ctx, "/original/description").
		WithTargetDir("/original/target")
	original.AddLocalRepo("/repo1", nil)
	original.AddLocalRepo("/repo2", nil)

	cloned := original.Clone()

	// Verify clone has same values
	assert.Equal(t, original.DescriptionDir(), cloned.DescriptionDir())
	assert.Equal(t, original.TargetDir(), cloned.TargetDir())

	// Modify clone and verify original is unchanged
	cloned.WithTargetDir("/cloned/target")
	cloned.AddLocalRepo("/repo3", nil)

	assert.Equal(t, "/original/target", original.TargetDir())
	assert.Equal(t, "/cloned/target", cloned.TargetDir())
}

func TestRunner_FluentMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		setup           func(*kiwi.Runner)
		wantTargetDir   string
		wantDescription string
	}{
		{
			name:            "initial state",
			setup:           func(_ *kiwi.Runner) {},
			wantTargetDir:   "",
			wantDescription: "/test/description",
		},
		{
			name: "with target dir",
			setup: func(r *kiwi.Runner) {
				r.WithTargetDir("/output/dir")
			},
			wantTargetDir:   "/output/dir",
			wantDescription: "/test/description",
		},
		{
			name: "fluent chaining",
			setup: func(r *kiwi.Runner) {
				r.WithTargetDir("/output").AddLocalRepo("/repo1", nil).AddLocalRepo("/repo2", nil)
			},
			wantTargetDir:   "/output",
			wantDescription: "/test/description",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := testctx.NewCtx()
			runner := kiwi.NewRunner(ctx, "/test/description")

			testCase.setup(runner)

			assert.Equal(t, testCase.wantTargetDir, runner.TargetDir())
			assert.Equal(t, testCase.wantDescription, runner.DescriptionDir())
		})
	}
}

func TestRunner_Build(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		descriptionDir     string
		targetDir          string
		localRepos         []string
		verbose            bool
		runErr             error
		wantErr            bool
		wantArgsContain    []string
		wantArgsNotContain []string
	}{
		{
			name:           "successful build",
			descriptionDir: "/path/to/description",
			targetDir:      "/path/to/output",
			localRepos:     nil,
			verbose:        false,
			runErr:         nil,
			wantErr:        false,
			wantArgsContain: []string{
				"sudo",
				kiwi.KiwiBinary,
				"--loglevel", "30",
				"system", "build",
				"--description", "/path/to/description",
				"--target-dir", "/path/to/output",
			},
		},
		{
			name:           "verbose mode uses debug log level",
			descriptionDir: "/path/to/description",
			targetDir:      "/path/to/output",
			localRepos:     nil,
			verbose:        true,
			runErr:         nil,
			wantErr:        false,
			wantArgsContain: []string{
				"--loglevel", "10",
			},
			wantArgsNotContain: []string{
				"30",
			},
		},
		{
			name:           "with local repositories",
			descriptionDir: "/path/to/description",
			targetDir:      "/path/to/output",
			localRepos:     []string{"/local/repo1", "/local/repo2"},
			verbose:        false,
			runErr:         nil,
			wantErr:        false,
			wantArgsContain: []string{
				addRepoFlag,
			},
		},
		{
			name:           "missing description directory",
			descriptionDir: "",
			targetDir:      "/path/to/output",
			localRepos:     nil,
			verbose:        false,
			runErr:         nil,
			wantErr:        true,
		},
		{
			name:           "missing target directory",
			descriptionDir: "/path/to/description",
			targetDir:      "",
			localRepos:     nil,
			verbose:        false,
			runErr:         nil,
			wantErr:        true,
		},
		{
			name:           "command execution failure",
			descriptionDir: "/path/to/description",
			targetDir:      "/path/to/output",
			localRepos:     nil,
			verbose:        false,
			runErr:         errors.New("kiwi-ng failed"),
			wantErr:        true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := testctx.NewCtx()
			ctx.VerboseValue = testCase.verbose

			var capturedArgs []string

			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				capturedArgs = cmd.Args

				return testCase.runErr
			}

			runner := kiwi.NewRunner(ctx, testCase.descriptionDir)
			if testCase.targetDir != "" {
				runner.WithTargetDir(testCase.targetDir)
			}

			for _, repoPath := range testCase.localRepos {
				runner.AddLocalRepo(repoPath, nil)
			}

			err := runner.Build(context.Background())

			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			// Verify expected arguments are present
			for _, wantArg := range testCase.wantArgsContain {
				assert.Contains(t, capturedArgs, wantArg,
					"expected argument %q in command args %v", wantArg, capturedArgs)
			}

			// Verify unwanted arguments are not present
			for _, notWantArg := range testCase.wantArgsNotContain {
				assert.NotContains(t, capturedArgs, notWantArg,
					"unexpected argument %q in command args %v", notWantArg, capturedArgs)
			}
		})
	}
}

func TestRunner_Build_LocalRepoFormatting(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	var capturedArgs []string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		capturedArgs = cmd.Args

		return nil
	}

	runner := kiwi.NewRunner(ctx, "/description").
		WithTargetDir("/output").
		AddLocalRepo("/repo/first", nil).
		AddLocalRepo("/repo/second", nil)

	err := runner.Build(context.Background())

	require.NoError(t, err)

	// Find all --add-repo arguments
	var addRepoArgs []string

	for i, arg := range capturedArgs {
		if arg == addRepoFlag && i+1 < len(capturedArgs) {
			addRepoArgs = append(addRepoArgs, capturedArgs[i+1])
		}
	}

	require.Len(t, addRepoArgs, 2, "expected 2 --add-repo arguments")

	// Verify format: dir://<path>,rpm-md,<alias>,<priority>
	// All local repos should have priority 1
	assert.Contains(t, addRepoArgs[0], "dir://")
	assert.Contains(t, addRepoArgs[0], "/repo/first")
	assert.Contains(t, addRepoArgs[0], "rpm-md")
	assert.Contains(t, addRepoArgs[0], "local-1")
	assert.True(t, strings.HasSuffix(addRepoArgs[0], ",1"), "expected priority 1 suffix, got: %s", addRepoArgs[0])

	assert.Contains(t, addRepoArgs[1], "/repo/second")
	assert.Contains(t, addRepoArgs[1], "local-2")
	assert.True(t, strings.HasSuffix(addRepoArgs[1], ",1"), "expected priority 1 suffix, got: %s", addRepoArgs[1])
}

func TestRunner_AddRemoteRepo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		uri     string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid https URL",
			uri:     "https://example.com/repo",
			wantErr: false,
		},
		{
			name:    "valid http URL",
			uri:     "http://example.com/repo",
			wantErr: false,
		},
		{
			name:    "https URL with path",
			uri:     "https://packages.example.com/azurelinux/4.0/base/x86_64",
			wantErr: false,
		},
		{
			name:    "unsupported ftp scheme",
			uri:     "ftp://example.com/repo",
			wantErr: true,
			errMsg:  "unsupported scheme",
		},
		{
			name:    "unsupported file scheme",
			uri:     "file:///local/path",
			wantErr: true,
			errMsg:  "unsupported scheme",
		},
		{
			name:    "missing scheme",
			uri:     "example.com/repo",
			wantErr: true,
			errMsg:  "unsupported scheme",
		},
		{
			name:    "empty string",
			uri:     "",
			wantErr: true,
			errMsg:  "unsupported scheme",
		},
		{
			name:    "dir scheme not allowed for remote",
			uri:     "dir:///local/path",
			wantErr: true,
			errMsg:  "unsupported scheme",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := testctx.NewCtx()
			runner := kiwi.NewRunner(ctx, "/description")

			err := runner.AddRemoteRepo(testCase.uri, nil)

			if testCase.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.errMsg)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestRunner_Build_RemoteRepoFormatting_GPGCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		opts       *kiwi.RepoOptions
		wantSuffix string
	}{
		{
			name:       "default omits gpg field",
			opts:       nil,
			wantSuffix: ",50",
		},
		{
			name:       "disabled",
			opts:       &kiwi.RepoOptions{DisableRepoGPGCheck: true},
			wantSuffix: ",false",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := testctx.NewCtx()

			var capturedArgs []string

			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				capturedArgs = cmd.Args

				return nil
			}

			runner := kiwi.NewRunner(ctx, "/description").
				WithTargetDir("/output")
			require.NoError(t, runner.AddRemoteRepo("https://repo1.example.com/packages", testCase.opts))
			require.NoError(t, runner.AddRemoteRepo("https://repo2.example.com/packages", testCase.opts))

			err := runner.Build(context.Background())

			require.NoError(t, err)

			// Find all --add-repo arguments
			var addRepoArgs []string

			for i, arg := range capturedArgs {
				if arg == addRepoFlag && i+1 < len(capturedArgs) {
					addRepoArgs = append(addRepoArgs, capturedArgs[i+1])
				}
			}

			require.Len(t, addRepoArgs, 2, "expected 2 --add-repo arguments")

			assert.Contains(t, addRepoArgs[0], "https://repo1.example.com/packages")
			assert.Contains(t, addRepoArgs[0], "rpm-md")
			assert.Contains(t, addRepoArgs[0], "remote-1")
			assert.True(t, strings.HasSuffix(addRepoArgs[0], testCase.wantSuffix),
				"expected suffix %q, got: %s", testCase.wantSuffix, addRepoArgs[0])

			assert.Contains(t, addRepoArgs[1], "https://repo2.example.com/packages")
			assert.Contains(t, addRepoArgs[1], "remote-2")
			assert.True(t, strings.HasSuffix(addRepoArgs[1], testCase.wantSuffix),
				"expected suffix %q, got: %s", testCase.wantSuffix, addRepoArgs[1])
		})
	}
}

func TestRunner_Build_RepoOrdering(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	var capturedArgs []string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		capturedArgs = cmd.Args

		return nil
	}

	// Add both remote and local repos
	runner := kiwi.NewRunner(ctx, "/description").
		WithTargetDir("/output")
	require.NoError(t, runner.AddRemoteRepo("https://remote.example.com/repo", nil))
	runner.AddLocalRepo("/local/repo", nil)

	err := runner.Build(context.Background())

	require.NoError(t, err)

	// Find all --add-repo arguments in order
	var addRepoArgs []string

	for i, arg := range capturedArgs {
		if arg == addRepoFlag && i+1 < len(capturedArgs) {
			addRepoArgs = append(addRepoArgs, capturedArgs[i+1])
		}
	}

	require.Len(t, addRepoArgs, 2, "expected 2 --add-repo arguments")

	// Remote repos should come first (lower priority number = higher priority in kiwi)
	// But we want local repos to override remote, so local repos get priority 1 and remote get 50
	assert.Contains(t, addRepoArgs[0], "https://remote.example.com/repo", "remote repo should be first")
	assert.Contains(t, addRepoArgs[0], ",50", "remote repo should have priority 50")

	assert.Contains(t, addRepoArgs[1], "/local/repo", "local repo should be second")
	assert.True(t, strings.HasSuffix(addRepoArgs[1], ",1"), "local repo should have priority 1")
}

func TestCheckPrerequisites(t *testing.T) {
	t.Parallel()

	t.Run("all tools available", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath("sudo")
		ctx.CmdFactory.RegisterCommandInSearchPath(kiwi.KiwiBinary)
		ctx.CmdFactory.RegisterCommandInSearchPath("sgdisk")
		ctx.DryRunValue = true

		err := kiwi.CheckPrerequisites(ctx)

		require.NoError(t, err)
	})

	t.Run("sudo not available", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath(kiwi.KiwiBinary)
		ctx.CmdFactory.RegisterCommandInSearchPath("sgdisk")
		ctx.DryRunValue = true
		ctx.PromptsAllowedValue = false

		err := kiwi.CheckPrerequisites(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "sudo prerequisite check failed")
	})

	t.Run("kiwi not available", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath("sudo")
		ctx.CmdFactory.RegisterCommandInSearchPath("sgdisk")
		ctx.DryRunValue = true
		ctx.PromptsAllowedValue = false

		err := kiwi.CheckPrerequisites(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "kiwi prerequisite check failed")
	})

	t.Run("sgdisk not available", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath("sudo")
		ctx.CmdFactory.RegisterCommandInSearchPath(kiwi.KiwiBinary)
		ctx.DryRunValue = true
		ctx.PromptsAllowedValue = false

		err := kiwi.CheckPrerequisites(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "sgdisk prerequisite check failed")
	})

	t.Run("kiwi not available with auto-install on azurelinux", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath("sudo")
		ctx.CmdFactory.RegisterCommandInSearchPath("sgdisk")
		ctx.DryRunValue = true
		ctx.AllPromptsAcceptedValue = true

		// Setup OS release file for Azure Linux
		err := fileutils.WriteFile(ctx.FS(), osrelease.EtcOsRelease,
			[]byte("ID="+prereqs.OSIDAzureLinux+"\n"), fileperms.PublicFile)
		require.NoError(t, err)

		// Mock the install command
		ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
			args := strings.Join(cmd.Args, " ")
			assert.Contains(t, args, "kiwi-cli",
				"install command should include kiwi-cli package")

			return nil
		}

		err = kiwi.CheckPrerequisites(ctx)
		// Note: Still errors because kiwi won't be in path after mock install
		require.Error(t, err)
	})
}

func TestParseResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		jsonContent string
		createFile  bool
		wantPaths   []string
		wantErr     bool
	}{
		{
			name: "valid result with multiple entries",
			jsonContent: `{
				"disk-image": {
					"filename": "/output/image.raw",
					"compress": false,
					"shasum": true,
					"use_for_bundle": true
				},
				"iso-image": {
					"filename": "/output/image.iso",
					"compress": false,
					"shasum": false,
					"use_for_bundle": false
				}
			}`,
			createFile: true,
			wantPaths:  []string{"/output/image.raw", "/output/image.iso"},
			wantErr:    false,
		},
		{
			name: "valid result with single entry",
			jsonContent: `{
				"disk-image": {
					"filename": "/output/vm.qcow2",
					"compress": true,
					"shasum": true,
					"use_for_bundle": true
				}
			}`,
			createFile: true,
			wantPaths:  []string{"/output/vm.qcow2"},
			wantErr:    false,
		},
		{
			name:        "empty result map",
			jsonContent: `{}`,
			createFile:  true,
			wantPaths:   []string{},
			wantErr:     false,
		},
		{
			name: "entry without filename",
			jsonContent: `{
				"disk-image": {
					"filename": "/output/image.raw",
					"compress": false,
					"shasum": true,
					"use_for_bundle": true
				},
				"checksum": {
					"filename": "",
					"compress": false,
					"shasum": false,
					"use_for_bundle": false
				}
			}`,
			createFile: true,
			wantPaths:  []string{"/output/image.raw"},
			wantErr:    false,
		},
		{
			name:       "missing result file",
			createFile: false,
			wantPaths:  nil,
			wantErr:    true,
		},
		{
			name:        "invalid JSON",
			jsonContent: `{ invalid json }`,
			createFile:  true,
			wantPaths:   nil,
			wantErr:     true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := testctx.NewCtx()
			targetDir := "/work/kiwi-output"

			if testCase.createFile {
				resultPath := targetDir + "/" + kiwi.ResultFilename
				err := fileutils.WriteFile(ctx.FS(), resultPath,
					[]byte(testCase.jsonContent), fileperms.PublicFile)
				require.NoError(t, err)
			}

			paths, err := kiwi.ParseResult(ctx.FS(), targetDir)

			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			// Use ElementsMatch because map iteration order is not guaranteed
			assert.ElementsMatch(t, testCase.wantPaths, paths)
		})
	}
}

func TestKiwiConstants(t *testing.T) {
	t.Parallel()

	// Verify constants match expected values
	assert.Equal(t, "kiwi", kiwi.KiwiBinary)
	assert.Equal(t, "kiwi.result.json", kiwi.ResultFilename)
}

func TestRunner_Build_RepoOptionsAllFields(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	var capturedArgs []string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		capturedArgs = cmd.Args

		return nil
	}

	opts := &kiwi.RepoOptions{
		Alias:                  "my-custom-repo",
		Priority:               10,
		ImageInclude:           true,
		DisablePackageGPGCheck: true,
		SigningKeys:            []string{"https://example.com/key1.asc", "https://example.com/key2.asc"},
		DisableRepoGPGCheck:    true,
		SourceType:             kiwi.RepoSourceTypeMetalink,
	}

	runner := kiwi.NewRunner(ctx, "/description").
		WithTargetDir("/output")
	require.NoError(t, runner.AddRemoteRepo("https://packages.example.com/repo", opts))

	err := runner.Build(context.Background())

	require.NoError(t, err)

	// Find the --add-repo argument
	var addRepoArgs []string

	for i, arg := range capturedArgs {
		if arg == addRepoFlag && i+1 < len(capturedArgs) {
			addRepoArgs = append(addRepoArgs, capturedArgs[i+1])
		}
	}

	require.Len(t, addRepoArgs, 1, "expected 1 --add-repo argument")

	// Verify all fields are present in the expected format:
	// <source>,rpm-md,<alias>,<priority>,<imageinclude>,<package_gpgcheck>,
	// {signing_keys},,<repo_gpgcheck>,<repo_sourcetype>
	arg := addRepoArgs[0]
	assert.Contains(t, arg, "https://packages.example.com/repo")
	assert.Contains(t, arg, "rpm-md")
	assert.Contains(t, arg, "my-custom-repo")
	assert.Contains(t, arg, ",10,")
	assert.Contains(t, arg, ",true,false,")
	assert.Contains(t, arg, "{https://example.com/key1.asc;https://example.com/key2.asc}")
	assert.True(t, strings.HasSuffix(arg, ",metalink"), "expected repo_sourcetype=metalink at end")

	// Verify the complete format
	expected := "https://packages.example.com/repo,rpm-md,my-custom-repo,10,true,false," +
		"{https://example.com/key1.asc;https://example.com/key2.asc},,,false,metalink"
	assert.Equal(t, expected, arg)
}

func TestRunner_Build_RepoOptionsDefaultsOmitted(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	var capturedArgs []string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		capturedArgs = cmd.Args

		return nil
	}

	// nil options — all defaults, no trailing fields
	runner := kiwi.NewRunner(ctx, "/description").
		WithTargetDir("/output")
	require.NoError(t, runner.AddRemoteRepo("https://packages.example.com/repo", nil))

	err := runner.Build(context.Background())

	require.NoError(t, err)

	var addRepoArgs []string

	for i, arg := range capturedArgs {
		if arg == addRepoFlag && i+1 < len(capturedArgs) {
			addRepoArgs = append(addRepoArgs, capturedArgs[i+1])
		}
	}

	require.Len(t, addRepoArgs, 1)

	// With nil options, the format should be minimal: <source>,rpm-md,<alias>,<priority>
	// No trailing empty commas.
	expected := "https://packages.example.com/repo,rpm-md,remote-1,50"
	assert.Equal(t, expected, addRepoArgs[0])
}

func TestRunner_Build_RepoOptionsSingleField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    *kiwi.RepoOptions
		wantArg string
	}{
		{
			name: "disable repo gpgcheck only",
			opts: &kiwi.RepoOptions{
				DisableRepoGPGCheck: true,
			},
			wantArg: "https://packages.example.com/repo,rpm-md,remote-1,50,,,,,,false",
		},
		{
			name: "imageinclude only",
			opts: &kiwi.RepoOptions{
				ImageInclude: true,
			},
			wantArg: "https://packages.example.com/repo,rpm-md,remote-1,50,true",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := testctx.NewCtx()

			var capturedArgs []string

			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				capturedArgs = cmd.Args

				return nil
			}

			runner := kiwi.NewRunner(ctx, "/description").
				WithTargetDir("/output")
			require.NoError(t, runner.AddRemoteRepo("https://packages.example.com/repo", testCase.opts))

			err := runner.Build(context.Background())
			require.NoError(t, err)

			var addRepoArgs []string

			for i, arg := range capturedArgs {
				if arg == addRepoFlag && i+1 < len(capturedArgs) {
					addRepoArgs = append(addRepoArgs, capturedArgs[i+1])
				}
			}

			require.Len(t, addRepoArgs, 1)
			assert.Equal(t, testCase.wantArg, addRepoArgs[0])
		})
	}
}

func TestRunner_Build_RepoOptionsCustomPriority(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	var capturedArgs []string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		capturedArgs = cmd.Args

		return nil
	}

	opts := &kiwi.RepoOptions{
		Priority: 99,
	}

	runner := kiwi.NewRunner(ctx, "/description").
		WithTargetDir("/output").
		AddLocalRepo("/local/repo", opts)

	err := runner.Build(context.Background())

	require.NoError(t, err)

	var addRepoArgs []string

	for i, arg := range capturedArgs {
		if arg == addRepoFlag && i+1 < len(capturedArgs) {
			addRepoArgs = append(addRepoArgs, capturedArgs[i+1])
		}
	}

	require.Len(t, addRepoArgs, 1)

	// Local repo with custom priority 99 instead of default 1
	assert.Contains(t, addRepoArgs[0], "/local/repo")
	assert.Contains(t, addRepoArgs[0], ",99")
	assert.NotContains(t, addRepoArgs[0], ",1,")
}

func TestRunner_Build_RepoOptionsSourceType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		sourceType     kiwi.RepoSourceType
		wantSuffix     string
		wantContains   string
		wantNotContain string
	}{
		{
			name:       "mirrorlist",
			sourceType: kiwi.RepoSourceTypeMirrorlist,
			wantSuffix: ",mirrorlist",
		},
		{
			name:       "metalink",
			sourceType: kiwi.RepoSourceTypeMetalink,
			wantSuffix: ",metalink",
		},
		{
			name:       "baseurl",
			sourceType: kiwi.RepoSourceTypeBaseURL,
			wantSuffix: ",baseurl",
		},
		{
			name:           "default omitted",
			sourceType:     kiwi.RepoSourceTypeDefault,
			wantNotContain: "baseurl",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := testctx.NewCtx()

			var capturedArgs []string

			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				capturedArgs = cmd.Args

				return nil
			}

			opts := &kiwi.RepoOptions{
				SourceType: testCase.sourceType,
			}

			runner := kiwi.NewRunner(ctx, "/description").
				WithTargetDir("/output")
			require.NoError(t, runner.AddRemoteRepo("https://packages.example.com/repo", opts))

			err := runner.Build(context.Background())
			require.NoError(t, err)

			var addRepoArgs []string

			for i, arg := range capturedArgs {
				if arg == addRepoFlag && i+1 < len(capturedArgs) {
					addRepoArgs = append(addRepoArgs, capturedArgs[i+1])
				}
			}

			require.Len(t, addRepoArgs, 1)

			if testCase.wantSuffix != "" {
				assert.True(t, strings.HasSuffix(addRepoArgs[0], testCase.wantSuffix),
					"expected suffix %q, got: %s", testCase.wantSuffix, addRepoArgs[0])
			}

			if testCase.wantNotContain != "" {
				assert.NotContains(t, addRepoArgs[0], testCase.wantNotContain)
			}
		})
	}
}
