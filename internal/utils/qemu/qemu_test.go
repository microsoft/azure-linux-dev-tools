// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package qemu_test

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"

	"github.com/acobaugh/osrelease"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/qemu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoArchToQEMUArch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		goarch   string
		expected string
	}{
		{
			name:     "amd64 to x86_64",
			goarch:   "amd64",
			expected: qemu.ArchX86_64,
		},
		{
			name:     "arm64 to aarch64",
			goarch:   "arm64",
			expected: qemu.ArchAarch64,
		},
		{
			name:     "unknown architecture passthrough",
			goarch:   "riscv64",
			expected: "riscv64",
		},
		{
			name:     "386 passthrough",
			goarch:   "386",
			expected: "386",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := qemu.GoArchToQEMUArch(testCase.goarch)

			assert.Equal(t, testCase.expected, result)
		})
	}
}

func TestBinaryAndPackageForArch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		arch        string
		wantBinary  string
		wantPackage string
	}{
		{
			name:        "x86_64 architecture",
			arch:        qemu.ArchX86_64,
			wantBinary:  "qemu-system-x86_64",
			wantPackage: "qemu-system-x86",
		},
		{
			name:        "aarch64 architecture",
			arch:        qemu.ArchAarch64,
			wantBinary:  "qemu-system-aarch64",
			wantPackage: "qemu-system-aarch64",
		},
		{
			name:        "unknown architecture uses arch name",
			arch:        "riscv64",
			wantBinary:  "qemu-system-riscv64",
			wantPackage: "qemu-system-riscv64",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			binary, pkg := qemu.BinaryAndPackageForArch(testCase.arch)

			assert.Equal(t, testCase.wantBinary, binary)
			assert.Equal(t, testCase.wantPackage, pkg)
		})
	}
}

func TestCanUseKVM(t *testing.T) {
	t.Parallel()

	hostArch := qemu.GoArchToQEMUArch(runtime.GOARCH)

	tests := []struct {
		name       string
		targetArch string
		wantKVM    bool
	}{
		{
			name:       "matching host architecture",
			targetArch: hostArch,
			wantKVM:    true,
		},
		{
			name:       "different architecture cannot use KVM",
			targetArch: "different-arch",
			wantKVM:    false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := qemu.CanUseKVM(testCase.targetArch)

			assert.Equal(t, testCase.wantKVM, result)
		})
	}
}

func TestMachineAndCPUForArch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		arch        string
		useKVM      bool
		wantMachine string
		wantCPU     string
	}{
		{
			name:        "x86_64 with KVM",
			arch:        qemu.ArchX86_64,
			useKVM:      true,
			wantMachine: "q35",
			wantCPU:     "host",
		},
		{
			name:        "x86_64 without KVM",
			arch:        qemu.ArchX86_64,
			useKVM:      false,
			wantMachine: "q35",
			wantCPU:     "qemu64",
		},
		{
			name:        "aarch64 with KVM",
			arch:        qemu.ArchAarch64,
			useKVM:      true,
			wantMachine: "virt",
			wantCPU:     "host",
		},
		{
			name:        "aarch64 without KVM",
			arch:        qemu.ArchAarch64,
			useKVM:      false,
			wantMachine: "virt",
			wantCPU:     "cortex-a57",
		},
		{
			name:        "unknown architecture",
			arch:        "riscv64",
			useKVM:      false,
			wantMachine: "virt",
			wantCPU:     "max",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			machine, cpu := qemu.MachineAndCPUForArch(testCase.arch, testCase.useKVM)

			assert.Equal(t, testCase.wantMachine, machine)
			assert.Equal(t, testCase.wantCPU, cpu)
		})
	}
}

func TestNewRunner(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	runner := qemu.NewRunner(ctx)

	require.NotNil(t, runner)
}

