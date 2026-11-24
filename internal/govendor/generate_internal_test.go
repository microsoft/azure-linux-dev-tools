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

func TestGenerationFailureScrubsRoot(t *testing.T) {
	ctx := testctx.NewCtx()
	ctx.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)

	var commands []string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		commands = append(commands, strings.Join(cmd.Args, " "))

		return nil
	}
	failure := errors.New("generation failed")
	ctx.CmdFactory.RunAndGetOutputHandler = func(*exec.Cmd) (string, error) {
		return "native tool failed", failure
	}
	err := execute(ctx, mock.NewRunner(ctx, "/mock.cfg"), Request{WorkDir: "/work", SpecName: "app.spec"}, "/output")
	require.ErrorIs(t, err, failure)
	assert.Contains(t, strings.Join(commands, "\n"), "--scrub all")
}

func TestReadArtifactRejectsInvalidOutputs(t *testing.T) {
	for _, testCase := range []struct{ name, wantErr string }{
		{"", "opening generated vendor output"},
		{"../host", "invalid generated vendor filename"},
		{"vendor.tar.bz2", "nonempty regular file"},
	} {
		t.Run(testCase.wantErr, func(t *testing.T) {
			fs := testctx.NewCtx().FS()
			require.NoError(t, fileutils.WriteFile(fs, "/work/output/vendor.tar.bz2", nil, fileperms.PublicFile))

			if testCase.name != "" {
				require.NoError(t, fileutils.WriteFile(fs, "/work/output/name", []byte(testCase.name), fileperms.PublicFile))
			}

			_, err := readArtifact(fs, "/work", "/work/output")
			require.ErrorContains(t, err, testCase.wantErr)
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

		if req.Config == "" {
			assert.Contains(t, command[len(command)-1], "cd cmd/app\ntest -f go.mod")
		} else {
			assert.Contains(t, command[len(command)-1], "cd .\ntest -f cmd/app/go.mod")
		}
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
