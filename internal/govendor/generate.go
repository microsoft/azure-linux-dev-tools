// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package govendor generates vendor artifacts without resolving components or editing specs.
package govendor

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spectool"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

const (
	// ConfigFilename is the tool configuration filename inside generation staging.
	ConfigFilename = "azldev-go-vendor.toml"
	sourceMount    = "/azldev-go-vendor"
	outputMount    = "/azldev-go-scratch"
	topDir         = "/tmp/azldev-go-rpm"
	cleanupTimeout = 5 * time.Minute
)

// Request supplies prepared spec bytes and input files. WorkDir must be a private
// scratch directory outside SourceDir; the caller owns its cleanup and dry-run policy.
// PrepSpec must record 'azldev-go-module-root' and 'azldev-go-archive-name' in RPM's topdir.
type Request struct {
	SourceDir, WorkDir, SpecName string
	ArchiveFile, ArchiveSuffix   string
	ModuleDir, ToolConfig        string
	ExpectedLicense              string
	PrepSpec                     []byte
	Packages                     []string
}

// Artifact is validated generation output, ready for publication by the caller.
type Artifact struct {
	Path, Name, License, Digest string
}

// Generate prepares inputs and generates an archive in one isolated mock root.
// It never changes SourceDir or publishes a spec or source manifest.
func Generate(ctx context.Context, fs opctx.FS, base *mock.Runner, request Request) (Artifact, error) {
	staging := filepath.Join(request.WorkDir, "work")

	sourceCopy := filepath.Join(request.WorkDir, "sources")
	if err := stage(fs, request, sourceCopy, staging); err != nil {
		return Artifact{}, err
	}

	runner := base.Clone().WithBaseDir(filepath.Join(request.WorkDir, "mock")).EnableNetwork().WithUnprivileged()
	runner.AddBindMount(sourceCopy, sourceMount).AddBindMount(staging, outputMount)

	if err := execute(ctx, runner, request); err != nil {
		return Artifact{}, err
	}

	if err := validateInputs(fs, staging, request.SourceDir); err != nil {
		return Artifact{}, err
	}

	return readArtifact(fs, staging, request)
}

func stage(fs opctx.FS, request Request, sourceCopy, staging string) error {
	for _, name := range []string{request.SpecName, request.ArchiveFile} {
		if err := fileutils.ValidateFilename(name); err != nil {
			return fmt.Errorf("invalid Go vendor staging filename:\n%w", err)
		}
	}

	if err := fileutils.MkdirAll(fs, staging); err != nil {
		return fmt.Errorf("creating Go vendor staging directory:\n%w", err)
	}

	if err := fileutils.CopyDirRecursiveCrossFS(fs, request.SourceDir, fs, sourceCopy,
		fileutils.CopyDirOptions{CopyFileOptions: fileutils.CopyFileOptions{PreserveFileMode: true}}); err != nil {
		return fmt.Errorf("copying Go sources for isolated prep:\n%w", err)
	}

	for path, data := range map[string][]byte{
		filepath.Join(sourceCopy, request.SpecName): request.PrepSpec,
		filepath.Join(staging, ConfigFilename):      []byte(request.ToolConfig),
	} {
		if err := fileutils.WriteFile(fs, path, data, fileperms.PublicFile); err != nil {
			return fmt.Errorf("staging Go vendor input %#q:\n%w", path, err)
		}
	}

	return nil
}

func execute(ctx context.Context, runner *mock.Runner, request Request) (err error) {
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()

		if cleanupErr := runner.ScrubRoot(cleanupCtx); cleanupErr != nil {
			slog.Error("Failed to clean Go vendor mock root", "error", cleanupErr)

			if err == nil {
				err = cleanupErr
			}
		}
	}()

	if err := runner.InitRoot(ctx); err != nil {
		return fmt.Errorf("initializing Go vendor mock root:\n%w", err)
	}

	packages := append([]string{
		"go-vendor-tools", "askalono-cli", "golang", "go-rpm-macros", "rpm-build", "rpmdevtools",
		"rpmautospec", "git", "python3-click",
	}, request.Packages...)
	if err := runner.InstallPackages(ctx, packages); err != nil {
		return fmt.Errorf("installing Go vendor prerequisites:\n%w", err)
	}

	cmd, err := runner.CmdInChroot(ctx, generationCommand(request, runner.ConfigOpts()["target_arch"]), false)
	if err != nil {
		return fmt.Errorf("constructing Go vendor command:\n%w", err)
	}

	output, err := cmd.RunAndGetOutput(ctx)
	if err != nil {
		return fmt.Errorf("go vendor generation or license detection failed; use 'prep-packages' for missing prep tools "+
			"and 'tool-config' for reviewed license exceptions:\n%s\n%w", output, err)
	}

	return nil
}