func TestFindFirmware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		arch             string
		secureBoot       bool
		fwFiles          []string
		nvramFiles       []string
		wantErr          bool
		wantErrContain   string
		wantFWContain    string
		wantNVRAMContain string
	}{
		{
			name:       "x86_64 standard boot",
			arch:       qemu.ArchX86_64,
			secureBoot: false,
			fwFiles: []string{
				"/usr/share/OVMF/OVMF_CODE.fd",
			},
			nvramFiles: []string{
				"/usr/share/OVMF/OVMF_VARS.fd",
			},
			wantFWContain:    "OVMF_CODE.fd",
			wantNVRAMContain: "OVMF_VARS.fd",
		},
		{
			name:       "x86_64 secure boot",
			arch:       qemu.ArchX86_64,
			secureBoot: true,
			fwFiles: []string{
				"/usr/share/OVMF/OVMF_CODE.secboot.fd",
			},
			nvramFiles: []string{
				"/usr/share/OVMF/OVMF_VARS.secboot.fd",
			},
			wantFWContain:    "OVMF_CODE.secboot.fd",
			wantNVRAMContain: "OVMF_VARS.secboot.fd",
		},
		{
			name:       "x86_64 4M variant",
			arch:       qemu.ArchX86_64,
			secureBoot: false,
			fwFiles: []string{
				"/usr/share/OVMF/OVMF_CODE_4M.fd",
			},
			nvramFiles: []string{
				"/usr/share/OVMF/OVMF_VARS_4M.fd",
			},
			wantFWContain:    "OVMF_CODE_4M.fd",
			wantNVRAMContain: "OVMF_VARS_4M.fd",
		},
		{
			name:       "aarch64 standard boot",
			arch:       qemu.ArchAarch64,
			secureBoot: false,
			fwFiles: []string{
				"/usr/share/AAVMF/AAVMF_CODE.fd",
			},
			nvramFiles: []string{
				"/usr/share/AAVMF/AAVMF_VARS.fd",
			},
			wantFWContain:    "AAVMF_CODE.fd",
			wantNVRAMContain: "AAVMF_VARS.fd",
		},
		{
			name:       "aarch64 secure boot",
			arch:       qemu.ArchAarch64,
			secureBoot: true,
			fwFiles: []string{
				"/usr/share/AAVMF/AAVMF_CODE.secboot.fd",
			},
			nvramFiles: []string{
				"/usr/share/AAVMF/AAVMF_VARS.fd",
			},
			wantFWContain:    "AAVMF_CODE.secboot.fd",
			wantNVRAMContain: "AAVMF_VARS.fd",
		},
		{
			name:       "aarch64 edk2 variant",
			arch:       qemu.ArchAarch64,
			secureBoot: false,
			fwFiles: []string{
				"/usr/share/edk2/aarch64/QEMU_EFI-pflash.raw",
			},
			nvramFiles: []string{
				"/usr/share/edk2/aarch64/vars-template-pflash.raw",
			},
			wantFWContain:    "QEMU_EFI-pflash.raw",
			wantNVRAMContain: "vars-template-pflash.raw",
		},
		{
			name:           "firmware not found",
			arch:           qemu.ArchX86_64,
			secureBoot:     false,
			fwFiles:        []string{},
			nvramFiles:     []string{},
			wantErr:        true,
			wantErrContain: "firmware not found",
		},
		{
			name:       "firmware found but NVRAM not found",
			arch:       qemu.ArchX86_64,
			secureBoot: false,
			fwFiles: []string{
				"/usr/share/OVMF/OVMF_CODE.fd",
			},
			nvramFiles:     []string{},
			wantErr:        true,
			wantErrContain: "NVRAM template not found",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := testctx.NewCtx()

			// Create firmware files in memory FS
			for _, fwFile := range testCase.fwFiles {
				err := fileutils.WriteFile(ctx.FS(), fwFile, []byte("firmware"), fileperms.PublicFile)
				require.NoError(t, err)
			}

			for _, nvramFile := range testCase.nvramFiles {
				err := fileutils.WriteFile(ctx.FS(), nvramFile, []byte("nvram"), fileperms.PublicFile)
				require.NoError(t, err)
			}

			runner := qemu.NewRunner(ctx)
			fwPath, nvramPath, err := runner.FindFirmware(testCase.arch, testCase.secureBoot)

			if testCase.wantErr {
				require.Error(t, err)

				if testCase.wantErrContain != "" {
					assert.Contains(t, err.Error(), testCase.wantErrContain)
				}

				return
			}

			require.NoError(t, err)
			assert.Contains(t, fwPath, testCase.wantFWContain)
			assert.Contains(t, nvramPath, testCase.wantNVRAMContain)
		})
	}
}

