// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
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
// defaultRenderOutputDir is the default output directory for rendered specs.
const defaultRenderOutputDir = "SPECS"

// RenderOptions holds the options for the render command.
type RenderOptions struct {
	ComponentFilter components.ComponentFilter
	OutputDir       string
	FailOnError     bool
	CleanStale      bool
}

func renderOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewRenderCmd())
}

// NewRenderCmd constructs a [cobra.Command] for the "component render" CLI subcommand.
func NewRenderCmd() *cobra.Command {
	var options RenderOptions

	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render post-overlay specs and sidecar files to a checked-in directory",
		Long: `Render the final spec and sidecar files for components after applying all
configured overlays. The output is written to a directory (default: SPECS/)
as generated artifacts intended for check-in.

Unlike prepare-sources, render skips downloading source tarballs from the
lookaside cache — only spec files, patches, scripts, and other git-tracked
sidecar files are included. Multiple components can be rendered at once.`,
		Example: `  # Render all components
  azldev component render -a

  # Render a single component
  azldev component render -p curl

  # Render to a custom directory
  azldev component render -a -o rendered/`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

			return RenderComponents(env, &options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
		Annotations: map[string]string{
			azldev.CommandAnnotationRootOK: "true",
		},
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().StringVarP(&options.OutputDir, "output-dir", "o", defaultRenderOutputDir,
		"output directory for rendered specs")
	_ = cmd.MarkFlagDirname("output-dir")

	cmd.Flags().BoolVar(&options.FailOnError, "fail-on-error", false,
		"exit with error if any component fails to render (useful for CI)")

	cmd.Flags().BoolVar(&options.CleanStale, "clean-stale", false,
		"remove stale rendered directories not matching any current component "+
			"(required with -a and custom --output-dir)")

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
	renderStatusWarning   = "warning"
	renderStatusError     = "error"
	renderStatusCancelled = "cancelled"
)

// concurrentRenderLimit returns the number of concurrent component renders to allow.
// Each render involves a git clone + overlay application + rpmautospec + spectool,
// so this bounds both network and I/O load. Uses 2x CPU count since renders are I/O-bound.
func concurrentRenderLimit() int {
	return max(1, 2*runtime.NumCPU()) //nolint:mnd // 2x CPU empirically chosen via benchmarking
}

// RenderComponents renders the post-overlay spec and sidecar files for each
// selected component into the output directory.
func RenderComponents(env *azldev.Env, options *RenderOptions) ([]*RenderResult, error) {
	if err := validateOutputDir(options.OutputDir); err != nil {
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

	// When rendering all components with a custom output directory, require
	// --clean-stale so the user acknowledges that stale subdirectories will be
	// deleted. For the default directory this is always safe.
	if options.ComponentFilter.IncludeAllComponents &&
		options.OutputDir != defaultRenderOutputDir &&
		!options.CleanStale {
		return nil, fmt.Errorf(
			"rendering all components to a custom directory %#q requires --clean-stale "+
				"to confirm removal of stale subdirectories", options.OutputDir)
	}

	results, errCount := renderComponentsParallel(env, comps.Components(), options.OutputDir)

	// Clean up stale rendered directories when rendering all components.
	if options.ComponentFilter.IncludeAllComponents {
		if cleanupErr := cleanupStaleRenders(env.FS(), comps, options.OutputDir); cleanupErr != nil {
			return results, fmt.Errorf("cleaning up stale rendered output:\n%w", cleanupErr)
		}
	}

	if errCount > 0 {
		slog.Error("Some components failed to render", "errorCount", errCount)

		if options.FailOnError {
			return results, fmt.Errorf("%d component(s) failed to render", errCount)
		}
	}

	// When FailOnError is not set, intentionally return nil error even when
	// some components fail. Returning an error would suppress the results
	// table (runFuncInternal skips reportResults on error), hiding the status
	// of all ~7k components. Individual failures are visible in the table's
	// Status/Error columns and via RENDER_FAILED marker files.
	return results, nil
}

// indexedResult pairs a render result with its original index for ordered collection.
type indexedResult struct {
	index  int
	result *RenderResult
}

// renderComponentsParallel renders all components concurrently, bounded by
// [concurrentRenderLimit]. Returns collected results and the error count.
func renderComponentsParallel(
	env *azldev.Env,
	comps []components.Component,
	outputDir string,
) ([]*RenderResult, int) {
	progressEvent := env.StartEvent("Rendering components", "count", len(comps))
	defer progressEvent.End()

	// Create a cancellable child env so ctrl+c stops remaining goroutines.
	workerEnv, cancel := env.WithCancel()
	defer cancel()

	resultsChan := make(chan indexedResult, len(comps))
	semaphore := make(chan struct{}, concurrentRenderLimit())

	var waitGroup sync.WaitGroup

	for compIdx, comp := range comps {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			resultsChan <- renderWithSemaphore(workerEnv, semaphore, compIdx, comp, outputDir)
		}()
	}

	// Close channel when all goroutines complete.
	go func() { waitGroup.Wait(); close(resultsChan) }()

	// Collect results in order.
	results := make([]*RenderResult, len(comps))
	total := int64(len(comps))

	var (
		completed int64
		errCount  int
	)

	for indexed := range resultsChan {
		results[indexed.index] = indexed.result
		completed++
		progressEvent.SetProgress(completed, total)

		if indexed.result.Status == renderStatusError {
			errCount++
		}
	}

	return results, errCount
}

// renderWithSemaphore acquires the semaphore (respecting context cancellation),
// renders a single component, and returns an indexed result.
func renderWithSemaphore(
	env *azldev.Env,
	semaphore chan struct{},
	index int,
	comp components.Component,
	outputDir string,
) indexedResult {
	// Validate component name before constructing any filesystem paths to prevent
	// path traversal in the error-handling path (RemoveAll, writeRenderErrorMarker).
	if err := validateComponentName(comp.GetName()); err != nil {
		return indexedResult{
			index: index,
			result: &RenderResult{
				Component: comp.GetName(),
				OutputDir: "(invalid)",
				Status:    renderStatusError,
				Error:     err.Error(),
			},
		}
	}

	compOutputDir := filepath.Join(outputDir, comp.GetName())

	// Context-aware semaphore acquisition.
	select {
	case semaphore <- struct{}{}:
		defer func() { <-semaphore }()
	case <-env.Done():
		return indexedResult{
			index: index,
			result: &RenderResult{
				Component: comp.GetName(),
				OutputDir: compOutputDir,
				Status:    renderStatusCancelled,
				Error:     "context cancelled",
			},
		}
	}

	result := &RenderResult{
		Component: comp.GetName(),
		OutputDir: compOutputDir,
		Status:    renderStatusOK,
	}

	renderStatus, renderErr := renderSingleComponent(env, comp, outputDir)
	if renderErr != nil {
		slog.Error("Failed to render component",
			"component", comp.GetName(),
			"error", renderErr,
		)

		result.Status = renderStatusError
		result.Error = renderErr.Error()

		// Clean any stale good output before writing the failure marker,
		// so we don't end up with a marker alongside outdated rendered files.
		if removeErr := env.FS().RemoveAll(compOutputDir); removeErr != nil {
			slog.Debug("Failed to clean output before writing error marker",
				"path", compOutputDir, "error", removeErr)
		}

		// Write a marker file so the failure is visible in git diff.
		writeRenderErrorMarker(env.FS(), compOutputDir)
	} else if renderStatus == renderStatusWarning {
		result.Status = renderStatusWarning
	}

	return indexedResult{index: index, result: result}
}

// renderSingleComponent renders one component's spec + sidecar files to the output directory.
// Returns a status string ("ok" or "warning") and an error if the render failed entirely.
func renderSingleComponent(
	env *azldev.Env,
	comp components.Component,
	baseOutputDir string,
) (string, error) {
	componentName := comp.GetName()

	if err := validateComponentName(componentName); err != nil {
		return "", err
	}

	event := env.StartEvent("Rendering component", "component", componentName)
	defer event.End()

	status := renderStatusOK

	// Resolve the effective distro for this component.
	distro, err := sourceproviders.ResolveDistro(env, comp)
	if err != nil {
		return "", fmt.Errorf("resolving distro for %#q:\n%w", componentName, err)
	}

	// Create source manager.
	sourceManager, err := sourceproviders.NewSourceManager(env, distro)
	if err != nil {
		return "", fmt.Errorf("creating source manager for %#q:\n%w", componentName, err)
	}

	// Create a temp directory for source preparation.
	tempDir, err := fileutils.MkdirTempInTempDir(env.FS(), "azldev-render-"+componentName+"-")
	if err != nil {
		return "", fmt.Errorf("creating temp directory:\n%w", err)
	}

	defer func() {
		if removeErr := env.FS().RemoveAll(tempDir); removeErr != nil {
			slog.Debug("Failed to clean up render temp directory", "path", tempDir, "error", removeErr)
		}
	}()

	// Prepare sources with overlays, skipping lookaside downloads.
	// WithGitRepo preserves upstream .git and creates synthetic history so
	// rpmautospec can expand %autorelease and %autochangelog correctly.
	// WithSkipLookaside avoids expensive tarball downloads — only spec +
	// sidecar files are needed for rendering.
	preparerOpts := []sources.PreparerOption{
		sources.WithGitRepo(),
		sources.WithSkipLookaside(),
	}

	preparer, err := sources.NewPreparer(sourceManager, env.FS(), env, env, preparerOpts...)
	if err != nil {
		return "", fmt.Errorf("creating source preparer for %#q:\n%w", componentName, err)
	}

	err = preparer.PrepareSources(env, comp, tempDir, true /*applyOverlays*/)
	if err != nil {
		return "", fmt.Errorf("preparing sources for %#q:\n%w", componentName, err)
	}

	// Post-process: expand macros and filter cruft files.
	// .git must stay until after rpmautospec runs (it reads git history),
	// so postProcessRenderedSources handles rpmautospec first, then filters.
	postStatus := postProcessRenderedSources(env, tempDir, componentName)
	if postStatus != renderStatusOK {
		status = postStatus
	}

	// Remove .git directory unconditionally — it must not appear in rendered output.
	// This runs after postProcessRenderedSources so rpmautospec can read git history.
	gitDir := filepath.Join(tempDir, ".git")
	if removeErr := env.FS().RemoveAll(gitDir); removeErr != nil {
		slog.Debug("Failed to remove .git directory", "path", gitDir, "error", removeErr)
	}

	// Copy rendered files to the component's output directory.
	if copyErr := copyRenderedOutput(env, tempDir, baseOutputDir, componentName); copyErr != nil {
		return "", copyErr
	}

	slog.Info("Rendered component", "component", componentName, "output", filepath.Join(baseOutputDir, componentName))

	return status, nil
}

// postProcessRenderedSources runs rpmautospec macro expansion and filters out
// non-spec files from the rendered output.
// Returns the render status (ok or warning).
// Note: .git removal is handled by the caller after this function returns.
func postProcessRenderedSources(env *azldev.Env, tempDir, componentName string) string {
	status := renderStatusOK

	// Expand %autorelease and %autochangelog macros via rpmautospec.
	// This must run BEFORE .git removal since rpmautospec reads git history.
	specPath, specErr := findSpecFile(env.FS(), tempDir, componentName)
	if specErr != nil {
		slog.Warn("Could not find spec file for post-processing",
			"component", componentName,
			"error", specErr,
		)

		return renderStatusWarning
	}

	if autospecErr := sources.ProcessAutospecMacros(env, env, specPath, specPath); autospecErr != nil {
		slog.Warn("rpmautospec expansion failed; spec will contain unexpanded macros",
			"component", componentName,
			"error", autospecErr,
		)

		status = renderStatusWarning
	}

	// Filter out files not referenced by the spec (Fedora test infra, metadata, etc.).
	// Uses spectool to determine which Source/Patch files the spec references.
	// If spectool fails (undefined macros — same ~430 packages rpmautospec fails on),
	// skip filtering and copy everything.
	if filterErr := filterRenderedFiles(env, tempDir, specPath, componentName); filterErr != nil {
		slog.Warn("Skipping file filtering; spectool could not parse spec",
			"component", componentName,
			"error", filterErr,
		)

		status = renderStatusWarning
	}

	return status
}

// copyRenderedOutput copies the rendered files from tempDir to the component's output directory,
// cleaning up any existing output first.
func copyRenderedOutput(env *azldev.Env, tempDir, baseOutputDir, componentName string) error {
	componentOutputDir := filepath.Join(baseOutputDir, componentName)

	// Clean up any existing rendered output for this component.
	if removeErr := env.FS().RemoveAll(componentOutputDir); removeErr != nil {
		return fmt.Errorf("cleaning output directory %#q:\n%w", componentOutputDir, removeErr)
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
		return fmt.Errorf("copying rendered files for %#q:\n%w", componentName, copyErr)
	}

	return nil
}

// filterRenderedFiles removes files from the temp directory that aren't referenced
// by the spec. Uses spectool to determine the keep-list (Source + Patch basenames).
// The spec file itself is always kept.
//
// Returns an error if spectool fails (e.g., undefined macros), signaling the caller
// should skip filtering.
func filterRenderedFiles(env *azldev.Env, tempDir, specPath, componentName string) error {
	specFiles, err := sources.ListSpecFiles(env, env, specPath)
	if err != nil {
		return fmt.Errorf("listing spec files for %#q:\n%w", componentName, err)
	}

	return removeUnreferencedFiles(env.FS(), tempDir, specPath, specFiles, componentName)
}

// removeUnreferencedFiles removes files from the directory that aren't in the keep-list.
// The keep-list is built from the spec file, the "sources" directory, and all
// source/patch filenames provided. For paths with subdirectories (e.g., "patches/fix.patch"),
// the top-level directory ("patches") is kept.
func removeUnreferencedFiles(fs opctx.FS, tempDir, specPath string, specFiles []string, componentName string) error {
	keepSet := make(map[string]bool, len(specFiles))
	keepSet[filepath.Base(specPath)] = true
	keepSet["sources"] = true

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

		slog.Debug("Filtering out non-spec file",
			"component", componentName,
			"file", entry.Name(),
		)

		if removeErr := fs.RemoveAll(removePath); removeErr != nil {
			slog.Warn("Failed to remove filtered file",
				"component", componentName,
				"file", entry.Name(),
				"error", removeErr,
			)
		}
	}

	return nil
}

