// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/evr"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
)

// rpm.labelCompare results, named so the verdict logic reads in terms of the candidate EVR.
const (
	checkEVRCompareCurrentIsNewer = -1
	checkEVRCompareEqual          = 0
	checkEVRCompareCurrentIsOlder = 1
)

// CheckOptions holds options for the component check command.
type CheckOptions struct {
	// ComponentFilter determines which current components participate in the
	// check. With '--all-components', historical lock-only components are also
	// considered so deletions can be reported correctly.
	ComponentFilter components.ComponentFilter
	// EVR selects SRPM EVR validation. It is a mode flag so future component
	// checks can share the same command without adding more top-level
	// subcommands.
	EVR bool
	// From is the previous git ref whose rendered specs form the EVR baseline.
	// It is optional: without it only the release-only rebuild simulation runs.
	From string
	// To is the git ref to validate. It defaults to HEAD and requires '--from'.
	To string
}

func checkOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewCheckCmd())
}

// NewCheckCmd constructs a [cobra.Command] for the "component check" subcommand.
func NewCheckCmd() *cobra.Command {
	options := &CheckOptions{}

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run component validation checks",
		Long: `Run selected component validation checks.

The '--evr' mode proves that rendered specs still produce usable SRPM EVRs in
the target mock chroot. It always runs a release-only rebuild simulation, and
additionally compares two git refs when '--from' is supplied.

Rebuild simulation (always): for every component whose Release a render would
bump through a release counter, the counter is first validated against the
current rendered spec. The rendered output is then staged twice, the counter is
incremented by one in the second copy, both copies are evaluated with rpmspec,
and the fully expanded EVR must strictly increase. Because the comparison
happens after macro expansion, it catches silent no-op counters that static
analysis cannot prove, such as a counter that fuses with an adjacent macro
expansion. Components whose Release is owned by rpmautospec ('autorelease') or
by the maintainer ('manual') are excluded because no counter drives them. The
repository's rendered specs are never modified.

This does not predict whether a given mass-rebuild commit will bump a counter.
A render derives the counter from the component's tracked-input history, so a
commit that leaves a component's inputs untouched produces byte-identical
output even though this mode reports 'ok'.

Cross-ref regression check (with '--from'): rendered specs at '--from' and
'--to' are compared, and a component whose rendered content changed without an
SRPM EVR increase fails, as does any EVR decrease. A release-only bump is
accepted even when the spec content is otherwise unchanged. Components added or
deleted between refs are accepted without an EVR comparison. Components whose
complete rendered directory is byte-identical at both refs skip evaluation,
because identical inputs in the same mock environment cannot produce a
different EVR.

Per-component verdicts are 'ok', 'counter-invalid' (the configured counter does
not resolve against the rendered spec), 'bump-ineffective' (incrementing the
counter did not raise the expanded EVR), 'evr-regressed' (rendered content
changed without an EVR increase, or the EVR decreased), and 'check-failed' (the
component could not be staged, evaluated, or compared). The command exits
non-zero when any component's verdict is not 'ok'.

This command is intended for downstream CI alongside rendered-spec and lock
checks. It does not run automatically as part of component update, render, or
build commands. Developers may invoke it locally to reproduce a CI failure.
It compares '--from' directly with '--to'. CI must choose a comparison baseline
that matches its checked-out PR ref and pass it with '--from'.

Rendered specs and component build options are evaluated inside mock so RPM
macros, conditional build options, and RPM's native version comparison match
the target distribution rather than the host. There is no static-only mode:
'--evr' always requires a working mock configuration. Component selection and
the rendered-specs directory are resolved from the current checkout; run the
command from a checkout matching '--to' for accurate results.`,
		Example: "  # CI: prove a release-only rebuild would produce new EVRs\n" +
			"  azldev component check --evr -a -q -O json\n\n" +
			"  # CI: also check all rendered specs against its selected baseline\n" +
			"  azldev component check --evr --from \"$BASELINE_REF\" -a\n\n" +
			"  # Diagnose one component after a CI failure\n" +
			"  azldev component check --evr --from origin/main --to HEAD -p nss",
		RunE:              runCheckCmd(options),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().BoolVar(&options.EVR, "evr", false,
		"verify SRPM EVRs: simulate a release-only rebuild, and compare '--from' to '--to' when given")
	cmd.Flags().StringVar(&options.From, "from", "",
		"Git ref to compare from; enables the cross-ref EVR regression check")
	cmd.Flags().StringVar(&options.To, "to", "HEAD", "Git ref to compare to (requires --from)")

	// This command compares arbitrary historical renders, so the current lock
	// state is unrelated to its correctness.
	_ = cmd.Flags().MarkHidden("skip-lock-validation")

	azldev.ExportAsReadOnlyMCPTool(cmd)

	return cmd
}

func runCheckCmd(options *CheckOptions) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		env, err := azldev.GetEnvFromCommand(cmd)
		if err != nil {
			return fmt.Errorf("getting command environment:\n%w", err)
		}

		if env.Config() == nil || env.ProjectDir() == "" {
			env.AddFixSuggestion(
				"Please either use the -C option to specify a path to the root directory " +
					"of your Azure Linux project/repo, or else run this tool from within a directory " +
					"tree that contains an 'azldev.toml' file at its root. " +
					"Most commands will not function correctly without a valid configuration.",
			)

			return errors.New("a valid project and configuration are required to execute this command")
		}

		if err := validateCheckCmdOptions(cmd, options); err != nil {
			cmd.SilenceUsage = false

			return err
		}

		options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

		report, checkErr := CheckEVR(env, options)
		if reportErr := reportCheckEVR(env, report); reportErr != nil {
			return reportErr
		}

		return checkErr
	}
}

