// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package qemu provides utilities for running QEMU virtual machines.
package qemu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
	"github.com/spf13/pflag"
)

const (
	// Architecture names used by QEMU.
	ArchX86_64  = "x86_64"
	ArchAarch64 = "aarch64"
)

// SupportedArchitectures returns the list of supported QEMU architectures.
func SupportedArchitectures() []string {
	return []string{ArchX86_64, ArchAarch64}
}

// Arch represents a QEMU architecture and implements [pflag.Value] for flag parsing.
type Arch string

// Assert that [Arch] implements the [pflag.Value] interface.
var _ pflag.Value = (*Arch)(nil)

func (a *Arch) String() string {
	return string(*a)
}

// Set parses and validates the architecture value from a string.
func (a *Arch) Set(value string) error {
	switch value {
	case ArchX86_64:
		*a = Arch(ArchX86_64)
	case ArchAarch64:
		*a = Arch(ArchAarch64)
	default:
		return fmt.Errorf("unsupported architecture %#q; supported: %v", value, SupportedArchitectures())
	}

	return nil
}

// Type returns a descriptive string used in command-line help.
func (a *Arch) Type() string {
	return "arch"
}

// Runner encapsulates options and dependencies for invoking QEMU.
type Runner struct {
	fs            opctx.FS
	cmdFactory    opctx.CmdFactory
	eventListener opctx.EventListener
	verbose       bool
}

// NewRunner constructs a new [Runner] that can be used to invoke QEMU.
func NewRunner(ctx opctx.Ctx) *Runner {
	return &Runner{
		fs:            ctx.FS(),
		cmdFactory:    ctx,
		eventListener: ctx,
		verbose:       ctx.Verbose(),
	}
}

// RunOptions contains configuration for running a QEMU VM.
type RunOptions struct {
	Arch         string
	FirmwarePath string
	NVRAMPath    string
	DiskPath     string
	DiskType     string
	// CloudInitISOPath is an optional cloud-init NoCloud seed ISO. When set, it is
	// attached as a non-bootable IDE CD-ROM, independent of any install ISO.
	CloudInitISOPath string
	// InstallISOPath is an optional ISO to boot from (e.g., a livecd or installer).
	// When set, it is attached as a bootable SCSI CD-ROM with bootindex=1 so the VM
	// boots from the ISO first; the disk receives bootindex=2.
	InstallISOPath string
	SecureBoot     bool
	SSHPort        int
	CPUs           int
	Memory         string
}

