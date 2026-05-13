// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/dirdiff"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

// RenderOptions holds the options for the render command.
type RenderOptions struct {
	ComponentFilter   components.ComponentFilter
	OutputDir         string
	OutputDirExplicit bool // True when --output-dir was explicitly passed on the CLI.
	FailOnError       bool
	Force             bool
	CleanStale        bool
	CheckOnly         bool
	AllowDirty        bool
}

func renderOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewRenderCmd())
}

// NewRenderCmd constructs a [cobra.Command] for the "component render" CLI subcommand.
func NewRenderCmd() *cobra.Command { //nolint:funlen // flag registrations push count slightly over
	var options RenderOptions

	var cmd *cobra.Command

	cmd = &cobra.Command{
		Use:   "render",
		Short: "Render post-overlay specs and sidecar files to a checked-in directory",
		Long: `Render the final spec and sidecar files for components after applying all
configured overlays. The output is written to a directory as generated artifacts
intended for check-in.

The output directory is set via rendered-specs-dir in the project config, or
via --output-dir on the command line. If neither is set, an error is returned.
Within the output directory, components are organized into letter-prefixed
subdirectories based on the first character of their name (e.g., specs/c/curl,
specs/v/vim).

Unlike prepare-sources, render skips downloading source tarballs from the
lookaside cache — only spec files, patches, scripts, and other git-tracked
sidecar files are included. Multiple components can be rendered at once.

When rendering all components (-a), the --clean-stale flag prunes orphan
rendered-spec directories (per-component dirs that no longer correspond to
any component in the project config). Per-component dirs that ARE in config
are overwritten in place by the render itself; this means each render's
result table accurately reflects which components actually changed on disk.
Top-level non-component siblings (e.g. a hand-placed README.md) are
preserved. When using a custom output directory (--output-dir), --force is
required alongside --clean-stale as a safety measure. This flag is only
valid with -a.`,
		Example: `  # Render all components (output dir from config)
  azldev component render -a

  # Render a single component
  azldev component render -p curl

  # Render to a custom directory, allowing removal of existing rendered component directories
  azldev component render -a -o rendered/ --force

  # Render all and remove stale directories
  azldev component render -a --clean-stale`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)
			options.OutputDirExplicit = cmd.Flags().Changed("output-dir")

			return RenderComponents(env, &options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
		Annotations: map[string]string{
			azldev.CommandAnnotationRootOK: "true",
		},
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().StringVarP(&options.OutputDir, "output-dir", "o", "",
		"output directory for rendered specs (overrides rendered-specs-dir from config)")
	_ = cmd.MarkFlagDirname("output-dir")

	cmd.Flags().BoolVar(&options.FailOnError, "fail-on-error", false,
		"exit with error if any component fails to render (useful for CI)")

	cmd.Flags().BoolVarP(&options.Force, "force", "f", false,
		"allow overwriting existing rendered component directories")

	cmd.Flags().BoolVar(&options.CleanStale, "clean-stale", false,
		"prune rendered-spec directories that no longer correspond to a configured "+
			"component (only with -a; requires -f with -o). Top-level non-component "+
			"siblings are preserved.")

	cmd.Flags().BoolVar(&options.CheckOnly, "check-only", false,
		"render to a staging area and compare against the existing on-disk output "+
			"without modifying the output directory. Exits 0 when nothing would change "+
			"and 1 when any component would drift. With -a + --clean-stale, also fails "+
			"on orphan rendered-spec directories. Intended for CI gates.")

	cmd.Flags().BoolVarP(&options.AllowDirty, "allow-dirty", "d", false,
		"include uncommitted changes in synthetic history")

	// --check-only is a read-only diff against on-disk state; --fail-on-error
	// is the loud-failure-per-run knob. Combining them is semantically
	// muddled (CI would fail on stale failures even when on-disk markers
	// already record them) and forcing a choice keeps the contract crisp.
	cmd.MarkFlagsMutuallyExclusive("fail-on-error", "check-only")

	// --check-only validates committed state for CI gates; --allow-dirty
	// injects uncommitted state. Combining them would validate against
	// working-tree state instead of committed state, defeating the purpose.
	cmd.MarkFlagsMutuallyExclusive("check-only", "allow-dirty")

	return cmd
}

// RenderResult holds the result of rendering a single component.
type RenderResult struct {
	Component string `json:"component"       table:"Component"`
	OutputDir string `json:"outputDir"       table:"Output"`
	Status    string `json:"status"          table:"Status"`
	Error     string `json:"error,omitempty" table:"Error,omitempty"`
	Changed   bool   `json:"changed"         table:"Changed"`
}

// Render status constants.
const (
	renderStatusOK        = "ok"
	renderStatusError     = "error"
	renderStatusCancelled = "cancelled"
)

// RenderComponents renders the post-overlay spec and sidecar files for each
// selected component into the output directory. Processing is done in three phases:
//  1. Parallel source preparation (clone, overlay, synthetic git)
//  2. Batch mock processing (rpmautospec + spectool in a single chroot call)
//  3. Parallel finishing (filter files, remove .git, copy output)
func RenderComponents(env *azldev.Env, options *RenderOptions) ([]*RenderResult, error) {
	if err := resolveAndValidateOutputDir(env, options); err != nil {
		return nil, err
	}

	if err := validateCleanStaleOptions(options); err != nil {
		return nil, err
	}

	resolver := components.NewResolver(env)

	comps, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve components:\n%w", err)
	}

	if comps.Len() == 0 {
		return nil, errors.New("no components were selected; " +
			"please use command-line options to indicate which components to render",
		)
	}

	// Create mock processor for rpmautospec/spectool.
	mockProcessor := createMockProcessor(env)
	if mockProcessor == nil {
		return nil, errors.New(
			"mock config required for rendering; ensure the project has a valid distro with mock config")
	}

	defer mockProcessor.Destroy(env)

	// Create a shared staging directory. Each component gets a subdirectory
	// named by component name, enabling a single bind mount for the batch
	// mock invocation. Use the project work dir instead of /tmp to avoid
	// filling up tmpfs on large renders.
	if err := env.FS().MkdirAll(env.WorkDir(), fileperms.PublicDir); err != nil {
		return nil, fmt.Errorf("creating work directory:\n%w", err)
	}

	stagingDir, err := fileutils.MkdirTemp(env.FS(), env.WorkDir(), "azldev-render-staging-")
	if err != nil {
		return nil, fmt.Errorf("creating staging directory:\n%w", err)
	}

	defer func() {
		if removeErr := env.FS().RemoveAll(stagingDir); removeErr != nil {
			slog.Debug("Failed to clean up staging directory", "path", stagingDir, "error", removeErr)
		}
	}()

	componentList := comps.Components()
	results := make([]*RenderResult, len(componentList))

	// ── Phase 1: Parallel source preparation ──
	prepared := parallelPrepare(env, componentList, stagingDir, options.OutputDir, results, options.AllowDirty)

	// ── Phase 2: Batch mock processing ──
	mockResultMap := batchMockProcess(env, mockProcessor, stagingDir, prepared)

	// Prune orphan component dirs (components removed from config) when
	// --clean-stale is set. Per-component output dirs that match the resolved
	// component set are NOT touched here; phase 3 will RemoveAll+rewrite each
	// of them via copyRenderedOutput. This keeps the diff against existing
	// output meaningful (so result.Changed reflects actual content drift
	// instead of being unconditionally true after a blanket wipe) and reduces
	// the blast radius of a Ctrl-C: we only ever delete dirs that wouldn't
	// have been re-rendered anyway.
	//
	// Skipped in --check-only mode -- check-only must never touch disk; the
	// orphan list is computed read-only later via checkOnlyRenderResult.
	if options.CleanStale && !options.CheckOnly {
		names := make([]string, len(componentList))
		for idx, comp := range componentList {
			names[idx] = comp.GetName()
		}

		if pruneErr := pruneOrphanRenderedDirs(env.FS(), options.OutputDir, names); pruneErr != nil {
			return nil, fmt.Errorf("pruning orphan rendered-spec dirs in %#q:\n%w", options.OutputDir, pruneErr)
		}
	}

	// ── Phase 3: Parallel finishing ──
	parallelFinish(env, prepared, mockResultMap, results, stagingDir,
		options.Force, options.CheckOnly)

	// Write RENDER_FAILED markers for any component that errored in phase 1
	// (source preparation) or phase 3 (mock result application + copy).
	// Centralizing this here makes it idempotent with the --clean-stale wipe
	// (which sits between phases 2 and 3) and keeps the per-phase code free
	// of bookkeeping. In --check-only mode this verifies that on-disk state
	// matches the expected single-marker shape and flags drift on mismatch.
	writeFailureMarkers(env.FS(), results, options.Force, options.CheckOnly)

	// Sort results alphabetically for consistent output.
	sortRenderResults(results)

	if options.CheckOnly {
		return results, checkOnlyRenderResult(env.FS(), options, componentList, results)
	}

	return results, checkRenderErrors(results, options.FailOnError)
}

