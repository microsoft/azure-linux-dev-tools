// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spectool"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

const (
	goVendorMount          = "/azldev-go-vendor"
	goVendorTopDir         = "/tmp/azldev-go-rpm"
	goVendorBuild          = goVendorTopDir + "/BUILD"
	goVendorConfig         = "azldev-go-vendor.toml"
	goVendorScratch        = "/azldev-go-scratch"
	goVendorCleanupTimeout = 5 * time.Minute
)

// WithGoVendorRunner provides a template runner. Every conversion uses a fresh,
// isolated root; only the component staging directory is exposed to it.
func WithGoVendorRunner(runner *mock.Runner) PreparerOption {
	return func(p *sourcePreparerImpl) { p.goVendorRunner = runner }
}

func (p *sourcePreparerImpl) prepareGoVendor(
	ctx context.Context, component components.Component, outputDir string,
) (err error) {
	config := component.GetConfig().GoVendor
	if !config.IsEnabled() {
		return nil
	}

	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid Go vendor configuration:\n%w", err)
	}

	if p.dryRunnable.DryRun() {
		slog.Info("Dry run; skipping Go vendor conversion and archive generation", "component", component.GetName())

		return nil
	}

	if p.goVendorRunner == nil {
		return errors.New("go vendoring requires a mock runner with the project's build distro configuration")
	}

	archiveName := component.GetName() + "-vendor.tar.bz2"

	specPath, err := p.resolveSpecPath(component, outputDir)
	if err != nil {
		return err
	}

	data, err := fileutils.ReadFile(p.fs, specPath)
	if err != nil {
		return fmt.Errorf("reading Go spec:\n%w", err)
	}

	converted, err := spec.ConvertGoVendor(data, spec.GoVendorOptions{
		ArchiveName: archiveName, ConfigName: goVendorConfig, ModuleDir: config.ModuleDir,
	})
	if err != nil {
		return fmt.Errorf("converting Go spec for %#q:\n%w", component.GetName(), err)
	}

	if err := p.checkGoVendorSourceCollision(outputDir, archiveName); err != nil {
		return err
	}

	scratch, err := fileutils.MkdirTempInTempDir(p.fs, "azldev-go-vendor-*")
	if err != nil {
		return fmt.Errorf("creating Go vendor scratch directory:\n%w", err)
	}
	defer fileutils.RemoveAllAndUpdateErrorIfNil(p.fs, scratch, &err)

	staging := filepath.Join(scratch, "work")
	if err := fileutils.MkdirAll(p.fs, staging); err != nil {
		return fmt.Errorf("creating Go vendor staging directory:\n%w", err)
	}

	sourceCopy := filepath.Join(scratch, "sources")
	if err := p.stageGoVendorInputs(outputDir, sourceCopy, staging, filepath.Base(specPath),
		converted.PrepSpec, config.ToolConfig); err != nil {
		return err
	}

	runner := p.goVendorRunner.Clone().WithBaseDir(filepath.Join(scratch, "mock")).
		EnableNetwork().WithUnprivileged()
	runner.AddBindMount(sourceCopy, goVendorMount)
	runner.AddBindMount(staging, goVendorScratch)

	if err := p.runGoVendor(ctx, runner, archiveName, filepath.Base(specPath), config); err != nil {
		return err
	}

	if err := p.validateGoVendorInputs(staging, outputDir); err != nil {
		return err
	}

	return p.publishGoVendor(staging, outputDir, specPath, archiveName, converted.Spec, config.ToolConfig)
}

func (p *sourcePreparerImpl) stageGoVendorInputs(
	outputDir, sourceCopy, staging, specFilename string, prepSpec []byte, toolConfig string,
) error {
	if err := fileutils.CopyDirRecursive(p.dryRunnable, p.fs, outputDir, sourceCopy,
		fileutils.CopyDirOptions{CopyFileOptions: fileutils.CopyFileOptions{PreserveFileMode: true}}); err != nil {
		return fmt.Errorf("copying Go sources for isolated prep:\n%w", err)
	}

	if err := fileutils.WriteFile(p.fs,
		filepath.Join(sourceCopy, specFilename), prepSpec, fileperms.PublicFile); err != nil {
		return fmt.Errorf("staging Go prep spec:\n%w", err)
	}

	if err := fileutils.WriteFile(p.fs, filepath.Join(staging, goVendorConfig),
		[]byte(toolConfig), fileperms.PublicFile); err != nil {
		return fmt.Errorf("staging Go vendor tool configuration:\n%w", err)
	}

	return nil
}