// Run starts a QEMU VM with the specified options.
// It runs the VM interactively with stdin/stdout/stderr attached.
func (r *Runner) Run(ctx context.Context, options RunOptions) error {
	var secureBootOnOff string
	if options.SecureBoot {
		secureBootOnOff = "on"
	} else {
		secureBootOnOff = "off"
	}

	qemuBinary, _ := BinaryAndPackageForArch(options.Arch)
	useKVM := CanUseKVM(options.Arch)
	machineType, cpuType := MachineAndCPUForArch(options.Arch, useKVM)

	qemuArgs := []string{qemuBinary}
	if useKVM {
		qemuArgs = append(qemuArgs, "-enable-kvm")
	}

	qemuArgs = append(qemuArgs,
		"-machine", machineType+",smm=on",
		"-cpu", cpuType,
		"-smp", fmt.Sprintf("cores=%d,threads=1", options.CPUs),
		"-m", options.Memory,
		"-object", "rng-random,filename=/dev/urandom,id=rng0",
		"-device", "virtio-rng-pci,rng=rng0",
		"-global", "driver=cfi.pflash01,property=secure,value="+secureBootOnOff,
		"-drive", fmt.Sprintf("if=pflash,format=raw,unit=0,file=%s,readonly=on", options.FirmwarePath),
		"-drive", "if=pflash,format=raw,unit=1,file="+options.NVRAMPath,
		"-drive", fmt.Sprintf("if=none,id=hd,file=%s,format=%s", options.DiskPath, options.DiskType),
		"-device", "virtio-scsi-pci,id=scsi",
	)

	// Boot order: when an install/live ISO is attached, it boots first (bootindex=1)
	// and the disk follows (bootindex=2). Otherwise, the disk boots first.
	diskBootIndex := 1
	if options.InstallISOPath != "" {
		diskBootIndex = 2
	}

	qemuArgs = append(qemuArgs,
		"-device", fmt.Sprintf("scsi-hd,drive=hd,bootindex=%d", diskBootIndex),
	)

	if options.InstallISOPath != "" {
		qemuArgs = append(qemuArgs,
			"-drive", fmt.Sprintf("if=none,id=installcd,file=%s,media=cdrom,readonly=on", options.InstallISOPath),
			"-device", "scsi-cd,drive=installcd,bootindex=1",
		)
	}

	// Attach the cloud-init seed ISO (if any) as a separate non-bootable IDE CD-ROM.
	// It coexists with an install ISO on a different bus. Whether the booted system
	// honors it is a runtime concern (cloud-init must be installed and enabled).
	if options.CloudInitISOPath != "" {
		qemuArgs = append(qemuArgs, "-cdrom", options.CloudInitISOPath)
	}

	qemuArgs = append(qemuArgs,
		"-netdev", fmt.Sprintf("user,id=n1,hostfwd=tcp::%d-:22", options.SSHPort),
		"-device", "virtio-net-pci,netdev=n1",
		"-nographic", "-serial", "mon:stdio",
	)

	qemuCmd := exec.CommandContext(ctx, "sudo", qemuArgs...)
	qemuCmd.Stdout = os.Stdout
	qemuCmd.Stderr = os.Stderr
	qemuCmd.Stdin = os.Stdin

	cmd, err := r.cmdFactory.Command(qemuCmd)
	if err != nil {
		return fmt.Errorf("failed to create QEMU command:\n%w", err)
	}

	err = cmd.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to run VM in QEMU:\n%w", err)
	}

	return nil
}

// FindFirmware locates UEFI firmware and NVRAM template files for the given architecture.
// Returns paths to the firmware code and NVRAM template files.
func (r *Runner) FindFirmware(arch string, secureBoot bool) (fwPath, nvramTemplatePath string, err error) {
	var (
		fwPaths            []string
		nvramTemplatePaths []string
	)

	switch arch {
	case ArchAarch64:
		nvramTemplatePaths = []string{
			"/usr/share/AAVMF/AAVMF_VARS.fd",
			"/usr/share/edk2/aarch64/vars-template-pflash.raw",
		}

		if secureBoot {
			fwPaths = []string{
				"/usr/share/AAVMF/AAVMF_CODE.secboot.fd",
				"/usr/share/edk2/aarch64/QEMU_EFI-pflash.secboot.raw",
			}
		} else {
			fwPaths = []string{
				"/usr/share/AAVMF/AAVMF_CODE.fd",
				"/usr/share/edk2/aarch64/QEMU_EFI-pflash.raw",
			}
		}

	case ArchX86_64:
		if secureBoot {
			fwPaths = []string{
				"/usr/share/OVMF/OVMF_CODE.secboot.fd",
				"/usr/share/OVMF/OVMF_CODE_4M.secboot.fd",
			}
			nvramTemplatePaths = []string{
				"/usr/share/OVMF/OVMF_VARS.secboot.fd",
				"/usr/share/OVMF/OVMF_VARS_4M.secboot.fd",
			}
		} else {
			fwPaths = []string{
				"/usr/share/OVMF/OVMF_CODE.fd",
				"/usr/share/OVMF/OVMF_CODE_4M.fd",
			}
			nvramTemplatePaths = []string{
				"/usr/share/OVMF/OVMF_VARS.fd",
				"/usr/share/OVMF/OVMF_VARS_4M.fd",
			}
		}
	}

	for _, candidatePath := range fwPaths {
		if _, statErr := r.fs.Stat(candidatePath); statErr == nil {
			fwPath = candidatePath

			break
		}
	}

	if fwPath == "" {
		return fwPath, nvramTemplatePath, errors.New("firmware not found")
	}

	for _, candidatePath := range nvramTemplatePaths {
		if _, statErr := r.fs.Stat(candidatePath); statErr == nil {
			nvramTemplatePath = candidatePath

			break
		}
	}

	if nvramTemplatePath == "" {
		return fwPath, nvramTemplatePath, errors.New("NVRAM template not found")
	}

	return fwPath, nvramTemplatePath, nil
}

