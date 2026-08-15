// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/evr"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
)

// checkEVRRebuildPhase is the per-component working state of the counter-bump simulation: the
// component's rendered output is staged twice, the counter is incremented in the second copy,
// and both copies are evaluated with rpmspec so the expanded EVRs can be compared. The
// repository's own rendered specs are never modified.
type checkEVRRebuildPhase struct {
	// Source identifies the physical counter source a render would bump.
	Source string
	// CounterFrom and CounterTo record the simulated counter bump. They explain a
	// bump-ineffective verdict where the counter changed but the expanded EVR did not.
	CounterFrom string
	CounterTo   string
	// CounterError records a counter that does not resolve against the rendered spec. It is a
	// configuration failure rather than an infrastructure failure, so it is tracked separately.
	CounterError string
	// PreparationError records any other component-local failure that happened before
	// extraction, such as an unreadable rendered spec directory.
	PreparationError string
	// CurrentInput and RebuiltInput identify the staged specs sent to the batch extractor.
	CurrentInput *evr.Input
	RebuiltInput *evr.Input
	// CurrentResult and RebuiltResult are per-component extraction outcomes.
	CurrentResult *evr.Result
	RebuiltResult *evr.Result
	// Comparison is the target-rpm labelCompare result once both EVRs were extracted.
	Comparison *evr.ComparisonResult
}

// stageCheckEVRRebuildPhases copies every selected component's rendered output twice and bumps
// the counter only in the second copy. Staging is I/O bound and independent per component, so it
// runs in parallel; results stay in selection order.
func stageCheckEVRRebuildPhases(
	env *azldev.Env,
	selections []checkEVRCounterSelection,
	stagingDir string,
) []*checkEVRRebuildPhase {
	workerEnv, cancel := env.WithCancel()
	defer cancel()

	progressEvent := env.StartEvent("Staging release counter bumps", "count", len(selections))
	defer progressEvent.End()

	total := int64(len(selections))

	results := parmap.Map(
		workerEnv,
		env.IOBoundConcurrency(),
		selections,
		func(done, _ int) { progressEvent.SetProgress(int64(done), total) },
		func(_ context.Context, selection checkEVRCounterSelection) *checkEVRRebuildPhase {
			return stageOneCheckEVRRebuildPhase(workerEnv, selection, stagingDir)
		},
	)

	phases := make([]*checkEVRRebuildPhase, len(selections))

	for idx, result := range results {
		if result.Cancelled {
			phases[idx] = &checkEVRRebuildPhase{
				Source:           string(selections[idx].Counter.Source),
				PreparationError: "staging was cancelled before it started",
			}

			continue
		}

		phases[idx] = result.Value
	}

	return phases
}

// stageOneCheckEVRRebuildPhase validates the counter against the component's own rendered spec
// before staging anything, so a misconfigured counter costs one parse instead of two directory
// copies and is reported as a configuration failure rather than an evaluation failure.
func stageOneCheckEVRRebuildPhase(
	env *azldev.Env,
	selection checkEVRCounterSelection,
	stagingDir string,
) *checkEVRRebuildPhase {
	phase := &checkEVRRebuildPhase{Source: string(selection.Counter.Source)}

	if selection.SelectionError != "" {
		phase.PreparationError = selection.SelectionError

		return phase
	}

	if selection.Config.RenderedSpecDir == "" {
		phase.PreparationError = "component has no rendered spec directory configured"

		return phase
	}

	renderedSpecPath := filepath.Join(selection.Config.RenderedSpecDir, selection.Name+".spec")
	if err := sources.ValidateReleaseCounterInSpec(
		env.FS(), selection.Counter, renderedSpecPath, selection.Config.Build,
	); err != nil {
		phase.CounterError = err.Error()

		return phase
	}

	currentDir := filepath.Join(stagingDir, "current", selection.Name)
	rebuiltDir := filepath.Join(stagingDir, "rebuilt", selection.Name)

	for _, destinationDir := range []string{currentDir, rebuiltDir} {
		if err := fileutils.CopyDirRecursiveCrossFS(
			env.FS(), selection.Config.RenderedSpecDir, env.FS(), destinationDir, fileutils.CopyDirOptions{},
		); err != nil {
			phase.PreparationError = fmt.Sprintf("staging rendered specs: %v", err)

			return phase
		}
	}

	rebuiltSpecPath := filepath.Join(rebuiltDir, selection.Name+".spec")

	counterFrom, counterTo, err := sources.BumpReleaseCounterInSpecFile(
		env.FS(), selection.Counter, rebuiltSpecPath, selection.Config.Build, 1)
	if err != nil {
		phase.CounterError = fmt.Sprintf("simulating release counter bump: %v", err)

		return phase
	}

	phase.CounterFrom = counterFrom
	phase.CounterTo = counterTo

	buildOptions := checkEVRBuildOptions(selection.Config)
	phase.CurrentInput = &evr.Input{
		Component:    selection.Name,
		SpecPath:     filepath.Join(currentDir, selection.Name+".spec"),
		BuildOptions: buildOptions,
	}
	phase.RebuiltInput = &evr.Input{
		Component:    selection.Name,
		SpecPath:     rebuiltSpecPath,
		BuildOptions: buildOptions,
	}

	return phase
}

