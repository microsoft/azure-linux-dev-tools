// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package govendor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateArtifact(t *testing.T) {
	for _, invalid := range []string{"", "archive-name", "license", "sources-list"} {
		t.Run("invalid-"+invalid, func(t *testing.T) {
			ctx := testctx.NewCtx()
			ctx.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)
			require.NoError(t, fileutils.WriteFile(ctx.FS(), "/input/app.spec", []byte("original"), fileperms.PublicFile))
			request := Request{
				SourceDir: "/input", WorkDir: "/scratch", SpecName: "app.spec",
				ArchiveFile: "vendor.tar.bz2", ArchiveSuffix: ".tar.bz2", PrepSpec: []byte("prepared"),
			}
			ctx.CmdFactory.RunAndGetOutputHandler = func(command *exec.Cmd) (string, error) {
				assert.Contains(t, command.Args, "--unpriv")
				assert.Contains(t, command.Args, "--enable-network")

				for name, content := range map[string]string{
					request.ArchiveFile: "archive", "archive-name": "app-vendor.tar.bz2\n",
					"license": "MIT\n", "sources-list": "Source0: app.spec\n",
				} {
					if name == invalid {
						content = "%{unexpanded}"
					}

					require.NoError(t, fileutils.WriteFile(ctx.FS(),
						filepath.Join("/scratch/work", name), []byte(content), fileperms.PublicFile))
				}

				return "", nil
			}

			artifact, err := Generate(ctx, ctx.FS(), mock.NewRunner(ctx, "/mock.cfg"), request)
			if invalid != "" {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "/scratch/artifacts/vendor.tar.bz2", artifact.Path,
				"published artifacts must come from the host-only snapshot")
			assert.Equal(t, "app-vendor.tar.bz2", artifact.Name)
			assert.Equal(t, "MIT", artifact.License)
			digest, err := fileutils.ComputeFileHash(ctx.FS(), fileutils.HashTypeSHA512, artifact.Path)
			require.NoError(t, err)
			assert.Equal(t, digest, artifact.Digest)

			original, err := fileutils.ReadFile(ctx.FS(), "/input/app.spec")
			require.NoError(t, err)
			assert.Equal(t, "original", string(original))
		})
	}
}

func TestGenerationCommand(t *testing.T) {
	for _, expected := range []string{"", "MIT AND Apache-2.0"} {
		command := generationCommand(Request{
			ArchiveFile: "app-vendor.tar.bz2", ModuleDir: "cmd/app", SpecName: "app.spec", ExpectedLicense: expected,
		}, "aarch64")
		assert.Equal(t, []string{"bash", "-euo", "pipefail", "-c"}, command[:4])
		script := command[4]
		assert.Less(t, strings.Index(script, "rpmautospec"), strings.Index(script, "rpmbuild"))
		assert.Less(t, strings.Index(script, "rpmbuild"), strings.Index(script, "go_vendor_archive"))
		assert.Less(t, strings.Index(script, "go_vendor_archive"), strings.Index(script, "go_vendor_license"))
		assert.Contains(t, script, "cd cmd/app")
		assert.Contains(t, script,
			"cd "+sourceMount+"\ngo_vendor_archive create --config "+outputMount+"/"+ConfigFilename+
				" --output "+outputMount+`/app-vendor.tar.bz2 "$module_root"`+"\n"+`cd "$module_root"`)
		assert.Contains(t, script, "--target aarch64")
		assert.Contains(t, script, "--no-prompt")
		assert.Contains(t, script, "--autofill off")
		assert.NotContains(t, script, "--idempotent")
		assert.Contains(t, script, "cp "+topDir+"/azldev-go-archive-name "+outputMount+"/archive-name")
		assert.Equal(t, expected != "", strings.Contains(script, "--verify"))
	}
}

func TestResolveModuleDir(t *testing.T) {
	for _, testCase := range []struct {
		name, config, module, want string
		wantErr                    bool
	}{
		{name: "default"},
		{name: "upstream", config: "[general]\ngo_mod_dir = 'cmd/app'\n[archive]\ntidy = false"},
		{name: "matching", config: "[general]\ngo_mod_dir = 'cmd/app'", module: "cmd/app"},
		{name: "conflicting", config: "[general]\ngo_mod_dir = 'upstream'", module: "cmd/app", wantErr: true},
		{name: "explicit", module: "cmd/app", want: "cmd/app"},
		{name: "root", config: "[general]\ngo_mod_dir = '.'"},
		{name: "absolute", config: "[general]\ngo_mod_dir = '/etc'", wantErr: true},
		{name: "traversal", config: "[general]\ngo_mod_dir = '../outside'", wantErr: true},
		{name: "macro", config: "[general]\ngo_mod_dir = '%{bad}'", wantErr: true},
		{name: "type", config: "[general]\ngo_mod_dir = 42", wantErr: true},
		{name: "toml", config: "[broken", wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			original := Request{ModuleDir: testCase.module, ConfigContents: testCase.config}

			actual, err := resolveModuleDir(original)
			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, actual.ModuleDir)
			assert.Equal(t, testCase.config, actual.ConfigContents)
			assert.Equal(t, testCase.config, original.ConfigContents)
		})
	}
}