// CheckPrerequisites verifies that QEMU and sudo are available for the given architecture.
func CheckPrerequisites(ctx opctx.Ctx, arch string) error {
	qemuBinary, qemuPackage := BinaryAndPackageForArch(arch)

	if err := prereqs.RequireExecutable(ctx, qemuBinary, &prereqs.PackagePrereq{
		AzureLinuxPackages: []string{qemuPackage},
		FedoraPackages:     []string{qemuPackage},
	}); err != nil {
		return fmt.Errorf("QEMU prerequisite check failed:\n%w", err)
	}

	if err := prereqs.RequireExecutable(ctx, "sudo", nil); err != nil {
		return fmt.Errorf("sudo prerequisite check failed:\n%w", err)
	}

	return nil
}

// CheckQEMUImgPrerequisite verifies that the 'qemu-img' tool is available.
func CheckQEMUImgPrerequisite(ctx opctx.Ctx) error {
	if err := prereqs.RequireExecutable(ctx, "qemu-img", &prereqs.PackagePrereq{
		AzureLinuxPackages: []string{"qemu-img"},
		FedoraPackages:     []string{"qemu-img"},
	}); err != nil {
		return fmt.Errorf("'qemu-img' prerequisite check failed:\n%w", err)
	}

	return nil
}

// CreateEmptyQcow2 creates an empty qcow2 disk image at the given path with the specified size.
// The size should be a QEMU-compatible size string (e.g., "10G", "512M").
func (r *Runner) CreateEmptyQcow2(ctx context.Context, path, size string) error {
	createCmd := exec.CommandContext(ctx, "qemu-img", "create", "-f", "qcow2", path, size)

	var stderr bytes.Buffer

	createCmd.Stderr = &stderr

	cmd, err := r.cmdFactory.Command(createCmd)
	if err != nil {
		return fmt.Errorf("failed to create qemu-img command:\n%w", err)
	}

	err = cmd.Run(ctx)
	if err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText != "" {
			return fmt.Errorf("failed to create empty qcow2 disk image (qemu-img stderr: %s):\n%w",
				stderrText, err)
		}

		return fmt.Errorf("failed to create empty qcow2 disk image:\n%w", err)
	}

	return nil
}

// GoArchToQEMUArch converts Go's GOARCH to QEMU architecture names.
func GoArchToQEMUArch(goarch string) string {
	switch goarch {
	case "amd64":
		return ArchX86_64
	case "arm64":
		return ArchAarch64
	default:
		return goarch
	}
}

// BinaryAndPackageForArch returns the QEMU binary name and package for the given architecture.
func BinaryAndPackageForArch(arch string) (binary, pkg string) {
	switch arch {
	case ArchAarch64:
		return "qemu-system-aarch64", "qemu-system-aarch64"
	case ArchX86_64:
		return "qemu-system-x86_64", "qemu-system-x86"
	default:
		return "qemu-system-" + arch, "qemu-system-" + arch
	}
}

// CanUseKVM returns true if KVM acceleration can be used for the given target architecture.
// KVM is only available when the target architecture matches the host architecture.
func CanUseKVM(targetArch string) bool {
	hostArch := GoArchToQEMUArch(runtime.GOARCH)

	return targetArch == hostArch
}

// MachineAndCPUForArch returns the QEMU machine type and CPU for the given architecture.
func MachineAndCPUForArch(arch string, useKVM bool) (machine, cpu string) {
	const machineVirt = "virt"

	switch arch {
	case ArchAarch64:
		if useKVM {
			return machineVirt, "host"
		}

		return machineVirt, "cortex-a57"
	case ArchX86_64:
		if useKVM {
			return "q35", "host"
		}

		return "q35", "qemu64"
	default:
		return machineVirt, "max"
	}
}
