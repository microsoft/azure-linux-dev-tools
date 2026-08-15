// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package evr evaluates and compares source RPM EVRs in a target mock chroot.
package evr

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

const chrootStagingPath = "/tmp/evr"

//go:embed process.py
var processScript []byte

// EVR is an evaluated RPM epoch, version, and release tuple.
type EVR struct {
	// Epoch is the normalized RPM epoch. A missing epoch is represented as "0".
	Epoch string `json:"epoch"`
	// Version is the RPM version field after target-mock macro evaluation.
	Version string `json:"version"`
	// Release is the RPM release field after target-mock macro evaluation.
	Release string `json:"release"`
}

// String returns the conventional string representation of an EVR.
func (e EVR) String() string {
	if e.Epoch != "" && e.Epoch != "0" {
		return e.Epoch + ":" + e.Version + "-" + e.Release
	}

	return e.Version + "-" + e.Release
}

// Input identifies one rendered spec to evaluate. SpecPath must live below the
// staging directory supplied to [Extractor.Extract].
type Input struct {
	// Component identifies the result row and must be unique within one batch.
	Component string
	// SpecPath is the host path to the staged rendered spec. It must remain
	// below the staging directory passed to [Extractor.Extract].
	SpecPath string
	// BuildOptions reproduces the component's RPM conditionals and macro
	// definitions when rpmspec evaluates the staged spec.
	BuildOptions rpm.BuildOptions
}

// Result is the EVR evaluation result for one component. A malformed or
// otherwise unqueryable spec is represented by Error without failing the batch.
type Result struct {
	// Component matches the [Input.Component] that produced this result.
	Component string
	// EVR is populated only when the spec was evaluated successfully.
	EVR *EVR
	// Error describes a component-local evaluation failure. It intentionally
	// does not fail the full batch so callers can report every bad component.
	Error error
}

// Comparison identifies one pair of evaluated EVRs to compare.
type Comparison struct {
	// Component identifies the result row and must be unique within one batch.
	Component string
	// Previous is the EVR evaluated from the baseline rendered spec.
	Previous EVR
	// Current is the EVR evaluated from the candidate rendered spec.
	Current EVR
}

// ComparisonResult is the rpm.labelCompare result for one component. Compare
// is negative when Current is newer than Previous, zero when equal, and
// positive when Current is older than Previous.
type ComparisonResult struct {
	// Component matches the [Comparison.Component] that produced this result.
	Component string
	// Compare is rpm.labelCompare(Previous, Current): -1 means Current is
	// newer, 0 means equal, and 1 means Current is older.
	Compare int
	// Error describes a component-local comparison failure without aborting the
	// rest of the batch.
	Error error
}

// Extractor evaluates rendered specs and compares their EVRs in one lazily
// initialized mock chroot. It serializes calls because every invocation shares
// a single mutable chroot root.
type Extractor struct {
	// mu serializes access to the shared mutable mock root and cached init state.
	mu sync.Mutex
	// runner is the reusable mock configuration cloned for each chroot command.
	runner *mock.Runner
	// initialized records that initialization either completed or permanently
	// failed, so repeated batches do not rebuild the chroot.
	initialized bool
	// initErr caches the first chroot initialization failure for later callers.
	initErr error
}

// ExtractorOption configures an [Extractor] at construction time.
type ExtractorOption func(*mock.Runner)

// WithIsolatedMockBaseDir places the extractor's mock root under baseDir
// instead of mock's shared default. This keeps [Extractor.Destroy] from
// scrubbing a build or render chroot that uses the same mock config.
func WithIsolatedMockBaseDir(baseDir string) ExtractorOption {
	return func(runner *mock.Runner) {
		if baseDir != "" {
			runner.WithBaseDir(baseDir)
		}
	}
}

// NewExtractor constructs a batch extractor for the supplied mock config. The
// mock root is initialized on the first [Extractor.Extract] or
// [Extractor.Compare] call.
func NewExtractor(ctx opctx.Ctx, mockConfigPath string, options ...ExtractorOption) *Extractor {
	runner := mock.NewRunner(ctx, mockConfigPath)
	for _, option := range options {
		option(runner)
	}

	return &Extractor{runner: runner}
}