func validateCheckCmdOptions(cmd *cobra.Command, options *CheckOptions) error {
	if !options.EVR {
		return fmt.Errorf("%w: select a component check mode with '--evr'", azldev.ErrInvalidUsage)
	}

	if options.From == "" && cmd.Flags().Changed("to") {
		return fmt.Errorf("%w: '--to' requires '--from'", azldev.ErrInvalidUsage)
	}

	return nil
}

func reportCheckEVR(env *azldev.Env, report *CheckEVRReport) error {
	if report == nil {
		return nil
	}

	results := any(report.Components)
	if env.DefaultReportFormat() == azldev.ReportFormatJSON {
		results = report
	}

	if err := azldev.ReportResults(env, results); err != nil {
		return fmt.Errorf("reporting component check results:\n%w", err)
	}

	return nil
}

// CheckEVRReport is the stable JSON artifact emitted by 'component check --evr'.
type CheckEVRReport struct {
	// Selector identifies how the component set was chosen. It is currently
	// "identity": the caller explicitly selects components rather than a
	// dependency-analysis selector deriving them.
	Selector string `json:"selector"`
	// Components contains one verdict for every checked component, sorted by
	// component name for stable CI artifacts.
	Components []CheckEVRResult `json:"components"`
}

// CheckEVRResult is the combined SRPM EVR verdict for one component.
type CheckEVRResult struct {
	// Component is the component name used for config resolution and rendered
	// spec lookup.
	Component string `json:"component" table:"Component"`
	// ReleaseMode is the resolved 'release.calculation' mode from the current
	// configuration. It helps CI explain why a component was or was not bumped.
	ReleaseMode string `json:"releaseMode" table:"Release Mode"`
	// CounterSource identifies the physical counter source a render would bump.
	// It is absent for a component that no counter drives.
	CounterSource string `json:"counterSource,omitempty" table:"Counter,omitempty"`
	// Rebuild holds the release-only rebuild simulation detail. It is JSON-only
	// because the nested fields do not fit usefully in the table view.
	Rebuild *CheckEVRRebuildResult `json:"rebuild,omitempty" table:"-"`
	// SRPM contains the cross-ref evaluated source-RPM EVRs and their
	// comparison. It is absent unless '--from' was supplied.
	SRPM *CheckEVRSRPMResult `json:"srpm,omitempty" table:"-"`
	// Verdict is the gate result. Anything other than [CheckEVRVerdictOK] makes
	// the command fail.
	Verdict CheckEVRVerdict `json:"verdict" table:"Verdict"`
	// Notes explains an accepted exceptional case (such as an added component)
	// or gives the concrete reason the component failed.
	Notes []string `json:"notes,omitempty" table:"Notes,omitempty"`
}

// CheckEVRRebuildResult captures one simulated release-only rebuild.
type CheckEVRRebuildResult struct {
	// CounterFrom and CounterTo are the raw counter values before and after the
	// simulated bump. They explain a bump-ineffective verdict where the counter
	// changed but the expanded EVR did not.
	CounterFrom string `json:"counterFrom,omitempty"`
	CounterTo   string `json:"counterTo,omitempty"`
	// CurrentEVR is the fully expanded EVR of the current rendered spec.
	CurrentEVR string `json:"currentEvr,omitempty"`
	// RebuiltEVR is the fully expanded EVR after the counter is incremented once.
	RebuiltEVR string `json:"rebuiltEvr,omitempty"`
}

// CheckEVRSRPMResult captures the previous/current evaluated source RPM EVR.
type CheckEVRSRPMResult struct {
	// OldEVR is the EVR evaluated from the rendered spec at '--from'. It is
	// absent for components with no previous rendered spec or failed extraction.
	OldEVR string `json:"oldEvr,omitempty"`
	// NewEVR is the EVR evaluated from the rendered spec at '--to'. It is
	// absent for deleted components or failed extraction.
	NewEVR string `json:"newEvr,omitempty"`
	// EVRCmp is "gt", "eq", or "lt" when both EVRs could be compared with
	// the target distro's RPM implementation.
	EVRCmp string `json:"evrCmp,omitempty"`
}

// CheckEVRVerdict is the CI verdict for one component.
type CheckEVRVerdict string

const (
	// CheckEVRVerdictOK means every applicable check passed.
	CheckEVRVerdictOK CheckEVRVerdict = "ok"
	// CheckEVRVerdictCounterInvalid means the configured release counter does not
	// resolve against the component's current rendered spec.
	CheckEVRVerdictCounterInvalid CheckEVRVerdict = "counter-invalid"
	// CheckEVRVerdictBumpIneffective means incrementing the release counter did
	// not raise the fully expanded EVR, so a rebuild would emit an identical
	// NEVRA.
	CheckEVRVerdictBumpIneffective CheckEVRVerdict = "bump-ineffective"
	// CheckEVRVerdictEVRRegressed means the rendered content changed between
	// '--from' and '--to' without an SRPM EVR increase, or the EVR decreased.
	CheckEVRVerdictEVRRegressed CheckEVRVerdict = "evr-regressed"
	// CheckEVRVerdictCheckFailed means the component could not be staged,
	// evaluated, or compared, so no claim about its EVR can be made.
	CheckEVRVerdictCheckFailed CheckEVRVerdict = "check-failed"
)

// Verdict ranks, ordered so a component failing several checks reports the most
// explanatory root cause: an unresolvable counter outranks its downstream symptoms.
const (
	checkEVRSeverityOK = iota
	checkEVRSeverityCheckFailed
	checkEVRSeverityEVRRegressed
	checkEVRSeverityBumpIneffective
	checkEVRSeverityCounterInvalid
)

