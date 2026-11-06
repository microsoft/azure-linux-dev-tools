// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/dirdiff"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

// RenderOptions holds the options for the render command.
type RenderOptions struct {
	componentCommandOptions
	ComponentFilter          components.ComponentFilter
	OutputDir                string
	OutputDirExplicit        bool // True when --output-dir was explicitly passed on the CLI.
	FailOnError              bool
	Force                    bool
	CleanStale               bool
	CheckOnly                bool
	NoChangeRebuild          bool
	ChangelogComment         string
	NoGitCommit              bool
	AllowUncommitted         bool
	RPMDevBumpspec           bool
	changelogCommentExplicit bool
}

func renderOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewRenderCmd())
}

const renderWorktreeHelp = `Before rendering any lockless component, require a clean Git repository.
Uncommitted tracked changes and untracked files anywhere in the repository block
rendering; Git-ignored files are excluded. --allow-uncommitted bypasses this check.
--check-only and --no-git-commit do not bypass it. Locked components are unaffected.`

const renderLocalHelp = `Local lockless components copy local files except the TOML defining the spec path.
Their Release and changelog are never automatically modified, even with --rpmdev-bumpspec.
They also reject unchanged renders unless --no-change-rebuild is provided, and
automatically commit rendered output unless --no-git-commit is provided.
For local components, --changelog-comment supplies only the Git commit message;
without it, Git prompts in its own editor. Output must be inside the project repository.`

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

Render mode is selected per component by lockless-components.txt beside the
top-level azldev.toml. Each nonempty line names one component to render without
its lock file, using configured upstream commits and their TOML history instead.
All other components use lock files. A missing or empty file means all components
use lock files.

For lockless upstream components, prepare the changelog, then Release, using the
upstream spec and last committed render before writing macros, applying overlays,
and updating sources. Overlays can override the prepared release and changelog.
Compare output; unchanged renders require --no-change-rebuild to record a timestamp.
Non-autorelease specs use rpmdev-bumpspec -c with --changelog-comment, or edited text.
Autorelease skips bumpspec; without --changelog-comment, Git prompts for a message.
The first commit also includes all pending changes outside the rendered output directory.
Later commits are component-only. --no-git-commit skips staging and committing.
Output must be inside the project repository; configure Git author identity and
install rpmdev-bumpspec. Commit updated upstream-commit TOML before rendering.
--check-only stops before bumping, replacing output, or committing.
Lockless rendering never uses mock or rpmautospec process-distgit.
The spec retains its RPM macros, including %autorelease and %autochangelog.
All prepared files except .git are retained, without filtering or spectool. Host
rpmautospec generate-changelog/calculate-release are used only when needed for
release history or provenance. Provenance is captured before carryover/bumping
and reused for the macros file. Locked components keep their existing behavior.` + "\n\n" + renderLocalHelp + `