// checkOnlyRenderResult inspects results from a --check-only run and returns
// a non-nil error when any component changed or any orphan rendered-spec
// directory was detected. Orphan detection runs only with -a + --clean-stale
// (the only configuration where a normal run would actually remove orphans).
// The error message names every changed component and orphan so CI logs are
// useful at a glance.
func checkOnlyRenderResult(
	fileSystem opctx.FS,
	options *RenderOptions,
	resolvedComps []components.Component,
	results []*RenderResult,
) error {
	var changed []string

	for _, result := range results {
		if result != nil && result.Changed {
			changed = append(changed, result.Component)
		}
	}

	var orphans []string

	if options.ComponentFilter.IncludeAllComponents && options.CleanStale {
		names := make([]string, len(resolvedComps))
		for idx, comp := range resolvedComps {
			names[idx] = comp.GetName()
		}

		found, err := findOrphanRenderedDirs(fileSystem, options.OutputDir, names)
		if err != nil {
			return fmt.Errorf("checking for orphan rendered-spec dirs:\n%w", err)
		}

		orphans = found
	}

	if len(changed) == 0 && len(orphans) == 0 {
		return nil
	}

	parts := make([]string, 0)
	if len(changed) > 0 {
		parts = append(parts, fmt.Sprintf("%d component(s) would change: %s",
			len(changed), strings.Join(changed, ", ")))
	}

	if len(orphans) > 0 {
		parts = append(parts, fmt.Sprintf("%d orphan rendered-spec dir(s): %s",
			len(orphans), strings.Join(orphans, ", ")))
	}

	return fmt.Errorf("rendered output is stale; %s. Run 'azldev component render -a' to refresh",
		strings.Join(parts, "; "))
}

// sortRenderResults sorts render results alphabetically by component name,
// with nil entries sorted to the end.
func sortRenderResults(results []*RenderResult) {
	slices.SortFunc(results, func(left, right *RenderResult) int {
		switch {
		case left == nil && right == nil:
			return 0
		case left == nil:
			return 1 // nils sort to end
		case right == nil:
			return -1
		default:
			return strings.Compare(left.Component, right.Component)
		}
	})
}