func checkEVRVerdictSeverity(verdict CheckEVRVerdict) int {
	switch verdict {
	case CheckEVRVerdictOK:
		return checkEVRSeverityOK
	case CheckEVRVerdictCheckFailed:
		return checkEVRSeverityCheckFailed
	case CheckEVRVerdictEVRRegressed:
		return checkEVRSeverityEVRRegressed
	case CheckEVRVerdictBumpIneffective:
		return checkEVRSeverityBumpIneffective
	case CheckEVRVerdictCounterInvalid:
		return checkEVRSeverityCounterInvalid
	default:
		return checkEVRSeverityCheckFailed
	}
}

// CheckEVR performs the '--evr' mode of [NewCheckCmd]. It always simulates a
// release-only rebuild of every counter-bumped component, and additionally
// compares rendered specs between two git refs when '--from' is supplied. It
// returns a report even when the gate fails so callers can serialize the
// complete machine-readable result before returning the non-zero error.
func CheckEVR(env *azldev.Env, options *CheckOptions) (*CheckEVRReport, error) {
	componentSet, err := resolveCheckEVRComponents(env, options)
	if err != nil {
		return nil, err
	}

	stagingDir, err := createCheckEVRStagingDir(env)
	if err != nil {
		return nil, err
	}
	defer removeCheckEVRStagingDir(env, stagingDir)

	records := stageCheckEVRRebuildRecords(env, componentSet, stagingDir)

	if options.From != "" {
		records, err = appendCheckEVRRegressionRecords(env, options, componentSet, stagingDir, records)
		if err != nil {
			return nil, err
		}
	}

	return buildCheckEVRReport(env, stagingDir, records)
}

func resolveCheckEVRComponents(env *azldev.Env, options *CheckOptions) (*components.ComponentSet, error) {
	options.ComponentFilter.SkipLockValidation = true

	componentSet, err := components.NewResolver(env).FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("resolving components:\n%w", err)
	}

	if componentSet.Len() == 0 && !options.ComponentFilter.IncludeAllComponents {
		return nil, errors.New("no components matched the filter")
	}

	return componentSet, nil
}

func buildCheckEVRReport(
	env *azldev.Env,
	stagingDir string,
	records []*checkEVRRecord,
) (*CheckEVRReport, error) {
	report := &CheckEVRReport{Selector: "identity"}
	if err := evaluateCheckEVRRecords(env, stagingDir, records); err != nil {
		report.Components = classifyCheckEVRRecords(records)

		return report, err
	}

	report.Components = classifyCheckEVRRecords(records)

	return report, checkEVRFailure(report.Components)
}

func createCheckEVRStagingDir(env *azldev.Env) (string, error) {
	if err := fileutils.MkdirAll(env.FS(), env.WorkDir()); err != nil {
		return "", fmt.Errorf("creating work directory:\n%w", err)
	}

	stagingDir, err := fileutils.MkdirTemp(env.FS(), env.WorkDir(), "azldev-check-evr-")
	if err != nil {
		return "", fmt.Errorf("creating EVR check staging directory:\n%w", err)
	}

	return stagingDir, nil
}

func removeCheckEVRStagingDir(env *azldev.Env, stagingDir string) {
	if err := env.FS().RemoveAll(stagingDir); err != nil {
		slog.Debug("Failed to clean up EVR check staging directory", "path", stagingDir, "error", err)
	}
}

// checkEVRMockSubdir isolates the check command's mock root from build and
// render roots that may use the same mock config concurrently.
const checkEVRMockSubdir = "azldev-check-evr-mock"

// checkEVRRecord is the per-component working state of one '--evr' run. Each
// phase is nil when it does not apply to the component, so a record always
// carries at least one evaluated phase.
type checkEVRRecord struct {
	// Component is the name reported in the final result and used as the
	// extractor manifest key.
	Component string
	// ReleaseMode is the resolved 'release.calculation' mode from the current
	// configuration.
	ReleaseMode string
	// Rebuild is the release-only rebuild simulation. It is nil for a component
	// whose Release a render would not bump through a counter.
	Rebuild *checkEVRRebuildPhase
	// Regression is the cross-ref comparison. It is nil unless '--from' was
	// supplied and the component participates in the comparison.
	Regression *checkEVRRegressionPhase
}

func stageCheckEVRRebuildRecords(
	env *azldev.Env,
	componentSet *components.ComponentSet,
	stagingDir string,
) []*checkEVRRecord {
	selections := checkEVRCounterSelections(env.FS(), componentSet.Components())
	phases := stageCheckEVRRebuildPhases(env, selections, stagingDir)

	records := make([]*checkEVRRecord, len(selections))
	for idx, selection := range selections {
		records[idx] = &checkEVRRecord{
			Component:   selection.Name,
			ReleaseMode: checkEVRReleaseMode(selection.Config),
			Rebuild:     phases[idx],
		}
	}

	return records
}

// appendCheckEVRRegressionRecords attaches the cross-ref comparison to the
// records the rebuild simulation already produced, and appends a record for any
// component that participates only in the comparison (such as a lock-only
// component deleted between the two refs).
func appendCheckEVRRegressionRecords(
	env *azldev.Env,
	options *CheckOptions,
	componentSet *components.ComponentSet,
	stagingDir string,
	records []*checkEVRRecord,
) ([]*checkEVRRecord, error) {
	gitState, err := resolveCheckEVRGitState(env, options, componentSet)
	if err != nil {
		return nil, err
	}

	byComponent := make(map[string]*checkEVRRecord, len(records))
	for _, record := range records {
		byComponent[record.Component] = record
	}

	for _, selection := range gitState.selections {
		phase, include := stageOneCheckEVRRegressionPhase(
			env, gitState.changedCtx, gitState.fromTree, gitState.toTree, selection, stagingDir,
		)
		if !include {
			continue
		}

		record, found := byComponent[selection.Name]
		if !found {
			record = &checkEVRRecord{
				Component:   selection.Name,
				ReleaseMode: checkEVRReleaseMode(selection.Config),
			}
			byComponent[selection.Name] = record
			records = append(records, record)
		}

		record.Regression = phase
	}

	return records, nil
}