func checkEVRRebuildInputs(records []*checkEVRRecord) ([]evr.Input, []evr.Input) {
	currentInputs := make([]evr.Input, 0, len(records))

	rebuiltInputs := make([]evr.Input, 0, len(records))

	for _, record := range records {
		if record.Rebuild == nil || record.Rebuild.CurrentInput == nil || record.Rebuild.RebuiltInput == nil {
			continue
		}

		currentInputs = append(currentInputs, *record.Rebuild.CurrentInput)
		rebuiltInputs = append(rebuiltInputs, *record.Rebuild.RebuiltInput)
	}

	return currentInputs, rebuiltInputs
}

func attachCheckEVRRebuildResults(records []*checkEVRRecord, results []evr.Result, current bool) {
	byComponent := make(map[string]evr.Result, len(results))
	for _, result := range results {
		byComponent[result.Component] = result
	}

	for _, record := range records {
		if record.Rebuild == nil {
			continue
		}

		result, found := byComponent[record.Component]
		if !found {
			continue
		}

		if current {
			record.Rebuild.CurrentResult = &result
		} else {
			record.Rebuild.RebuiltResult = &result
		}
	}
}

func checkEVRRebuildComparisons(records []*checkEVRRecord) []evr.Comparison {
	comparisons := make([]evr.Comparison, 0, len(records))

	for _, record := range records {
		if record.Rebuild == nil || !checkEVRPairIsComparable(record.Rebuild.CurrentResult, record.Rebuild.RebuiltResult) {
			continue
		}

		comparisons = append(comparisons, evr.Comparison{
			Component: record.Component,
			Previous:  *record.Rebuild.CurrentResult.EVR,
			Current:   *record.Rebuild.RebuiltResult.EVR,
		})
	}

	return comparisons
}

func attachCheckEVRRebuildComparisons(records []*checkEVRRecord, results []evr.ComparisonResult) {
	byComponent := make(map[string]evr.ComparisonResult, len(results))
	for _, result := range results {
		byComponent[result.Component] = result
	}

	for _, record := range records {
		if record.Rebuild == nil {
			continue
		}

		if result, found := byComponent[record.Component]; found {
			record.Rebuild.Comparison = &result
		}
	}
}

// classifyCheckEVRRebuildPhase maps the counter-bump simulation into a verdict. Only a strict
// increase proves a release-only rebuild would produce a distinct NEVRA.
func classifyCheckEVRRebuildPhase(phase *checkEVRRebuildPhase) (CheckEVRVerdict, string) {
	if verdict, note, unavailable := checkEVRRebuildUnavailable(phase); unavailable {
		return verdict, note
	}

	switch phase.Comparison.Compare {
	case checkEVRCompareCurrentIsNewer:
		return CheckEVRVerdictOK, ""
	case checkEVRCompareEqual:
		return CheckEVRVerdictBumpIneffective, fmt.Sprintf(
			"incrementing the release counter %s did not change the evaluated EVR; a rebuild would emit an "+
				"identical NEVRA, so the counter is a no-op and must target a field that changes the "+
				"expanded Release", checkEVRCounterPhrase(phase))
	case checkEVRCompareCurrentIsOlder:
		return CheckEVRVerdictBumpIneffective, fmt.Sprintf(
			"incrementing the release counter %s lowered the evaluated EVR from %#q to %#q",
			checkEVRCounterPhrase(phase), phase.CurrentResult.EVR.String(), phase.RebuiltResult.EVR.String())
	default:
		return CheckEVRVerdictCheckFailed, fmt.Sprintf(
			"unexpected rpm.labelCompare result: %d", phase.Comparison.Compare)
	}
}

// checkEVRRebuildUnavailable reports the reason a counter-bump simulation cannot reach
// comparison. Every such reason fails the gate: a component that cannot be evaluated cannot be
// proven to rebuild into a distinct NEVRA.
func checkEVRRebuildUnavailable(phase *checkEVRRebuildPhase) (CheckEVRVerdict, string, bool) {
	switch {
	case phase.CounterError != "":
		return CheckEVRVerdictCounterInvalid, phase.CounterError, true
	case phase.PreparationError != "":
		return CheckEVRVerdictCheckFailed, phase.PreparationError, true
	case phase.CurrentResult == nil:
		return CheckEVRVerdictCheckFailed, "current SRPM EVR evaluation returned no result", true
	case phase.CurrentResult.Error != nil:
		return CheckEVRVerdictCheckFailed,
			fmt.Sprintf("failed to evaluate current SRPM EVR: %v", phase.CurrentResult.Error), true
	case phase.RebuiltResult == nil:
		return CheckEVRVerdictCheckFailed, "rebuilt SRPM EVR evaluation returned no result", true
	case phase.RebuiltResult.Error != nil:
		return CheckEVRVerdictCheckFailed,
			fmt.Sprintf("failed to evaluate rebuilt SRPM EVR: %v", phase.RebuiltResult.Error), true
	case phase.CurrentResult.EVR == nil || phase.RebuiltResult.EVR == nil:
		return CheckEVRVerdictCheckFailed, "SRPM EVR evaluation returned no result", true
	case phase.Comparison == nil:
		return CheckEVRVerdictCheckFailed, "simulated rebuild SRPM EVR comparison returned no result", true
	case phase.Comparison.Error != nil:
		return CheckEVRVerdictCheckFailed,
			fmt.Sprintf("failed to compare simulated rebuild SRPM EVRs: %v", phase.Comparison.Error), true
	}

	return CheckEVRVerdictOK, "", false
}

func checkEVRCounterPhrase(phase *checkEVRRebuildPhase) string {
	if phase.CounterFrom == "" || phase.CounterTo == "" {
		return "once"
	}

	return fmt.Sprintf("from %#q to %#q", phase.CounterFrom, phase.CounterTo)
}
