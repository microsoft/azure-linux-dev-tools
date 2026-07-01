// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/archive"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// customGenScriptDir is the path inside the mock chroot where the generation script
// is bind-mounted (read-only).
const customGenScriptDir = "/azldev-gen/script"

// customGenOutputDir is the path inside the mock chroot where the generation script
// must write its output.
const customGenOutputDir = "/azldev-gen/output"

// customFileSourceProvider implements [FileSourceProvider] for source files with
// [projectconfig.OriginTypeCustom]. It executes a user-supplied script inside a
// fresh mock chroot and packages the output directory as a deterministic archive.
type customFileSourceProvider struct {
	fs     opctx.FS
	runner *mock.Runner
}

var _ FileSourceProvider = (*customFileSourceProvider)(nil)

// GetFile implements [FileSourceProvider]. It returns [ErrNotFound] for any file
// reference whose origin type is not [projectconfig.OriginTypeCustom], allowing
// the source manager to fall through to lookaside and other configured origins.
func (p *customFileSourceProvider) GetFile(
	ctx context.Context,
	component components.Component,
	ref projectconfig.SourceFileReference,
	destDirPath string,
) error {
	if ref.Origin.Type != projectconfig.OriginTypeCustom {
		return ErrNotFound
	}

	destPath := filepath.Join(destDirPath, ref.Filename)

	return generateCustomSourceFile(ctx, p.fs, p.runner, component, &ref, destPath)
}

// generateCustomSourceFile runs a single [projectconfig.SourceFileReference] through a
// fresh mock chroot and places the resulting deterministic archive at destPath.
//
// The runner must be configured with the distro's mock config path. A clone of the runner
// is created for each call so bind mounts and package installs do not bleed across invocations.
func generateCustomSourceFile(
	ctx context.Context,
	fs opctx.FS,
	baseRunner *mock.Runner,
	component components.Component,
	ref *projectconfig.SourceFileReference,
	destPath string,
) error {
	slog.Info("Generating custom source file",
		"filename", ref.Filename,
		"script", ref.Script,
		"component", component.GetName())

	specDir, err := resolveComponentSpecDir(component)
	if err != nil {
		return fmt.Errorf("failed to resolve spec directory for component %#q:\n%w",
			component.GetName(), err)
	}

	// Verify the generation script is present before spinning up a mock chroot.
	scriptHostPath := filepath.Join(specDir, ref.Script)

	if _, statErr := fs.Stat(scriptHostPath); statErr != nil {
		return fmt.Errorf("generation script %#q not found at %#q:\n%w",
			ref.Script, scriptHostPath, statErr)
	}

	scriptTmpDir, genOutputTmpDir, cleanup, err := prepareStagingDirs(fs, scriptHostPath, ref.Script)
	if err != nil {
		return err
	}

	defer cleanup()

	// Clone the base runner so bind mounts added here don't persist to other calls.
	// Network access is always enabled — custom source scripts commonly need to
	// download upstream tarballs or toolchain artifacts.
	runner := baseRunner.Clone()
	runner.EnableNetwork()
	runner.AddBindMount(scriptTmpDir, customGenScriptDir)
	runner.AddBindMount(genOutputTmpDir, customGenOutputDir)

	if err := execScriptInChroot(ctx, runner, ref); err != nil {
		return err
	}

	// Package the output directory as a deterministic archive whose format is
	// inferred from the filename extension (e.g., .tar.gz, .tar.xz).
	comp, compErr := archive.DetectCompression(ref.Filename)
	if compErr != nil {
		return fmt.Errorf("cannot determine archive format for custom source %#q:\n%w",
			ref.Filename, compErr)
	}

	// Ensure the destination directory exists. FetchFiles runs before FetchComponent,
	// so the output directory may not have been created yet when this is called.
	if mkdirErr := fileutils.MkdirAll(fs, filepath.Dir(destPath)); mkdirErr != nil {
		return fmt.Errorf("failed to create destination directory for %#q:\n%w",
			ref.Filename, mkdirErr)
	}

	if archiveErr := archive.CreateDeterministicArchive(destPath, genOutputTmpDir, comp); archiveErr != nil {
		return fmt.Errorf("failed to create archive %#q:\n%w", ref.Filename, archiveErr)
	}

	slog.Info("Custom source file generated successfully",
		"filename", ref.Filename,
		"path", destPath)

	return nil
}

