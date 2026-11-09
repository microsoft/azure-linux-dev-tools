// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package govendor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerate(t *testing.T) {
	for _, mode := range []string{"success", "failure", "missing name", "bad name", "empty archive"} {
		t.Run(mode, func(t *testing.T) {
			ctx := testctx.NewCtx()
			ctx.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)
			require.NoError(t, fileutils.WriteFile(ctx.FS(), "/input/app.spec", []byte("upstream"), fileperms.PublicFile))

			var commands []string

			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				commands = append(commands, strings.Join(cmd.Args, " "))

				return nil
			}
			ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.Contains(t, strings.Join(cmd.Args, " "), "go_vendor_archive create")

				if mode == "failure" {
					return "native tool failed", errors.New("generation failed")
				}

				name := "vendor.tar.bz2"
				if mode == "bad name" {
					name = "../host"
				}

				if mode != "missing name" {
					require.NoError(t, fileutils.WriteFile(ctx.FS(), "/work/output/name", []byte(name+"\n"), fileperms.PublicFile))
				}

				payload := "archive"
				if mode == "empty archive" {
					payload = ""
				}

				require.NoError(t, fileutils.WriteFile(ctx.FS(), "/work/output/vendor.tar.bz2",
					[]byte(payload), fileperms.PublicFile))

				return "", nil
			}
			req := Request{SourceDir: "/input", WorkDir: "/work", SpecName: "app.spec", PrepSpec: []byte("private")}
			artifact, err := Generate(ctx, ctx.FS(), mock.NewRunner(ctx, "/mock.cfg"), req)

			assert.Contains(t, strings.Join(commands, "\n"), "--scrub all")

			if mode != "success" {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "vendor.tar.bz2", artifact.Name)
			assert.Equal(t, "/work/vendor.tar.bz2", artifact.Path)

			source, err := fileutils.ReadFile(ctx.FS(), "/input/app.spec")
			require.NoError(t, err)
			assert.Equal(t, "upstream", string(source))
		})
	}
}

func TestGenerationModuleLayout(t *testing.T) {
	for _, req := range []Request{
		{ModuleDir: "cmd/app"},
		{Config: "[general]\ngo_mod_dir = 'cmd/app'\n"},
		{ModuleDir: "cmd/app", Config: "[general]\ngo_mod_dir = 'cmd/app'\n"},
	} {
		command, err := generationCommand(req, "aarch64")
		require.NoError(t, err)
		assert.Contains(t, command[len(command)-1], "--target aarch64")
		assert.Contains(t, command[len(command)-1], "go_vendor_archive create")
		assert.LessOrEqual(t, strings.Count(command[len(command)-1], "cd cmd/app"), 1)
	}

	for _, req := range []Request{
		{ModuleDir: "../bad"},
		{Config: "[general]\ngo_mod_dir = '../bad'\n"},
		{ModuleDir: "other", Config: "[general]\ngo_mod_dir = 'cmd/app'\n"},
		{Config: "not valid toml"},
	} {
		_, err := generationCommand(req, "")
		require.Error(t, err)
	}
}

func TestCollectRejectsSymlinks(t *testing.T) {
	// Real FS is necessary to exercise the kernel's O_NOFOLLOW semantics.
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	link := filepath.Join(directory, "link")

	require.NoError(t, os.WriteFile(target, []byte("host data"), fileperms.PublicFile))
	require.NoError(t, os.Symlink(target, link))
	err := collect(afero.NewOsFs(), link, filepath.Join(directory, "copy"))
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(directory, "copy"))
}
