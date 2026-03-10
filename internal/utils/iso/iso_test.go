// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package iso_test

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
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/iso"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRunner(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	runner := iso.NewRunner(ctx)

	require.NotNil(t, runner)
}

func TestCreateISO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		options        iso.CreateISOOptions
		runErr         error
		wantArgs       []string
		wantErr        bool
		wantErrContain string
	}{
		{
			name: "basic ISO creation",
			options: iso.CreateISOOptions{
				OutputPath: "/output/test.iso",
				VolumeID:   "MYVOLUME",
				InputFiles: []string{"/input/file1", "/input/file2"},
			},
			wantArgs: []string{
				iso.GenisoimageBinary,
				"-output", "/output/test.iso",
				"-volid", "MYVOLUME",
				"/input/file1", "/input/file2",
			},
			wantErr: false,
		},
		{
			name: "ISO with Joliet extension",
			options: iso.CreateISOOptions{
				OutputPath: "/output/test.iso",
				VolumeID:   "MYVOLUME",
				InputFiles: []string{"/input/file1"},
				UseJoliet:  true,
			},
			wantArgs: []string{
				iso.GenisoimageBinary,
				"-output", "/output/test.iso",
				"-volid", "MYVOLUME",
				"-joliet",
				"/input/file1",
			},
			wantErr: false,
		},
		{
			name: "ISO with Rock Ridge extension",
			options: iso.CreateISOOptions{
				OutputPath:   "/output/test.iso",
				VolumeID:     "MYVOLUME",
				InputFiles:   []string{"/input/file1"},
				UseRockRidge: true,
			},
			wantArgs: []string{
				iso.GenisoimageBinary,
				"-output", "/output/test.iso",
				"-volid", "MYVOLUME",
				"-rock",
				"/input/file1",
			},
			wantErr: false,
		},
		{
			name: "ISO with both Joliet and Rock Ridge",
			options: iso.CreateISOOptions{
				OutputPath:   "/output/test.iso",
				VolumeID:     "MYVOLUME",
				InputFiles:   []string{"/input/file1"},
				UseJoliet:    true,
				UseRockRidge: true,
			},
			wantArgs: []string{
				iso.GenisoimageBinary,
				"-output", "/output/test.iso",
				"-volid", "MYVOLUME",
				"-joliet",
				"-rock",
				"/input/file1",
			},
			wantErr: false,
		},
		{
			name: "ISO with custom description",
			options: iso.CreateISOOptions{
				OutputPath:  "/output/test.iso",
				VolumeID:    "MYVOLUME",
				InputFiles:  []string{"/input/file1"},
				Description: "Creating cloud-init ISO",
			},
			wantArgs: []string{
				iso.GenisoimageBinary,
				"-output", "/output/test.iso",
				"-volid", "MYVOLUME",
				"/input/file1",
			},
			wantErr: false,
		},
		{
			name: "command execution failure",
			options: iso.CreateISOOptions{
				OutputPath: "/output/test.iso",
				VolumeID:   "MYVOLUME",
				InputFiles: []string{"/input/file1"},
			},
			runErr:         errors.New("genisoimage failed"),
			wantErr:        true,
			wantErrContain: "failed to create ISO image",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := testctx.NewCtx()

			var capturedArgs []string

			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				capturedArgs = cmd.Args

				return testCase.runErr
			}

			runner := iso.NewRunner(ctx)
			err := runner.CreateISO(context.Background(), testCase.options)

			if testCase.wantErr {
				require.Error(t, err)

				if testCase.wantErrContain != "" {
					assert.Contains(t, err.Error(), testCase.wantErrContain)
				}

				return
			}

			require.NoError(t, err)

			// Verify the command arguments
			for _, wantArg := range testCase.wantArgs {
				assert.Contains(t, capturedArgs, wantArg,
					"expected argument %q in command args %v", wantArg, capturedArgs)
			}

			// Verify argument ordering for flags
			if testCase.options.UseJoliet {
				jolietIdx := indexOf(capturedArgs, "-joliet")
				outputIdx := indexOf(capturedArgs, "-output")
				assert.Greater(t, jolietIdx, outputIdx, "-joliet should come after -output")
			}

			if testCase.options.UseRockRidge {
				rockIdx := indexOf(capturedArgs, "-rock")
				outputIdx := indexOf(capturedArgs, "-output")
				assert.Greater(t, rockIdx, outputIdx, "-rock should come after -output")
			}
		})
	}
}

func TestCreateISO_MultipleInputFiles(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	var capturedArgs []string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		capturedArgs = cmd.Args

		return nil
	}

	runner := iso.NewRunner(ctx)

	options := iso.CreateISOOptions{
		OutputPath: "/output/test.iso",
		VolumeID:   "MYVOLUME",
		InputFiles: []string{"/input/meta-data", "/input/user-data", "/input/network-config"},
	}

	err := runner.CreateISO(context.Background(), options)

	require.NoError(t, err)

	// All input files should be present
	for _, inputFile := range options.InputFiles {
		assert.Contains(t, capturedArgs, inputFile)
	}

	// Input files should come after all other options
	volidIdx := indexOf(capturedArgs, "-volid")
	for _, inputFile := range options.InputFiles {
		fileIdx := indexOf(capturedArgs, inputFile)
		assert.Greater(t, fileIdx, volidIdx+1,
			"input file %q should come after -volid argument", inputFile)
	}
}

func TestCheckPrerequisites(t *testing.T) {
	t.Parallel()

	t.Run("genisoimage available", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath(iso.GenisoimageBinary)
		ctx.DryRunValue = true

		err := iso.CheckPrerequisites(ctx)

		require.NoError(t, err)
	})

	t.Run("genisoimage not available and prompts disallowed", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.DryRunValue = true
		ctx.PromptsAllowedValue = false
		ctx.AllPromptsAcceptedValue = false

		err := iso.CheckPrerequisites(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "genisoimage prerequisite check failed")
	})

	t.Run("genisoimage not available with auto-install on azurelinux", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.DryRunValue = true
		ctx.AllPromptsAcceptedValue = true

		// Setup OS release file for Azure Linux
		err := fileutils.WriteFile(ctx.FS(), osrelease.EtcOsRelease,
			[]byte("ID="+prereqs.OSIDAzureLinux+"\n"), fileperms.PublicFile)
		require.NoError(t, err)

		// Mock the install command
		ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
			args := strings.Join(cmd.Args, " ")
			assert.Contains(t, args, "cdrkit",
				"install command should include cdrkit package")

			return nil
		}

		err = iso.CheckPrerequisites(ctx)
		// Note: Still errors because genisoimage won't be in path after mock install
		require.Error(t, err)
	})
}

func TestGenisoimageBinaryConstant(t *testing.T) {
	t.Parallel()

	// Verify the binary constant matches expected value
	assert.Equal(t, "genisoimage", iso.GenisoimageBinary)
}

// indexOf returns the index of the first occurrence of value in slice, or -1 if not found.
func indexOf(slice []string, value string) int {
	for i, v := range slice {
		if v == value {
			return i
		}
	}

	return -1
}
