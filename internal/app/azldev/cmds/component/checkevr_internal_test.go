// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/evr"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassifyCheckEVRRegressionPhase covers the cross-ref verdicts, which only apply when
// '--from' is supplied.
func TestClassifyCheckEVRRegressionPhase(t *testing.T) {
	previous := evr.Result{Component: "nss", EVR: &evr.EVR{Epoch: "0", Version: "3.123.1", Release: "1.azl4"}}
	current := evr.Result{Component: "nss", EVR: &evr.EVR{Epoch: "0", Version: "3.123.1", Release: "2.azl4"}}

	tests := []struct {
		name           string
		phase          checkEVRRegressionPhase
		from           evr.Result
		to             evr.Result
		comparison     evr.ComparisonResult
		wantVerdict    CheckEVRVerdict
		wantComparison string
	}{
		{
			name:           "unchanged spec with equal EVR is ok",
			phase:          checkEVRRegressionPhase{},
			from:           previous,
			to:             previous,
			comparison:     evr.ComparisonResult{Component: "nss", Compare: checkEVRCompareEqual},
			wantVerdict:    CheckEVRVerdictOK,
			wantComparison: "eq",
		},
		{
			name:           "release-only bump is accepted",
			phase:          checkEVRRegressionPhase{},
			from:           previous,
			to:             current,
			comparison:     evr.ComparisonResult{Component: "nss", Compare: checkEVRCompareCurrentIsNewer},
			wantVerdict:    CheckEVRVerdictOK,
			wantComparison: "gt",
		},
		{
			name:           "content change with increasing EVR is accepted",
			phase:          checkEVRRegressionPhase{ContentChanged: true},
			from:           previous,
			to:             current,
			comparison:     evr.ComparisonResult{Component: "nss", Compare: checkEVRCompareCurrentIsNewer},
			wantVerdict:    CheckEVRVerdictOK,
			wantComparison: "gt",
		},
		{
			name:           "content change without EVR bump regresses",
			phase:          checkEVRRegressionPhase{ContentChanged: true},
			from:           previous,
			to:             previous,
			comparison:     evr.ComparisonResult{Component: "nss", Compare: checkEVRCompareEqual},
			wantVerdict:    CheckEVRVerdictEVRRegressed,
			wantComparison: "eq",
		},
		{
			name:           "EVR decrease always regresses",
			phase:          checkEVRRegressionPhase{},
			from:           current,
			to:             previous,
			comparison:     evr.ComparisonResult{Component: "nss", Compare: checkEVRCompareCurrentIsOlder},
			wantVerdict:    CheckEVRVerdictEVRRegressed,
			wantComparison: "lt",
		},
		{
			name:        "added component is accepted",
			phase:       checkEVRRegressionPhase{Added: true},
			wantVerdict: CheckEVRVerdictOK,
		},
		{
			name:        "deleted component is accepted",
			phase:       checkEVRRegressionPhase{Deleted: true},
			wantVerdict: CheckEVRVerdictOK,
		},
		{
			name:        "unchanged rendered directory is accepted without evaluation",
			phase:       checkEVRRegressionPhase{RenderedDirUnchanged: true},
			wantVerdict: CheckEVRVerdictOK,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			phase := testCase.phase
			if !phase.Added && !phase.Deleted && !phase.RenderedDirUnchanged {
				phase.FromResult = &testCase.from
				phase.ToResult = &testCase.to
				phase.Comparison = &testCase.comparison
			}

			verdict, _ := classifyCheckEVRRegressionPhase(&phase)
			assert.Equal(t, testCase.wantVerdict, verdict)
			assert.Equal(t, testCase.wantComparison, checkEVRRegressionDetail(&phase).EVRCmp)
		})
	}
}

