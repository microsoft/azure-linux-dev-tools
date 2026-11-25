// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/govendor"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// WithGoVendorRunner supplies the distro's isolated archive-generation environment.
func WithGoVendorRunner(runner *mock.Runner) PreparerOption {
	return func(p *sourcePreparerImpl) { p.goVendorRunner = runner }
}

// WithRenderManifest renders declared vendor pins without downloading sources or generating payloads.
func WithRenderManifest() PreparerOption {
	return func(p *sourcePreparerImpl) { p.skipLookaside, p.renderManifest = true, true }
}

func (p *sourcePreparerImpl) needsVendorInputs(config *projectconfig.ComponentConfig) bool {
	return !p.renderManifest && config.HasGoVendorSources()
}

type vendorComponent struct {
	components.Component
	config projectconfig.ComponentConfig
}

func (c vendorComponent) GetConfig() *projectconfig.ComponentConfig { return &c.config }

func vendorFingerprintConfig(
	effective, declared *projectconfig.ComponentConfig,
) *projectconfig.ComponentConfig {
	if !declared.HasGoVendorSources() {
		return effective
	}

	result := *effective
	result.SourceFiles = slices.Clone(effective.SourceFiles)
	// Generated hashes are outputs, not declared build inputs.
	for index, ref := range declared.SourceFiles {
		if ref.Origin.Type == projectconfig.OriginTypeGoVendor && index < len(result.SourceFiles) {
			result.SourceFiles[index] = ref
		}
	}

	return &result
}

func (p *sourcePreparerImpl) prepareVendorSources(
	ctx context.Context, component components.Component, directory string,
) (components.Component, error) {
	config := *component.GetConfig()
	if !config.HasGoVendorSources() {
		return component, nil
	}

	config.SourceFiles = slices.Clone(config.SourceFiles)
	if p.dryRunnable.DryRun() {
		config.SourceFiles = slices.DeleteFunc(config.SourceFiles, func(ref projectconfig.SourceFileReference) bool {
			return ref.Origin.Type == projectconfig.OriginTypeGoVendor
		})

		return vendorComponent{Component: component, config: config}, nil
	}

	if p.goVendorRunner == nil {
		return nil, errors.New("'go-vendor' sources require a project distro with 'mock-config'")
	}

	for index, ref := range config.SourceFiles {
		if ref.Origin.Type != projectconfig.OriginTypeGoVendor {
			continue
		}

		resolved, err := p.generateVendorSource(ctx, component, directory, ref)
		if err != nil {
			return nil, err
		}

		config.SourceFiles[index] = resolved
	}

	return vendorComponent{Component: component, config: config}, nil
}

func (p *sourcePreparerImpl) generateVendorSource(
	ctx context.Context, component components.Component, directory string, ref projectconfig.SourceFileReference,
) (resolved projectconfig.SourceFileReference, err error) {
	specPath, err := p.resolveSpecPath(component, directory)
	if err != nil {
		return ref, err
	}

	data, err := fileutils.ReadFile(p.fs, specPath)
	if err != nil {
		return ref, fmt.Errorf("reading vendor prep spec:\n%w", err)
	}

	plan, err := spec.PrepareVendorSource(data, ref.Filename)
	if err != nil {
		return ref, fmt.Errorf("planning private vendor prep:\n%w", err)
	}

	settings, err := p.vendorSettings(directory, plan.ConfigName, ref.Origin)
	if err != nil {
		return ref, err
	}

	scratch, err := p.vendorScratchDir()
	if err != nil {
		return ref, fmt.Errorf("creating vendor scratch:\n%w", err)
	}
	defer fileutils.RemoveAllAndUpdateErrorIfNil(p.fs, scratch, &err)

	artifact, err := govendor.Generate(ctx, p.fs, p.goVendorRunner, govendor.Request{
		SourceDir: directory, WorkDir: scratch, Filename: ref.Filename, SpecName: filepath.Base(specPath),
		PrepSpec: plan.Spec, Config: settings, ModuleDir: ref.Origin.ModuleDir, Packages: ref.Origin.PrepPackages,
	})
	if err != nil {
		return ref, fmt.Errorf("running vendor generation:\n%w", err)
	}

	return p.publishVendorSource(component, directory, ref, artifact)
}

