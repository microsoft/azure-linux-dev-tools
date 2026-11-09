// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package govendor generates source archives without changing published RPM packaging.
package govendor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/sys/unix"
)

// Request contains an isolated private spec and the effective native tool settings.
type Request struct {
	SourceDir, WorkDir, SpecName string
	PrepSpec                     []byte
	Config                       string
	ModuleDir                    string
	Packages                     []string
}

// Artifact is a host-owned copy of the generated tarball.
type Artifact struct {
	Name, Path string
}

const cleanupTimeout = 5 * time.Minute

// Generate runs prep and native go-vendor-tools in a fresh mock root.
func Generate(ctx context.Context, fs opctx.FS, base *mock.Runner, req Request) (Artifact, error) {
	output, err := stage(fs, req)
	if err != nil {
		return Artifact{}, err
	}

	if err := execute(ctx, base, req, output); err != nil {
		return Artifact{}, err
	}

	return readArtifact(fs, req.WorkDir, output)
}

func stage(fs opctx.FS, req Request) (string, error) {
	relative, err := filepath.Rel(req.SourceDir, req.WorkDir)
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return "", errors.New("vendor work directory must be outside the prepared sources")
	}

	if err := fileutils.ValidateFilename(req.SpecName); err != nil {
		return "", fmt.Errorf("validating vendor spec basename:\n%w", err)
	}

	staged := filepath.Join(req.WorkDir, "sources")

	output := filepath.Join(req.WorkDir, "output")
	if err := fileutils.CopyDirRecursiveCrossFS(fs, req.SourceDir, fs, staged,
		fileutils.CopyDirOptions{CopyFileOptions: fileutils.CopyFileOptions{PreserveFileMode: true}}); err != nil {
		return "", fmt.Errorf("staging Go vendor inputs:\n%w", err)
	}

	if err := fileutils.MkdirAll(fs, output); err != nil {
		return "", fmt.Errorf("creating vendor output directory:\n%w", err)
	}

	for path, content := range map[string][]byte{
		filepath.Join(staged, req.SpecName):  req.PrepSpec,
		filepath.Join(output, "config.toml"): []byte(req.Config),
	} {
		if err := fileutils.WriteFile(fs, path, content, fileperms.PublicFile); err != nil {
			return "", fmt.Errorf("writing private vendor input:\n%w", err)
		}
	}

	return output, nil
}

func execute(ctx context.Context, base *mock.Runner, req Request, output string) (err error) {
	command, err := generationCommand(req, base.ConfigOpts()["target_arch"])
	if err != nil {
		return err
	}

	runner := base.Clone().WithBaseDir(filepath.Join(req.WorkDir, "mock")).EnableNetwork().WithUnprivileged()
	runner.AddBindMount(filepath.Join(req.WorkDir, "sources"), "/azldev-vendor-sources").
		AddBindMount(output, "/azldev-vendor-output")

	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()

		if cleanupErr := runner.ScrubRoot(cleanup); cleanupErr != nil {
			slog.Error("Failed to scrub Go vendor generation root", "error", cleanupErr)

			if err == nil {
				err = cleanupErr
			}
		}
	}()

	if err := runner.InitRoot(ctx); err != nil {
		return fmt.Errorf("initializing Go vendor root:\n%w", err)
	}

	packages := append([]string{
		"go-vendor-tools", "golang", "go-rpm-macros", "rpm-build",
		"rpmdevtools", "rpmautospec", "git", "python3-click",
	}, req.Packages...)
	if err := runner.InstallPackages(ctx, packages); err != nil {
		return fmt.Errorf("installing Go vendor tools:\n%w", err)
	}

	cmd, err := runner.CmdInChroot(ctx, command, false)
	if err != nil {
		return fmt.Errorf("constructing vendor generation command:\n%w", err)
	}

	log, err := cmd.RunAndGetOutput(ctx)
	if err != nil {
		return fmt.Errorf("generating Go vendor archive:\n%s\n%w", log, err)
	}

	return nil
}

