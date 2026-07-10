// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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

// maxCustomScriptOutputBytes limits the retained stdout and stderr from a custom
// generation script. The tail is retained because failures are typically reported
// near the end of command output.
const maxCustomScriptOutputBytes = 64 * 1024

// customFileSourceProvider implements [FileSourceProvider] for source files with
// [projectconfig.OriginTypeCustom]. It executes a user-supplied script inside a
// fresh mock chroot and packages the output directory as a deterministic archive.
type customFileSourceProvider struct {
	dryRunnable opctx.DryRunnable
	fs          opctx.FS
	runner      *mock.Runner
	verbose     bool
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

	return generateCustomSourceFile(ctx, p.dryRunnable, p.fs, p.runner, p.verbose, component, &ref, destPath)
}

// generateCustomSourceFile runs a single [projectconfig.SourceFileReference] through a
// fresh mock chroot and places the resulting deterministic archive at destPath.
//
// The runner must be configured with the distro's mock config path. A clone of the runner
// is created for each call so bind mounts and package installs do not bleed across invocations.
func generateCustomSourceFile(
	ctx context.Context,
	dryRunnable opctx.DryRunnable,
	fs opctx.FS,
	baseRunner *mock.Runner,
	verbose bool,
	component components.Component,
	ref *projectconfig.SourceFileReference,
	destPath string,
) error {
	slog.Info("Generating custom source file",
		"filename", ref.Filename,
		"script", ref.Origin.Script,
		"component", component.GetName())

	if dryRunnable.DryRun() {
		slog.Info("Dry run; skipping custom source file generation", "filename", ref.Filename)

		return nil
	}

	specDir, err := resolveComponentSpecDir(component)
	if err != nil {
		return fmt.Errorf("failed to resolve spec directory for component %#q:\n%w",
			component.GetName(), err)
	}

	// Verify the generation script is present before spinning up a mock chroot.
	scriptHostPath := filepath.Join(specDir, ref.Origin.Script)

	if _, statErr := fs.Stat(scriptHostPath); statErr != nil {
		return fmt.Errorf("generation script %#q not found at %#q:\n%w",
			ref.Origin.Script, scriptHostPath, statErr)
	}

	scriptTmpDir, genOutputTmpDir, cleanup, err := prepareStagingDirs(fs, scriptHostPath, ref.Origin.Script)
	if err != nil {
		return err
	}

	defer cleanup()

	// Stage declared input files next to the script so script authors can refer to
	// them by filename without knowing azldev's internal mount paths.
	destDirPath := filepath.Dir(destPath)

	if len(ref.Origin.Inputs) > 0 {
		if inputsErr := stageInputFiles(
			dryRunnable, fs, ref.Origin.Inputs, destDirPath, scriptTmpDir, ref.Origin.Script,
		); inputsErr != nil {
			return fmt.Errorf("failed to resolve inputs for custom source %#q:\n%w",
				ref.Filename, inputsErr)
		}
	}

	// Package the output directory as a deterministic archive whose format is
	// inferred from the filename extension (e.g., .tar.gz, .tar.xz).
	comp, compErr := archive.DetectCompression(ref.Filename)
	if compErr != nil {
		return fmt.Errorf("cannot determine archive format for custom source %#q:\n%w",
			ref.Filename, compErr)
	}

	// Clone the base runner so bind mounts added here don't persist to other calls.
	// Network access is always enabled — custom source scripts commonly need to
	// download upstream tarballs or toolchain artifacts.
	runner := buildCustomRunner(baseRunner, scriptTmpDir, genOutputTmpDir)

	if err := execScriptInChroot(ctx, runner, verbose, ref); err != nil {
		return err
	}

	// Ensure the destination directory exists before writing the archive.
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

// buildCustomRunner returns a clone of baseRunner configured with the standard bind mounts
// for custom source generation. Network access is always enabled; the unprivileged flag is
// set so the script runs as the 'mockbuild' user rather than root.
func buildCustomRunner(baseRunner *mock.Runner, scriptTmpDir, genOutputTmpDir string) *mock.Runner {
	runner := baseRunner.Clone()
	runner.EnableNetwork()
	runner.WithUnprivileged()
	runner.AddBindMount(scriptTmpDir, customGenScriptDir)
	runner.AddBindMount(genOutputTmpDir, customGenOutputDir)

	return runner
}

// execScriptInChroot initialises a fresh mock root, optionally installs extra packages,
// runs the generation script, and scrubs the root on return.
func execScriptInChroot(
	ctx context.Context,
	runner *mock.Runner,
	verbose bool,
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

	if len(ref.Origin.MockPackages) > 0 {
		if installErr := runner.InstallPackages(ctx, ref.Origin.MockPackages); installErr != nil {
			return fmt.Errorf("failed to install mock packages for generating %#q:\n%w",
				ref.Filename, installErr)
		}
	}

	// Use positional parameters so the script name is never re-parsed as shell code
	// ($1=scriptDir, $2=scriptName; '--' sets $0 and keeps bash from consuming them).
	cmd, cmdErr := runner.CmdInChroot(ctx, []string{
		"sh", "-c", `cd "$1" && ./"$2"`, "--", customGenScriptDir, ref.Origin.Script,
	}, false /* interactive */)
	if cmdErr != nil {
		return fmt.Errorf("failed to create chroot command for generating %#q:\n%w",
			ref.Filename, cmdErr)
	}

	stdout := newOutputTail(maxCustomScriptOutputBytes)
	stderr := newOutputTail(maxCustomScriptOutputBytes)

	if verbose {
		cmd.SetStdout(io.MultiWriter(os.Stdout, stdout))
		cmd.SetStderr(io.MultiWriter(os.Stderr, stderr))
	} else {
		cmd.SetStdout(stdout)
		cmd.SetStderr(stderr)
	}

	if runErr := cmd.Run(ctx); runErr != nil {
		scriptOutput := formatCustomScriptOutput(stdout.String(), stderr.String())

		return fmt.Errorf("generation script %#q failed for source %#q%s\n%w",
			ref.Origin.Script, ref.Filename, scriptOutput, runErr)
	}

	return nil
}

// outputTail retains the last limit bytes written to it. It implements [io.Writer]
// so command output can be captured without unbounded memory growth.
type outputTail struct {
	buf       []byte
	limit     int
	truncated bool
}

func newOutputTail(limit int) *outputTail {
	return &outputTail{buf: make([]byte, 0, limit), limit: limit}
}

func (b *outputTail) Write(data []byte) (n int, err error) {
	n = len(data)
	if n >= b.limit {
		b.truncated = b.truncated || n > b.limit || len(b.buf) > 0
		b.buf = append(b.buf[:0], data[n-b.limit:]...)

		return n, nil
	}

	if overflow := len(b.buf) + n - b.limit; overflow > 0 {
		b.buf = append(b.buf[:0], b.buf[overflow:]...)
		b.truncated = true
	}

	b.buf = append(b.buf, data...)

	return n, nil
}

func (b *outputTail) String() string {
	if b.truncated {
		return fmt.Sprintf("[output truncated; showing last %d bytes]\n%s", b.limit, b.buf)
	}

	return string(b.buf)
}

func formatCustomScriptOutput(stdout, stderr string) string {
	var output strings.Builder

	if stdout != "" {
		fmt.Fprintf(&output, "\nstdout:\n%s", stdout)
	}

	if stderr != "" {
		fmt.Fprintf(&output, "\nstderr:\n%s", stderr)
	}

	return output.String()
}

// stageInputFiles copies the files listed in inputs next to the generation script.
//
// All inputs must already be present in destDirPath, which contains the full set of upstream
// source tarballs, sidecar files, and any earlier 'source-files' entries fetched by
// [FetchComponent] and prior [FetchFiles] calls. If a filename is absent, an error is returned.
func stageInputFiles(
	dryRunnable opctx.DryRunnable,
	fs opctx.FS,
	inputs []string,
	destDirPath, scriptTmpDir, scriptName string,
) error {
	for _, filename := range inputs {
		if err := fileutils.ValidateFilename(filename); err != nil {
			return fmt.Errorf("invalid input filename %#q:\n%w", filename, err)
		}

		if filename == scriptName {
			return fmt.Errorf("input file %#q conflicts with generation script filename", filename)
		}

		sourcePath := filepath.Join(destDirPath, filename)

		exists, statErr := fileutils.Exists(fs, sourcePath)
		if statErr != nil {
			return fmt.Errorf("failed to stat input file %#q:\n%w", filename, statErr)
		}

		if !exists {
			return fmt.Errorf(
				"input file %#q not found in output directory %#q",
				filename, destDirPath)
		}

		stagedPath := filepath.Join(scriptTmpDir, filename)

		if writeErr := fileutils.CopyFile(
			dryRunnable, fs, sourcePath, stagedPath,
			fileutils.CopyFileOptions{PreserveFileMode: true},
		); writeErr != nil {
			return fmt.Errorf("failed to stage input file %#q:\n%w", filename, writeErr)
		}
	}

	return nil
}