func TestFindFirmware_FirstMatchWins(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	// Create both possible firmware paths - the first one should be returned
	err := fileutils.WriteFile(ctx.FS(), "/usr/share/OVMF/OVMF_CODE.fd", []byte("fw1"), fileperms.PublicFile)
	require.NoError(t, err)

	err = fileutils.WriteFile(ctx.FS(), "/usr/share/OVMF/OVMF_CODE_4M.fd", []byte("fw2"), fileperms.PublicFile)
	require.NoError(t, err)

	err = fileutils.WriteFile(ctx.FS(), "/usr/share/OVMF/OVMF_VARS.fd", []byte("nvram1"), fileperms.PublicFile)
	require.NoError(t, err)

	err = fileutils.WriteFile(ctx.FS(), "/usr/share/OVMF/OVMF_VARS_4M.fd", []byte("nvram2"), fileperms.PublicFile)
	require.NoError(t, err)

	runner := qemu.NewRunner(ctx)
	fwPath, nvramPath, err := runner.FindFirmware(qemu.ArchX86_64, false)

	require.NoError(t, err)
	// First match should be returned
	assert.Equal(t, "/usr/share/OVMF/OVMF_CODE.fd", fwPath)
	assert.Equal(t, "/usr/share/OVMF/OVMF_VARS.fd", nvramPath)
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		options         qemu.RunOptions
		runErr          error
		wantErr         bool
		wantErrContain  string
		wantArgsContain []string
	}{
		{
			name: "basic VM run",
			options: qemu.RunOptions{
				Arch:             qemu.ArchX86_64,
				FirmwarePath:     "/usr/share/OVMF/OVMF_CODE.fd",
				NVRAMPath:        "/tmp/nvram.fd",
				DiskPath:         "/images/disk.raw",
				DiskType:         "raw",
				CloudInitISOPath: "/tmp/cloud-init.iso",
				SecureBoot:       false,
				SSHPort:          2222,
				CPUs:             2,
				Memory:           "2G",
			},
			wantArgsContain: []string{
				"qemu-system-x86_64",
				"-m", "2G",
				"-cdrom", "/tmp/cloud-init.iso",
				"-nographic",
			},
		},
		{
			name: "VM with secure boot",
			options: qemu.RunOptions{
				Arch:             qemu.ArchX86_64,
				FirmwarePath:     "/usr/share/OVMF/OVMF_CODE.secboot.fd",
				NVRAMPath:        "/tmp/nvram.fd",
				DiskPath:         "/images/disk.raw",
				DiskType:         "raw",
				CloudInitISOPath: "/tmp/cloud-init.iso",
				SecureBoot:       true,
				SSHPort:          2222,
				CPUs:             4,
				Memory:           "4G",
			},
			wantArgsContain: []string{
				"qemu-system-x86_64",
				"secure,value=on",
			},
		},
		{
			name: "aarch64 VM",
			options: qemu.RunOptions{
				Arch:             qemu.ArchAarch64,
				FirmwarePath:     "/usr/share/AAVMF/AAVMF_CODE.fd",
				NVRAMPath:        "/tmp/nvram.fd",
				DiskPath:         "/images/disk.raw",
				DiskType:         "raw",
				CloudInitISOPath: "/tmp/cloud-init.iso",
				SecureBoot:       false,
				SSHPort:          2222,
				CPUs:             2,
				Memory:           "2G",
			},
			wantArgsContain: []string{
				"qemu-system-aarch64",
			},
		},
		{
			name: "QEMU execution failure",
			options: qemu.RunOptions{
				Arch:             qemu.ArchX86_64,
				FirmwarePath:     "/usr/share/OVMF/OVMF_CODE.fd",
				NVRAMPath:        "/tmp/nvram.fd",
				DiskPath:         "/images/disk.raw",
				DiskType:         "raw",
				CloudInitISOPath: "/tmp/cloud-init.iso",
				SecureBoot:       false,
				SSHPort:          2222,
				CPUs:             2,
				Memory:           "2G",
			},
			runErr:         errors.New("QEMU crashed"),
			wantErr:        true,
			wantErrContain: "failed to run VM in QEMU",
		},
		{
			name: "VM with install ISO boots ISO first",
			options: qemu.RunOptions{
				Arch:           qemu.ArchX86_64,
				FirmwarePath:   "/usr/share/OVMF/OVMF_CODE.fd",
				NVRAMPath:      "/tmp/nvram.fd",
				DiskPath:       "/images/disk.qcow2",
				DiskType:       "qcow2",
				InstallISOPath: "/tmp/installer.iso",
				SecureBoot:     false,
				SSHPort:        2222,
				CPUs:           2,
				Memory:         "2G",
			},
			wantArgsContain: []string{
				"qemu-system-x86_64",
				"if=none,id=installcd,file=/tmp/installer.iso,media=cdrom,readonly=on",
				"scsi-cd,drive=installcd,bootindex=1",
				"scsi-hd,drive=hd,bootindex=2",
			},
		},
		{
			name: "VM without install ISO boots disk first",
			options: qemu.RunOptions{
				Arch:         qemu.ArchX86_64,
				FirmwarePath: "/usr/share/OVMF/OVMF_CODE.fd",
				NVRAMPath:    "/tmp/nvram.fd",
				DiskPath:     "/images/disk.qcow2",
				DiskType:     "qcow2",
				SecureBoot:   false,
				SSHPort:      2222,
				CPUs:         2,
				Memory:       "2G",
			},
			wantArgsContain: []string{
				"scsi-hd,drive=hd,bootindex=1",
			},
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

			runner := qemu.NewRunner(ctx)
			err := runner.Run(context.Background(), testCase.options)

			if testCase.wantErr {
				require.Error(t, err)

				if testCase.wantErrContain != "" {
					assert.Contains(t, err.Error(), testCase.wantErrContain)
				}

				return
			}

			require.NoError(t, err)

			// Check expected args are present
			argsStr := argsToString(capturedArgs)
			for _, wantArg := range testCase.wantArgsContain {
				assert.Contains(t, argsStr, wantArg,
					"expected %q in command args: %v", wantArg, capturedArgs)
			}
		})
	}
}