func generationCommand(request Request, targetArch string) []string {
	quote := shellquote.Join

	prepArgs := []string{
		"rpmbuild", "-bp", "--nodeps", "--define", "_topdir " + topDir,
		"--define", "_builddir " + topDir + "/BUILD", "--define", "_sourcedir " + sourceMount,
	}
	if targetArch != "" {
		prepArgs = append(prepArgs, "--target", targetArch)
	}

	licenseArgs := []string{
		"go_vendor_license", "--config", outputMount + "/" + ConfigFilename, "--path", ".",
		"report", "--no-prompt", "--autofill", "off", "expression",
	}
	if request.ExpectedLicense != "" {
		licenseArgs = append(licenseArgs, "--verify", request.ExpectedLicense)
	}

	commands := []string{
		"cd " + sourceMount,
		"mkdir -p " + topDir + "/BUILD",
		quote("git", "config", "--global", "--add", "safe.directory", sourceMount),
		quote("rpmautospec", "process-distgit", request.SpecName, request.SpecName),
		quote(append(prepArgs, sourceMount+"/"+request.SpecName)...),
		quote("cp", topDir+"/azldev-go-archive-name", outputMount+"/archive-name"),
		`IFS= read -r module_root < ` + topDir + "/azldev-go-module-root",
		`cd "$module_root"`,
		"cd " + quote(cmp.Or(request.ModuleDir, ".")),
		"test -f go.mod",
		"export GOTOOLCHAIN=local",
		quote("go_vendor_archive", "create", "--config", outputMount+"/"+ConfigFilename,
			"--output", outputMount+"/"+request.ArchiveFile, "."),
		quote("tar", "-xf", outputMount+"/"+request.ArchiveFile),
		quote(licenseArgs...) + " > " + outputMount + "/license",
		"cd " + sourceMount,
		quote("spectool", "--define", "_sourcedir "+sourceMount, "-l", "-a", request.SpecName) +
			" > " + outputMount + "/sources-list",
	}

	return []string{"bash", "-euo", "pipefail", "-c", strings.Join(commands, "\n")}
}

func readArtifact(fs opctx.FS, staging string, request Request) (Artifact, error) {
	result := Artifact{Path: filepath.Join(staging, request.ArchiveFile)}

	info, err := fs.Stat(result.Path)
	if err != nil {
		return result, fmt.Errorf("missing generated Go vendor archive:\n%w", err)
	}

	if !info.Mode().IsRegular() || info.Size() == 0 {
		return result, errors.New("generated Go vendor archive must be a nonempty regular file")
	}

	for name, destination := range map[string]*string{"archive-name": &result.Name, "license": &result.License} {
		data, err := fileutils.ReadFile(fs, filepath.Join(staging, name))
		if err != nil {
			return result, fmt.Errorf("reading Go vendor output %#q:\n%w", name, err)
		}

		*destination = strings.TrimSpace(string(data))
		if *destination == "" || strings.ContainsAny(*destination, "%\r\n") {
			return result, fmt.Errorf("invalid Go vendor output %#q: %#q", name, *destination)
		}
	}

	if err := fileutils.ValidateFilename(result.Name); err != nil {
		return result, fmt.Errorf("invalid expanded Go vendor archive name:\n%w", err)
	}

	if !strings.HasSuffix(result.Name, request.ArchiveSuffix) {
		return result, fmt.Errorf("unexpected expanded Go vendor archive name %#q", result.Name)
	}

	result.Digest, err = fileutils.ComputeFileHash(fs, fileutils.HashTypeSHA512, result.Path)
	if err != nil {
		return result, fmt.Errorf("hashing generated Go vendor archive:\n%w", err)
	}

	return result, nil
}

func validateInputs(fs opctx.FS, staging, sourceDir string) error {
	data, err := fileutils.ReadFile(fs, filepath.Join(staging, "sources-list"))
	if err != nil {
		return fmt.Errorf("reading prepared Go source list:\n%w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}

		files := spectool.ParseSpectoolOutput(line)
		if len(files) != 1 || strings.Contains(files[0], "%") {
			return fmt.Errorf("cannot resolve Go source/patch reference %#q", line)
		}

		info, err := fs.Stat(filepath.Join(sourceDir, files[0]))
		if err != nil {
			return fmt.Errorf("missing prepared Go source/patch %#q:\n%w", files[0], err)
		}

		if !info.Mode().IsRegular() {
			return fmt.Errorf("go source/patch %#q is not a regular file", files[0])
		}
	}

	return nil
}