type checkEVRGitState struct {
	// changedCtx owns the opened project repository and current-checkout paths.
	changedCtx *changedContext
	// fromTree and toTree are the immutable Git trees compared by this run.
	fromTree *object.Tree
	toTree   *object.Tree
	// selections is the resolved union of current and historical components.
	selections []checkEVRSelection
}

func resolveCheckEVRGitState(
	env *azldev.Env,
	options *CheckOptions,
	componentSet *components.ComponentSet,
) (*checkEVRGitState, error) {
	changedCtx, err := newChangedContext(env)
	if err != nil {
		return nil, err
	}

	fromHash, toHash, err := resolveCheckEVRRefs(changedCtx, options)
	if err != nil {
		return nil, err
	}

	fromTree, err := resolveTree(changedCtx.repo, fromHash)
	if err != nil {
		return nil, fmt.Errorf("resolving tree for --from:\n%w", err)
	}

	toTree, err := resolveTree(changedCtx.repo, toHash)
	if err != nil {
		return nil, fmt.Errorf("resolving tree for --to:\n%w", err)
	}

	selections, err := checkEVRSelections(
		componentSet.Components(), options.ComponentFilter.IncludeAllComponents, changedCtx, fromHash, toHash,
	)
	if err != nil {
		return nil, err
	}

	if len(selections) == 0 {
		return nil, errors.New("no components matched the filter")
	}

	return &checkEVRGitState{
		changedCtx: changedCtx,
		fromTree:   fromTree,
		toTree:     toTree,
		selections: selections,
	}, nil
}

func resolveCheckEVRRefs(changedCtx *changedContext, options *CheckOptions) (string, string, error) {
	fromHash, err := resolveCommitHash(changedCtx.repo, options.From)
	if err != nil {
		return "", "", fmt.Errorf("resolving --from ref %#q:\n%w", options.From, err)
	}

	toHash, err := resolveCommitHash(changedCtx.repo, options.To)
	if err != nil {
		return "", "", fmt.Errorf("resolving --to ref %#q:\n%w", options.To, err)
	}

	return fromHash, toHash, nil
}

type checkEVRSelection struct {
	// Name is the component name used to resolve the rendered spec directory.
	Name string
	// Config is the current resolved component configuration. It is nil for a
	// historical lock-only component that no longer exists in the current config.
	Config *projectconfig.ComponentConfig
	// Historical marks a component discovered only from locks at '--from' or
	// '--to'. It allows a fully removed component to be treated as a deletion
	// instead of a configuration-resolution error.
	Historical bool
}

func checkEVRSelections(
	comps []components.Component,
	includeAll bool,
	changedCtx *changedContext,
	fromHash, toHash string,
) ([]checkEVRSelection, error) {
	selections := make([]checkEVRSelection, 0, len(comps))
	known := make(map[string]bool, len(comps))

	for _, comp := range comps {
		selections = append(selections, checkEVRSelection{Name: comp.GetName(), Config: comp.GetConfig()})
		known[comp.GetName()] = true
	}

	if !includeAll {
		return selections, nil
	}

	fromLocks, err := lockfile.ReadAllAtCommit(changedCtx.repo, fromHash, changedCtx.lockRelDir)
	if err != nil {
		return nil, fmt.Errorf("reading locks at --from:\n%w", err)
	}

	toLocks, err := lockfile.ReadAllAtCommit(changedCtx.repo, toHash, changedCtx.lockRelDir)
	if err != nil {
		return nil, fmt.Errorf("reading locks at --to:\n%w", err)
	}

	historicalNames := checkEVRHistoricalNames(known, fromLocks, toLocks)
	for _, name := range historicalNames {
		selections = append(selections, checkEVRSelection{Name: name, Historical: true})
	}

	return selections, nil
}

func checkEVRHistoricalNames(
	known map[string]bool,
	fromLocks, toLocks map[string]lockfile.ComponentLock,
) []string {
	names := make([]string, 0, len(fromLocks)+len(toLocks))
	for name := range fromLocks {
		if !known[name] {
			names = append(names, name)
			known[name] = true
		}
	}

	for name := range toLocks {
		if !known[name] {
			names = append(names, name)
			known[name] = true
		}
	}

	sort.Strings(names)

	return names
}

// checkEVRRegressionPhase is the per-component working state of the cross-ref
// comparison between the rendered specs at '--from' and '--to'.
type checkEVRRegressionPhase struct {
	// ContentChanged is true when the Git tree hash for the component's complete
	// rendered-spec directory differs between '--from' and '--to'. The directory
	// includes the spec, patches, generated macros, and scripts that can affect
	// the SRPM, so comparing only the spec blob would miss real build changes.
	ContentChanged bool
	// RenderedDirUnchanged means the complete rendered-spec directory has an
	// identical Git tree hash at both refs. It lets the gate skip staging and
	// rpmspec work because identical inputs in the same mock environment must
	// produce an identical SRPM EVR.
	RenderedDirUnchanged bool
	// Added and Deleted describe one-sided rendered specs. Both are accepted
	// because no old/new EVR pair exists to compare.
	Added   bool
	Deleted bool
	// PreparationError records a safe pre-extraction failure, such as a missing
	// spec at both refs or missing current configuration for a historical entry.
	PreparationError string
	// FromInput and ToInput identify the staged spec files and build options sent
	// to the batch extractor. Nil inputs mean no extraction is needed.
	FromInput *evr.Input
	ToInput   *evr.Input
	// FromResult and ToResult are per-component extraction outcomes. An error in
	// either result is reported as a failed check rather than aborting the batch.
	FromResult *evr.Result
	ToResult   *evr.Result
	// Comparison is the target-rpm labelCompare result once both EVRs were
	// extracted successfully. Nil means there was no safe pair to compare.
	Comparison *evr.ComparisonResult
}