// checkRenderErrors counts error and cancelled results and returns an error if FailOnError is set.
func checkRenderErrors(results []*RenderResult, failOnError bool) error {
	var errCount, cancelledCount int

	for _, result := range results {
		if result == nil {
			continue
		}

		switch result.Status {
		case renderStatusError:
			errCount++
		case renderStatusCancelled:
			cancelledCount++
		}
	}

	failCount := errCount + cancelledCount

	if failCount > 0 {
		slog.Error("Some components failed to render",
			"errorCount", errCount, "cancelledCount", cancelledCount)

		if failOnError {
			return fmt.Errorf("%d component(s) failed to render", failCount)
		}
	}

	// When FailOnError is not set, intentionally return nil error even when
	// some components fail. Returning an error would suppress the results
	// table (runFuncInternal skips reportResults on error), hiding the status
	// of all ~7k components. Individual failures are visible in the table's
	// Status/Error columns and via RENDER_FAILED marker files.
	return nil
}

// preparedComponent holds the intermediate state after source preparation,
// before mock processing.
type preparedComponent struct {
	index         int
	comp          components.Component
	specFilename  string // e.g., "curl.spec"
	compOutputDir string // validated output path computed in phase 1
}

// prepResult pairs a prepared component (on success) or a render result (on error).
type prepResult struct {
	prepared *preparedComponent
	result   *RenderResult // non-nil on error
}

// ──────────────────────────────────────────────────────────────────────────────
// Phase 1: Parallel source preparation
// ──────────────────────────────────────────────────────────────────────────────

// parallelPrepare prepares sources for all components concurrently, bounded by
// [azldev.Env.IOBoundConcurrency]. Each component's sources are written to a
// subdirectory of stagingDir. Failed and cancelled components get their
// result written directly into results; successful ones are returned in the
// prepared slice for phase 2 / phase 3.
func parallelPrepare(
	env *azldev.Env,
	comps []components.Component,
	stagingDir string,
	outputDir string,
	results []*RenderResult,
	allowDirty bool,
) []*preparedComponent {
	progressEvent := env.StartEvent("Preparing component sources", "count", len(comps))
	defer progressEvent.End()

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	total := int64(len(comps))

	parmapResults := parmap.Map(
		workerEnv,
		env.IOBoundConcurrency(),
		comps,
		func(done, _ int) { progressEvent.SetProgress(int64(done), total) },
		func(_ context.Context, comp components.Component) prepResult {
			// workerEnv (captured) is the effective context for this call chain;
			// the parmap-supplied ctx is identical and unused here.
			//nolint:contextcheck // env carries the ctx
			return prepareOneComponent(workerEnv, comp, stagingDir, outputDir, allowDirty)
		},
	)

	prepared := make([]*preparedComponent, 0, len(comps))

	for idx, result := range parmapResults {
		switch {
		case result.Cancelled:
			// Worker never started — ctx ended before parmap reached it.
			compName := comps[idx].GetName()

			compOutputDir, nameErr := components.RenderedSpecDir(outputDir, compName)
			if nameErr != nil {
				compOutputDir = "(invalid)"
			}

			results[idx] = &RenderResult{
				Component: compName,
				OutputDir: compOutputDir,
				Status:    renderStatusCancelled,
				Error:     "context cancelled",
			}
		case result.Value.result != nil:
			results[idx] = result.Value.result
		default:
			result.Value.prepared.index = idx
			prepared = append(prepared, result.Value.prepared)
		}
	}

	return prepared
}

// prepareOneComponent validates the output path for a single component and
// prepares its sources. Returns a [prepResult] carrying either a successful
// preparedComponent or a [RenderResult] describing the error.
//
// Called from a [parmap.Map] worker; semaphore acquisition and ctx-aware
// cancellation are handled by parmap. Errors from [prepareComponentSources]
// (including ctx cancellation mid-flight) surface as [renderStatusError] here.
func prepareOneComponent(
	env *azldev.Env,
	comp components.Component,
	stagingDir string,
	outputDir string,
	allowDirty bool,
) prepResult {
	componentName := comp.GetName()

	// Validate component name and compute output directory.
	compOutputDir, nameErr := components.RenderedSpecDir(outputDir, componentName)
	if nameErr != nil {
		return prepResult{result: &RenderResult{
			Component: componentName,
			OutputDir: "(invalid)",
			Status:    renderStatusError,
			Error:     nameErr.Error(),
		}}
	}

	prep, err := prepareComponentSources(env, comp, stagingDir, allowDirty)
	if err != nil {
		slog.Error("Failed to prepare component sources",
			"component", componentName, "error", err)

		return prepResult{result: &RenderResult{
			Component: componentName,
			OutputDir: compOutputDir,
			Status:    renderStatusError,
			Error:     err.Error(),
		}}
	}

	prep.compOutputDir = compOutputDir

	return prepResult{prepared: prep}
}

