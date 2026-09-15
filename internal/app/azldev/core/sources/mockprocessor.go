// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/kballard/go-shellquote"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// chrootProvenanceDir is the in-chroot mount point for the single component's
// source directory when resolving %autorelease.
const chrootProvenanceDir = "/tmp/provenance"

// MockProcessor provides a shared mock chroot for source processing operations
// that require RPM tooling. The chroot is lazily initialized on first use.
type MockProcessor struct {
	mu               sync.Mutex
	runner           *mock.Runner
	requiredPackages []string
	initialized      bool
	initErr          error
}

// MockProcessorOption configures a [MockProcessor] at construction time.
type MockProcessorOption func(*MockProcessor)

// WithIsolatedMockBaseDir returns a [MockProcessorOption] that places the
// processor's mock root under baseDir instead of mock's shared default
// (/var/lib/mock). This gives the processor a self-contained root tree so a
// concurrently used build chroot (which shares the same mock config) cannot
// scrub it, and so destroying the processor scrubs only its own tree. A blank
// baseDir is ignored, preserving the default location.
func WithIsolatedMockBaseDir(baseDir string) MockProcessorOption {
	return func(processor *MockProcessor) {
		if baseDir != "" {
			processor.runner.WithBaseDir(baseDir)
		}
	}
}

// WithRequiredPackages returns a [MockProcessorOption] that installs packages
// in the mock chroot on first use.
func WithRequiredPackages(requiredPackages ...string) MockProcessorOption {
	return func(processor *MockProcessor) {
		processor.requiredPackages = append([]string(nil), requiredPackages...)
	}
}

// NewMockProcessor creates a new processor that will lazily initialize
// a mock chroot using the given config path. The runner is created eagerly
// but the chroot is only initialized on first use.
func NewMockProcessor(ctx opctx.Ctx, mockConfigPath string, opts ...MockProcessorOption) *MockProcessor {
	processor := &MockProcessor{
		runner: mock.NewRunner(ctx, mockConfigPath),
	}
	for _, opt := range opts {
		opt(processor)
	}

	return processor
}

// initOnce lazily initializes the mock chroot. Caller must hold p.mu.
func (p *MockProcessor) initOnce(ctx context.Context) error {
	if p.initialized {
		return p.initErr
	}

	slog.Info("Initializing mock chroot for source processing")

	p.runner.EnableNetwork()

	if err := p.runner.InitRoot(ctx); err != nil {
		p.initErr = fmt.Errorf("failed to initialize mock chroot:\n%w", err)
		p.initialized = true

		return p.initErr
	}

	if len(p.requiredPackages) > 0 {
		if err := p.runner.InstallPackages(ctx, p.requiredPackages); err != nil {
			p.initErr = fmt.Errorf("failed to install packages in mock chroot:\n%w", err)
			p.initialized = true

			return p.initErr
		}
	}

	p.initialized = true

	slog.Info("Mock chroot ready")

	return nil
}

// batchBindMount describes one host-to-chroot bind mount used by runBatchScript.
type batchBindMount struct {
	Host     string
	InChroot string
}

// runBatchScriptOptions parameterizes a single batch-script invocation.
type runBatchScriptOptions struct {
	// Mounts is the full set of host-to-chroot bind mounts to add to the runner.
	// The scratch dir must be reachable via one of these mounts (typically the
	// first entry), so the script can locate its inputs and write results.
	Mounts []batchBindMount
	// ScratchHost is the host-side directory where the script, inputs manifest,
	// and results file are read and written.
	ScratchHost string
	// ScratchInChroot is the in-chroot path that maps to ScratchHost.
	ScratchInChroot string
	// ScriptName is the basename used when writing the embedded Python script
	// into ScratchHost.
	ScriptName  string
	ScriptBytes []byte
	// InputsJSON is the JSON-encoded inputs manifest, written as
	// <ScratchHost>/inputs.json.
	InputsJSON []byte
	// ResultsName is the basename of the results file the script is expected
	// to write into ScratchHost (e.g. "results.json").
	ResultsName string
	// ScriptArgs is appended to the python3 invocation after the script path.
	ScriptArgs []string
	// ProgressLabel labels the progress event surfaced to the user.
	ProgressLabel string
	// ProgressTotal is the total used for progress reporting from PROGRESS lines.
	ProgressTotal int64
	FS            opctx.FS
}

// runBatchScript executes a batched, parallelizable Python helper inside the
// shared mock chroot. It owns the lock + lazy init, writes the script and
// inputs into the host-side scratch dir, runs the script (which is expected to
// emit "PROGRESS <i>/<total> <name>" lines and write a results file), and
// returns the raw results bytes.
//
// This is shared scaffolding for batched mock operations such as querying
// specs. Per-operation concerns live in the callers.
//

