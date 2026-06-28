// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spectool"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

//go:embed render_process.py
var renderProcessScript []byte

// MockProcessor provides a shared mock chroot for running rpmautospec and
// spectool during component rendering. The chroot is lazily initialized on
// first use and supports batch processing of multiple components in a single
// mock invocation.
type MockProcessor struct {
	mu               sync.Mutex
	runner           *mock.Runner
	requiredPackages []string
	initialized      bool
	initErr          error
}

// NewMockProcessor creates a new processor that will lazily initialize
// a mock chroot using the given config path. The runner is created eagerly
// but the chroot is only initialized on first use. requiredPackages are
// installed inside the chroot on first use; pass nil/empty to skip the
// install step (rely on whatever the buildroot ships by default).
func NewMockProcessor(ctx opctx.Ctx, mockConfigPath string, requiredPackages []string) *MockProcessor {
	return &MockProcessor{
		runner:           mock.NewRunner(ctx, mockConfigPath),
		requiredPackages: append([]string(nil), requiredPackages...),
	}
}

// ComponentInput describes a single component to process in the mock chroot.
// Name must match the subdirectory name under the staging directory.
type ComponentInput struct {
	Name         string // Component name (matches subdirectory in staging dir)
	SpecFilename string // Just the filename, e.g., "curl.spec"
}

// ComponentMockResult holds the mock processing result for one component.
type ComponentMockResult struct {
	Name      string   // Component name
	SpecFiles []string // Files listed by spectool (basenames/relative paths)
	Error     error    // Non-nil if rpmautospec or spectool failed for this component
}

// validateInputs validates all component inputs before batch processing.
// Rejects empty names, path traversal, absolute paths, non-basename spec filenames,
// and duplicate component names.
func validateInputs(inputs []ComponentInput) error {
	seen := make(map[string]bool, len(inputs))

	for _, input := range inputs {
		if err := validateComponentInput(input); err != nil {
			return err
		}

		if seen[input.Name] {
			return fmt.Errorf("duplicate component name %#q", input.Name)
		}

		seen[input.Name] = true
	}

	return nil
}

// validateComponentInput rejects component inputs that could cause path traversal
// or other safety issues when used to construct paths inside the mock chroot.
func validateComponentInput(input ComponentInput) error {
	if err := fileutils.ValidateFilename(input.Name); err != nil {
		return fmt.Errorf("invalid component name %#q:\n%w", input.Name, err)
	}

	if err := fileutils.ValidateFilename(input.SpecFilename); err != nil {
		return fmt.Errorf("invalid spec filename %#q for component %#q:\n%w", input.SpecFilename, input.Name, err)
	}

	return nil
}

// initOnce lazily initializes the mock chroot. Caller must hold p.mu.
func (p *MockProcessor) initOnce(ctx context.Context) error {
	if p.initialized {
		return p.initErr
	}

	slog.Info("Initializing mock chroot")

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

// BatchProcess runs rpmautospec and spectool for multiple components in a single
// mock chroot invocation. stagingDir is the host directory containing one
// subdirectory per component (named by ComponentInput.Name). A single bind
// mount exposes the entire staging tree to the chroot.
//
// Components are processed in parallel inside the chroot by an embedded
// Python script (render_process.py) which returns a JSON file, and reports
// per-component progress on stderr (mapped by mock to stdout).
func (p *MockProcessor) BatchProcess(
	ctx context.Context, events opctx.EventListener,
	stagingDir string, inputs []ComponentInput, fs opctx.FS, maxWorkers int,
) ([]ComponentMockResult, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	if err := validateInputs(inputs); err != nil {
		return nil, err
	}

	jsonInputs := make([]componentInputJSON, len(inputs))
	for idx, input := range inputs {
		jsonInputs[idx] = componentInputJSON(input)
	}

	inputsBytes, err := json.Marshal(jsonInputs)
	if err != nil {
		return nil, fmt.Errorf("marshaling inputs:\n%w", err)
	}

	slog.Info("Batch processing components in mock chroot", "count", len(inputs))

	const chrootStagingPath = "/tmp/render"

	workers := strconv.Itoa(max(1, maxWorkers)) // 1x CPU; mock work is CPU-bound

	rawResults, err := p.runBatchScript(ctx, events, runBatchScriptOptions{
		Mounts:          []batchBindMount{{Host: stagingDir, InChroot: chrootStagingPath}},
		ScratchHost:     stagingDir,
		ScratchInChroot: chrootStagingPath,
		ScriptName:      "render_process.py",
		ScriptBytes:     renderProcessScript,
		InputsJSON:      inputsBytes,
		ResultsName:     "results.json",
		ScriptArgs:      []string{chrootStagingPath, workers},
		ProgressLabel:   "Processing specs in mock chroot",
		ProgressTotal:   int64(len(inputs)),
		FS:              fs,
	})
	if err != nil {
		return nil, err
	}

	return parseBatchJSON(string(rawResults), inputs)
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
	// into ScratchHost (e.g. "render_process.py").
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
// This is the shared scaffolding for BatchProcess (rendering) and
// BatchQuerySpecs (querying). Per-operation concerns (input/result shape,
// embedded script, result parsing) live in the callers.
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

// componentInputJSON is the JSON-serializable form written to inputs.json.
type componentInputJSON struct {
	Name         string `json:"name"`
	SpecFilename string `json:"specFilename"`
}

// componentResultJSON mirrors the JSON output from render_process.py.
type componentResultJSON struct {
	Name      string  `json:"name"`
	SpecFiles string  `json:"specFiles"`
	Error     *string `json:"error"`
}

// parseBatchJSON parses the JSON array produced by render_process.py into
// ComponentMockResult values. The spectool output (raw lines) is parsed into
// individual filenames.
func parseBatchJSON(stdout string, inputs []ComponentInput) ([]ComponentMockResult, error) {
	var jsonResults []componentResultJSON
	if err := json.Unmarshal([]byte(stdout), &jsonResults); err != nil {
		return nil, fmt.Errorf("parsing batch results JSON:\n%w", err)
	}

	// Build a lookup map from the JSON results.
	resultMap := make(map[string]*componentResultJSON, len(jsonResults))
	for idx := range jsonResults {
		resultMap[jsonResults[idx].Name] = &jsonResults[idx]
	}

	results := make([]ComponentMockResult, len(inputs))

	for idx, input := range inputs {
		results[idx].Name = input.Name

		compResult, ok := resultMap[input.Name]
		if !ok {
			results[idx].Error = fmt.Errorf("no result returned for %#q", input.Name)

			continue
		}

		if compResult.Error != nil {
			results[idx].Error = fmt.Errorf("%s", *compResult.Error)

			continue
		}

		results[idx].SpecFiles = spectool.ParseSpectoolOutput(compResult.SpecFiles)
	}

	return results, nil
}

// Destroy cleans up the mock chroot. Should be called when rendering is complete.
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
