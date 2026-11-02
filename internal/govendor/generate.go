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
	"github.com/pelletier/go-toml/v2"
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
	ModuleDir, ConfigContents    string
	ExpectedLicense              string
	PrepSpec                     []byte
	Packages                     []string
	configModuleDir              string
}

// Artifact is validated generation output, ready for publication by the caller.
type Artifact struct {
	Path, Name, License, Digest string
}

// Generate prepares inputs and generates an archive in one isolated mock root.
// It never changes SourceDir or publishes a spec or source manifest.
func Generate(ctx context.Context, fs opctx.FS, base *mock.Runner, request Request) (Artifact, error) {
	request, err := resolveModuleDir(request)
	if err != nil {
		return Artifact{}, err
	}

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

	// Snapshot only regular outputs into a host-only directory. Subsequent
	// validation, hashing and publication never reopen chroot-writable paths.
	trusted := filepath.Join(request.WorkDir, "artifacts")
	if err := fileutils.MkdirAll(fs, trusted); err != nil {
		return Artifact{}, fmt.Errorf("creating trusted Go vendor output directory:\n%w", err)
	}

	for _, name := range []string{request.ArchiveFile, "archive-name", "license", "sources-list"} {
		if err := fileutils.CopyFileCrossFS(nilDryRun{}, fs, filepath.Join(staging, name),
			fs, filepath.Join(trusted, name), fileutils.CopyFileOptions{NoFollow: true}); err != nil {
			return Artifact{}, fmt.Errorf("collecting Go vendor output %#q:\n%w", name, err)
		}
	}

	if err := validateInputs(fs, trusted, request.SourceDir); err != nil {
		return Artifact{}, err
	}

	return readArtifact(fs, trusted, request)
}

type nilDryRun struct{}

func (nilDryRun) DryRun() bool { return false }

func resolveModuleDir(request Request) (Request, error) {
	var settings map[string]any
	if err := toml.Unmarshal([]byte(request.ConfigContents), &settings); err != nil {
		return request, fmt.Errorf("parsing Go vendor tool configuration:\n%w", err)
	}

	general, _ := settings["general"].(map[string]any)
	if raw, exists := general["go_mod_dir"]; exists {
		directory, ok := raw.(string)
		if !ok {
			return request, errors.New("go vendor 'general.go_mod_dir' must be a string")
		}

		request.configModuleDir = directory
	}

	for _, directory := range []string{request.ModuleDir, request.configModuleDir} {
		if directory == "" || directory == "." {
			continue
		}

		if err := fileutils.ValidateRelPath(directory); err != nil {
			return request, fmt.Errorf("invalid Go vendor module directory:\n%w", err)
		}

		if strings.ContainsAny(directory, "%\r\n") {
			return request, errors.New("go vendor module directory must not contain RPM macros or newlines")
		}
	}

	if request.configModuleDir != "" {
		if request.ModuleDir != "" && request.ModuleDir != request.configModuleDir {
			return request, errors.New("'module-dir' conflicts with upstream 'general.go_mod_dir'; " +
				"use 'config-file' to replace the upstream settings")
		}

		// Native tools preserve this subdirectory in the archive and use it for
		// licensing. Keep their root and config intact, without descending twice.
		request.ModuleDir = ""
	}

	return request, nil
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
		filepath.Join(staging, ConfigFilename):      []byte(request.ConfigContents),
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
			"and 'config-file' for reviewed license exceptions:\n%s\n%w", output, err)
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
		quote("test", "-f", filepath.Join(request.configModuleDir, "go.mod")),
		`module_root=$(pwd -P)`,
		"export GOTOOLCHAIN=local",
		"cd " + sourceMount,
		quote("go_vendor_archive", "create", "--config", outputMount+"/"+ConfigFilename,
			"--output", outputMount+"/"+request.ArchiveFile) + ` "$module_root"`,
		`cd "$module_root"`,
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