// prepareComponentSources resolves the distro, creates a source manager, and
// prepares sources (clone + overlays + synthetic git) for a single component
// into a subdirectory of stagingDir.
func prepareComponentSources(
	env *azldev.Env,
	comp components.Component,
	stagingDir string,
	allowDirty bool,
) (*preparedComponent, error) {
	componentName := comp.GetName()

	event := env.StartEvent("Preparing component sources", "component", componentName)
	defer event.End()

	// Resolve the effective distro for this component.
	distro, err := sourceproviders.ResolveDistro(env, comp)
	if err != nil {
		return nil, fmt.Errorf("resolving distro for %#q:\n%w", componentName, err)
	}

	// Create source manager.
	sourceManager, err := sourceproviders.NewSourceManager(env, distro)
	if err != nil {
		return nil, fmt.Errorf("creating source manager for %#q:\n%w", componentName, err)
	}

	// Component sources go into stagingDir/<componentName>/.
	componentDir := filepath.Join(stagingDir, componentName)

	if mkdirErr := fileutils.MkdirAll(env.FS(), componentDir); mkdirErr != nil {
		return nil, fmt.Errorf("creating component staging directory:\n%w", mkdirErr)
	}

	// Prepare sources with overlays, skipping lookaside downloads.
	// WithGitRepo preserves upstream .git and creates synthetic history so
	// rpmautospec can expand %autorelease and %autochangelog correctly.
	// WithSkipLookaside avoids expensive tarball downloads — only spec +
	// sidecar files are needed for rendering.
	preparerOpts := []sources.PreparerOption{
		sources.WithGitRepo(env, env.LockReader(), distro.Version.ReleaseVer),
		sources.WithSkipLookaside(),
	}

	if allowDirty {
		preparerOpts = append(preparerOpts, sources.WithDirtyDetection())
	}

	preparer, err := sources.NewPreparer(sourceManager, env.FS(), env, env, preparerOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating source preparer for %#q:\n%w", componentName, err)
	}

	prepErr := preparer.PrepareSources(env, comp, componentDir, true /*applyOverlays*/)

	for _, hint := range preparer.Hints() {
		env.AddFixSuggestion(hint)
	}

	if prepErr != nil {
		return nil, fmt.Errorf("preparing sources for %#q:\n%w", componentName, prepErr)
	}

	// Find the spec file so we can pass the filename to mock.
	specPath, specErr := findSpecFile(env.FS(), componentDir, componentName)
	if specErr != nil {
		return nil, fmt.Errorf("finding spec file for %#q:\n%w", componentName, specErr)
	}

	return &preparedComponent{
		comp:         comp,
		specFilename: filepath.Base(specPath),
	}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Phase 2: Batch mock processing
// ──────────────────────────────────────────────────────────────────────────────

// batchMockProcess runs rpmautospec and spectool for all prepared components in
// a single mock chroot invocation. Returns a map from component name to result.
//
// NOTE: All components share one mock chroot initialized from the project's
// default distro. Phase 1 resolves distro per-component for source fetching,
// but the mock environment (macros, rpmautospec version, etc.) is uniform.
// This matches the current Koji build model where all components target the
// same distro version.
func batchMockProcess(
	env *azldev.Env,
	mockProcessor *sources.MockProcessor,
	stagingDir string,
	prepared []*preparedComponent,
) map[string]*sources.ComponentMockResult {
	if len(prepared) == 0 {
		return nil
	}

	// Build batch inputs from prepared components.
	inputs := make([]sources.ComponentInput, len(prepared))
	for idx, prep := range prepared {
		inputs[idx] = sources.ComponentInput{
			Name:         prep.comp.GetName(),
			SpecFilename: prep.specFilename,
		}
	}

	mockResults, err := mockProcessor.BatchProcess(env, env, stagingDir, inputs, env.FS(), env.CPUBoundConcurrency())
	if err != nil {
		slog.Error("Batch mock processing failed", "error", err)
		// Return empty map — all components will get reported as errors in phase 3.
		return nil
	}

	// Build lookup map for phase 3.
	resultMap := make(map[string]*sources.ComponentMockResult, len(mockResults))
	for idx := range mockResults {
		resultMap[mockResults[idx].Name] = &mockResults[idx]
	}

	return resultMap
}

// ──────────────────────────────────────────────────────────────────────────────
// Phase 3: Parallel finishing
// ──────────────────────────────────────────────────────────────────────────────

// parallelFinish applies mock results (file filtering, .git removal) and copies
// rendered output for all successfully prepared components. In --check-only
// mode, the copy step is replaced with a tree comparison and no disk writes
// happen.
func parallelFinish(
	env *azldev.Env,
	prepared []*preparedComponent,
	mockResultMap map[string]*sources.ComponentMockResult,
	results []*RenderResult,
	stagingDir string,
	allowOverwrite bool,
	checkOnly bool,
) {
	if len(prepared) == 0 {
		return
	}

	progressEvent := env.StartEvent("Finishing rendered output", "count", len(prepared))
	defer progressEvent.End()

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	total := int64(len(prepared))

	parmapResults := parmap.Map(
		workerEnv,
		env.IOBoundConcurrency(),
		prepared,
		func(done, _ int) { progressEvent.SetProgress(int64(done), total) },
		func(_ context.Context, prep *preparedComponent) *RenderResult {
			return finishOneComponent(workerEnv, env, prep, mockResultMap, stagingDir, allowOverwrite, checkOnly)
		},
	)

	for i, result := range parmapResults {
		prep := prepared[i]

		switch {
		case result.Cancelled:
			// Worker never started — ctx ended before parmap reached it.
			results[prep.index] = &RenderResult{
				Component: prep.comp.GetName(),
				OutputDir: prep.compOutputDir,
				Status:    renderStatusCancelled,
				Error:     "context cancelled",
			}
		default:
			results[prep.index] = result.Value
		}
	}
}

// finishOneComponent wraps [finishComponentRender] with the per-component
// result bookkeeping (status, error message). Called from a [parmap.Map]
// worker; semaphore acquisition is handled by parmap.
func finishOneComponent(
	workerEnv *azldev.Env,
	env *azldev.Env,
	prep *preparedComponent,
	mockResultMap map[string]*sources.ComponentMockResult,
	stagingDir string,
	allowOverwrite bool,
	checkOnly bool,
) *RenderResult {
	componentName := prep.comp.GetName()
	compOutputDir := prep.compOutputDir

	// Bail out early if ctx is already done so we don't write to disk after
	// a Ctrl-C while the worker pool is draining.
	if workerEnv.Err() != nil {
		return &RenderResult{
			Component: componentName,
			OutputDir: compOutputDir,
			Status:    renderStatusCancelled,
			Error:     "context cancelled",
		}
	}

	result := &RenderResult{
		Component: componentName,
		OutputDir: compOutputDir,
		Status:    renderStatusOK,
	}

	drifted, err := finishComponentRender(env, prep, mockResultMap, stagingDir, allowOverwrite, checkOnly)
	if err != nil {
		slog.Error("Failed to finish rendering component",
			"component", componentName, "error", err)

		result.Status = renderStatusError
		result.Error = err.Error()
	}

	result.Changed = drifted

	return result
}

// finishComponentRender applies mock results, filters unreferenced files,
// removes .git, diffs the staging tree against the existing on-disk output,
// and (unless checkOnly is set) copies the staging tree to the output dir.
// stagingDir is the root staging directory containing the component's subdirectory.
//
// Returns changed=true when the staging tree differs from the existing output
// (or no existing output is present). The diff is computed unconditionally so
// every render run gets a meaningful 'Changed' value in its result table; the
// disk write is the only thing gated by checkOnly.
func finishComponentRender(
	env *azldev.Env,
	prep *preparedComponent,
	mockResultMap map[string]*sources.ComponentMockResult,
	stagingDir string,
	allowOverwrite bool,
	checkOnly bool,
) (bool, error) {
	componentName := prep.comp.GetName()
	componentDir := filepath.Join(stagingDir, componentName)
	specPath := filepath.Join(componentDir, prep.specFilename)

	// Check mock result.
	mockResult, hasMockResult := mockResultMap[componentName]
	if !hasMockResult {
		return false, fmt.Errorf(
			"no mock result for %#q (batch mock processing failed; see earlier errors)", componentName)
	}

	if mockResult.Error != nil {
		return false, fmt.Errorf(
			"mock processing failed for %#q:\n%w", componentName, mockResult.Error)
	}

	// Filter files using spectool result from batch mock.
	// Skip filtering when:
	// 1. The component config explicitly opts out via 'skip-file-filter'.
	// 2. spectool output contains unexpanded RPM macros (%{...}), indicating
	//    that the reported filenames don't match the real files on disk.
	if prep.comp.GetConfig().Render.SkipFileFilter {
		slog.Info("Skipping file filter ('skip-file-filter' is set)", "component", componentName)
	} else if macro := findUnexpandedMacro(mockResult.SpecFiles); macro != "" {
		slog.Info("Skipping file filter (spectool output contains unexpanded macros)",
			"component", componentName, "example", macro)
	} else if filterErr := removeUnreferencedFiles(
		env.FS(), componentDir, specPath, mockResult.SpecFiles, componentName,
	); filterErr != nil {
		return false, fmt.Errorf("filtering unreferenced files for %#q:\n%w", componentName, filterErr)
	}

	// Remove .git directory — must not appear in rendered output.
	// rpmautospec already read it during the batch mock phase.
	gitDir := filepath.Join(componentDir, ".git")
	if removeErr := env.FS().RemoveAll(gitDir); removeErr != nil {
		slog.Debug("Failed to remove .git directory", "path", gitDir, "error", removeErr)
	}

	// Compare staging tree to existing output. Always done so the result table
	// reflects which components actually changed on disk this run, not just
	// in --check-only mode.
	changed, diffErr := diffRenderedOutput(env.FS(), componentDir, prep.compOutputDir)
	if diffErr != nil {
		return false, fmt.Errorf("comparing rendered output for %#q:\n%w", componentName, diffErr)
	}

	if checkOnly {
		return changed, nil
	}

	// Copy rendered files to the component's output directory.
	if copyErr := copyRenderedOutput(env, componentDir, prep.compOutputDir, allowOverwrite); copyErr != nil {
		return changed, copyErr
	}

	// Best-effort: create a sibling symlink at the URL-encoded component name to
	// bridge a path-encoding mismatch. We percent-encode component names like
	// 'libxml++' into 'libxml%2B%2B' when building the SCM URL fragment passed to
	// the build host (koji), but the build system then uses that fragment as a
	// filesystem path without decoding it. The symlink lets the build host find
	// the component under either form.
	//
	//nolint:godox // tracked by TODO(koji-fragment-decode) tag.
	// TODO(koji-fragment-decode): remove once the build system decodes fragments.
	if aliasErr := writeAliasSymlink(env.FS(), prep.compOutputDir, componentName); aliasErr != nil {
		slog.Warn("Failed to create rendered-spec alias symlink; downstream build steps"+
			" that consume the percent-encoded path may fail to locate this component",
			"component", componentName, "error", aliasErr)
	}

	slog.Info("Rendered component", "component", componentName,
		"output", prep.compOutputDir)

	return changed, nil
}

// writeAliasSymlink creates a sibling symlink alongside componentOutputDir at the
// URL-encoded form of componentName, pointing back at the real directory with a
// relative target.
//
// No-ops when no encoding is needed (plain ASCII names) or when the underlying
// filesystem doesn't support symlinks (e.g., in-memory test FS).
//
// Refuses to overwrite a non-symlink at the alias path — if a real component
// directory already lives there (the hypothetical 'gtk%2B' next to 'gtk+'
// case), bail with an error rather than silently destroying that component's
// rendered output. RPM names don't use '%' in practice, so this is belt-and
// suspenders.
func writeAliasSymlink(fileSystem opctx.FS, componentOutputDir, componentName string) error {
	aliasName := components.RenderedSpecDirAliasName(componentName)
	if aliasName == "" {
		return nil
	}

	linker, ok := fileSystem.(afero.Linker)
	if !ok {
		slog.Debug("Filesystem doesn't support symlinks; skipping rendered-spec alias",
			"component", componentName)

		return nil
	}

	parentDir := filepath.Dir(componentOutputDir)
	aliasPath := filepath.Join(parentDir, aliasName)

	// Inspect any existing entry at the alias path. We only ever clobber a
	// pre-existing symlink (a stale alias from a previous render); a real
	// directory or file there means a name collision with another component
	// and must be reported, not silently destroyed.
	info, lstatErr := lstatIfPossible(fileSystem, aliasPath)
	switch {
	case lstatErr == nil && info.Mode()&os.ModeSymlink == 0:
		return fmt.Errorf(
			"alias path %#q is already occupied by a non-symlink entry; refusing to overwrite",
			aliasPath)
	case lstatErr == nil:
		// Existing symlink — remove and replace below.
		if removeErr := fileSystem.Remove(aliasPath); removeErr != nil {
			return fmt.Errorf("removing existing alias symlink %#q:\n%w", aliasPath, removeErr)
		}
	case !errors.Is(lstatErr, os.ErrNotExist):
		return fmt.Errorf("inspecting alias path %#q:\n%w", aliasPath, lstatErr)
	}

	// Use a relative target so the rendered tree stays portable.
	target := filepath.Base(componentOutputDir)
	if symErr := linker.SymlinkIfPossible(target, aliasPath); symErr != nil {
		return fmt.Errorf("creating alias symlink %#q -> %#q:\n%w", aliasPath, target, symErr)
	}

	return nil
}

// lstatIfPossible returns the link info at path without following symlinks, if
// the underlying filesystem supports it. Falls back to a regular Stat otherwise.
func lstatIfPossible(fileSystem opctx.FS, path string) (os.FileInfo, error) {
	if lstater, ok := fileSystem.(afero.Lstater); ok {
		info, _, err := lstater.LstatIfPossible(path)

		return info, err //nolint:wrapcheck // pass-through to the caller.
	}

	return fileSystem.Stat(path) //nolint:wrapcheck // pass-through to the caller.
}

// copyRenderedOutput copies the rendered files from tempDir to the component's output directory.
// For managed output (inside project root), existing output is removed before copying.
// For external output, existing directories cause an error.
func copyRenderedOutput(env *azldev.Env, tempDir, componentOutputDir string, allowOverwrite bool) error {
	exists, existsErr := fileutils.DirExists(env.FS(), componentOutputDir)
	if existsErr != nil {
		return fmt.Errorf("checking output directory %#q:\n%w", componentOutputDir, existsErr)
	}

	if exists {
		if !allowOverwrite {
			return fmt.Errorf(
				"output directory %#q already exists; use --force to overwrite",
				componentOutputDir)
		}

		// Clean up existing rendered output for this component.
		if removeErr := env.FS().RemoveAll(componentOutputDir); removeErr != nil {
			return fmt.Errorf("cleaning output directory %#q:\n%w", componentOutputDir, removeErr)
		}
	}

	if mkdirErr := fileutils.MkdirAll(env.FS(), componentOutputDir); mkdirErr != nil {
		return fmt.Errorf("creating output directory %#q:\n%w", componentOutputDir, mkdirErr)
	}

	// Copy all files from temp to output.
	copyOptions := fileutils.CopyDirOptions{
		CopyFileOptions: fileutils.CopyFileOptions{
			PreserveFileMode: true,
		},
	}

	if copyErr := fileutils.CopyDirRecursive(env, env.FS(), tempDir, componentOutputDir, copyOptions); copyErr != nil {
		return fmt.Errorf("copying rendered files to %#q:\n%w", componentOutputDir, copyErr)
	}

	return nil
}

// findUnexpandedMacro returns the first filename from specFiles that contains
// an unexpanded RPM macro (i.e., a literal "%{...}" sequence), or "" if all
// macros were resolved. When spectool cannot resolve a macro, it emits the raw
// macro text as part of the filename (e.g., "57-%{fontpkgname1}.xml"), which
// won't match any real file on disk.
func findUnexpandedMacro(specFiles []string) string {
	for _, f := range specFiles {
		if strings.Contains(f, "%{") {
			return f
		}
	}

	return ""
}

// removeUnreferencedFiles removes files from the directory that aren't in the keep-list.
// The keep-list is built from the spec file, the "sources" directory, and all
// source/patch filenames provided. For paths with subdirectories (e.g., "patches/fix.patch"),
// the top-level directory ("patches") is kept.
func removeUnreferencedFiles(fs opctx.FS, tempDir, specPath string, specFiles []string, componentName string) error {
	keepSet := make(map[string]bool, len(specFiles))
	keepSet[filepath.Base(specPath)] = true
	keepSet["sources"] = true // lookaside hashes/signatures; always preserved

	for _, f := range specFiles {
		// Extract the first path component so subdirectory entries are preserved.
		topLevel := strings.SplitN(f, string(filepath.Separator), 2)[0] //nolint:mnd // split into at most 2 parts
		keepSet[topLevel] = true
	}

	// Walk the temp directory and remove anything not in the keep set.
	entries, readErr := fileutils.ReadDir(fs, tempDir)
	if readErr != nil {
		return fmt.Errorf("reading temp directory %#q:\n%w", tempDir, readErr)
	}

	for _, entry := range entries {
		if keepSet[entry.Name()] {
			continue
		}

		removePath := filepath.Join(tempDir, entry.Name())

		slog.Debug("Filtering out unreferenced entry",
			"component", componentName,
			"file", entry.Name(),
		)

		if removeErr := fs.RemoveAll(removePath); removeErr != nil {
			return fmt.Errorf("failed to remove filtered entry %#q for component %#q:\n%w",
				entry.Name(), componentName, removeErr)
		}
	}

	return nil
}

// findSpecFile locates the spec file for a component in the given directory.
func findSpecFile(fs opctx.FS, dir, componentName string) (string, error) {
	specPath := filepath.Join(dir, componentName+".spec")

	exists, err := fileutils.Exists(fs, specPath)
	if err != nil {
		return "", fmt.Errorf("checking spec file %#q:\n%w", specPath, err)
	}

	if !exists {
		return "", fmt.Errorf("expected spec file %#q not found for component %#q", specPath, componentName)
	}

	return specPath, nil
}

// renderErrorMarkerFile is the name of the marker file written to a component's
// output directory when rendering fails. This makes the failure visible in git diff
// when the issue is later fixed (the marker file disappears, replaced by real output).
const renderErrorMarkerFile = "RENDER_FAILED"

// writeRenderErrorMarker writes a static marker file to the component's output directory
// indicating that rendering failed. The content is intentionally static (no error details)
// so the file is deterministic across runs and safe to check in.
//
// This is always written on failure, even without --force. The --force flag controls
// deletion of existing directories (RemoveAll), not creation of new files. Writing a
// small marker into an existing directory is safe and provides visible git diff feedback.
func writeRenderErrorMarker(fs opctx.FS, componentOutputDir string) {
	if mkdirErr := fileutils.MkdirAll(fs, componentOutputDir); mkdirErr != nil {
		slog.Debug("Failed to create directory for error marker", "path", componentOutputDir, "error", mkdirErr)

		return
	}

	markerPath := filepath.Join(componentOutputDir, renderErrorMarkerFile)

	if writeErr := fileutils.WriteFile(
		fs, markerPath, []byte(renderErrorMarkerContent), fileperms.PublicFile,
	); writeErr != nil {
		slog.Debug("Failed to write render error marker", "path", markerPath, "error", writeErr)
	}
}

// resolveAndValidateOutputDir resolves the output directory from CLI flags and
// project config. If neither the config nor --output-dir provides a path, an
// error is returned. When the output dir comes from config, --force is auto-set
// to allow overwriting component output (the configured path is trusted).
func resolveAndValidateOutputDir(env *azldev.Env, options *RenderOptions) error {
	configDir := env.Config().Project.RenderedSpecsDir

	switch {
	case options.OutputDirExplicit:
		// CLI flag wins — use as-is.
	case configDir != "":
		// Config provides the output dir; auto-trust it for overwrites.
		options.OutputDir = configDir
		options.Force = true
	default:
		return errors.New(
			"no output directory configured; set rendered-specs-dir in the project config " +
				"or pass --output-dir (-o) on the command line")
	}

	return validateOutputDir(options.OutputDir)
}

// validateOutputDir rejects output directory values that could cause the
// --clean-stale wipe to delete unrelated directories.
func validateOutputDir(outputDir string) error {
	cleaned := filepath.Clean(outputDir)
	if cleaned == "." || cleaned == string(filepath.Separator) ||
		cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf(
			"output directory %#q is unsafe; use a dedicated subdirectory (e.g., ./SPECS/)", outputDir)
	}

	return nil
}