// Kernel-enforced O_NOFOLLOW cannot be exercised by the in-memory filesystem.
func TestGenerateRejectsSymlinkOutputs(t *testing.T) {
	for _, name := range []string{"vendor.tar.bz2", "archive-name", "license", "sources-list"} {
		t.Run(name, func(t *testing.T) {
			ctx := testctx.NewCtx(testctx.WithHostFS())
			ctx.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)

			root := t.TempDir()
			request := Request{
				SourceDir: filepath.Join(root, "input"), WorkDir: filepath.Join(root, "scratch"), SpecName: "app.spec",
				ArchiveFile: "vendor.tar.bz2", ArchiveSuffix: ".tar.bz2", PrepSpec: []byte("prepared"),
			}
			require.NoError(t, fileutils.MkdirAll(ctx.FS(), request.SourceDir))
			require.NoError(t, fileutils.WriteFile(ctx.FS(), filepath.Join(request.SourceDir, "app.spec"),
				[]byte("original"), fileperms.PublicFile))
			target := filepath.Join(root, "host-only")
			require.NoError(t, fileutils.WriteFile(ctx.FS(), target, []byte("host-only contents"), fileperms.PublicFile))
			ctx.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
				for filename, content := range map[string]string{
					request.ArchiveFile: "archive", "archive-name": "app-vendor.tar.bz2\n",
					"license": "MIT\n", "sources-list": "Source0: app.spec\n",
				} {
					path := filepath.Join(request.WorkDir, "work", filename)
					if filename == name {
						require.NoError(t, os.Symlink(target, path))
					} else {
						require.NoError(t, fileutils.WriteFile(ctx.FS(), path, []byte(content), fileperms.PublicFile))
					}
				}

				return "", nil
			}
			artifact, err := Generate(ctx, ctx.FS(), mock.NewRunner(ctx, "/mock.cfg"), request)
			require.ErrorContains(t, err, "without following symlinks")
			assert.Empty(t, artifact.Path)
		})
	}
}

func TestCollectOutputRejectsNonRegularFile(t *testing.T) {
	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), "/input/directory"))
	err := fileutils.CopyFile(ctx, ctx.FS(), "/input/directory", "/output/file",
		fileutils.CopyFileOptions{NoFollow: true})
	require.ErrorContains(t, err, "not a regular file")
}

func TestGenerateUsesUpstreamModuleWithoutChangingInputs(t *testing.T) {
	ctx := testctx.NewCtx()
	ctx.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/input/app.spec", []byte("original"), fileperms.PublicFile))

	settings := "[general]\ngo_mod_dir = 'cmd/app'\n"
	ctx.CmdFactory.RunAndGetOutputHandler = func(command *exec.Cmd) (string, error) {
		script := strings.Join(command.Args, " ")
		assert.Contains(t, script, "test -f cmd/app/go.mod")
		assert.NotContains(t, script, "cd cmd/app")

		config, err := fileutils.ReadFile(ctx.FS(), "/scratch/work/"+ConfigFilename)
		require.NoError(t, err)
		assert.Equal(t, settings, string(config))

		for name, content := range map[string]string{
			"vendor.tar.bz2": "archive", "archive-name": "app-vendor.tar.bz2\n",
			"license": "MIT\n", "sources-list": "Source0: app.spec\n",
		} {
			require.NoError(t, fileutils.WriteFile(ctx.FS(), filepath.Join("/scratch/work", name),
				[]byte(content), fileperms.PublicFile))
		}

		return "", nil
	}
	request := Request{
		SourceDir: "/input", WorkDir: "/scratch", SpecName: "app.spec",
		ArchiveFile: "vendor.tar.bz2", ConfigContents: settings,
	}
	_, err := Generate(ctx, ctx.FS(), mock.NewRunner(ctx, "/mock.cfg"), request)
	require.NoError(t, err)
	assert.Equal(t, settings, request.ConfigContents)
	assert.Empty(t, request.ModuleDir)
}