func (p *sourcePreparerImpl) publishVendorSource(
	component components.Component, directory string, ref projectconfig.SourceFileReference,
	artifact govendor.Artifact,
) (projectconfig.SourceFileReference, error) {
	if ref.HashType == "" {
		ref.HashType = fileutils.HashTypeSHA512
	}

	digest, err := fileutils.ComputeFileHash(p.fs, ref.HashType, artifact.Path)
	if err != nil {
		return ref, fmt.Errorf("hashing generated vendor archive:\n%w", err)
	}

	if ref.Hash != "" && !strings.EqualFold(ref.Hash, digest) {
		return ref, fmt.Errorf("generated Go vendor hash mismatch for %#q: expected %s, got %s",
			ref.Filename, ref.Hash, digest)
	}

	ref.Hash = digest
	if err := p.resolveVendorTarget(component, directory, ref); err != nil {
		return ref, err
	}

	return ref, p.copyVendorArchive(directory, ref.Filename, artifact.Path)
}

func (p *sourcePreparerImpl) copyVendorArchive(directory, name, source string) error {
	destination := filepath.Join(directory, name)
	// Remove the old leaf first so copying cannot follow an upstream symlink.
	if err := p.fs.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing previous vendor output:\n%w", err)
	}

	if err := fileutils.CopyFile(p.dryRunnable, p.fs, source, destination, fileutils.CopyFileOptions{}); err != nil {
		return fmt.Errorf("publishing vendor archive:\n%w", err)
	}

	ignorePath := filepath.Join(directory, ".gitignore")

	ignore, err := p.readSourcesFileIfExists(ignorePath)
	if err != nil {
		return err
	}

	if !slices.Contains(strings.Split(ignore, "\n"), "/"+name) {
		if err := fileutils.WriteFile(p.fs, ignorePath,
			[]byte(strings.TrimRight(ignore, "\n")+"\n/"+name+"\n"), fileperms.PublicFile); err != nil {
			return fmt.Errorf("ignoring generated vendor archive:\n%w", err)
		}
	}

	return nil
}

func (p *sourcePreparerImpl) vendorScratchDir() (string, error) {
	base := p.goVendorRunner.BaseDir()
	if base == "" {
		path, err := fileutils.MkdirTempInTempDir(p.fs, "azldev-vendor-")
		if err != nil {
			return "", fmt.Errorf("creating temporary vendor directory:\n%w", err)
		}

		return path, nil
	}

	if err := fileutils.MkdirAll(p.fs, base); err != nil {
		return "", fmt.Errorf("creating vendor work directory:\n%w", err)
	}

	path, err := fileutils.MkdirTemp(p.fs, base, "azldev-vendor-")
	if err != nil {
		return "", fmt.Errorf("creating private vendor directory:\n%w", err)
	}

	return path, nil
}

func (p *sourcePreparerImpl) vendorSettings(
	directory, upstreamName string, origin projectconfig.Origin,
) (string, error) {
	if origin.ConfigFile != "" {
		data, err := origin.VendorConfig(p.fs)
		if err != nil {
			return "", fmt.Errorf("loading vendor settings:\n%w", err)
		}

		return data, nil
	}

	if upstreamName == "" {
		upstreamName = "go-vendor-tools.toml"

		exists, err := fileutils.Exists(p.fs, filepath.Join(directory, upstreamName))
		if err != nil {
			return "", fmt.Errorf("checking native vendor settings:\n%w", err)
		}

		if !exists {
			return "", nil
		}
	}

	data, err := fileutils.ReadFile(p.fs, filepath.Join(directory, upstreamName))
	if err != nil {
		return "", fmt.Errorf("reading upstream vendor settings:\n%w", err)
	}

	return string(data), nil
}

func (p *sourcePreparerImpl) resolveVendorTarget(
	component components.Component, directory string, ref projectconfig.SourceFileReference,
) error {
	for _, other := range component.GetConfig().SourceFiles {
		if other.Origin.Type != projectconfig.OriginTypeGoVendor && other.Filename == ref.Filename {
			return fmt.Errorf("go vendor output %#q conflicts with another 'source-files' entry", ref.Filename)
		}
	}

	manifest, err := p.readSourcesFileIfExists(filepath.Join(directory, fedorasource.SourcesFileName))
	if err != nil {
		return err
	}

	lines, err := fedorasource.ReadSourcesFile(manifest)
	if err != nil {
		return fmt.Errorf("parsing vendor target manifest:\n%w", err)
	}

	for _, overlay := range component.GetConfig().Overlays {
		if overlay.Archive == ref.Filename {
			return errors.New("archive overlays cannot target regenerated vendor output; use native generation hooks")
		}
	}

	if !ref.ReplaceUpstream {
		exists, err := fileutils.Exists(p.fs, filepath.Join(directory, ref.Filename))
		if err != nil {
			return fmt.Errorf("checking vendor destination:\n%w", err)
		}

		if exists {
			return fmt.Errorf("vendor output %#q conflicts with an existing source", ref.Filename)
		}
	}

	_, err = p.buildSourceEntries([]projectconfig.SourceFileReference{ref}, lines, component.GetName(), directory)

	return err
}