// Extract evaluates every input's SRPM EVR in parallel inside a single mock
// chroot. Per-component query errors are returned in the corresponding result;
// only chroot, script, manifest, or result-file failures return a batch error.
func (e *Extractor) Extract(
	ctx context.Context,
	events opctx.EventListener,
	stagingDir string,
	inputs []Input,
	fs opctx.FS,
	maxWorkers int,
) ([]Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(inputs) == 0 {
		return nil, nil
	}

	manifest, err := makeExtractManifest(stagingDir, inputs)
	if err != nil {
		return nil, err
	}

	if err := e.initOnce(ctx); err != nil {
		return nil, err
	}

	if err := fileutils.WriteJSONFile(fs, filepath.Join(stagingDir, "extract-inputs.json"), manifest); err != nil {
		return nil, fmt.Errorf("writing EVR extraction manifest:\n%w", err)
	}

	if err := e.writeScript(fs, stagingDir); err != nil {
		return nil, err
	}

	if err := e.runScript(ctx, events, stagingDir, "extract", len(inputs), maxWorkers); err != nil {
		return nil, err
	}

	resultsData, err := fileutils.ReadFile(fs, filepath.Join(stagingDir, "extract-results.json"))
	if err != nil {
		return nil, fmt.Errorf("reading EVR extraction results:\n%w", err)
	}

	return parseExtractResults(resultsData, inputs)
}

// Compare compares every EVR pair with python3-rpm's rpm.labelCompare inside
// the extractor's mock chroot. Per-component comparison errors do not fail the
// batch; infrastructure failures return an error.
func (e *Extractor) Compare(
	ctx context.Context,
	events opctx.EventListener,
	stagingDir string,
	comparisons []Comparison,
	fs opctx.FS,
	maxWorkers int,
) ([]ComparisonResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(comparisons) == 0 {
		return nil, nil
	}

	manifest, err := makeComparisonManifest(comparisons)
	if err != nil {
		return nil, err
	}

	if err := e.initOnce(ctx); err != nil {
		return nil, err
	}

	if err := fileutils.WriteJSONFile(fs, filepath.Join(stagingDir, "comparison-inputs.json"), manifest); err != nil {
		return nil, fmt.Errorf("writing EVR comparison manifest:\n%w", err)
	}

	if err := e.writeScript(fs, stagingDir); err != nil {
		return nil, err
	}

	if err := e.runScript(ctx, events, stagingDir, "compare", len(comparisons), maxWorkers); err != nil {
		return nil, err
	}

	resultsData, err := fileutils.ReadFile(fs, filepath.Join(stagingDir, "comparison-results.json"))
	if err != nil {
		return nil, fmt.Errorf("reading EVR comparison results:\n%w", err)
	}

	return parseComparisonResults(resultsData, comparisons)
}

// Destroy removes the lazily initialized mock root. The extractor must not be
// reused after it is destroyed.
func (e *Extractor) Destroy(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.runner == nil || !e.initialized {
		return
	}

	if err := e.runner.ScrubRoot(ctx); err != nil {
		slog.Warn("Failed to clean up EVR mock chroot", "error", err)
	}
}

func (e *Extractor) initOnce(ctx context.Context) error {
	if e.initialized {
		return e.initErr
	}

	slog.Info("Initializing mock chroot for EVR evaluation")

	if err := e.runner.InitRoot(ctx); err != nil {
		e.initErr = fmt.Errorf("initializing EVR mock chroot:\n%w", err)
		e.initialized = true

		return e.initErr
	}

	// rpmspec is provided by the base RPM toolchain. python3-rpm provides the
	// authoritative labelCompare implementation used for comparison.
	if err := e.runner.InstallPackages(ctx, []string{"python3-rpm"}); err != nil {
		e.initErr = fmt.Errorf("installing EVR mock packages:\n%w", err)
		e.initialized = true

		return e.initErr
	}

	e.initialized = true

	slog.Info("Mock chroot ready for EVR evaluation")

	return nil
}