func TestClassifyCheckEVRRegressionPhase_EvaluationFailureIsCheckFailed(t *testing.T) {
	verdict, note := classifyCheckEVRRegressionPhase(&checkEVRRegressionPhase{
		ContentChanged: true,
		FromResult:     &evr.Result{Component: "nss", Error: assert.AnError},
	})

	assert.Equal(t, CheckEVRVerdictCheckFailed, verdict)
	assert.Contains(t, note, "failed to evaluate previous")
}

// TestClassifyCheckEVRRecord_CombinesPhaseVerdicts proves both checks contribute to one
// component verdict and that the most explanatory root cause wins.
func TestClassifyCheckEVRRecord_CombinesPhaseVerdicts(t *testing.T) {
	older := evr.EVR{Epoch: "0", Version: "1", Release: "1"}
	newer := evr.EVR{Epoch: "0", Version: "1", Release: "2"}

	tests := []struct {
		name        string
		record      checkEVRRecord
		wantVerdict CheckEVRVerdict
		wantNotes   int
	}{
		{
			name: "both phases pass",
			record: checkEVRRecord{
				Component:  "nss",
				Rebuild:    newRebuildPhase(older, newer, checkEVRCompareCurrentIsNewer),
				Regression: newRegressionPhase(older, newer, checkEVRCompareCurrentIsNewer),
			},
			wantVerdict: CheckEVRVerdictOK,
		},
		{
			name: "regression failure alone",
			record: checkEVRRecord{
				Component:  "nss",
				Rebuild:    newRebuildPhase(older, newer, checkEVRCompareCurrentIsNewer),
				Regression: newRegressionPhase(older, older, checkEVRCompareEqual),
			},
			wantVerdict: CheckEVRVerdictEVRRegressed,
			wantNotes:   1,
		},
		{
			name: "an ineffective bump outranks a regression",
			record: checkEVRRecord{
				Component:  "nss",
				Rebuild:    newRebuildPhase(older, older, checkEVRCompareEqual),
				Regression: newRegressionPhase(older, older, checkEVRCompareEqual),
			},
			wantVerdict: CheckEVRVerdictBumpIneffective,
			wantNotes:   2,
		},
		{
			name: "an invalid counter outranks every downstream symptom",
			record: checkEVRRecord{
				Component:  "nss",
				Rebuild:    &checkEVRRebuildPhase{CounterError: "regex did not match"},
				Regression: newRegressionPhase(older, older, checkEVRCompareEqual),
			},
			wantVerdict: CheckEVRVerdictCounterInvalid,
			wantNotes:   2,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result := classifyCheckEVRRecord(&testCase.record)

			assert.Equal(t, testCase.wantVerdict, result.Verdict)
			assert.Len(t, result.Notes, testCase.wantNotes)
			assert.NotNil(t, result.Rebuild)
			assert.NotNil(t, result.SRPM)
		})
	}
}

// TestClassifyCheckEVRRecord_RebuildOnly proves a run without '--from' reports only the
// release-only rebuild simulation.
func TestClassifyCheckEVRRecord_RebuildOnly(t *testing.T) {
	older := evr.EVR{Epoch: "0", Version: "1", Release: "1"}
	newer := evr.EVR{Epoch: "0", Version: "1", Release: "2"}

	result := classifyCheckEVRRecord(&checkEVRRecord{
		Component:   "nss",
		ReleaseMode: "auto",
		Rebuild:     newRebuildPhase(older, newer, checkEVRCompareCurrentIsNewer),
	})

	assert.Equal(t, CheckEVRVerdictOK, result.Verdict)
	assert.Nil(t, result.SRPM, "a run without --from must not report a cross-ref comparison")
	require.NotNil(t, result.Rebuild)
	assert.Equal(t, "1-1", result.Rebuild.CurrentEVR)
	assert.Equal(t, "1-2", result.Rebuild.RebuiltEVR)
}

