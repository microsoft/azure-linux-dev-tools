// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
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
}

func renderOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewRenderCmd())
}

// NewRenderCmd constructs a [cobra.Command] for the "component render" CLI subcommand.
func NewRenderCmd() *cobra.Command {
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

When rendering all components (-a), the --clean-stale flag removes rendered
directories that no longer correspond to any current component. Stale cleanup
is skipped when rendering individual components to avoid accidentally removing
directories for components not included in the filter.`,
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
		"remove stale rendered directories not matching any current component (only with -a)")

	return cmd
}

// RenderResult holds the result of rendering a single component.
type RenderResult struct {
	Component string `json:"component"       table:"Component"`
	OutputDir string `json:"outputDir"       table:"Output"`
	Status    string `json:"status"          table:"Status"`
	Error     string `json:"error,omitempty" table:"Error,omitempty"`
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

	if options.CleanStale && !options.ComponentFilter.IncludeAllComponents {
		return nil, errors.New("--clean-stale requires -a (render all components)")
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
	// mock invocation.
	stagingDir, err := fileutils.MkdirTempInTempDir(env.FS(), "azldev-render-staging-")
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
	prepared := parallelPrepare(env, componentList, stagingDir, options.OutputDir, options.Force, results)

	// ── Phase 2: Batch mock processing ──
	mockResultMap := batchMockProcess(env, mockProcessor, stagingDir, prepared)

	// ── Phase 3: Parallel finishing ──
	parallelFinish(env, prepared, mockResultMap, results, stagingDir,
		options.Force)

	// Clean up stale rendered directories when explicitly requested.
	if options.CleanStale && options.ComponentFilter.IncludeAllComponents {
		if cleanupErr := cleanupStaleRenders(env.FS(), comps, options.OutputDir); cleanupErr != nil {
			return results, fmt.Errorf("cleaning up stale rendered output:\n%w", cleanupErr)
		}
	}

	// Sort results alphabetically for consistent output.
	sortRenderResults(results)

	return results, checkRenderErrors(results, options.FailOnError)
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
	index    int
	prepared *preparedComponent
	result   *RenderResult // non-nil on error
}

// ──────────────────────────────────────────────────────────────────────────────
// Phase 1: Parallel source preparation
// ──────────────────────────────────────────────────────────────────────────────

// parallelPrepare prepares sources for all components concurrently, bounded by
// [concurrentRenderLimit]. Each component's sources are written to a subdirectory
// of stagingDir. Failed components get their result written directly; successful
// ones are returned in the prepared slice.
func parallelPrepare(
	env *azldev.Env,
	comps []components.Component,
	stagingDir string,
	outputDir string,
	allowOverwrite bool,
	results []*RenderResult,
) []*preparedComponent {
	progressEvent := env.StartEvent("Preparing component sources", "count", len(comps))
	defer progressEvent.End()

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	resultsChan := make(chan prepResult, len(comps))
	semaphore := make(chan struct{}, env.IOBoundConcurrency())

	var waitGroup sync.WaitGroup

	for compIdx, comp := range comps {
		waitGroup.Add(1)

		go func(idx int, comp components.Component) {
			defer waitGroup.Done()

			resultsChan <- prepWithSemaphore(workerEnv, semaphore, idx, comp, stagingDir, outputDir, allowOverwrite)
		}(compIdx, comp)
	}

	go func() { waitGroup.Wait(); close(resultsChan) }()

	var prepared []*preparedComponent

	total := int64(len(comps))

	var completed int64

	for prepRes := range resultsChan {
		completed++
		progressEvent.SetProgress(completed, total)

		if prepRes.result != nil {
			results[prepRes.index] = prepRes.result
		} else {
			prepared = append(prepared, prepRes.prepared)
		}
	}

	return prepared
}

// prepWithSemaphore acquires the semaphore (respecting context cancellation),
// prepares a single component's sources, and returns a prep result.
func prepWithSemaphore(
	env *azldev.Env,
	semaphore chan struct{},
	index int,
	comp components.Component,
	stagingDir string,
	outputDir string,
	allowOverwrite bool,
) prepResult {
	componentName := comp.GetName()

	// Validate component name and compute output directory.
	compOutputDir, nameErr := components.RenderedSpecDir(outputDir, componentName)
	if nameErr != nil {
		return prepResult{index: index, result: &RenderResult{
			Component: componentName,
			OutputDir: "(invalid)",
			Status:    renderStatusError,
			Error:     nameErr.Error(),
		}}
	}

	// Context-aware semaphore acquisition.
	select {
	case semaphore <- struct{}{}:
		defer func() { <-semaphore }()
	case <-env.Done():
		return prepResult{index: index, result: &RenderResult{
			Component: componentName,
			OutputDir: compOutputDir,
			Status:    renderStatusCancelled,
			Error:     "context cancelled",
		}}
	}

	prep, err := prepareComponentSources(env, comp, stagingDir)
	if err != nil {
		slog.Error("Failed to prepare component sources",
			"component", componentName, "error", err)

		// Write error marker so the failure is visible in git diff.
		if allowOverwrite {
			if removeErr := env.FS().RemoveAll(compOutputDir); removeErr != nil {
				slog.Debug("Failed to clean output before writing error marker",
					"path", compOutputDir, "error", removeErr)
			}
		}

		writeRenderErrorMarker(env.FS(), compOutputDir)

		return prepResult{index: index, result: &RenderResult{
			Component: componentName,
			OutputDir: compOutputDir,
			Status:    renderStatusError,
			Error:     err.Error(),
		}}
	}

	prep.index = index
	prep.compOutputDir = compOutputDir

	return prepResult{index: index, prepared: prep}
}

// prepareComponentSources resolves the distro, creates a source manager, and
// prepares sources (clone + overlays + synthetic git) for a single component
// into a subdirectory of stagingDir.
func prepareComponentSources(
	env *azldev.Env,
	comp components.Component,
	stagingDir string,
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
		sources.WithGitRepo(env.Config().Project.DefaultAuthorEmail),
		sources.WithSkipLookaside(),
	}

	preparer, err := sources.NewPreparer(sourceManager, env.FS(), env, env, preparerOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating source preparer for %#q:\n%w", componentName, err)
	}

	if prepErr := preparer.PrepareSources(env, comp, componentDir, true /*applyOverlays*/); prepErr != nil {
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
// rendered output for all successfully prepared components.
func parallelFinish(
	env *azldev.Env,
	prepared []*preparedComponent,
	mockResultMap map[string]*sources.ComponentMockResult,
	results []*RenderResult,
	stagingDir string,
	allowOverwrite bool,
) {
	if len(prepared) == 0 {
		return
	}

	progressEvent := env.StartEvent("Finishing rendered output", "count", len(prepared))
	defer progressEvent.End()

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	type finishResult struct {
		index  int
		result *RenderResult
	}

	resultsChan := make(chan finishResult, len(prepared))
	semaphore := make(chan struct{}, env.IOBoundConcurrency())

	var waitGroup sync.WaitGroup

	for _, prep := range prepared {
		waitGroup.Add(1)

		go func(prep *preparedComponent) {
			defer waitGroup.Done()

			result := finishOneComponent(workerEnv, env, prep, mockResultMap, semaphore, stagingDir, allowOverwrite)
			resultsChan <- finishResult{index: prep.index, result: result}
		}(prep)
	}

	go func() { waitGroup.Wait(); close(resultsChan) }()

	total := int64(len(prepared))

	var completed int64

	for fr := range resultsChan {
		completed++
		progressEvent.SetProgress(completed, total)

		results[fr.index] = fr.result
	}
}

// finishOneComponent handles the semaphore, context cancellation, and error
// wrapping for finishing a single component's render.
func finishOneComponent(
	workerEnv *azldev.Env,
	env *azldev.Env,
	prep *preparedComponent,
	mockResultMap map[string]*sources.ComponentMockResult,
	semaphore chan struct{},
	stagingDir string,
	allowOverwrite bool,
) *RenderResult {
	componentName := prep.comp.GetName()
	compOutputDir := prep.compOutputDir

	// Context-aware semaphore acquisition.
	select {
	case semaphore <- struct{}{}:
		defer func() { <-semaphore }()
	case <-workerEnv.Done():
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

	err := finishComponentRender(env, prep, mockResultMap, stagingDir, allowOverwrite)
	if err != nil {
		slog.Error("Failed to finish rendering component",
			"component", componentName, "error", err)

		result.Status = renderStatusError
		result.Error = err.Error()

		// Clean any stale good output before writing the failure marker.
		// Only allowed for managed (project-local) output directories.
		if allowOverwrite {
			if removeErr := env.FS().RemoveAll(compOutputDir); removeErr != nil {
				slog.Debug("Failed to clean output before writing error marker",
					"path", compOutputDir, "error", removeErr)
			}
		}

		writeRenderErrorMarker(env.FS(), compOutputDir)
	}

	return result
}

// finishComponentRender applies mock results, filters unreferenced files,
// removes .git, and copies rendered output for a single component.
// stagingDir is the root staging directory containing the component's subdirectory.
func finishComponentRender(
	env *azldev.Env,
	prep *preparedComponent,
	mockResultMap map[string]*sources.ComponentMockResult,
	stagingDir string,
	allowOverwrite bool,
) error {
	componentName := prep.comp.GetName()
	componentDir := filepath.Join(stagingDir, componentName)
	specPath := filepath.Join(componentDir, prep.specFilename)

	// Check mock result.
	mockResult, hasMockResult := mockResultMap[componentName]
	if !hasMockResult {
		return fmt.Errorf(
			"no mock result for %#q (batch mock processing failed; see earlier errors)", componentName)
	}

	if mockResult.Error != nil {
		return fmt.Errorf(
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
		return fmt.Errorf("filtering unreferenced files for %#q:\n%w", componentName, filterErr)
	}

	// Remove .git directory — must not appear in rendered output.
	// rpmautospec already read it during the batch mock phase.
	gitDir := filepath.Join(componentDir, ".git")
	if removeErr := env.FS().RemoveAll(gitDir); removeErr != nil {
		slog.Debug("Failed to remove .git directory", "path", gitDir, "error", removeErr)
	}

	// Copy rendered files to the component's output directory.
	if copyErr := copyRenderedOutput(env, componentDir, prep.compOutputDir, allowOverwrite); copyErr != nil {
		return copyErr
	}

	slog.Info("Rendered component", "component", componentName,
		"output", prep.compOutputDir)

	return nil
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

// cleanupStaleRenders removes rendered output directories for components that
// no longer exist in the current configuration. Only called during full renders (-a).
// The output directory uses letter-prefix subdirectories (e.g., SPECS/c/curl),
// so this walks two levels: letter directories, then component directories within each.
func cleanupStaleRenders(fs opctx.FS, currentComponents *components.ComponentSet, outputDir string) error {
	exists, existsErr := fileutils.Exists(fs, outputDir)
	if existsErr != nil {
		return fmt.Errorf("checking output directory %#q:\n%w", outputDir, existsErr)
	}

	if !exists {
		return nil
	}

	letterEntries, err := fileutils.ReadDir(fs, outputDir)
	if err != nil {
		return fmt.Errorf("reading output directory %#q:\n%w", outputDir, err)
	}

	// Build a set of current component names.
	currentNames := make(map[string]bool, currentComponents.Len())
	for _, comp := range currentComponents.Components() {
		currentNames[comp.GetName()] = true
	}

	for _, letterEntry := range letterEntries {
		if !letterEntry.IsDir() {
			continue
		}

		letterDir := filepath.Join(outputDir, letterEntry.Name())

		compEntries, readErr := fileutils.ReadDir(fs, letterDir)
		if readErr != nil {
			return fmt.Errorf("reading letter directory %#q:\n%w", letterDir, readErr)
		}

		for _, compEntry := range compEntries {
			if !compEntry.IsDir() {
				continue
			}

			if currentNames[compEntry.Name()] {
				continue
			}

			stalePath := filepath.Join(letterDir, compEntry.Name())

			slog.Info("Removing stale rendered output", "directory", stalePath)

			if removeErr := fs.RemoveAll(stalePath); removeErr != nil {
				return fmt.Errorf("removing stale directory %#q:\n%w", stalePath, removeErr)
			}
		}
	}

	return nil
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
	content := "Rendering failed. See azldev logs for details.\n"

	if writeErr := fileutils.WriteFile(fs, markerPath, []byte(content), fileperms.PublicFile); writeErr != nil {
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

// validateOutputDir rejects output directory values that could cause
// cleanupStaleRenders to delete unrelated directories.
func validateOutputDir(outputDir string) error {
	cleaned := filepath.Clean(outputDir)
	if cleaned == "." || cleaned == string(filepath.Separator) ||
		cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf(
			"output directory %#q is unsafe; use a dedicated subdirectory (e.g., ./SPECS/)", outputDir)
	}

	return nil
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