func (e *Extractor) writeScript(fs opctx.FS, stagingDir string) error {
	if err := fileutils.WriteFile(
		fs, filepath.Join(stagingDir, "process.py"), processScript, fileperms.PublicExecutable,
	); err != nil {
		return fmt.Errorf("writing EVR process script:\n%w", err)
	}

	return nil
}

func (e *Extractor) runScript(
	ctx context.Context,
	events opctx.EventListener,
	stagingDir, mode string,
	total, maxWorkers int,
) error {
	runner := e.runner.Clone()
	runner.WithUnprivileged()
	runner.AddBindMount(stagingDir, chrootStagingPath)

	workers := strconv.Itoa(max(1, maxWorkers))

	cmd, err := runner.CmdInChroot(ctx, []string{
		"python3", filepath.Join(chrootStagingPath, "process.py"), mode, chrootStagingPath, workers,
	}, false)
	if err != nil {
		return fmt.Errorf("creating EVR batch command in mock:\n%w", err)
	}

	progress := events.StartEvent("Evaluating EVRs in mock chroot", "count", total)

	progress.SetLongRunning("Evaluating EVRs in mock chroot")
	defer progress.End()

	if listenerErr := cmd.SetRealTimeStdoutListener(func(_ context.Context, line string) {
		after, found := strings.CutPrefix(line, "PROGRESS ")
		if !found {
			return
		}

		slashIndex := strings.Index(after, "/")
		if slashIndex <= 0 {
			return
		}

		completed, parseErr := strconv.ParseInt(after[:slashIndex], 10, 64)
		if parseErr == nil {
			progress.SetProgress(completed, int64(total))
		}
	}); listenerErr != nil {
		slog.Warn("Failed to listen for EVR evaluation progress", "error", listenerErr)
	}

	if err := cmd.Run(ctx); err != nil {
		return fmt.Errorf("running EVR batch script in mock:\n%w", err)
	}

	return nil
}

type extractManifestInput struct {
	Component string            `json:"component"`
	SpecPath  string            `json:"specPath"`
	With      []string          `json:"with,omitempty"`
	Without   []string          `json:"without,omitempty"`
	Defines   map[string]string `json:"defines,omitempty"`
	Undefines []string          `json:"undefines,omitempty"`
}

func makeExtractManifest(stagingDir string, inputs []Input) ([]extractManifestInput, error) {
	seen := make(map[string]bool, len(inputs))
	manifest := make([]extractManifestInput, 0, len(inputs))

	for _, input := range inputs {
		if err := validateComponentName(input.Component, seen); err != nil {
			return nil, err
		}

		relPath, err := specPathUnderStaging(stagingDir, input.SpecPath)
		if err != nil {
			return nil, fmt.Errorf("invalid spec path for component %#q:\n%w", input.Component, err)
		}

		defines := make(map[string]string, len(input.BuildOptions.Defines))
		for key, value := range input.BuildOptions.Defines {
			defines[key] = value
		}

		manifest = append(manifest, extractManifestInput{
			Component: input.Component,
			SpecPath:  filepath.ToSlash(relPath),
			With:      append([]string(nil), input.BuildOptions.With...),
			Without:   append([]string(nil), input.BuildOptions.Without...),
			Defines:   defines,
			Undefines: append([]string(nil), input.BuildOptions.Undefines...),
		})
	}

	return manifest, nil
}

type comparisonManifestInput struct {
	Component string `json:"component"`
	Previous  EVR    `json:"previous"`
	Current   EVR    `json:"current"`
}

func makeComparisonManifest(comparisons []Comparison) ([]comparisonManifestInput, error) {
	seen := make(map[string]bool, len(comparisons))
	manifest := make([]comparisonManifestInput, 0, len(comparisons))

	for _, comparison := range comparisons {
		if err := validateComponentName(comparison.Component, seen); err != nil {
			return nil, err
		}

		manifest = append(manifest, comparisonManifestInput(comparison))
	}

	return manifest, nil
}