func stageOneCheckEVRRegressionPhase(
	env *azldev.Env,
	changedCtx *changedContext,
	fromTree, toTree *object.Tree,
	selection checkEVRSelection,
	stagingDir string,
) (*checkEVRRegressionPhase, bool) {
	phase := &checkEVRRegressionPhase{}

	pair, err := readCheckEVRRenderedDirPair(changedCtx, fromTree, toTree, selection.Name)
	if err != nil {
		phase.PreparationError = err.Error()

		return phase, true
	}

	switch {
	case pair.fromMissing && pair.toMissing && selection.Historical:
		return nil, false
	case pair.fromMissing && pair.toMissing:
		phase.PreparationError = "rendered spec is missing at both refs"
	case pair.fromMissing:
		phase.Added = true
	case pair.toMissing:
		phase.Deleted = true
	case pair.fromTreeHash == pair.toTreeHash:
		phase.RenderedDirUnchanged = true
	case selection.Config == nil:
		phase.PreparationError = "component is absent from the current configuration; cannot resolve build options"
	default:
		phase.ContentChanged = true
		if err := materializeCheckEVRRegressionPhase(
			env, fromTree, toTree, stagingDir, selection, pair, phase,
		); err != nil {
			phase.PreparationError = err.Error()
		}
	}

	return phase, true
}

// checkEVRRenderedDirPair identifies one component's rendered-spec directory
// at both refs. The two hashes are Git tree hashes, so they include every file
// below the directory rather than only the primary spec file.
type checkEVRRenderedDirPair struct {
	// renderedDirRel is the repository-relative directory path used to stage
	// the complete rendered output into the mock bind mount.
	renderedDirRel string
	// fromTreeHash and toTreeHash identify the component directory at each ref.
	// Equal hashes prove all tracked rendered inputs are byte-identical.
	fromTreeHash plumbing.Hash
	toTreeHash   plumbing.Hash
	// fromMissing and toMissing distinguish a component addition or deletion
	// from an empty rendered-spec directory.
	fromMissing bool
	toMissing   bool
}

func readCheckEVRRenderedDirPair(
	changedCtx *changedContext,
	fromTree, toTree *object.Tree,
	componentName string,
) (checkEVRRenderedDirPair, error) {
	renderedDir, err := components.RenderedSpecDir(changedCtx.renderedSpecsDir, componentName)
	if err != nil {
		return checkEVRRenderedDirPair{}, fmt.Errorf("resolving rendered spec dir for %#q:\n%w", componentName, err)
	}

	renderedDirRel, err := repoRelPath(changedCtx.repoRoot, renderedDir)
	if err != nil {
		return checkEVRRenderedDirPair{}, fmt.Errorf("computing rendered spec directory for %#q:\n%w", componentName, err)
	}

	fromTreeHash, fromMissing, err := renderedSpecDirTreeHash(fromTree, renderedDirRel)
	if err != nil {
		return checkEVRRenderedDirPair{}, fmt.Errorf(
			"reading rendered spec directory for %#q at --from:\n%w", componentName, err,
		)
	}

	toTreeHash, toMissing, err := renderedSpecDirTreeHash(toTree, renderedDirRel)
	if err != nil {
		return checkEVRRenderedDirPair{}, fmt.Errorf(
			"reading rendered spec directory for %#q at --to:\n%w", componentName, err,
		)
	}

	return checkEVRRenderedDirPair{
		renderedDirRel: renderedDirRel,
		fromTreeHash:   fromTreeHash,
		toTreeHash:     toTreeHash,
		fromMissing:    fromMissing,
		toMissing:      toMissing,
	}, nil
}

// renderedSpecDirTreeHash returns the Git tree hash stored for one complete
// rendered-spec directory. A missing directory is not an error because it
// represents a component addition or deletion between the two refs.
func renderedSpecDirTreeHash(tree *object.Tree, renderedDirRel string) (plumbing.Hash, bool, error) {
	renderedDirTree, err := tree.Tree(filepath.ToSlash(renderedDirRel))
	if err != nil {
		if isFileNotFound(err) {
			return plumbing.ZeroHash, true, nil
		}

		return plumbing.ZeroHash, false, fmt.Errorf("reading rendered spec tree %#q:\n%w", renderedDirRel, err)
	}

	return renderedDirTree.Hash, false, nil
}

func materializeCheckEVRRegressionPhase(
	env *azldev.Env,
	fromTree, toTree *object.Tree,
	stagingDir string,
	selection checkEVRSelection,
	pair checkEVRRenderedDirPair,
	phase *checkEVRRegressionPhase,
) error {
	fromDir := filepath.Join(stagingDir, "from", selection.Name)

	toDir := filepath.Join(stagingDir, "to", selection.Name)
	if err := materializeRenderedSpecDir(env, fromTree, pair.renderedDirRel, fromDir); err != nil {
		return fmt.Errorf("materializing rendered specs for %#q:\n%w", selection.Name, err)
	}

	if err := materializeRenderedSpecDir(env, toTree, pair.renderedDirRel, toDir); err != nil {
		return fmt.Errorf("materializing rendered specs for %#q:\n%w", selection.Name, err)
	}

	fromPath := filepath.Join(fromDir, selection.Name+".spec")
	toPath := filepath.Join(toDir, selection.Name+".spec")

	buildOptions := checkEVRBuildOptions(selection.Config)
	phase.FromInput = &evr.Input{Component: selection.Name, SpecPath: fromPath, BuildOptions: buildOptions}
	phase.ToInput = &evr.Input{Component: selection.Name, SpecPath: toPath, BuildOptions: buildOptions}

	return nil
}

