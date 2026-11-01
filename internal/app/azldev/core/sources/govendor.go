// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/govendor"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// WithGoVendorRunner provides a template for an isolated generation root.
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
		slog.Info("Dry run; skipping Go vendor generation", "component", component.GetName())

		return nil
	}

	if p.goVendorRunner == nil {
		return errors.New("go vendoring requires a mock runner with the project's build distro configuration")
	}

	specPath, err := p.resolveSpecPath(component, outputDir)
	if err != nil {
		return err
	}

	data, err := fileutils.ReadFile(p.fs, specPath)
	if err != nil {
		return fmt.Errorf("reading Go spec:\n%w", err)
	}

	plan, err := spec.ConvertGoVendor(data, spec.GoVendorOptions{
		ArchiveName: component.GetName() + "-vendor.tar.bz2",
		ConfigName:  govendor.ConfigFilename, ModuleDir: config.ModuleDir,
	})
	if err != nil {
		return fmt.Errorf("planning Go vendoring for %#q:\n%w", component.GetName(), err)
	}

	toolConfig, err := p.resolveGoVendorConfig(outputDir, plan, config.ToolConfig)
	if err != nil {
		return err
	}

	scratch, err := fileutils.MkdirTempInTempDir(p.fs, "azldev-go-vendor-*")
	if err != nil {
		return fmt.Errorf("creating Go vendor scratch directory:\n%w", err)
	}
	defer fileutils.RemoveAllAndUpdateErrorIfNil(p.fs, scratch, &err)

	slog.Info("Generating Go vendor archive", "spec", filepath.Base(specPath), "regenerating", plan.AllowReplacement)

	artifact, err := govendor.Generate(ctx, p.fs, p.goVendorRunner, govendor.Request{
		SourceDir: outputDir, WorkDir: scratch, SpecName: filepath.Base(specPath), PrepSpec: plan.PrepSpec,
		ArchiveFile: plan.ArchiveFile, ArchiveSuffix: plan.ArchiveSuffix, ModuleDir: config.ModuleDir,
		ToolConfig: toolConfig, Packages: config.PrepPackages, ExpectedLicense: plan.ExpectedLicense,
	})
	if err != nil {
		return fmt.Errorf("generating Go vendor sources:\n%w", err)
	}

	return p.publishGoVendor(outputDir, filepath.Base(specPath), toolConfig, plan, artifact)
}

func (p *sourcePreparerImpl) resolveGoVendorConfig(
	outputDir string, plan *spec.GoVendorSpec, configured string,
) (string, error) {
	if !plan.AllowReplacement {
		if err := p.checkGoVendorSourceCollision(outputDir, plan.ArchiveFile, plan.ConfigName); err != nil {
			return "", err
		}
	}

	if configured != "" || !plan.AllowReplacement {
		return configured, nil
	}

	data, err := fileutils.ReadFile(p.fs, filepath.Join(outputDir, plan.ConfigName))
	if err != nil {
		return "", fmt.Errorf("reading upstream Go vendor configuration:\n%w", err)
	}

	return string(data), nil
}

func (p *sourcePreparerImpl) publishGoVendor(
	outputDir, specName, toolConfig string, plan *spec.GoVendorSpec, artifact govendor.Artifact,
) error {
	if !plan.AllowReplacement && artifact.Name != plan.ArchiveFile {
		return fmt.Errorf("generated Go archive %#q does not match planned name %#q", artifact.Name, plan.ArchiveFile)
	}

	finalSpec, err := plan.FinalizeLicense(artifact.License)
	if err != nil {
		return fmt.Errorf("finalizing Go vendor spec:\n%w", err)
	}

	if err := fileutils.CopyFile(p.dryRunnable, p.fs, artifact.Path,
		filepath.Join(outputDir, artifact.Name), fileutils.CopyFileOptions{}); err != nil {
		return fmt.Errorf("publishing Go vendor archive:\n%w", err)
	}

	for name, contents := range map[string][]byte{specName: finalSpec, plan.ConfigName: []byte(toolConfig)} {
		if err := fileutils.WriteFile(p.fs, filepath.Join(outputDir, name), contents, fileperms.PublicFile); err != nil {
			return fmt.Errorf("publishing Go vendor file %#q:\n%w", name, err)
		}
	}

	return p.recordGoVendorArchive(outputDir, artifact, plan.AllowReplacement)
}

func (p *sourcePreparerImpl) checkGoVendorSourceCollision(outputDir string, names ...string) error {
	for _, name := range names {
		exists, err := fileutils.Exists(p.fs, filepath.Join(outputDir, name))
		if err != nil {
			return fmt.Errorf("checking generated Go source path:\n%w", err)
		}

		if exists {
			return fmt.Errorf("generated Go vendor file %#q conflicts with an existing source", name)
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
		if line.Entry != nil && slices.Contains(names, line.Entry.Filename) {
			return fmt.Errorf("generated Go vendor file %#q conflicts with an upstream 'sources' entry", line.Entry.Filename)
		}
	}

	return nil
}

func (p *sourcePreparerImpl) recordGoVendorArchive(outputDir string, artifact govendor.Artifact, replace bool) error {
	path := filepath.Join(outputDir, fedorasource.SourcesFileName)

	content, err := p.readSourcesFileIfExists(path)
	if err != nil {
		return err
	}

	content, err = upsertGoVendorEntry(content, artifact, replace)
	if err != nil {
		return err
	}

	if err := fileutils.WriteFile(p.fs, path, []byte(content), fileperms.PublicFile); err != nil {
		return fmt.Errorf("recording Go vendor digest:\n%w", err)
	}

	slog.Info("Recorded generated Go vendor archive", "filename", artifact.Name,
		"hashType", fileutils.HashTypeSHA512, "hash", artifact.Digest)

	ignorePath := filepath.Join(outputDir, ".gitignore")

	ignore, err := p.readSourcesFileIfExists(ignorePath)
	if err != nil {
		return err
	}

	for _, line := range strings.Split(ignore, "\n") {
		if strings.TrimPrefix(line, "/") == artifact.Name {
			return nil
		}
	}

	if err := fileutils.WriteFile(p.fs, ignorePath,
		[]byte(strings.TrimRight(ignore, "\n")+"\n/"+artifact.Name+"\n"), fileperms.PublicFile); err != nil {
		return fmt.Errorf("ignoring generated Go archive in git:\n%w", err)
	}

	return nil
}

func upsertGoVendorEntry(content string, artifact govendor.Artifact, replace bool) (string, error) {
	lines, err := fedorasource.ReadSourcesFile(strings.TrimRight(content, "\n"))
	if err != nil {
		return "", fmt.Errorf("parsing Go vendor source manifest:\n%w", err)
	}

	entry := fedorasource.FormatSourcesEntry(artifact.Name, fileutils.HashTypeSHA512, artifact.Digest)
	raw := make([]string, 0, len(lines)+1)
	found := false

	for _, line := range lines {
		value := line.Raw
		if line.Entry != nil && line.Entry.Filename == artifact.Name {
			if !replace {
				return "", fmt.Errorf("go vendor source %#q already exists", artifact.Name)
			}

			value, found = entry, true
		}

		raw = append(raw, value)
	}

	if !found {
		raw = append(raw, entry)
	}

	return strings.Join(raw, "\n") + "\n", nil
}