func validateComponentName(component string, seen map[string]bool) error {
	if err := fileutils.ValidateFilename(component); err != nil {
		return fmt.Errorf("invalid component name %#q:\n%w", component, err)
	}

	if seen[component] {
		return fmt.Errorf("duplicate component name %#q", component)
	}

	seen[component] = true

	return nil
}

func specPathUnderStaging(stagingDir, specPath string) (string, error) {
	relPath, err := filepath.Rel(stagingDir, specPath)
	if err != nil {
		return "", fmt.Errorf("computing path relative to staging directory:\n%w", err)
	}

	if relPath == "." || relPath == ".." || filepath.IsAbs(relPath) ||
		strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %#q escapes staging directory %#q", specPath, stagingDir)
	}

	return relPath, nil
}

type extractResultJSON struct {
	Component string  `json:"component"`
	EVR       *EVR    `json:"evr"`
	Error     *string `json:"error"`
}

func parseExtractResults(data []byte, inputs []Input) ([]Result, error) {
	var jsonResults []extractResultJSON
	if err := json.Unmarshal(data, &jsonResults); err != nil {
		return nil, fmt.Errorf("parsing EVR extraction results JSON:\n%w", err)
	}

	byComponent := make(map[string]extractResultJSON, len(jsonResults))
	for _, result := range jsonResults {
		if _, found := byComponent[result.Component]; found {
			return nil, fmt.Errorf("EVR extraction returned duplicate result for %#q", result.Component)
		}

		byComponent[result.Component] = result
	}

	results := make([]Result, 0, len(inputs))
	for _, input := range inputs {
		jsonResult, found := byComponent[input.Component]
		if !found {
			results = append(results, Result{
				Component: input.Component,
				Error:     fmt.Errorf("no EVR result returned for %#q", input.Component),
			})

			continue
		}

		if jsonResult.Error != nil {
			results = append(results, Result{Component: input.Component, Error: fmt.Errorf("%s", *jsonResult.Error)})

			continue
		}

		if jsonResult.EVR == nil || jsonResult.EVR.Version == "" || jsonResult.EVR.Release == "" {
			results = append(results, Result{
				Component: input.Component,
				Error:     fmt.Errorf("EVR result for %#q is missing version or release", input.Component),
			})

			continue
		}

		if jsonResult.EVR.Epoch == "" || jsonResult.EVR.Epoch == "(none)" {
			jsonResult.EVR.Epoch = "0"
		}

		results = append(results, Result{Component: input.Component, EVR: jsonResult.EVR})
	}

	return results, nil
}

type comparisonResultJSON struct {
	Component string  `json:"component"`
	Compare   *int    `json:"compare"`
	Error     *string `json:"error"`
}

func parseComparisonResults(data []byte, comparisons []Comparison) ([]ComparisonResult, error) {
	var jsonResults []comparisonResultJSON
	if err := json.Unmarshal(data, &jsonResults); err != nil {
		return nil, fmt.Errorf("parsing EVR comparison results JSON:\n%w", err)
	}

	byComponent := make(map[string]comparisonResultJSON, len(jsonResults))
	for _, result := range jsonResults {
		if _, found := byComponent[result.Component]; found {
			return nil, fmt.Errorf("EVR comparison returned duplicate result for %#q", result.Component)
		}

		byComponent[result.Component] = result
	}

	results := make([]ComparisonResult, 0, len(comparisons))
	for _, comparison := range comparisons {
		jsonResult, found := byComponent[comparison.Component]
		if !found {
			results = append(results, ComparisonResult{
				Component: comparison.Component,
				Error:     fmt.Errorf("no EVR comparison result returned for %#q", comparison.Component),
			})

			continue
		}

		if jsonResult.Error != nil {
			results = append(results, ComparisonResult{
				Component: comparison.Component,
				Error:     fmt.Errorf("%s", *jsonResult.Error),
			})

			continue
		}

		if jsonResult.Compare == nil {
			results = append(results, ComparisonResult{
				Component: comparison.Component,
				Error:     fmt.Errorf("EVR comparison result for %#q is missing compare", comparison.Component),
			})

			continue
		}

		results = append(results, ComparisonResult{Component: comparison.Component, Compare: *jsonResult.Compare})
	}

	return results, nil
}