// resolveComponentSpecDir returns the directory on the host filesystem that contains the
// component's spec file and its sidecar files (patches, generation scripts, etc.).
//
// For local components the spec path is explicit; its parent directory is returned directly.
// For upstream components the directory is inferred from the config file that defines the
// component, because the script is a project-local file rather than something fetched from
// the upstream dist-git.
func resolveComponentSpecDir(component components.Component) (string, error) {
	config := component.GetConfig()

	// Local components: the spec path is an absolute path on disk.
	if config.Spec.SourceType == projectconfig.SpecSourceTypeLocal && config.Spec.Path != "" {
		return filepath.Dir(config.Spec.Path), nil
	}

	// Upstream components: derive from the config file that defines the component.
	if config.SourceConfigFile != nil {
		return config.SourceConfigFile.Dir(), nil
	}

	return "", fmt.Errorf(
		"cannot determine spec directory for component %#q: "+
			"neither a local spec path nor a config file directory is available",
		config.Name)
}

// prepareStagingDirs creates two temporary host directories:
//   - a read-only script directory containing a copy of the generation script
//   - a read-write output directory to be bind-mounted inside the chroot
//
// The returned cleanup function removes both directories; callers must defer it.
func prepareStagingDirs(
	fs opctx.FS,
	scriptHostPath string,
	scriptName string,
) (scriptDir string, outputDir string, cleanup func(), err error) {
	scriptDir, err = fileutils.MkdirTempInTempDir(fs, "azldev-script-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create temporary script directory:\n%w", err)
	}

	outputDir, err = fileutils.MkdirTempInTempDir(fs, "azldev-output-*")
	if err != nil {
		_ = fs.RemoveAll(scriptDir)

		return "", "", nil, fmt.Errorf("failed to create temporary output directory:\n%w", err)
	}

	cleanup = func() {
		_ = fs.RemoveAll(scriptDir)
		_ = fs.RemoveAll(outputDir)
	}

	// Copy the script into the staging directory so we bind-mount only that file,
	// without leaking other sidecar files from the spec directory.
	scriptData, readErr := fileutils.ReadFile(fs, scriptHostPath)
	if readErr != nil {
		cleanup()

		return "", "", nil, fmt.Errorf("failed to read generation script %#q:\n%w",
			scriptName, readErr)
	}

	hostScriptCopy := filepath.Join(scriptDir, scriptName)

	if writeErr := fileutils.WriteFile(fs, hostScriptCopy, scriptData, fileperms.PublicExecutable); writeErr != nil {
		cleanup()

		return "", "", nil, fmt.Errorf("failed to stage generation script to %#q:\n%w",
			hostScriptCopy, writeErr)
	}

	return scriptDir, outputDir, cleanup, nil
}

// execScriptInChroot initialises a fresh mock root, optionally installs extra packages,
// runs the generation script, and scrubs the root on return.
func execScriptInChroot(
	ctx context.Context,
	runner *mock.Runner,
	ref *projectconfig.SourceFileReference,
) error {
	if initErr := runner.InitRoot(ctx); initErr != nil {
		return fmt.Errorf("failed to initialize mock root for generating %#q:\n%w",
			ref.Filename, initErr)
	}

	defer func() {
		// Best-effort cleanup. Log on failure rather than overwriting the primary error.
		if scrubErr := runner.ScrubRoot(ctx); scrubErr != nil {
			slog.Warn("Failed to scrub mock root after custom source generation",
				"filename", ref.Filename,
				"error", scrubErr)
		}
	}()

	if len(ref.MockPackages) > 0 {
		if installErr := runner.InstallPackages(ctx, ref.MockPackages); installErr != nil {
			return fmt.Errorf("failed to install mock packages for generating %#q:\n%w",
				ref.Filename, installErr)
		}
	}

	scriptChrootPath := filepath.Join(customGenScriptDir, ref.Script)

	cmd, cmdErr := runner.CmdInChroot(ctx, []string{scriptChrootPath}, false /* interactive */, true /* pipeOutput */)
	if cmdErr != nil {
		return fmt.Errorf("failed to create chroot command for generating %#q:\n%w",
			ref.Filename, cmdErr)
	}

	if runErr := cmd.Run(ctx); runErr != nil {
		return fmt.Errorf("generation script %#q failed for source %#q:\n%w",
			ref.Script, ref.Filename, runErr)
	}

	return nil
}