// pruneOrphanRenderedDirs removes per-component rendered-spec directories
// under outputDir that don't correspond to any component in resolvedComps.
// Per-component dirs that ARE in resolvedComps are left alone -- phase 3 will
// overwrite them via copyRenderedOutput, and leaving the prior content here
// lets the unconditional diff in finishComponentRender produce a meaningful
// result.Changed value for the user-visible table.
//
// Top-level non-letter entries (e.g. a hand-placed README.md at the root of
// SPECS/) are intentionally NOT removed. The previous implementation wiped
// them too, but in practice the only callers want orphan cleanup, not a
// blanket sweep.
func pruneOrphanRenderedDirs(
	fileSystem opctx.FS, outputDir string, componentNames []string,
) error {
	orphans, err := findOrphanRenderedDirs(fileSystem, outputDir, componentNames)
	if err != nil {
		return err
	}

	for _, rel := range orphans {
		fullPath := filepath.Join(outputDir, rel)
		if removeErr := fileSystem.RemoveAll(fullPath); removeErr != nil {
			return fmt.Errorf("removing orphan rendered-spec dir %#q:\n%w", fullPath, removeErr)
		}

		slog.Info("Removed orphan rendered-spec dir", "path", fullPath)
	}

	return nil
}

// writeFailureMarkers walks the final results slice and writes a RENDER_FAILED
// marker into each errored component's output directory. When allowOverwrite
// is set, any pre-existing content at the path is removed first so the marker
// isn't surrounded by stale render output.
//
// Cancelled components are intentionally skipped — a Ctrl-C is not a render
// failure, just an incomplete run, and silently planting markers under those
// circumstances would lie in git diff.
//
// In --check-only mode, no marker is written. Instead, the existing on-disk
// state for each errored component is verified to be exactly the standard
// failure marker; any deviation flips result.Changed so the caller can fail
// the run. This delivers the 1:1 invariant the user asked for: a component
// that would fail must already be marked as failed on disk, with no extra
// stale output around it.
func writeFailureMarkers(
	fileSystem opctx.FS, results []*RenderResult, allowOverwrite, checkOnly bool,
) {
	for _, result := range results {
		if result == nil || result.Status != renderStatusError {
			continue
		}

		if checkOnly {
			drifted, err := outputDriftsFromMarker(fileSystem, result.OutputDir)
			if err != nil {
				// Surface inspection errors at Warn so a CI failure is
				// debuggable. Treat them as drift -- safer to fail loudly
				// than silently pass.
				slog.Warn("Failed to inspect output dir for failure-marker check; treating as drift",
					"path", result.OutputDir, "error", err)

				result.Changed = true

				continue
			}

			if drifted {
				result.Changed = true
			}

			continue
		}

		if allowOverwrite {
			if removeErr := fileSystem.RemoveAll(result.OutputDir); removeErr != nil {
				slog.Debug("Failed to clean output before writing error marker",
					"path", result.OutputDir, "error", removeErr)
			}
		}

		writeRenderErrorMarker(fileSystem, result.OutputDir)
	}
}