func (p *sourcePreparerImpl) publishGoVendor(
	staging, outputDir, specPath, archiveName string, data []byte, toolConfig string,
) error {
	archiveInfo, err := p.fs.Stat(filepath.Join(staging, archiveName))
	if err != nil {
		return fmt.Errorf("missing generated Go vendor archive:\n%w", err)
	}

	if !archiveInfo.Mode().IsRegular() || archiveInfo.Size() == 0 {
		return errors.New("generated Go vendor archive must be a nonempty regular file")
	}

	license, err := fileutils.ReadFile(p.fs, filepath.Join(staging, "license"))
	if err != nil {
		return fmt.Errorf("reading Go vendor license report:\n%w", err)
	}

	expression := strings.TrimSpace(string(license))
	if expression == "" || strings.ContainsAny(expression, "\r\n%") {
		return fmt.Errorf("invalid license expression returned by 'go_vendor_license': %#q", expression)
	}

	editor, err := spec.OpenSpec(bytes.NewReader(data), spec.WithEditor(spec.EditorStructural))
	if err != nil {
		return fmt.Errorf("reading converted Go spec:\n%w", err)
	}

	if err := editor.UpdateExistingTag("", "License", expression); err != nil {
		return fmt.Errorf("updating bundled Go licenses:\n%w", err)
	}

	var finalSpec bytes.Buffer
	if err := editor.Serialize(&finalSpec); err != nil {
		return fmt.Errorf("serializing converted Go spec:\n%w", err)
	}
	// Publish only after generation and license detection succeed.
	if err := fileutils.CopyFile(p.dryRunnable, p.fs,
		filepath.Join(staging, archiveName), filepath.Join(outputDir, archiveName), fileutils.CopyFileOptions{}); err != nil {
		return fmt.Errorf("publishing Go vendor archive:\n%w", err)
	}

	for name, contents := range map[string][]byte{
		filepath.Base(specPath): finalSpec.Bytes(),
		goVendorConfig:          []byte(toolConfig),
	} {
		if err := fileutils.WriteFile(p.fs, filepath.Join(outputDir, name), contents, fileperms.PublicFile); err != nil {
			return fmt.Errorf("publishing Go vendor file %#q:\n%w", name, err)
		}
	}

	return p.recordGoVendorArchive(outputDir, archiveName)
}

func (p *sourcePreparerImpl) runGoVendor(
	ctx context.Context, runner *mock.Runner, archiveName, specFilename string, config *projectconfig.GoVendorConfig,
) (err error) {
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), goVendorCleanupTimeout)
		defer cancel()

		if scrubErr := runner.ScrubRoot(cleanupCtx); scrubErr != nil {
			slog.Error("Failed to clean Go vendor mock root", "error", scrubErr)

			if err == nil {
				err = scrubErr
			}
		}
	}()

	if err := runner.InitRoot(ctx); err != nil {
		return fmt.Errorf("initializing Go vendor mock root:\n%w", err)
	}

	packages := append([]string{
		"go-vendor-tools", "askalono-cli", "golang", "go-rpm-macros", "rpm-build", "rpmdevtools",
		"rpmautospec", "git", "python3-click",
	}, config.PrepPackages...)
	if err := runner.InstallPackages(ctx, packages); err != nil {
		return fmt.Errorf("installing Go vendor prerequisites:\n%w", err)
	}

	cmd, err := runner.CmdInChroot(ctx, []string{
		"bash", "-euo", "pipefail", "-c",
		goVendorScript(archiveName, config.ModuleDir, runner.ConfigOpts()["target_arch"], specFilename),
	}, false)
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