// TestClassifyCheckEVRRecords_SortsAndAggregates covers report ordering and the exit-code error
// derived from it.
func TestClassifyCheckEVRRecords_SortsAndAggregates(t *testing.T) {
	older := evr.EVR{Epoch: "0", Version: "1", Release: "1"}
	newer := evr.EVR{Epoch: "0", Version: "1", Release: "2"}

	results := classifyCheckEVRRecords([]*checkEVRRecord{
		{Component: "zeta", Rebuild: newRebuildPhase(older, older, checkEVRCompareEqual)},
		{Component: "alpha", Rebuild: newRebuildPhase(older, newer, checkEVRCompareCurrentIsNewer)},
	})

	require.Len(t, results, 2)
	assert.Equal(t, "alpha", results[0].Component)
	assert.Equal(t, CheckEVRVerdictOK, results[0].Verdict)
	assert.Equal(t, "zeta", results[1].Component)
	assert.Equal(t, CheckEVRVerdictBumpIneffective, results[1].Verdict)

	err := checkEVRFailure(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 component(s)")
	assert.Contains(t, err.Error(), "zeta")
	assert.NotContains(t, err.Error(), "alpha")
}

// TestCheckEVRFailure_ExitCodeAggregation proves every non-ok verdict fails the command and an
// all-ok report does not.
func TestCheckEVRFailure_ExitCodeAggregation(t *testing.T) {
	for _, verdict := range []CheckEVRVerdict{
		CheckEVRVerdictCounterInvalid,
		CheckEVRVerdictBumpIneffective,
		CheckEVRVerdictEVRRegressed,
		CheckEVRVerdictCheckFailed,
	} {
		t.Run(string(verdict), func(t *testing.T) {
			err := checkEVRFailure([]CheckEVRResult{
				{Component: "curl", Verdict: CheckEVRVerdictOK},
				{Component: "nss", Verdict: verdict},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "nss")
			assert.NotContains(t, err.Error(), "curl")
		})
	}

	assert.NoError(t, checkEVRFailure([]CheckEVRResult{
		{Component: "alpha", Verdict: CheckEVRVerdictOK},
		{Component: "zeta", Verdict: CheckEVRVerdictOK},
	}))
}

func TestMaterializeRenderedSpecDirIncludesSidecars(t *testing.T) {
	repo, fromRef, toRef := testRepoWithTwoCommits(t,
		map[string][]byte{
			"SPECS/n/nss/nss.spec":          []byte("%include macros.inc\nName: nss\n"),
			"SPECS/n/nss/macros.inc":        []byte("%global release 1\n"),
			"SPECS/n/nss/patches/fix.patch": []byte("patch one\n"),
		},
		map[string][]byte{
			"SPECS/n/nss/nss.spec":          []byte("%include macros.inc\nName: nss\n"),
			"SPECS/n/nss/macros.inc":        []byte("%global release 2\n"),
			"SPECS/n/nss/patches/fix.patch": []byte("patch two\n"),
		},
	)
	fromTree, err := resolveTree(repo, fromRef)
	require.NoError(t, err)
	toTree, err := resolveTree(repo, toRef)
	require.NoError(t, err)

	env := testutils.NewTestEnv(t)
	checkCtx := &changedContext{repoRoot: "/", renderedSpecsDir: testSpecsDirSPECS}
	phase, include := stageOneCheckEVRRegressionPhase(
		env.Env,
		checkCtx,
		fromTree,
		toTree,
		checkEVRSelection{Name: "nss", Config: &projectconfig.ComponentConfig{Name: "nss"}},
		"/work/staging",
	)
	require.True(t, include)
	require.NotNil(t, phase)
	assert.True(t, phase.ContentChanged,
		"changing a rendered sidecar must mark the component as changed even when the spec blob is identical")
	assert.False(t, phase.RenderedDirUnchanged)
	require.NotNil(t, phase.FromInput)
	require.NotNil(t, phase.ToInput)

	require.NoError(t, materializeRenderedSpecDir(env.Env, fromTree, "SPECS/n/nss", "/work/from/nss"))

	spec, err := fileutils.ReadFile(env.TestFS, "/work/from/nss/nss.spec")
	require.NoError(t, err)
	assert.Contains(t, string(spec), "%include macros.inc")

	macros, err := fileutils.ReadFile(env.TestFS, "/work/from/nss/macros.inc")
	require.NoError(t, err)
	assert.Equal(t, "%global release 1\n", string(macros))

	patch, err := fileutils.ReadFile(env.TestFS, "/work/from/nss/patches/fix.patch")
	require.NoError(t, err)
	assert.Equal(t, "patch one\n", string(patch))
}

func TestStageOneCheckEVRRegressionPhaseSkipsUnchangedDirectory(t *testing.T) {
	fromTree := checkEVRTestTree(t, map[string][]byte{
		"SPECS/n/nss/nss.spec":   []byte("Name: nss\n"),
		"SPECS/n/nss/macros.inc": []byte("%global release 1\n"),
	})
	toTree := checkEVRTestTree(t, map[string][]byte{
		"SPECS/n/nss/nss.spec":   []byte("Name: nss\n"),
		"SPECS/n/nss/macros.inc": []byte("%global release 1\n"),
	})
	env := testutils.NewTestEnv(t)

	phase, include := stageOneCheckEVRRegressionPhase(
		env.Env,
		&changedContext{repoRoot: "/", renderedSpecsDir: testSpecsDirSPECS},
		fromTree,
		toTree,
		checkEVRSelection{Name: "nss", Config: &projectconfig.ComponentConfig{Name: "nss"}},
		"/work/staging",
	)

	require.True(t, include)
	require.NotNil(t, phase)
	assert.False(t, phase.ContentChanged)
	assert.True(t, phase.RenderedDirUnchanged)
	assert.Nil(t, phase.FromInput)
	assert.Nil(t, phase.ToInput)

	records := []*checkEVRRecord{{Component: "nss", Regression: phase}}
	fromInputs, toInputs := checkEVRRegressionInputs(records)
	assert.Empty(t, fromInputs)
	assert.Empty(t, toInputs)
	assert.Equal(t, CheckEVRVerdictOK, classifyCheckEVRRecord(records[0]).Verdict)
}

func TestStageOneCheckEVRRegressionPhaseClassifiesDirectoryPresence(t *testing.T) {
	tests := []struct {
		name        string
		fromFiles   map[string][]byte
		toFiles     map[string][]byte
		historical  bool
		wantInclude bool
		wantAdded   bool
		wantDeleted bool
		wantPrepErr bool
	}{
		{
			name:        "added rendered directory",
			fromFiles:   map[string][]byte{"placeholder": []byte("x")},
			toFiles:     map[string][]byte{"SPECS/n/nss/nss.spec": []byte("Name: nss\n")},
			wantInclude: true,
			wantAdded:   true,
		},
		{
			name:        "deleted rendered directory",
			fromFiles:   map[string][]byte{"SPECS/n/nss/nss.spec": []byte("Name: nss\n")},
			toFiles:     map[string][]byte{"placeholder": []byte("x")},
			wantInclude: true,
			wantDeleted: true,
		},
		{
			name:        "missing directory for current component",
			fromFiles:   map[string][]byte{"placeholder": []byte("x")},
			toFiles:     map[string][]byte{"placeholder": []byte("x")},
			wantInclude: true,
			wantPrepErr: true,
		},
		{
			name:        "missing directory for historical component",
			fromFiles:   map[string][]byte{"placeholder": []byte("x")},
			toFiles:     map[string][]byte{"placeholder": []byte("x")},
			historical:  true,
			wantInclude: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			phase, include := stageOneCheckEVRRegressionPhase(
				env.Env,
				&changedContext{repoRoot: "/", renderedSpecsDir: testSpecsDirSPECS},
				checkEVRTestTree(t, testCase.fromFiles),
				checkEVRTestTree(t, testCase.toFiles),
				checkEVRSelection{
					Name:       "nss",
					Config:     &projectconfig.ComponentConfig{Name: "nss"},
					Historical: testCase.historical,
				},
				"/work/staging",
			)

			assert.Equal(t, testCase.wantInclude, include)

			if !testCase.wantInclude {
				assert.Nil(t, phase)

				return
			}

			require.NotNil(t, phase)
			assert.Equal(t, testCase.wantAdded, phase.Added)
			assert.Equal(t, testCase.wantDeleted, phase.Deleted)
			assert.Equal(t, testCase.wantPrepErr, phase.PreparationError != "")
		})
	}
}

func TestStageOneCheckEVRRegressionPhaseRecordsPreparationFailures(t *testing.T) {
	env := testutils.NewTestEnv(t)
	tree := checkEVRTestTree(t, map[string][]byte{"SPECS/n/nss/nss.spec": []byte("Name: nss\n")})

	phase, include := stageOneCheckEVRRegressionPhase(
		env.Env,
		&changedContext{repoRoot: "/", renderedSpecsDir: testSpecsDirSPECS},
		tree,
		tree,
		checkEVRSelection{Name: "../invalid", Config: &projectconfig.ComponentConfig{Name: "../invalid"}},
		"/work/staging",
	)

	require.True(t, include)
	require.NotNil(t, phase)
	assert.NotEmpty(t, phase.PreparationError)
	assert.Equal(t, CheckEVRVerdictCheckFailed,
		classifyCheckEVRRecord(&checkEVRRecord{Component: "../invalid", Regression: phase}).Verdict)
}

func TestCheckEVRBuildOptionsIncludesUndefines(t *testing.T) {
	options := checkEVRBuildOptions(&projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{
			With:      []string{"asan"},
			Without:   []string{"docs"},
			Defines:   map[string]string{"dist": ".azl4"},
			Undefines: []string{"_with_asan", "dist"},
		},
	})

	assert.Equal(t, []string{"asan"}, options.With)
	assert.Equal(t, []string{"docs"}, options.Without)
	assert.Equal(t, map[string]string{"dist": ".azl4"}, options.Defines)
	assert.Equal(t, []string{"_with_asan", "dist"}, options.Undefines)
}