// createMockProcessor creates a [sources.MockProcessor] using the project's
// mock config. Returns nil if the mock config is not available (e.g., no project
// config loaded, or no mock config path configured).
func createMockProcessor(env *azldev.Env) *sources.MockProcessor {
	_, distroVerDef, err := env.Distro()
	if err != nil {
		slog.Info("Mock processor unavailable; could not resolve distro", "error", err)

		return nil
	}

	if distroVerDef.MockConfigPath == "" {
		slog.Info("Mock processor unavailable; no mock config path configured")

		return nil
	}

	slog.Info("Mock processor available", "mockConfig", distroVerDef.MockConfigPath)

	return sources.NewMockProcessor(env, distroVerDef.MockConfigPath)
}

// validateCleanStaleOptions enforces the constraints around --clean-stale.
// Extracted from RenderComponents to keep its complexity below the linter's
// cyclomatic threshold.
func validateCleanStaleOptions(options *RenderOptions) error {
	if !options.CleanStale {
		return nil
	}

	if !options.ComponentFilter.IncludeAllComponents {
		return errors.New("--clean-stale requires -a (render all components)")
	}

	if options.OutputDirExplicit && !options.Force {
		return errors.New("--clean-stale with --output-dir requires --force (-f)")
	}

	return nil
}