func TestRun_SSHPortForwarding(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	var capturedArgs []string

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		capturedArgs = cmd.Args

		return nil
	}

	runner := qemu.NewRunner(ctx)
	options := qemu.RunOptions{
		Arch:             qemu.ArchX86_64,
		FirmwarePath:     "/usr/share/OVMF/OVMF_CODE.fd",
		NVRAMPath:        "/tmp/nvram.fd",
		DiskPath:         "/images/disk.raw",
		DiskType:         "raw",
		CloudInitISOPath: "/tmp/cloud-init.iso",
		SecureBoot:       false,
		SSHPort:          3333,
		CPUs:             2,
		Memory:           "2G",
	}

	err := runner.Run(context.Background(), options)

	require.NoError(t, err)

	argsStr := argsToString(capturedArgs)
	assert.Contains(t, argsStr, "hostfwd=tcp::3333-:22",
		"SSH port forwarding should be configured with port 3333")
}

func TestCheckPrerequisites(t *testing.T) {
	t.Parallel()

	t.Run("QEMU and sudo available", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath("qemu-system-x86_64")
		ctx.CmdFactory.RegisterCommandInSearchPath("sudo")
		ctx.DryRunValue = true

		err := qemu.CheckPrerequisites(ctx, qemu.ArchX86_64)

		require.NoError(t, err)
	})

	t.Run("QEMU missing", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath("sudo")
		ctx.DryRunValue = true
		ctx.PromptsAllowedValue = false
		ctx.AllPromptsAcceptedValue = false

		err := qemu.CheckPrerequisites(ctx, qemu.ArchX86_64)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "QEMU prerequisite check failed")
	})

	t.Run("sudo missing", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath("qemu-system-x86_64")
		ctx.DryRunValue = true
		ctx.PromptsAllowedValue = false
		ctx.AllPromptsAcceptedValue = false

		err := qemu.CheckPrerequisites(ctx, qemu.ArchX86_64)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "sudo prerequisite check failed")
	})

	t.Run("aarch64 prerequisite check", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath("qemu-system-aarch64")
		ctx.CmdFactory.RegisterCommandInSearchPath("sudo")
		ctx.DryRunValue = true

		err := qemu.CheckPrerequisites(ctx, qemu.ArchAarch64)

		require.NoError(t, err)
	})

	t.Run("auto-install on azurelinux", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath("sudo")
		ctx.DryRunValue = true
		ctx.AllPromptsAcceptedValue = true

		// Setup OS release file for Azure Linux
		err := fileutils.WriteFile(ctx.FS(), osrelease.EtcOsRelease,
			[]byte("ID="+prereqs.OSIDAzureLinux+"\n"), fileperms.PublicFile)
		require.NoError(t, err)

		// Mock the install command
		ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
			argsStr := argsToString(cmd.Args)
			// Verify correct package is being installed
			assert.Contains(t, argsStr, "qemu-system-x86")

			return nil
		}

		err = qemu.CheckPrerequisites(ctx, qemu.ArchX86_64)
		// Note: Still errors because qemu won't be in path after mock install
		require.Error(t, err)
	})
}