// findSpecFile locates the spec file for a component in the given directory.
func findSpecFile(fs opctx.FS, dir, componentName string) (string, error) {
	// Try the expected name first.
	specPath := filepath.Join(dir, componentName+".spec")

	exists, err := fileutils.Exists(fs, specPath)
	if err != nil {
		return "", fmt.Errorf("checking spec file %#q:\n%w", specPath, err)
	}

	if exists {
		return specPath, nil
	}

	// Fall back to searching for any .spec file.
	entries, err := fileutils.ReadDir(fs, dir)
	if err != nil {
		return "", fmt.Errorf("reading directory %#q:\n%w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".spec" {
			return filepath.Join(dir, entry.Name()), nil
		}
	}

	return "", fmt.Errorf("no spec file found in %#q for component %#q", dir, componentName)
}

// cleanupStaleRenders removes rendered output directories for components that
// no longer exist in the current configuration. Only called during full renders (-a).
func cleanupStaleRenders(fs opctx.FS, currentComponents *components.ComponentSet, outputDir string) error {
	exists, existsErr := fileutils.Exists(fs, outputDir)
	if existsErr != nil {
		return fmt.Errorf("checking output directory %#q:\n%w", outputDir, existsErr)
	}

	if !exists {
		return nil
	}

	entries, err := fileutils.ReadDir(fs, outputDir)
	if err != nil {
		return fmt.Errorf("reading output directory %#q:\n%w", outputDir, err)
	}

	// Build a set of current component names.
	currentNames := make(map[string]bool, currentComponents.Len())
	for _, comp := range currentComponents.Components() {
		currentNames[comp.GetName()] = true
	}

	for _, entry := range entries {
		// Skip non-directories and known non-component files.
		if !entry.IsDir() {
			continue
		}

		if currentNames[entry.Name()] {
			continue
		}

		stalePath := filepath.Join(outputDir, entry.Name())

		slog.Info("Removing stale rendered output", "directory", stalePath)

		if removeErr := fs.RemoveAll(stalePath); removeErr != nil {
			return fmt.Errorf("removing stale directory %#q:\n%w", stalePath, removeErr)
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

// validateOutputDir rejects output directory values that could cause
// cleanupStaleRenders to delete unrelated directories.
func validateOutputDir(outputDir string) error {
	cleaned := filepath.Clean(outputDir)
	if cleaned == "." || cleaned == string(filepath.Separator) || strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf(
			"output directory %#q is unsafe; use a dedicated subdirectory (e.g., ./SPECS/)", outputDir)
	}

	return nil
}

// validateComponentName rejects component names that could cause path traversal
// when used as directory names in filepath.Join.
func validateComponentName(name string) error {
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return fmt.Errorf(
			"component name %#q contains path separators or traversal sequences", name)
	}

	return nil
}