// renderErrorMarkerContent is the static body of the RENDER_FAILED marker file.
// It must match exactly what writeRenderErrorMarker writes; --check-only relies
// on this constant to verify on-disk failure markers are byte-identical to a
// fresh run's output.
const renderErrorMarkerContent = "Rendering failed. See azldev logs for details.\n"

// diffRenderedOutput compares the rendered staging tree (expectedDir) against
// the existing on-disk output (actualDir) and returns true when they differ.
// A missing actualDir always counts as drift. Symlinks are compared by target
// (filesystems without symlink support skip that check; matches production
// render behavior).
func diffRenderedOutput(fileSystem opctx.FS, expectedDir, actualDir string) (bool, error) {
	actualExists, err := fileutils.DirExists(fileSystem, actualDir)
	if err != nil {
		return false, fmt.Errorf("checking actual output dir %#q:\n%w", actualDir, err)
	}

	if !actualExists {
		// First-time render -- every file in expectedDir is drift.
		return true, nil
	}

	result, err := dirdiff.DiffDirs(fileSystem, actualDir, expectedDir)
	if err != nil {
		return false, fmt.Errorf("diffing %#q vs %#q:\n%w", actualDir, expectedDir, err)
	}

	return len(result.Files) > 0, nil
}

// outputDriftsFromMarker reports whether outputDir's contents diverge from a
// fresh failure write -- i.e., a single RENDER_FAILED file containing the
// canonical marker body. Returns true when the on-disk state would change
// if a real failure write ran. Used by --check-only to enforce 1:1 parity:
// a component that would fail must already be marked failed on disk, with
// no extra stale output around it.
func outputDriftsFromMarker(fileSystem opctx.FS, outputDir string) (bool, error) {
	exists, err := fileutils.DirExists(fileSystem, outputDir)
	if err != nil {
		return false, fmt.Errorf("checking output dir %#q:\n%w", outputDir, err)
	}

	if !exists {
		return true, nil
	}

	entries, err := fileutils.ReadDir(fileSystem, outputDir)
	if err != nil {
		return false, fmt.Errorf("reading output dir %#q:\n%w", outputDir, err)
	}

	if len(entries) != 1 || entries[0].Name() != renderErrorMarkerFile {
		return true, nil
	}

	content, err := fileutils.ReadFile(fileSystem, filepath.Join(outputDir, renderErrorMarkerFile))
	if err != nil {
		return false, fmt.Errorf("reading marker %#q:\n%w", outputDir, err)
	}

	return string(content) != renderErrorMarkerContent, nil
}