func materializeRenderedSpecDir(
	env *azldev.Env,
	tree *object.Tree,
	renderedDirRel, destinationDir string,
) error {
	renderedTree, err := tree.Tree(filepath.ToSlash(renderedDirRel))
	if err != nil {
		return fmt.Errorf("reading rendered spec tree %#q:\n%w", renderedDirRel, err)
	}

	err = renderedTree.Files().ForEach(func(file *object.File) error {
		destinationPath := filepath.Join(destinationDir, filepath.FromSlash(file.Name))
		if err := ensureStagedPath(destinationDir, destinationPath); err != nil {
			return err
		}

		contents, err := file.Contents()
		if err != nil {
			return fmt.Errorf("reading rendered sidecar %#q:\n%w", file.Name, err)
		}

		if err := fileutils.MkdirAll(env.FS(), filepath.Dir(destinationPath)); err != nil {
			return fmt.Errorf("creating staged sidecar directory:\n%w", err)
		}

		if err := fileutils.WriteFile(env.FS(), destinationPath, []byte(contents), fileperms.PublicFile); err != nil {
			return fmt.Errorf("writing staged sidecar %#q:\n%w", file.Name, err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("iterating rendered spec files:\n%w", err)
	}

	return nil
}

func ensureStagedPath(destinationDir, destinationPath string) error {
	relPath, err := filepath.Rel(destinationDir, destinationPath)
	if err != nil {
		return fmt.Errorf("computing staged sidecar path:\n%w", err)
	}

	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return fmt.Errorf("rendered sidecar path %#q escapes staging directory", destinationPath)
	}

	return nil
}

func checkEVRBuildOptions(config *projectconfig.ComponentConfig) rpm.BuildOptions {
	return rpm.BuildOptions{
		With:      config.Build.With,
		Without:   config.Build.Without,
		Defines:   config.Build.Defines,
		Undefines: config.Build.Undefines,
	}
}

func checkEVRReleaseMode(config *projectconfig.ComponentConfig) string {
	if config == nil {
		return "unknown"
	}

	if config.Release.Calculation == "" {
		return string(projectconfig.ReleaseCalculationAuto)
	}

	return string(config.Release.Calculation)
}

// checkEVRBatch is one baseline/candidate set of staged specs evaluated and
// compared inside the shared mock chroot. Each set is a single batched rpmspec
// run, so one batch costs three chroot invocations regardless of how many
// components participate.
type checkEVRBatch struct {
	// Name labels the batch in error messages.
	Name string
	// Baseline and Candidate are the two staged manifests to evaluate.
	Baseline  []evr.Input
	Candidate []evr.Input
	// AttachResults stores one extracted manifest back onto the records.
	AttachResults func(results []evr.Result, baseline bool)
	// Comparisons selects the pairs that are safe to compare, after extraction.
	Comparisons func() []evr.Comparison
	// AttachComparisons stores the comparison results back onto the records.
	AttachComparisons func(results []evr.ComparisonResult)
}

func evaluateCheckEVRRecords(env *azldev.Env, stagingDir string, records []*checkEVRRecord) error {
	batches := checkEVRBatches(records)
	if len(batches) == 0 {
		return nil
	}

	extractor, err := newCheckEVRExtractor(env)
	if err != nil {
		return err
	}
	defer destroyCheckEVRExtractor(env, extractor)

	for _, batch := range batches {
		if err := runCheckEVRBatch(env, extractor, stagingDir, batch); err != nil {
			return err
		}
	}

	return nil
}

// checkEVRPhaseCount is the number of batches a '--evr' run can issue: the
// release-only rebuild simulation and the cross-ref comparison.
const checkEVRPhaseCount = 2

// checkEVRBatches builds the batches this run needs. A batch with no staged
// inputs is omitted so a run that only validates counters never starts mock.
func checkEVRBatches(records []*checkEVRRecord) []checkEVRBatch {
	batches := make([]checkEVRBatch, 0, checkEVRPhaseCount)

	if currentInputs, rebuiltInputs := checkEVRRebuildInputs(records); len(currentInputs) > 0 {
		batches = append(batches, checkEVRBatch{
			Name:      "release-only rebuild",
			Baseline:  currentInputs,
			Candidate: rebuiltInputs,
			AttachResults: func(results []evr.Result, baseline bool) {
				attachCheckEVRRebuildResults(records, results, baseline)
			},
			Comparisons:       func() []evr.Comparison { return checkEVRRebuildComparisons(records) },
			AttachComparisons: func(results []evr.ComparisonResult) { attachCheckEVRRebuildComparisons(records, results) },
		})
	}

	if fromInputs, toInputs := checkEVRRegressionInputs(records); len(fromInputs) > 0 {
		batches = append(batches, checkEVRBatch{
			Name:      "cross-ref",
			Baseline:  fromInputs,
			Candidate: toInputs,
			AttachResults: func(results []evr.Result, baseline bool) {
				attachCheckEVRRegressionResults(records, results, baseline)
			},
			Comparisons: func() []evr.Comparison { return checkEVRRegressionComparisons(records) },
			AttachComparisons: func(results []evr.ComparisonResult) {
				attachCheckEVRRegressionComparisons(records, results)
			},
		})
	}

	return batches
}

func runCheckEVRBatch(
	env *azldev.Env,
	extractor *evr.Extractor,
	stagingDir string,
	batch checkEVRBatch,
) error {
	baselineResults, err := extractor.Extract(
		env, env, stagingDir, batch.Baseline, env.FS(), env.CPUBoundConcurrency(),
	)
	if err != nil {
		return fmt.Errorf("extracting %s baseline SRPM EVRs:\n%w", batch.Name, err)
	}

	candidateResults, err := extractor.Extract(
		env, env, stagingDir, batch.Candidate, env.FS(), env.CPUBoundConcurrency(),
	)
	if err != nil {
		return fmt.Errorf("extracting %s candidate SRPM EVRs:\n%w", batch.Name, err)
	}

	batch.AttachResults(baselineResults, true)
	batch.AttachResults(candidateResults, false)

	comparisonResults, err := extractor.Compare(
		env, env, stagingDir, batch.Comparisons(), env.FS(), env.CPUBoundConcurrency(),
	)
	if err != nil {
		return fmt.Errorf("comparing %s SRPM EVRs:\n%w", batch.Name, err)
	}

	batch.AttachComparisons(comparisonResults)

	return nil
}

func checkEVRRegressionInputs(records []*checkEVRRecord) ([]evr.Input, []evr.Input) {
	fromInputs := make([]evr.Input, 0, len(records))

	toInputs := make([]evr.Input, 0, len(records))

	for _, record := range records {
		// Equal directory tree hashes mean all staged build inputs are equal,
		// so there is no need to materialize or run rpmspec for this component.
		if record.Regression == nil || record.Regression.RenderedDirUnchanged {
			continue
		}

		if record.Regression.FromInput == nil || record.Regression.ToInput == nil {
			continue
		}

		fromInputs = append(fromInputs, *record.Regression.FromInput)
		toInputs = append(toInputs, *record.Regression.ToInput)
	}

	return fromInputs, toInputs
}

func attachCheckEVRRegressionResults(records []*checkEVRRecord, results []evr.Result, previous bool) {
	byComponent := make(map[string]evr.Result, len(results))
	for _, result := range results {
		byComponent[result.Component] = result
	}

	for _, record := range records {
		if record.Regression == nil {
			continue
		}

		result, found := byComponent[record.Component]
		if !found {
			continue
		}

		if previous {
			record.Regression.FromResult = &result
		} else {
			record.Regression.ToResult = &result
		}
	}
}

func checkEVRRegressionComparisons(records []*checkEVRRecord) []evr.Comparison {
	comparisons := make([]evr.Comparison, 0, len(records))

	for _, record := range records {
		if record.Regression == nil ||
			!checkEVRPairIsComparable(record.Regression.FromResult, record.Regression.ToResult) {
			continue
		}

		comparisons = append(comparisons, evr.Comparison{
			Component: record.Component,
			Previous:  *record.Regression.FromResult.EVR,
			Current:   *record.Regression.ToResult.EVR,
		})
	}

	return comparisons
}

func attachCheckEVRRegressionComparisons(records []*checkEVRRecord, results []evr.ComparisonResult) {
	byComponent := make(map[string]evr.ComparisonResult, len(results))
	for _, result := range results {
		byComponent[result.Component] = result
	}

	for _, record := range records {
		if record.Regression == nil {
			continue
		}

		if result, found := byComponent[record.Component]; found {
			record.Regression.Comparison = &result
		}
	}
}

// checkEVRPairIsComparable reports whether both evaluated EVRs are present, so a
// failed extraction never reaches labelCompare with a nil EVR.
func checkEVRPairIsComparable(baseline, candidate *evr.Result) bool {
	return baseline != nil && candidate != nil &&
		baseline.Error == nil && candidate.Error == nil &&
		baseline.EVR != nil && candidate.EVR != nil
}

func newCheckEVRExtractor(env *azldev.Env) (*evr.Extractor, error) {
	_, distroVersion, err := env.Distro()
	if err != nil {
		return nil, fmt.Errorf("resolving project distro for EVR evaluation:\n%w", err)
	}

	if distroVersion.MockConfigPath == "" {
		return nil, errors.New("mock config required for EVR evaluation; configure a project distro mock config")
	}

	var options []evr.ExtractorOption
	if baseDir := checkEVRMockBaseDir(env); baseDir != "" {
		options = append(options, evr.WithIsolatedMockBaseDir(baseDir))
	}

	return evr.NewExtractor(env, distroVersion.MockConfigPath, options...), nil
}

func checkEVRMockBaseDir(env *azldev.Env) string {
	if env.WorkDir() == "" {
		return ""
	}

	return filepath.Join(env.WorkDir(), checkEVRMockSubdir)
}

func destroyCheckEVRExtractor(env *azldev.Env, extractor *evr.Extractor) {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(env), mockProcessorCleanupTimeout)
	defer cancel()

	extractor.Destroy(cleanupContext)
}

func classifyCheckEVRRecords(records []*checkEVRRecord) []CheckEVRResult {
	results := make([]CheckEVRResult, 0, len(records))
	for _, record := range records {
		results = append(results, classifyCheckEVRRecord(record))
	}

	sort.Slice(results, func(left, right int) bool {
		return results[left].Component < results[right].Component
	})

	return results
}

func classifyCheckEVRRecord(record *checkEVRRecord) CheckEVRResult {
	result := CheckEVRResult{
		Component:   record.Component,
		ReleaseMode: record.ReleaseMode,
		Verdict:     CheckEVRVerdictOK,
	}

	if record.Rebuild != nil {
		result.CounterSource = record.Rebuild.Source
		result.Rebuild = checkEVRRebuildDetail(record.Rebuild)

		verdict, note := classifyCheckEVRRebuildPhase(record.Rebuild)
		result = applyCheckEVRVerdict(result, verdict, note)
	}

	if record.Regression != nil {
		result.SRPM = checkEVRRegressionDetail(record.Regression)

		verdict, note := classifyCheckEVRRegressionPhase(record.Regression)
		result = applyCheckEVRVerdict(result, verdict, note)
	}

	return result
}

// applyCheckEVRVerdict folds one phase verdict into the component's result. A
// note is always retained so a passing verdict can still explain an accepted
// exceptional case such as an added component.
func applyCheckEVRVerdict(result CheckEVRResult, verdict CheckEVRVerdict, note string) CheckEVRResult {
	if note != "" {
		result.Notes = append(result.Notes, note)
	}

	if checkEVRVerdictSeverity(verdict) > checkEVRVerdictSeverity(result.Verdict) {
		result.Verdict = verdict
	}

	return result
}

func checkEVRRebuildDetail(phase *checkEVRRebuildPhase) *CheckEVRRebuildResult {
	detail := &CheckEVRRebuildResult{CounterFrom: phase.CounterFrom, CounterTo: phase.CounterTo}

	if phase.CurrentResult != nil && phase.CurrentResult.EVR != nil {
		detail.CurrentEVR = phase.CurrentResult.EVR.String()
	}

	if phase.RebuiltResult != nil && phase.RebuiltResult.EVR != nil {
		detail.RebuiltEVR = phase.RebuiltResult.EVR.String()
	}

	return detail
}

func checkEVRRegressionDetail(phase *checkEVRRegressionPhase) *CheckEVRSRPMResult {
	detail := &CheckEVRSRPMResult{}

	if phase.FromResult != nil && phase.FromResult.EVR != nil {
		detail.OldEVR = phase.FromResult.EVR.String()
	}

	if phase.ToResult != nil && phase.ToResult.EVR != nil {
		detail.NewEVR = phase.ToResult.EVR.String()
	}

	detail.EVRCmp = checkEVRComparisonLabel(phase.Comparison)

	return detail
}

func checkEVRComparisonLabel(comparison *evr.ComparisonResult) string {
	if comparison == nil || comparison.Error != nil {
		return ""
	}

	switch comparison.Compare {
	case checkEVRCompareCurrentIsNewer:
		return "gt"
	case checkEVRCompareEqual:
		return "eq"
	case checkEVRCompareCurrentIsOlder:
		return "lt"
	default:
		return ""
	}
}

// classifyCheckEVRRegressionPhase maps the cross-ref RPM comparison into a
// verdict. Equal EVRs are accepted only when the rendered content is unchanged.
func classifyCheckEVRRegressionPhase(phase *checkEVRRegressionPhase) (CheckEVRVerdict, string) {
	if phase.RenderedDirUnchanged {
		// The Git tree hash proves every rendered input is identical at both
		// refs, so target-mock evaluation cannot produce a different EVR.
		return CheckEVRVerdictOK, ""
	}

	if verdict, note, unavailable := checkEVRRegressionUnavailable(phase); unavailable {
		return verdict, note
	}

	switch phase.Comparison.Compare {
	case checkEVRCompareCurrentIsNewer:
		return CheckEVRVerdictOK, ""
	case checkEVRCompareEqual:
		if phase.ContentChanged {
			return CheckEVRVerdictEVRRegressed, "rendered spec changed without an SRPM EVR increase"
		}

		return CheckEVRVerdictOK, ""
	case checkEVRCompareCurrentIsOlder:
		return CheckEVRVerdictEVRRegressed, "SRPM EVR decreased"
	default:
		return CheckEVRVerdictCheckFailed, fmt.Sprintf(
			"unexpected rpm.labelCompare result: %d", phase.Comparison.Compare)
	}
}

// checkEVRRegressionUnavailable classifies a record before rpm.labelCompare can
// run. A returned true means the verdict and note are final: accepted additions
// and deletions pass, while any unsafe or failed evaluation fails.
func checkEVRRegressionUnavailable(phase *checkEVRRegressionPhase) (CheckEVRVerdict, string, bool) {
	switch {
	case phase.PreparationError != "":
		return CheckEVRVerdictCheckFailed, phase.PreparationError, true
	case phase.Added:
		return CheckEVRVerdictOK, "component was added; no previous rendered spec to compare", true
	case phase.Deleted:
		return CheckEVRVerdictOK, "component was deleted; no current rendered spec to compare", true
	case phase.FromResult == nil:
		return CheckEVRVerdictCheckFailed, "previous SRPM EVR evaluation returned no result", true
	case phase.FromResult.Error != nil:
		return CheckEVRVerdictCheckFailed,
			fmt.Sprintf("failed to evaluate previous SRPM EVR: %v", phase.FromResult.Error), true
	case phase.ToResult == nil:
		return CheckEVRVerdictCheckFailed, "current SRPM EVR evaluation returned no result", true
	case phase.ToResult.Error != nil:
		return CheckEVRVerdictCheckFailed,
			fmt.Sprintf("failed to evaluate current SRPM EVR: %v", phase.ToResult.Error), true
	case phase.FromResult.EVR == nil || phase.ToResult.EVR == nil:
		return CheckEVRVerdictCheckFailed, "SRPM EVR evaluation returned no result", true
	case phase.Comparison == nil:
		return CheckEVRVerdictCheckFailed, "SRPM EVR comparison returned no result", true
	case phase.Comparison.Error != nil:
		return CheckEVRVerdictCheckFailed,
			fmt.Sprintf("failed to compare SRPM EVRs: %v", phase.Comparison.Error), true
	}

	return CheckEVRVerdictOK, "", false
}

// checkEVRFailure turns all failing rows into one stable CI error. The caller
// still receives the complete report for JSON or artifact output.
func checkEVRFailure(results []CheckEVRResult) error {
	failed := make([]string, 0)

	for _, result := range results {
		if result.Verdict != CheckEVRVerdictOK {
			failed = append(failed, result.Component)
		}
	}

	if len(failed) == 0 {
		return nil
	}

	return fmt.Errorf("EVR check failed for %d component(s): %s", len(failed), strings.Join(failed, ", "))
}