func goVendorScript(archiveName, moduleDir, targetArch, specFilename string) string {
	if moduleDir == "" {
		moduleDir = "."
	}

	quote := shellquote.Join

	prepArgs := []string{
		"rpmbuild", "-bp", "--nodeps", "--define", "_topdir /tmp/azldev-go-rpm",
		"--define", "_builddir " + goVendorBuild, "--define", "_sourcedir " + goVendorMount,
	}
	if targetArch != "" {
		prepArgs = append(prepArgs, "--target", targetArch)
	}

	prepArgs = append(prepArgs, goVendorMount+"/"+specFilename)

	return strings.Join([]string{
		"cd " + goVendorMount,
		"mkdir -p " + goVendorBuild,
		quote("git", "config", "--global", "--add", "safe.directory", goVendorMount),
		quote("rpmautospec", "process-distgit", specFilename, specFilename),
		quote(prepArgs...),
		`IFS= read -r module_root < ` + goVendorTopDir + "/azldev-go-module-root",
		`cd "$module_root"`,
		"cd " + quote(moduleDir),
		"test -f go.mod",
		"export GOTOOLCHAIN=local",
		quote("go_vendor_archive", "create", "--config", goVendorScratch+"/"+goVendorConfig,
			"--output", goVendorScratch+"/"+archiveName, "."),
		quote("tar", "-xf", goVendorScratch+"/"+archiveName),
		quote("go_vendor_license", "--config", goVendorScratch+"/"+goVendorConfig, "--path", ".",
			"report", "--no-prompt", "--autofill", "off", "expression") + " > " + goVendorScratch + "/license",
		"cd " + goVendorMount,
		quote("spectool", "--define", "_sourcedir "+goVendorMount, "-l", "-a",
			specFilename) + " > " + goVendorScratch + "/sources-list",
	}, "\n")
}

func (p *sourcePreparerImpl) checkGoVendorSourceCollision(outputDir, name string) error {
	for _, filename := range []string{name, goVendorConfig} {
		exists, err := fileutils.Exists(p.fs, filepath.Join(outputDir, filename))
		if err != nil {
			return fmt.Errorf("checking generated Go source path:\n%w", err)
		}

		if exists {
			return fmt.Errorf("generated Go vendor file %#q conflicts with an existing source", filename)
		}
	}

	content, err := p.readSourcesFileIfExists(filepath.Join(outputDir, fedorasource.SourcesFileName))
	if err != nil {
		return err
	}

	lines, err := fedorasource.ReadSourcesFile(content)
	if err != nil {
		return fmt.Errorf("reading Go vendor 'sources' entries:\n%w", err)
	}

	for _, line := range lines {
		if line.Entry != nil && line.Entry.Filename == name {
			return fmt.Errorf("generated Go vendor archive %#q conflicts with an upstream 'sources' entry", name)
		}
	}

	return nil
}

func (p *sourcePreparerImpl) recordGoVendorArchive(outputDir, name string) error {
	hash, err := fileutils.ComputeFileHash(p.fs, fileutils.HashTypeSHA512, filepath.Join(outputDir, name))
	if err != nil {
		return fmt.Errorf("hashing generated Go vendor archive:\n%w", err)
	}

	content, err := p.readSourcesFileIfExists(filepath.Join(outputDir, fedorasource.SourcesFileName))
	if err != nil {
		return err
	}

	content = strings.TrimRight(content, "\n")
	if content != "" {
		content += "\n"
	}

	content += fedorasource.FormatSourcesEntry(name, fileutils.HashTypeSHA512, hash) + "\n"
	if err := fileutils.WriteFile(p.fs, filepath.Join(outputDir, fedorasource.SourcesFileName),
		[]byte(content), fileperms.PublicFile); err != nil {
		return fmt.Errorf("recording Go vendor digest:\n%w", err)
	}
	// Generated payloads belong in lookaside storage, not synthetic git commits.
	ignorePath := filepath.Join(outputDir, ".gitignore")

	ignore, err := p.readSourcesFileIfExists(ignorePath)
	if err != nil {
		return err
	}

	if err := fileutils.WriteFile(p.fs, ignorePath,
		[]byte(strings.TrimRight(ignore, "\n")+"\n/"+name+"\n"), fileperms.PublicFile); err != nil {
		return fmt.Errorf("ignoring generated Go archive in git:\n%w", err)
	}

	return nil
}

func (p *sourcePreparerImpl) validateGoVendorInputs(scratch, outputDir string) error {
	data, err := fileutils.ReadFile(p.fs, filepath.Join(scratch, "sources-list"))
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

		info, err := p.fs.Stat(filepath.Join(outputDir, files[0]))
		if err != nil {
			return fmt.Errorf("missing prepared Go source/patch %#q:\n%w", files[0], err)
		}

		if !info.Mode().IsRegular() {
			return fmt.Errorf("go source/patch %#q is not a regular file", files[0])
		}
	}

	return nil
}