func TestCheckEVRMockBaseDir(t *testing.T) {
	env := testutils.NewTestEnv(t)

	assert.Equal(t, "/work/azldev-check-evr-mock", checkEVRMockBaseDir(env.Env))
}

// TestCheckEVRBatches_OmitsEmptyBatches proves a run whose components all failed staging never
// starts mock, and that a run with both phases staged issues exactly two batches.
func TestCheckEVRBatches_OmitsEmptyBatches(t *testing.T) {
	older := evr.EVR{Epoch: "0", Version: "1", Release: "1"}

	assert.Empty(t, checkEVRBatches([]*checkEVRRecord{
		{Component: "nss", Rebuild: &checkEVRRebuildPhase{CounterError: "regex did not match"}},
		{Component: "curl", Regression: &checkEVRRegressionPhase{RenderedDirUnchanged: true}},
	}))

	rebuildOnly := checkEVRBatches([]*checkEVRRecord{{
		Component: "nss",
		Rebuild:   newRebuildPhase(older, older, checkEVRCompareEqual),
	}})
	require.Len(t, rebuildOnly, 1)
	assert.Equal(t, "release-only rebuild", rebuildOnly[0].Name)

	both := checkEVRBatches([]*checkEVRRecord{{
		Component:  "nss",
		Rebuild:    newRebuildPhase(older, older, checkEVRCompareEqual),
		Regression: newRegressionPhase(older, older, checkEVRCompareEqual),
	}})
	require.Len(t, both, 2)
	assert.Equal(t, "cross-ref", both[1].Name)
}