func readArtifact(fs opctx.FS, workDir, output string) (Artifact, error) {
	var artifact Artifact
	// Copy opened regular files, never following chroot-controlled symlinks.
	nameFile := filepath.Join(workDir, "name")
	if err := collect(fs, filepath.Join(output, "name"), nameFile); err != nil {
		return artifact, err
	}

	name, err := fileutils.ReadFile(fs, nameFile)
	if err != nil {
		return artifact, fmt.Errorf("reading generated vendor name:\n%w", err)
	}

	artifact.Name = strings.TrimSpace(string(name))
	if err := fileutils.ValidateFilename(artifact.Name); err != nil {
		return artifact, fmt.Errorf("invalid generated vendor filename:\n%w", err)
	}

	if strings.ContainsAny(artifact.Name, "%\r\n") || !archiveFilename(artifact.Name) {
		return artifact, fmt.Errorf("unsupported generated vendor filename %#q", artifact.Name)
	}

	artifact.Path = filepath.Join(workDir, artifact.Name)
	if err := collect(fs, filepath.Join(output, artifact.Name), artifact.Path); err != nil {
		return artifact, err
	}

	return artifact, nil
}

func generationCommand(req Request, arch string) ([]string, error) {
	var config struct {
		General struct {
			ModuleDir string `toml:"go_mod_dir"`
		} `toml:"general"`
	}
	if err := toml.Unmarshal([]byte(req.Config), &config); err != nil {
		return nil, fmt.Errorf("parsing native vendor config:\n%w", err)
	}

	for _, directory := range []string{req.ModuleDir, config.General.ModuleDir} {
		if directory != "" {
			if err := fileutils.ValidateRelPath(directory); err != nil {
				return nil, fmt.Errorf("validating module directory:\n%w", err)
			}

			if strings.ContainsAny(directory, "%\r\n") {
				return nil, errors.New("invalid Go vendor module directory")
			}
		}
	}

	module := req.ModuleDir
	if config.General.ModuleDir != "" {
		if module != "" && module != config.General.ModuleDir {
			return nil, errors.New("'module-dir' conflicts with native 'general.go_mod_dir'")
		}

		module = ""
	}

	if module == "" {
		module = "."
	}

	quote := shellquote.Join

	prep := []string{
		"rpmbuild", "-bp", "--nodeps", "--define", "_topdir /tmp/azldev-vendor",
		"--define", "_sourcedir /azldev-vendor-sources",
	}
	if arch != "" {
		prep = append(prep, "--target", arch)
	}

	prep = append(prep, req.SpecName)
	lines := []string{
		"export GOTOOLCHAIN=local",
		"cd /azldev-vendor-sources",
		"mkdir -p /tmp/azldev-vendor/BUILD",
		"git config --global --add safe.directory /azldev-vendor-sources",
		quote("rpmautospec", "process-distgit", req.SpecName, req.SpecName),
		quote(prep...),
		"IFS= read -r root < /tmp/azldev-vendor/azldev-vendor-root",
		"IFS= read -r name < /tmp/azldev-vendor/azldev-vendor-name",
		`case "$name" in ""|*/*|*%*|.*) echo 'Invalid vendor filename' >&2; exit 1;; esac`,
		`cd "$root"`,
		"cd " + quote(module),
		quote("test", "-f", filepath.Join(config.General.ModuleDir, "go.mod")),
		`root=$(pwd -P)`,
		"cd /azldev-vendor-sources",
		`go_vendor_archive create --config /azldev-vendor-output/config.toml --output "/azldev-vendor-output/$name" "$root"`,
		`printf '%s\n' "$name" > /azldev-vendor-output/name`,
	}

	return []string{"bash", "-euo", "pipefail", "-c", strings.Join(lines, "\n")}, nil
}

func archiveFilename(name string) bool {
	for _, suffix := range []string{".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tar.zstd"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	return false
}

func collect(fs opctx.FS, source, destination string) (err error) {
	file, err := fs.OpenFile(source, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("opening generated vendor output %#q:\n%w", source, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("checking generated vendor output:\n%w", err)
	}

	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("generated output %#q must be a nonempty regular file", source)
	}

	dest, err := fs.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileperms.PublicFile)
	if err != nil {
		return fmt.Errorf("creating private vendor artifact:\n%w", err)
	}

	defer func() {
		if closeErr := dest.Close(); err == nil {
			err = closeErr
		}
	}()

	_, err = io.Copy(dest, file)
	if err != nil {
		return fmt.Errorf("copying generated vendor output:\n%w", err)
	}

	return nil
}
