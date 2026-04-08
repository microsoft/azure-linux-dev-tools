// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
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
	mu          sync.Mutex
	runner      *mock.Runner
	initialized bool
	initErr     error
}

// NewMockProcessor creates a new processor that will lazily initialize
// a mock chroot using the given config path. The runner is created eagerly
// but the chroot is only initialized on first use.
func NewMockProcessor(ctx opctx.Ctx, mockConfigPath string) *MockProcessor {
	return &MockProcessor{
		runner: mock.NewRunner(ctx, mockConfigPath),
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

// isSimpleName returns true if s is a non-empty, single-component filename
// without path separators, traversal sequences, or null bytes.
func isSimpleName(s string) bool {
	return s != "" && s != "." && s != ".." &&
		!strings.ContainsAny(s, "/\\") &&
		!strings.Contains(s, "..") &&
		!strings.ContainsRune(s, 0)
}

// validateComponentInput rejects component inputs that could cause path traversal
// or other safety issues when used to construct paths inside the mock chroot.
func validateComponentInput(input ComponentInput) error {
	if !isSimpleName(input.Name) {
		return fmt.Errorf(
			"invalid component name %#q: must be a simple name without path separators or traversal sequences", input.Name)
	}

	if !isSimpleName(input.SpecFilename) {
		return fmt.Errorf("invalid spec filename %#q for component %#q", input.SpecFilename, input.Name)
	}

	return nil
}

// initOnce lazily initializes the mock chroot. Caller must hold p.mu.
func (p *MockProcessor) initOnce(ctx context.Context) error {
	if p.initialized {
		return p.initErr
	}

	slog.Info("Initializing mock chroot for rendering")

	p.runner.EnableNetwork()

	if err := p.runner.InitRoot(ctx); err != nil {
		p.initErr = fmt.Errorf("failed to initialize mock chroot:\n%w", err)
		p.initialized = true

		return p.initErr
	}

	// Install rpmautospec (macro expansion), rpmdevtools (spectool), and git
	// (required for rpmautospec to read commit history).
	// python3-click is required by rpmautospec but not declared as an RPM dependency.
	// Ecosystem macro packages (go-srpm-macros, etc.) are already present via
	// @buildsys-build → azurelinux-rpm-config.
	if err := p.runner.InstallPackages(ctx, []string{"rpmautospec", "rpmdevtools", "git", "python3-click"}); err != nil {
		p.initErr = fmt.Errorf("failed to install packages in mock chroot:\n%w", err)
		p.initialized = true

		return p.initErr
	}

	p.initialized = true

	slog.Info("Mock chroot ready for rendering")

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
	stagingDir string, inputs []ComponentInput, fs opctx.FS,
) ([]ComponentMockResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(inputs) == 0 {
		return nil, nil
	}

	if err := validateInputs(inputs); err != nil {
		return nil, err
	}

	if err := p.initOnce(ctx); err != nil {
		return nil, err
	}

	slog.Info("Batch processing components in mock chroot", "count", len(inputs))

	// Write the Python script and inputs manifest to the staging directory.
	scriptPath := filepath.Join(stagingDir, "render_process.py")
	if err := fileutils.WriteFile(fs, scriptPath, renderProcessScript, fileperms.PublicExecutable); err != nil {
		return nil, fmt.Errorf("writing render script:\n%w", err)
	}

	if err := writeInputsManifest(fs, stagingDir, inputs); err != nil {
		return nil, err
	}

	// Clone the runner and add a single bind mount for the staging directory.
	// WithUnprivileged drops to the mockbuild user for chroot commands,
	// matching how mock builds run and avoiding root-owned files in the
	// bind-mounted staging directory. This is safe because mock defaults
	// chrootuid to os.getuid() — the mockbuild user inside the chroot has
	// the same UID as the host user, so bind-mounted files remain writable.
	runner := p.runner.Clone()
	runner.WithUnprivileged()

	const chrootStagingPath = "/tmp/render"
	runner.AddBindMount(stagingDir, chrootStagingPath)

	chrootScript := filepath.Join(chrootStagingPath, "render_process.py")
	workers := strconv.Itoa(max(1, runtime.NumCPU())) // 1x CPU; mock work is CPU-bound
	args := []string{"python3", chrootScript, chrootStagingPath, workers}

	cmd, err := runner.CmdInChroot(ctx, args, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create batch command in mock:\n%w", err)
	}

	// Set up progress reporting from the Python script's output.
	// The script prints "PROGRESS <completed>/<total> <name>" to stderr, but
	// mock --chroot merges the inner command's stderr into stdout, so we
	// listen on stdout.
	mockProgress := events.StartEvent("Processing specs in mock chroot", "count", len(inputs))
	mockProgress.SetLongRunning("Processing specs in mock chroot")

	defer mockProgress.End()

	total := int64(len(inputs))

	if listenerErr := cmd.SetRealTimeStdoutListener(func(_ context.Context, line string) {
		// Parse "PROGRESS <i>/<total> <name>" lines.
		if after, found := strings.CutPrefix(line, "PROGRESS "); found {
			if slashIdx := strings.Index(after, "/"); slashIdx > 0 {
				if completed, parseErr := strconv.ParseInt(after[:slashIdx], 10, 64); parseErr == nil {
					mockProgress.SetProgress(completed, total)
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
	resultsPath := filepath.Join(stagingDir, "results.json")

	resultsData, readErr := fileutils.ReadFile(fs, resultsPath)
	if readErr != nil {
		return nil, fmt.Errorf("reading batch results from %#q:\n%w", resultsPath, readErr)
	}

	return parseBatchJSON(string(resultsData), inputs)
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

// writeInputsManifest writes the inputs.json manifest to the staging directory
// so it can be read by the Python script inside the mock chroot.
func writeInputsManifest(fs opctx.FS, stagingDir string, inputs []ComponentInput) error {
	jsonInputs := make([]componentInputJSON, len(inputs))
	for idx, input := range inputs {
		jsonInputs[idx] = componentInputJSON(input)
	}

	data, err := json.Marshal(jsonInputs)
	if err != nil {
		return fmt.Errorf("marshaling inputs:\n%w", err)
	}

	inputsPath := filepath.Join(stagingDir, "inputs.json")
	if err := fileutils.WriteFile(fs, inputsPath, data, fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing inputs manifest:\n%w", err)
	}

	return nil
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