func TestArchConstants(t *testing.T) {
	t.Parallel()

	// Verify architecture constants match expected QEMU naming
	assert.Equal(t, "x86_64", qemu.ArchX86_64)
	assert.Equal(t, "aarch64", qemu.ArchAarch64)
}

// argsToString joins command arguments into a single string for easier assertion.
func argsToString(args []string) string {
	result := ""
	for _, arg := range args {
		result += arg + " "
	}

	return result
}

func TestCreateEmptyQcow2(t *testing.T) {
	t.Parallel()

	t.Run("invokes qemu-img with expected args", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()

		var capturedArgs []string

		ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
			capturedArgs = cmd.Args

			return nil
		}

		runner := qemu.NewRunner(ctx)
		err := runner.CreateEmptyQcow2(context.Background(), "/tmp/disk.qcow2", "10G")
		require.NoError(t, err)

		require.NotEmpty(t, capturedArgs)
		assert.Equal(t, "qemu-img", capturedArgs[0])
		assert.Equal(t, []string{"qemu-img", "create", "-f", "qcow2", "/tmp/disk.qcow2", "10G"}, capturedArgs)
	})

	t.Run("propagates errors", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RunHandler = func(_ *exec.Cmd) error {
			return errors.New("disk full")
		}

		runner := qemu.NewRunner(ctx)
		err := runner.CreateEmptyQcow2(context.Background(), "/tmp/disk.qcow2", "10G")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create empty qcow2 disk image")
	})

	t.Run("surfaces qemu-img stderr on failure", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
			if cmd.Stderr != nil {
				_, _ = cmd.Stderr.Write([]byte("qemu-img: unable to allocate disk\n"))
			}

			return errors.New("exit status 1")
		}

		runner := qemu.NewRunner(ctx)
		err := runner.CreateEmptyQcow2(context.Background(), "/tmp/disk.qcow2", "10G")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "qemu-img: unable to allocate disk")
		assert.Contains(t, err.Error(), "failed to create empty qcow2 disk image")
	})
}

func TestCheckQEMUImgPrerequisite(t *testing.T) {
	t.Parallel()

	t.Run("qemu-img available", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.CmdFactory.RegisterCommandInSearchPath("qemu-img")
		ctx.DryRunValue = true

		err := qemu.CheckQEMUImgPrerequisite(ctx)
		require.NoError(t, err)
	})

	t.Run("qemu-img missing", func(t *testing.T) {
		t.Parallel()

		ctx := testctx.NewCtx()
		ctx.DryRunValue = true
		ctx.PromptsAllowedValue = false
		ctx.AllPromptsAcceptedValue = false

		err := qemu.CheckQEMUImgPrerequisite(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'qemu-img' prerequisite check failed")
	})
}