func checkEVRTestTree(t *testing.T, files map[string][]byte) *object.Tree {
	t.Helper()

	repo, hashes := testRepoWithCommits(t, []testRepoCommit{{files: files}})
	tree, err := resolveTree(repo, hashes[0])
	require.NoError(t, err)

	return tree
}

// newRegressionPhase builds a fully evaluated cross-ref phase for a component whose rendered
// content changed, which is the only case that can reach a regression verdict.
func newRegressionPhase(fromEVR, toEVR evr.EVR, compare int) *checkEVRRegressionPhase {
	return &checkEVRRegressionPhase{
		ContentChanged: true,
		FromInput:      &evr.Input{Component: "nss", SpecPath: "/staging/from/nss/nss.spec"},
		ToInput:        &evr.Input{Component: "nss", SpecPath: "/staging/to/nss/nss.spec"},
		FromResult:     &evr.Result{Component: "nss", EVR: &fromEVR},
		ToResult:       &evr.Result{Component: "nss", EVR: &toEVR},
		Comparison:     &evr.ComparisonResult{Component: "nss", Compare: compare},
	}
}

func TestEnsureStagedPathRejectsEscapes(t *testing.T) {
	tests := []struct {
		name            string
		destinationPath string
		wantErr         bool
	}{
		{
			name:            "inside staging directory",
			destinationPath: "/work/from/nss/patches/fix.patch",
		},
		{
			name:            "direct parent",
			destinationPath: "/work/from",
			wantErr:         true,
		},
		{
			name:            "nested parent escape",
			destinationPath: "/work/outside/fix.patch",
			wantErr:         true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := ensureStagedPath("/work/from/nss", testCase.destinationPath)
			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestMaterializeRenderedSpecDirRejectsMissingDirectory(t *testing.T) {
	repo, hashes := testRepoWithCommits(t, []testRepoCommit{
		{files: map[string][]byte{"placeholder": []byte("x")}},
		{files: map[string][]byte{}},
	})
	tree, err := resolveTree(repo, hashes[0])
	require.NoError(t, err)

	env := testutils.NewTestEnv(t)
	err = materializeRenderedSpecDir(env.Env, tree, "SPECS/n/missing", "/work/from/missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading rendered spec tree")
}

func TestReportCheckEVRJSON(t *testing.T) {
	env := testutils.NewTestEnv(t)
	env.Env.SetDefaultReportFormat(azldev.ReportFormatJSON)

	var output bytes.Buffer
	env.Env.SetReportFile(&output)

	report := &CheckEVRReport{
		Selector: "identity",
		Components: []CheckEVRResult{{
			Component:     "nss",
			ReleaseMode:   "auto",
			CounterSource: "release-tag",
			Rebuild: &CheckEVRRebuildResult{
				CounterFrom: "1", CounterTo: "2",
				CurrentEVR: "3.123.1-1.azl4", RebuiltEVR: "3.123.1-2.azl4",
			},
			SRPM:    &CheckEVRSRPMResult{OldEVR: "3.123.1-1.azl4", NewEVR: "3.123.1-2.azl4", EVRCmp: "gt"},
			Verdict: CheckEVRVerdictOK,
		}},
	}

	require.NoError(t, reportCheckEVR(env.Env, report))
	assert.JSONEq(t, `{
  "selector": "identity",
  "components": [{
    "component": "nss",
    "releaseMode": "auto",
    "counterSource": "release-tag",
    "rebuild": {
      "counterFrom": "1",
      "counterTo": "2",
      "currentEvr": "3.123.1-1.azl4",
      "rebuiltEvr": "3.123.1-2.azl4"
    },
    "srpm": {"oldEvr": "3.123.1-1.azl4", "newEvr": "3.123.1-2.azl4", "evrCmp": "gt"},
    "verdict": "ok"
  }]
}`, output.String())
}