When rendering all components (-a), the --clean-stale flag prunes orphan
rendered-spec directories (per-component dirs that no longer correspond to
any component in the project config). Per-component dirs that ARE in config
are overwritten in place by the render itself; this means each render's
result table accurately reflects which components actually changed on disk.
Top-level non-component siblings (e.g. a hand-placed README.md) are
preserved. When using a custom output directory (--output-dir), --force is
required alongside --clean-stale as a safety measure. This flag is only
valid with -a.` + "\n\n" + renderWorktreeHelp,
		Example: `  # Render all components (output dir from config)
  azldev component render -a

  # Render a single component
  azldev component render -p curl

  # Render to a custom directory, allowing removal of existing rendered component directories
  azldev component render -a -o rendered/ --force`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)
			options.OutputDirExplicit = cmd.Flags().Changed("output-dir")
			options.changelogCommentExplicit = cmd.Flags().Changed("changelog-comment")
			options.SpecEditor = specEditorFromCommand(cmd)

			return RenderComponents(env, &options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
		Annotations: map[string]string{
			azldev.CommandAnnotationRootOK: "true",
		},
	}

	addComponentFilterOptions(cmd, &options.ComponentFilter)
	addRenderOptions(cmd, &options)

	return cmd
}

func addRenderOptions(cmd *cobra.Command, options *RenderOptions) {
	cmd.Flags().StringVarP(&options.OutputDir, "output-dir", "o", "",
		"output directory for rendered specs (overrides rendered-specs-dir from config)")
	_ = cmd.MarkFlagDirname("output-dir")

	cmd.Flags().BoolVar(&options.FailOnError, "fail-on-error", false,
		"exit with error if any component fails to render (useful for CI)")

	cmd.Flags().BoolVarP(&options.Force, "force", "f", false, "allow overwriting existing rendered component directories")

	cmd.Flags().BoolVar(&options.CleanStale, "clean-stale", false,
		"prune rendered-spec directories that no longer correspond to a configured "+
			"component (only with -a; requires -f with -o). Top-level non-component "+
			"siblings are preserved.")

	cmd.Flags().BoolVar(&options.CheckOnly, "check-only", false,
		"render to a staging area and compare against the existing on-disk output "+
			"without modifying the output directory. Exits 0 when nothing would change "+
			"and 1 when any component would drift. With -a + --clean-stale, also fails "+
			"on orphan rendered-spec directories. Intended for CI gates.")
	cmd.Flags().BoolVar(&options.NoChangeRebuild, "no-change-rebuild", false,
		"allow unchanged lockless renders by recording 'date -Is' output in '.no-change-rebuild'")
	cmd.Flags().StringVar(&options.ChangelogComment, "changelog-comment", "",
		"Git commit message for lockless components and verbatim rpmdev-bumpspec -c comment "+
			"for upstream non-autorelease specs (if omitted, prompt in an editor)")
	cmd.Flags().BoolVar(&options.NoGitCommit, "no-git-commit", false,
		"leave lockless rendered output uncommitted without staging it")
	cmd.Flags().BoolVar(&options.AllowUncommitted, "allow-uncommitted", false,
		"allow lockless rendering with uncommitted or untracked files in the project repository")
	addRPMDevBumpspecFlag(cmd, &options.RPMDevBumpspec)

	// --check-only is a read-only diff against on-disk state; --fail-on-error
	// is the loud-failure-per-run knob. Combining them is semantically
	// muddled (CI would fail on stale failures even when on-disk markers
	// already record them) and forcing a choice keeps the contract crisp.
	cmd.MarkFlagsMutuallyExclusive("fail-on-error", "check-only")
}

// RenderResult holds the result of rendering a single component.
type RenderResult struct {
	Component string `json:"component"       table:"Component"`
	OutputDir string `json:"outputDir"       table:"Output"`
	Status    string `json:"status"          table:"Status"`
	Error     string `json:"error,omitempty" table:"Error,omitempty"`
	Changed   bool   `json:"changed"         table:"Changed"`
	renderErr error  `json:"-"               table:"-"`
	lockless  bool   `json:"-"               table:"-"`
}

// Render status constants.
const (
	renderStatusOK        = "ok"
	renderStatusError     = "error"
	renderStatusCancelled = "cancelled"
)

// RenderComponents renders the post-overlay spec and sidecar files for each
// selected component into the output directory. Processing is done in four phases:
//  1. Parallel source preparation (clone, overlay, synthetic git)
//  2. Batch mock processing for locked components only
//  3. Parallel finishing (filter locked files, compare without '.git', move output)
//  4. Serial project commits for lockless upstream component output
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

	env = env.WithMockRootComponents(comps.Names()...)

	componentList := comps.Components()

	if err := validateRenderComponents(env, componentList, options.AllowUncommitted); err != nil {
		return nil, err
	}

	mockProcessor, err := renderMockProcessor(env, componentList)
	if err != nil {
		return nil, err
	}

	defer destroyMockProcessor(env, mockProcessor)

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

	results := make([]*RenderResult, len(componentList))

	// ── Phase 1: Parallel source preparation ──
	prepared := parallelPrepare(env, mockProcessor, componentList, stagingDir, options, results)

	// ── Phase 2: Mode-specific source processing ──
	mockResultMap := processRenderSources(env, mockProcessor, stagingDir, prepared)

	// Prune orphan component dirs (components removed from config) when
	// --clean-stale is set. Per-component output dirs that match the resolved
	// component set are NOT touched here; phase 3 will RemoveAll+rewrite each
	// of them via moveRenderedOutput. This keeps the diff against existing
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
		options.Force, options.CheckOnly, options.NoChangeRebuild)

	// Only locked failures write markers; lockless failures preserve output.
	writeFailureMarkers(env.FS(), results, options.Force, options.CheckOnly)

	commitLocklessRenders(env, prepared, results, options.CheckOnly)

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

// checkRenderErrors reports no-change refusals regardless of failOnError, and
// other error or cancelled results only when failOnError is set.
func checkRenderErrors(results []*RenderResult, failOnError bool) error {
	var (
		errCount, cancelledCount int
		noChangeErrors           []error
	)

	for _, result := range results {
		if result == nil {
			continue
		}

		if errors.Is(result.renderErr, errNoRenderChanges) {
			noChangeErrors = append(noChangeErrors, fmt.Errorf("%#q: %w", result.Component, result.renderErr))
		}

		switch result.Status {
		case renderStatusError:
			errCount++
		case renderStatusCancelled:
			cancelledCount++
		}
	}

	failCount := errCount + cancelledCount
	if failCount == 0 {
		return nil
	}

	slog.Error("Some components failed to render",
		"errorCount", errCount, "cancelledCount", cancelledCount)

	if len(noChangeErrors) > 0 {
		return errors.Join(noChangeErrors...)
	}

	if failOnError {
		return fmt.Errorf("%d component(s) failed to render", failCount)
	}

	return nil
}

// preparedComponent holds the intermediate state after source preparation,
// before mock processing.
type preparedComponent struct {
	index         int
	comp          components.Component
	specFilename  string // e.g., "curl.spec"
	compOutputDir string // validated output path computed in phase 1
	update        *locklessRenderUpdate
	lockless      bool
	outputTouched bool
	outputExisted bool
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
	mockProcessor *sources.MockProcessor,
	comps []components.Component,
	stagingDir string,
	options *RenderOptions,
	results []*RenderResult,
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
			return prepareOneComponent(
				workerEnv, mockProcessor, comp, stagingDir, options.OutputDir,
				options.specEditorMode(), options.RPMDevBumpspec,
				env.ComponentWithoutLockfile(comp.GetName()))
		},
	)

	prepared := make([]*preparedComponent, 0, len(comps))

	for idx, result := range parmapResults {
		switch {
		case result.Cancelled:
			// Worker never started — ctx ended before parmap reached it.
			compName := comps[idx].GetName()

			compOutputDir, nameErr := components.RenderedSpecDir(options.OutputDir, compName)
			if nameErr != nil {
				compOutputDir = "(invalid)"
			}

			results[idx] = &RenderResult{
				Component: compName,
				OutputDir: compOutputDir,
				Status:    renderStatusCancelled,
				Error:     "context cancelled",
				lockless:  env.ComponentWithoutLockfile(compName),
			}
		case result.Value.result != nil:
			results[idx] = result.Value.result
		default:
			result.Value.prepared.index = idx
			if update := result.Value.prepared.update; update != nil {
				update.subject = options.ChangelogComment
				update.subjectProvided = options.changelogCommentExplicit || options.ChangelogComment != ""
				update.noGitCommit = options.NoGitCommit
			}

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
	mockProcessor *sources.MockProcessor,
	comp components.Component,
	stagingDir string,
	outputDir string,
	specEditor spec.EditorMode,
	rpmDevBumpspec bool,
	lockless bool,
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
			lockless:  lockless,
		}}
	}

	prep, err := prepareComponentSources(env, mockProcessor, comp, stagingDir, compOutputDir,
		specEditor, rpmDevBumpspec, lockless)
	if err != nil {
		slog.Error("Failed to prepare component sources",
			"component", componentName, "error", err)

		return prepResult{result: &RenderResult{
			Component: componentName,
			OutputDir: compOutputDir,
			Status:    renderStatusError,
			Error:     err.Error(),
			lockless:  lockless,
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
	mockProcessor *sources.MockProcessor,
	comp components.Component,
	stagingDir string,
	compOutputDir string,
	specEditor spec.EditorMode,
	rpmDevBumpspec bool,
	lockless bool,
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
	sourceManager, err := sourceproviders.NewSourceManager(env.WithMockRootComponents(componentName), distro)
	if err != nil {
		return nil, fmt.Errorf("creating source manager for %#q:\n%w", componentName, err)
	}

	// Component sources go into stagingDir/<componentName>/.
	componentDir := filepath.Join(stagingDir, componentName)

	if mkdirErr := fileutils.MkdirAll(env.FS(), componentDir); mkdirErr != nil {
		return nil, fmt.Errorf("creating component staging directory:\n%w", mkdirErr)
	}

	// Prepare sources with overlays, skipping lookaside downloads.
	// Preserve upstream .git for release queries. Only locked rendering creates
	// synthetic history for rpmautospec expansion.
	// WithSkipLookaside avoids expensive tarball downloads — only spec +
	// sidecar files are needed for rendering.
	preparerOpts := append(gitRepoPreparerOptionsForMode(env, distro, lockless),
		sources.WithSkipLookaside(),
		sources.WithUpstreamProvenance(sources.FedoraDistTag(distro.Ref.Name, distro.Version.ReleaseVer)),
	)
	if lockless {
		preparerOpts = append(preparerOpts, sources.WithoutSyntheticHistory(), sources.WithSkipLocalSpecConfig(),
			sources.WithAutoreleaseResolver(hostRenderProcessor{env: env}))
	} else {
		preparerOpts = append(preparerOpts, sources.WithMockProcessor(mockProcessor))
	}

	var update *locklessRenderUpdate

	if lockless {
		preparerOpts = append(preparerOpts, sources.WithFetchedSources(
			func(_ context.Context, fetched components.Component, dir string) error {
				var prepErr error
				//nolint:contextcheck // The callback context is the captured preparation env.
				update, prepErr = prepareFetchedLocklessRender(env, fetched, dir, compOutputDir, specEditor)

				return prepErr
			}))
	}

	if rpmDevBumpspec && (!lockless || comp.GetConfig().Spec.SourceType != projectconfig.SpecSourceTypeLocal) {
		preparerOpts = append(preparerOpts, sources.WithRPMDevBumpspec(env, env.WorkDir(), ""))
	}

	preparer, err := newSourcePreparer(sourceManager, env.FS(), env, env, append(
		preparerOpts,
		sources.WithSpecEditor(specEditor),
	)...)
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
		update:       update,
		lockless:     lockless,
	}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Phase 2: Batch mock processing
// ──────────────────────────────────────────────────────────────────────────────

// batchMockProcess runs rpmautospec and spectool for all prepared components in
// a single mock chroot invocation. Returns a map from component name to result.
//
// NOTE: All locked components share one mock chroot initialized from the project's
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

// parallelFinish applies source-query results, compares without '.git', then
// bumps and moves rendered output. Check-only stops after comparison.
func parallelFinish(
	env *azldev.Env,
	prepared []*preparedComponent,
	mockResultMap map[string]*sources.ComponentMockResult,
	results []*RenderResult,
	stagingDir string,
	allowOverwrite bool,
	checkOnly bool,
	noChangeRebuild bool,
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
		renderFinishConcurrency(env, prepared, checkOnly),
		prepared,
		func(done, _ int) { progressEvent.SetProgress(int64(done), total) },
		func(_ context.Context, prep *preparedComponent) *RenderResult {
			//nolint:contextcheck // workerEnv is the captured context supplied to parmap.
			return finishOneComponent(workerEnv, prep, mockResultMap, stagingDir, allowOverwrite, checkOnly, noChangeRebuild)
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
				lockless:  prep.lockless,
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
	prep *preparedComponent,
	mockResultMap map[string]*sources.ComponentMockResult,
	stagingDir string,
	allowOverwrite bool,
	checkOnly bool,
	noChangeRebuild bool,
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
			lockless:  prep.lockless,
		}
	}

	result := &RenderResult{
		Component: componentName,
		OutputDir: compOutputDir,
		Status:    renderStatusOK,
		lockless:  prep.lockless,
	}

	drifted, err := finishComponentRender(
		workerEnv, prep, mockResultMap, stagingDir, allowOverwrite, checkOnly, noChangeRebuild)
	if err != nil {
		slog.Error("Failed to finish rendering component",
			"component", componentName, "error", err)

		result.Status = renderStatusError
		result.Error = err.Error()
		result.renderErr = err
	}

	result.Changed = drifted

	return result
}

// finishComponentRender applies mock results, filters locked components' files,
// diffs the staging tree against the existing output while ignoring the root '.git',
// and (unless checkOnly is set) bumps lockless upstream specs and moves output.
// stagingDir is the root staging directory containing the component's subdirectory.
//
// Returns changed=true when the prepared tree differs from the existing output
// (or no existing output is present), or a subsequent release bump changes the spec.
// Check-only compares before bumping and does not replace output.
func finishComponentRender(
	env *azldev.Env,
	prep *preparedComponent,
	mockResultMap map[string]*sources.ComponentMockResult,
	stagingDir string,
	allowOverwrite bool,
	checkOnly bool,
	noChangeRebuild bool,
) (bool, error) {
	componentName := prep.comp.GetName()
	componentDir := filepath.Join(stagingDir, componentName)
	specPath := filepath.Join(componentDir, prep.specFilename)

	// Check the source-processing result.
	mockResult, hasMockResult := mockResultMap[componentName]
	if !hasMockResult {
		return false, fmt.Errorf(
			"no mock result for %#q (batch mock processing failed; see earlier errors)", componentName)
	}

	if mockResult.Error != nil {
		return false, fmt.Errorf(
			"source processing failed for %#q:\n%w", componentName, mockResult.Error)
	}

	// Filter locked components using the mock spectool result.
	// Skip filtering when:
	// 1. The component is lockless.
	// 2. The component config explicitly opts out via 'skip-file-filter'.
	// 3. spectool output contains unexpanded RPM macros (%{...}), indicating
	//    that the reported filenames don't match the real files on disk.
	if prep.lockless {
		slog.Debug("Preserving all files for lockless render", "component", componentName)
	} else if prep.comp.GetConfig().Render.SkipFileFilter {
		slog.Info("Skipping file filter ('skip-file-filter' is set)", "component", componentName)
	} else if macro := findUnexpandedMacro(mockResult.SpecFiles); macro != "" {
		slog.Info("Skipping file filter (spectool output contains unexpanded macros)",
			"component", componentName, "example", macro)
	} else if filterErr := removeUnreferencedFiles(
		env.FS(), componentDir, specPath, mockResult.SpecFiles, componentName,
	); filterErr != nil {
		return false, fmt.Errorf("filtering unreferenced files for %#q:\n%w", componentName, filterErr)
	}

	// Compare before the final bump so '--check-only' does not manufacture release drift.
	changed, diffErr := diffRenderedOutput(env.FS(), componentDir, prep.compOutputDir)
	if diffErr != nil {
		return false, fmt.Errorf("comparing rendered output for %#q:\n%w", componentName, diffErr)
	}

	if checkOnly {
		return changed, nil
	}

	if prep.update != nil {
		bumped, err := finishLocklessSpec(env, specPath, prep.update, changed, noChangeRebuild)
		if err != nil {
			return changed, err
		}

		changed = changed || bumped
	}

	// Move rendered files to the component's output directory.
	if moveErr := moveRenderedOutput(env, componentDir, prep, allowOverwrite); moveErr != nil {
		return changed, moveErr
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
	if !prep.lockless {
		writeRenderedAlias(env, prep)
	}

	slog.Info("Rendered component", "component", componentName,
		"output", prep.compOutputDir)

	return changed, nil
}

func writeRenderedAlias(env *azldev.Env, prep *preparedComponent) {
	name := prep.comp.GetName()
	if err := writeAliasSymlink(env.FS(), prep.compOutputDir, name); err != nil {
		slog.Warn("Failed to create rendered-spec alias symlink; downstream build steps"+
			" that consume the percent-encoded path may fail to locate this component",
			"component", name, "error", err)
	}
}

// writeAliasSymlink creates a sibling symlink alongside componentOutputDir at the
// URL-encoded form of componentName, pointing back at the real directory with a
// relative target.
//
// No-ops when no encoding is needed (plain ASCII names) or when the underlying
// filesystem doesn't support symlinks (e.g., in-memory test FS).
//
// Removes any existing entry at the alias path, including nonempty directories,
// before creating the symlink.
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

	if removeErr := fileSystem.RemoveAll(aliasPath); removeErr != nil {
		return fmt.Errorf("removing existing alias path %#q:\n%w", aliasPath, removeErr)
	}

	// Use a relative target so the rendered tree stays portable.
	target := filepath.Base(componentOutputDir)
	if symErr := linker.SymlinkIfPossible(target, aliasPath); symErr != nil {
		return fmt.Errorf("creating alias symlink %#q -> %#q:\n%w", aliasPath, target, symErr)
	}

	return nil
}

// moveRenderedOutput recreates the output directory and moves all prepared entries
// except '.git' into it. Existing output requires allowOverwrite.
func moveRenderedOutput(env *azldev.Env, tempDir string, prep *preparedComponent, allowOverwrite bool) error {
	componentOutputDir := prep.compOutputDir

	if env.DryRun() {
		slog.Info("Dry run; would replace rendered output", "source", tempDir, "dest", componentOutputDir)

		return nil
	}

	exists, existsErr := fileutils.DirExists(env.FS(), componentOutputDir)
	if existsErr != nil {
		return fmt.Errorf("checking output directory %#q:\n%w", componentOutputDir, existsErr)
	}

	if exists && !allowOverwrite {
		return fmt.Errorf(
			"output directory %#q already exists; use --force to overwrite",
			componentOutputDir)
	}

	prep.outputExisted = exists
	prep.outputTouched = true

	if exists {
		// Clean up existing rendered output for this component.
		if removeErr := env.FS().RemoveAll(componentOutputDir); removeErr != nil {
			return fmt.Errorf("cleaning output directory %#q:\n%w", componentOutputDir, removeErr)
		}
	}

	if mkdirErr := fileutils.MkdirAll(env.FS(), componentOutputDir); mkdirErr != nil {
		return fmt.Errorf("creating output directory %#q:\n%w", componentOutputDir, mkdirErr)
	}

	entries, err := fileutils.ReadDir(env.FS(), tempDir)
	if err != nil {
		return fmt.Errorf("reading prepared output directory %#q:\n%w", tempDir, err)
	}

	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}

		sourcePath := filepath.Join(tempDir, entry.Name())

		destPath := filepath.Join(componentOutputDir, entry.Name())
		if moveErr := env.FS().Rename(sourcePath, destPath); moveErr != nil {
			return fmt.Errorf("moving rendered entry %#q to %#q:\n%w", sourcePath, destPath, moveErr)
		}
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
// overwrite them via moveRenderedOutput, and leaving the prior content here
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

// writeFailureMarkers writes a RENDER_FAILED marker for errored locked components.
// Lockless failures preserve output, and fail check-only without requiring markers.
// When allowOverwrite is set, pre-existing locked output is removed first.
//
// Cancelled components are intentionally skipped — a Ctrl-C is not a render
// failure, just an incomplete run, and silently planting markers under those
// circumstances would lie in git diff.
// No-change refusals also leave the existing successful output intact.
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
		if result == nil || result.Status != renderStatusError || errors.Is(result.renderErr, errNoRenderChanges) {
			continue
		}

		if result.lockless {
			if checkOnly {
				result.Changed = true
			}

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
// render behavior). The root '.git' entry is ignored without traversing it.
// The root '.no-change-rebuild' marker is also ignored on both sides.
func diffRenderedOutput(fileSystem opctx.FS, expectedDir, actualDir string) (bool, error) {
	actualExists, err := fileutils.DirExists(fileSystem, actualDir)
	if err != nil {
		return false, fmt.Errorf("checking actual output dir %#q:\n%w", actualDir, err)
	}

	if !actualExists {
		// First-time render -- every file in expectedDir is drift.
		return true, nil
	}

	result, err := dirdiff.DiffDirs(fileSystem, actualDir, expectedDir,
		dirdiff.WithIgnoredPaths(".git", noChangeRebuildMarker))
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
