// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package govendor

import (
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
		assert.Contains(t, script, "--target aarch64")
		assert.Contains(t, script, "--no-prompt")
		assert.Contains(t, script, "--autofill off")
		assert.NotContains(t, script, "--idempotent")
		assert.Contains(t, script, "cp "+topDir+"/azldev-go-archive-name "+outputMount+"/archive-name")
		assert.Equal(t, expected != "", strings.Contains(script, "--verify"))
	}
}