func (p *MockProcessor) runBatchScript(
	ctx context.Context, events opctx.EventListener, opts runBatchScriptOptions,
) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.initOnce(ctx); err != nil {
		return nil, err
	}

	// Write the Python script and inputs manifest to the scratch directory.
	scriptHostPath := filepath.Join(opts.ScratchHost, opts.ScriptName)
	if err := fileutils.WriteFile(opts.FS, scriptHostPath, opts.ScriptBytes, fileperms.PublicExecutable); err != nil {
		return nil, fmt.Errorf("writing script %#q:\n%w", opts.ScriptName, err)
	}

	inputsHostPath := filepath.Join(opts.ScratchHost, "inputs.json")
	if err := fileutils.WriteFile(opts.FS, inputsHostPath, opts.InputsJSON, fileperms.PublicFile); err != nil {
		return nil, fmt.Errorf("writing inputs manifest:\n%w", err)
	}

	// Clone the runner and add the requested bind mounts.
	// WithUnprivileged drops to the mockbuild user for chroot commands,
	// matching how mock builds run and avoiding root-owned files in the
	// bind-mounted scratch directory. This is safe because mock defaults
	// chrootuid to os.getuid() — the mockbuild user inside the chroot has
	// the same UID as the host user, so bind-mounted files remain writable.
	runner := p.runner.Clone()
	runner.WithUnprivileged()

	for _, mount := range opts.Mounts {
		runner.AddBindMount(mount.Host, mount.InChroot)
	}

	scriptInChroot := path.Join(opts.ScratchInChroot, opts.ScriptName)
	args := append([]string{"python3", scriptInChroot}, opts.ScriptArgs...)

	cmd, err := runner.CmdInChroot(ctx, args, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create batch command in mock:\n%w", err)
	}

	// Set up progress reporting from the Python script's output.
	// The script prints "PROGRESS <completed>/<total> <name>" to stderr, but
	// mock --chroot merges the inner command's stderr into stdout, so we
	// listen on stdout.
	progress := events.StartEvent(opts.ProgressLabel, "count", opts.ProgressTotal)
	progress.SetLongRunning(opts.ProgressLabel)

	defer progress.End()

	if listenerErr := cmd.SetRealTimeStdoutListener(func(_ context.Context, line string) {
		// Parse "PROGRESS <i>/<total> <name>" lines.
		if after, found := strings.CutPrefix(line, "PROGRESS "); found {
			if slashIdx := strings.Index(after, "/"); slashIdx > 0 {
				if completed, parseErr := strconv.ParseInt(after[:slashIdx], 10, 64); parseErr == nil {
					progress.SetProgress(completed, opts.ProgressTotal)
				}
			}
		}
	}); listenerErr != nil {
		slog.Warn("Failed to set stdout listener for progress", "error", listenerErr)
	}

	if runErr := cmd.Run(ctx); runErr != nil {
		slog.Warn("Batch mock script exited with error", "error", runErr)

		return nil, fmt.Errorf("batch mock processing failed:\n%w", runErr)
	}

	// Read results from the file written by the Python script.
	// Using a file avoids bufio.Scanner token size limits that would truncate
	// large JSON payloads when capturing stdout (e.g., 7k components ≈ 560KB).
	resultsHostPath := filepath.Join(opts.ScratchHost, opts.ResultsName)

	resultsData, readErr := fileutils.ReadFile(opts.FS, resultsHostPath)
	if readErr != nil {
		return nil, fmt.Errorf("reading batch results from %#q:\n%w", resultsHostPath, readErr)
	}

	return resultsData, nil
}

// CalculateRelease resolves an rpmautospec %autorelease to a concrete Fedora
// release number (without the dist tag) by running `rpmautospec
// calculate-release --complete-release` inside the shared mock chroot. This
// keeps rpmautospec out of azldev's host dependencies — the chroot already has
// it installed by initOnce.
//
// specHostDir is the host directory holding the pristine spec and its .git
// history; it is bind-mounted read/write into the chroot. specFilename is the
// spec's basename within that directory. The raw command output is returned;
// the caller parses and validates it. The chroot is lazily initialized on first
// use.
func (p *MockProcessor) CalculateRelease(ctx context.Context, specHostDir, specFilename string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.initOnce(ctx); err != nil {
		return "", err
	}

	// Clone so this invocation's bind mount and privilege drop don't mutate the
	// shared template. WithUnprivileged runs as the mockbuild user, whose UID
	// matches the host user (mock chrootuid defaults to os.getuid()), so the
	// bind-mounted .git is owned consistently.
	runner := p.runner.Clone()
	runner.WithUnprivileged()
	runner.AddBindMount(specHostDir, chrootProvenanceDir)

	specInChroot := filepath.Join(chrootProvenanceDir, specFilename)

	// safe.directory guards against any residual host/chroot ownership mismatch
	// on the bind-mounted .git, mirroring the render batch path. Wrapped in
	// `sh -c` so the shell operators survive shellquote joining in CmdInChroot.
	shellCmd := "git config --global --add safe.directory '*' && " +
		"rpmautospec calculate-release --complete-release " + shellquote.Join(specInChroot)

	cmd, err := runner.CmdInChroot(ctx, []string{"sh", "-c", shellCmd}, false)
	if err != nil {
		return "", fmt.Errorf("creating rpmautospec command in mock chroot:\n%w", err)
	}

	out, err := cmd.RunAndGetOutput(ctx)
	if err != nil {
		return "", fmt.Errorf("rpmautospec calculate-release failed in mock chroot:\n%w", err)
	}

	return out, nil
}

// Destroy cleans up the mock chroot. It should be called when source processing is complete.
// The processor must not be reused after Destroy — create a new MockProcessor if needed.
// Attempts cleanup even if initialization partially failed (e.g., InitRoot succeeded
// but InstallPackages failed), since a partially initialized chroot still needs scrubbing.
func (p *MockProcessor) Destroy(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.runner != nil && p.initialized {
		slog.Debug("Destroying mock chroot")

		if err := p.runner.ScrubRoot(ctx); err != nil {
			slog.Warn("Failed to clean up mock chroot", "error", err)
		}
	}
}