// findOrphanRenderedDirs returns the names of rendered-spec directories under
// outputDir that don't correspond to any resolved component (or its alias).
// Names are returned as "<letter>/<name>" relative paths and sorted.
//
// Only meaningful with -a (we know the full component set) and --clean-stale
// (the only configuration where a normal run would actually remove orphans).
// Top-level non-letter entries are intentionally NOT flagged -- that matches
// the existing wipe semantics where users may store unrelated siblings in a
// custom output dir; flagging them here would surprise CI gates.
func findOrphanRenderedDirs(
	fileSystem opctx.FS, outputDir string, componentNames []string,
) ([]string, error) {
	exists, err := fileutils.DirExists(fileSystem, outputDir)
	if err != nil {
		return nil, fmt.Errorf("checking output dir %#q:\n%w", outputDir, err)
	}

	if !exists {
		return nil, nil
	}

	expectedNames := make(map[string]struct{}, len(componentNames)*2) //nolint:mnd // name + optional alias
	for _, name := range componentNames {
		expectedNames[name] = struct{}{}

		if alias := components.RenderedSpecDirAliasName(name); alias != "" {
			expectedNames[alias] = struct{}{}
		}
	}

	letterDirs, err := fileutils.ReadDir(fileSystem, outputDir)
	if err != nil {
		return nil, fmt.Errorf("reading output dir %#q:\n%w", outputDir, err)
	}

	var orphans []string

	for _, letterEntry := range letterDirs {
		if !letterEntry.IsDir() {
			continue
		}

		// Only descend into single-character prefix dirs (a/, c/, ...) --
		// matches the layout written by [components.RenderedSpecDir]. A
		// hand-placed sibling like 'tooling/' or 'overlays/' is left
		// alone; treating its children as orphans would silently delete
		// unrelated content on the next --clean-stale run.
		if len(letterEntry.Name()) != 1 {
			continue
		}

		letterPath := filepath.Join(outputDir, letterEntry.Name())

		children, readErr := fileutils.ReadDir(fileSystem, letterPath)
		if readErr != nil {
			return nil, fmt.Errorf("reading letter dir %#q:\n%w", letterPath, readErr)
		}

		for _, child := range children {
			// Component output is always a directory. Stray files (e.g. an
			// editor's swap file or a hand-placed .gitkeep) are not orphan
			// rendered-spec dirs and must not be flagged for removal.
			if !child.IsDir() {
				continue
			}

			if _, ok := expectedNames[child.Name()]; !ok {
				orphans = append(orphans, filepath.Join(letterEntry.Name(), child.Name()))
			}
		}
	}

	sort.Strings(orphans)

	return orphans, nil
}
