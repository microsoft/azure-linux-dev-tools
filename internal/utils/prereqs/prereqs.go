// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package prereqs

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/acobaugh/osrelease"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// Represents a prerequisite resource that must be present in the host; can
// provide information on how to acquire that resource if not ready.
type PackagePrereq struct {
	// List of Azure Linux distro packages that must be installed to provide this prerequisite.
	AzureLinuxPackages []string
	// List of Fedora distro packages that must be installed to provide this prerequisite.
	FedoraPackages []string
}

const (
	// The OS ID of Azure Linux.
	OSIDAzureLinux = "azurelinux"
	// The OS ID of Fedora.
	OSIDFedora = "fedora"
)

// ErrMissingExecutable is returned when a required executable cannot be found or acquired.
var ErrMissingExecutable = errors.New("executable missing, no auto-resolution")

// Checks that the executable identified by `programName` is available in the host system. If it
// can't be found but `prereq` is provided, then will attempt to auto-install the prerequisite,
// dependent on the policy configured in `ctx`. If `programName` isn't present and can't be
// auto-installed, returns an error.
func RequireExecutable(ctx opctx.Ctx, programName string, prereq *PackagePrereq) error {
	if ctx.CommandInSearchPath(programName) {
		return nil
	}

	var autoInstall bool

	if prereq != nil {
		prompt := fmt.Sprintf(
			"Required program '%s' not found; would you like it to be installed for you?", programName)
		autoInstall = ctx.ConfirmAutoResolution(prompt)
	}

	if !autoInstall {
		return fmt.Errorf("program '%s' required:\n%w", programName, ErrMissingExecutable)
	}

	err := prereq.Install(ctx)
	if err != nil {
		return err
	}

	// Try one last time.
	if !ctx.CommandInSearchPath(programName) {
		return fmt.Errorf("required program '%s' still not found after installing known prerequisites", programName)
	}

	return nil
}

// Installs the prerequisite on the host system.
func (p *PackagePrereq) Install(ctx opctx.Ctx) error {
	var (
		packageNames []string
		installer    string
	)

	installer, packageNames, err := p.selectInstallerAndPackagesForHost(ctx)
	if err != nil {
		return err
	}

	ctx.Event("Installing prerequisite", "installer", installer, "packages", packageNames)

	var cmd *exec.Cmd

	cmd, err = makePackageInstallCmd(ctx, installer, packageNames)
	if err != nil {
		return err
	}

	if ctx.DryRun() {
		slog.Info("Dry run; would run installer", "command", cmd.Path, "args", cmd.Args)

		return nil
	}

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to install prerequisite packages '%s' using '%s':\n%w", packageNames, installer, err)
	}

	return nil
}

func (p *PackagePrereq) selectInstallerAndPackagesForHost(
	ctx opctx.Ctx,
) (installer string, packageNames []string, err error) {
	var osid string

	osid, err = getHostOSID(ctx)
	if err != nil {
		return installer, packageNames, err
	}

	switch osid {
	case OSIDAzureLinux:
		return "tdnf", p.AzureLinuxPackages, nil
	case OSIDFedora:
		return "dnf", p.FedoraPackages, nil
	default:
		return installer, packageNames, fmt.Errorf("host OS not supported: '%s'", osid)
	}
}

func getHostOSID(ctx opctx.Ctx) (osid string, err error) {
	osReleaseBytes, readErr := fileutils.ReadFile(ctx.FS(), osrelease.EtcOsRelease)
	if readErr != nil && errors.Is(readErr, os.ErrNotExist) {
		osReleaseBytes, readErr = fileutils.ReadFile(ctx.FS(), osrelease.UsrLibOsRelease)
	}

	if readErr != nil {
		return osid, fmt.Errorf("failed to read os-release file to detect host OS:\n%w", readErr)
	}

	osRelease, err := osrelease.ReadString(string(osReleaseBytes))
	if err != nil {
		return osid, fmt.Errorf("failed to parse os-release file to detect host OS:\n%w", err)
	}

	if osid, found := osRelease["ID"]; found {
		return osid, nil
	}

	return osid, errors.New("failed to find OS ID in host's os-release file")
}

func makePackageInstallCmd(ctx opctx.Ctx, installer string, packageNames []string) (cmd *exec.Cmd, err error) {
	switch installer {
	case "tdnf":
		fallthrough
	case "dnf5":
		fallthrough
	case "dnf":
		args := append([]string{installer, "install", "-y"}, packageNames...)

		return exec.CommandContext(ctx, "sudo", args...), nil
	default:
		return cmd, fmt.Errorf("unsupported installer '%s'", installer)
	}
}
