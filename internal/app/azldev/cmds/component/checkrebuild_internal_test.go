// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/evr"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRebuildPhase(current, rebuilt evr.EVR, compare int) *checkEVRRebuildPhase {
	return &checkEVRRebuildPhase{
		Source:        string(projectconfig.ReleaseCounterSourceReleaseTag),
		CounterFrom:   "20",
		CounterTo:     "21",
		CurrentInput:  &evr.Input{Component: "nss", SpecPath: "/staging/current/nss/nss.spec"},
		RebuiltInput:  &evr.Input{Component: "nss", SpecPath: "/staging/rebuilt/nss/nss.spec"},
		CurrentResult: &evr.Result{Component: "nss", EVR: &current},
		RebuiltResult: &evr.Result{Component: "nss", EVR: &rebuilt},
		Comparison:    &evr.ComparisonResult{Component: "nss", Compare: compare},
	}
}

// TestClassifyCheckEVRRebuildPhase_Verdicts injects evaluated EVR pairs directly so the verdict
// logic is covered without running rpmspec in a chroot.
func TestClassifyCheckEVRRebuildPhase_Verdicts(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		phase          *checkEVRRebuildPhase
		wantVerdict    CheckEVRVerdict
		wantNote       string
		wantNoCounters bool
	}{
		{
			name: "higher evr is ok",
			phase: newRebuildPhase(
				evr.EVR{Epoch: "0", Version: "1", Release: "20"},
				evr.EVR{Epoch: "0", Version: "1", Release: "21"}, checkEVRCompareCurrentIsNewer),
			wantVerdict: CheckEVRVerdictOK,
		},
		{
			name: "equal evr is an ineffective bump",
			phase: newRebuildPhase(
				evr.EVR{Epoch: "0", Version: "1", Release: "51.20121130"},
				evr.EVR{Epoch: "0", Version: "1", Release: "51.20121130"}, checkEVRCompareEqual),
			wantVerdict: CheckEVRVerdictBumpIneffective,
			wantNote:    "identical NEVRA",
		},
		{
			name: "lower evr is an ineffective bump",
			phase: newRebuildPhase(
				evr.EVR{Epoch: "0", Version: "1", Release: "10"},
				evr.EVR{Epoch: "0", Version: "1", Release: "9"}, checkEVRCompareCurrentIsOlder),
			wantVerdict: CheckEVRVerdictBumpIneffective,
			wantNote:    "lowered the evaluated EVR",
		},
		{
			name: "unexpected compare fails the check",
			phase: newRebuildPhase(
				evr.EVR{Epoch: "0", Version: "1", Release: "1"},
				evr.EVR{Epoch: "0", Version: "1", Release: "2"}, 7),
			wantVerdict: CheckEVRVerdictCheckFailed,
			wantNote:    "unexpected rpm.labelCompare result",
			// An unexpected comparison result is reported verbatim without the counter phrase.
			wantNoCounters: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			verdict, note := classifyCheckEVRRebuildPhase(testCase.phase)
			assert.Equal(t, testCase.wantVerdict, verdict)

			detail := checkEVRRebuildDetail(testCase.phase)
			assert.Equal(t, testCase.phase.CurrentResult.EVR.String(), detail.CurrentEVR)
			assert.Equal(t, testCase.phase.RebuiltResult.EVR.String(), detail.RebuiltEVR)
			assert.Equal(t, "20", detail.CounterFrom)
			assert.Equal(t, "21", detail.CounterTo)

			if testCase.wantNote == "" {
				assert.Empty(t, note)

				return
			}

			assert.Contains(t, note, testCase.wantNote)

			if !testCase.wantNoCounters {
				assert.Contains(t, note, "from `20` to `21`", "note should name the simulated counter bump")
			}
		})
	}
}

// TestClassifyCheckEVRRebuildPhase_Unavailable proves a component that cannot be evaluated is
// reported rather than silently passing, and that an unresolvable counter is distinguished from
// an infrastructure failure.
func TestClassifyCheckEVRRebuildPhase_Unavailable(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		phase       *checkEVRRebuildPhase
		wantVerdict CheckEVRVerdict
		wantNote    string
	}{
		{
			name:        "counter does not resolve",
			phase:       &checkEVRRebuildPhase{CounterError: "release counter regex did not match"},
			wantVerdict: CheckEVRVerdictCounterInvalid,
			wantNote:    "release counter regex did not match",
		},
		{
			name:        "counter bump failed",
			phase:       &checkEVRRebuildPhase{CounterError: "simulating release counter bump: boom"},
			wantVerdict: CheckEVRVerdictCounterInvalid,
			wantNote:    "simulating release counter bump: boom",
		},
		{
			name:        "preparation error",
			phase:       &checkEVRRebuildPhase{PreparationError: "staging rendered specs: boom"},
			wantVerdict: CheckEVRVerdictCheckFailed,
			wantNote:    "staging rendered specs: boom",
		},
		{
			name:        "missing current result",
			phase:       &checkEVRRebuildPhase{},
			wantVerdict: CheckEVRVerdictCheckFailed,
			wantNote:    "current SRPM EVR evaluation returned no result",
		},
		{
			name: "current evaluation error",
			phase: &checkEVRRebuildPhase{
				CurrentResult: &evr.Result{Component: "nss", Error: errors.New("rpmspec failed")},
			},
			wantVerdict: CheckEVRVerdictCheckFailed,
			wantNote:    "failed to evaluate current SRPM EVR",
		},
		{
			name: "rebuilt evaluation error",
			phase: &checkEVRRebuildPhase{
				CurrentResult: &evr.Result{Component: "nss", EVR: &evr.EVR{Epoch: "0", Version: "1", Release: "1"}},
				RebuiltResult: &evr.Result{Component: "nss", Error: errors.New("rpmspec failed")},
			},
			wantVerdict: CheckEVRVerdictCheckFailed,
			wantNote:    "failed to evaluate rebuilt SRPM EVR",
		},
		{
			name: "missing comparison",
			phase: &checkEVRRebuildPhase{
				CurrentResult: &evr.Result{Component: "nss", EVR: &evr.EVR{Epoch: "0", Version: "1", Release: "1"}},
				RebuiltResult: &evr.Result{Component: "nss", EVR: &evr.EVR{Epoch: "0", Version: "1", Release: "2"}},
			},
			wantVerdict: CheckEVRVerdictCheckFailed,
			wantNote:    "comparison returned no result",
		},
		{
			name: "comparison error",
			phase: &checkEVRRebuildPhase{
				CurrentResult: &evr.Result{Component: "nss", EVR: &evr.EVR{Epoch: "0", Version: "1", Release: "1"}},
				RebuiltResult: &evr.Result{Component: "nss", EVR: &evr.EVR{Epoch: "0", Version: "1", Release: "2"}},
				Comparison:    &evr.ComparisonResult{Component: "nss", Error: errors.New("bad EVR")},
			},
			wantVerdict: CheckEVRVerdictCheckFailed,
			wantNote:    "failed to compare simulated rebuild SRPM EVRs",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			verdict, note := classifyCheckEVRRebuildPhase(testCase.phase)

			assert.Equal(t, testCase.wantVerdict, verdict)
			assert.Contains(t, note, testCase.wantNote)
		})
	}
}

// TestStageOneCheckEVRRebuildPhase_BumpsOnlyTheStagedCopy proves the check is read-only with
// respect to the repository's rendered specs.
func TestStageOneCheckEVRRebuildPhase_BumpsOnlyTheStagedCopy(t *testing.T) {
	env := testutils.NewTestEnv(t)
	renderedDir := testNSSRenderedDir
	specContent := "Name: nss\nVersion: 1\nRelease: 20%{?dist}\nSummary: Test\nLicense: MIT\n"

	require.NoError(t, fileutils.MkdirAll(env.TestFS, renderedDir))
	require.NoError(t, fileutils.WriteFile(
		env.TestFS, filepath.Join(renderedDir, "nss.spec"), []byte(specContent), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(
		env.TestFS, filepath.Join(renderedDir, "fix.patch"), []byte("patch\n"), fileperms.PublicFile))

	phase := stageOneCheckEVRRebuildPhase(env.Env, checkEVRCounterSelection{
		Name:    "nss",
		Config:  &projectconfig.ComponentConfig{Name: "nss", RenderedSpecDir: renderedDir},
		Counter: sources.DefaultReleaseCounter(),
	}, "/staging")

	require.Empty(t, phase.PreparationError)
	require.Empty(t, phase.CounterError)
	assert.Equal(t, "20", phase.CounterFrom)
	assert.Equal(t, "21", phase.CounterTo)
	require.NotNil(t, phase.CurrentInput)
	require.NotNil(t, phase.RebuiltInput)

	original, err := fileutils.ReadFile(env.TestFS, filepath.Join(renderedDir, "nss.spec"))
	require.NoError(t, err)
	assert.Equal(t, specContent, string(original), "the repository's rendered spec must not be modified")

	staged, err := fileutils.ReadFile(env.TestFS, phase.CurrentInput.SpecPath)
	require.NoError(t, err)
	assert.Equal(t, specContent, string(staged))

	bumped, err := fileutils.ReadFile(env.TestFS, phase.RebuiltInput.SpecPath)
	require.NoError(t, err)
	assert.Contains(t, string(bumped), "Release: 21%{?dist}")

	// Sidecar files must be staged too, since the spec may reference them.
	sidecarExists, err := fileutils.Exists(env.TestFS, filepath.Join("/staging", "rebuilt", "nss", "fix.patch"))
	require.NoError(t, err)
	assert.True(t, sidecarExists)
}

// TestStageOneCheckEVRRebuildPhase_UnresolvableCounterIsRecorded proves one bad component is
// recorded as counter-invalid instead of aborting the run, and that nothing is staged for it.
func TestStageOneCheckEVRRebuildPhase_UnresolvableCounterIsRecorded(t *testing.T) {
	env := testutils.NewTestEnv(t)
	renderedDir := testNSSRenderedDir

	require.NoError(t, fileutils.MkdirAll(env.TestFS, renderedDir))
	require.NoError(t, fileutils.WriteFile(env.TestFS, filepath.Join(renderedDir, "nss.spec"),
		[]byte("Name: nss\nVersion: 1\nRelease: notacounter\nSummary: Test\nLicense: MIT\n"), fileperms.PublicFile))

	phase := stageOneCheckEVRRebuildPhase(env.Env, checkEVRCounterSelection{
		Name:    "nss",
		Config:  &projectconfig.ComponentConfig{Name: "nss", RenderedSpecDir: renderedDir},
		Counter: sources.DefaultReleaseCounter(),
	}, "/staging")

	assert.NotEmpty(t, phase.CounterError)
	assert.Nil(t, phase.CurrentInput)
	assert.Nil(t, phase.RebuiltInput)

	verdict, _ := classifyCheckEVRRebuildPhase(phase)
	assert.Equal(t, CheckEVRVerdictCounterInvalid, verdict)

	stagedExists, err := fileutils.Exists(env.TestFS, "/staging/current/nss")
	require.NoError(t, err)
	assert.False(t, stagedExists, "a component with an unresolvable counter must not be staged")
}

func TestStageOneCheckEVRRebuildPhase_MissingRenderedSpecDirIsRecorded(t *testing.T) {
	env := testutils.NewTestEnv(t)

	phase := stageOneCheckEVRRebuildPhase(env.Env, checkEVRCounterSelection{
		Name:    "nss",
		Config:  &projectconfig.ComponentConfig{Name: "nss"},
		Counter: sources.DefaultReleaseCounter(),
	}, "/staging")

	assert.Contains(t, phase.PreparationError, "no rendered spec directory")

	verdict, _ := classifyCheckEVRRebuildPhase(phase)
	assert.Equal(t, CheckEVRVerdictCheckFailed, verdict)
}

func TestStageOneCheckEVRRebuildPhase_SelectionErrorIsRecorded(t *testing.T) {
	env := testutils.NewTestEnv(t)

	phase := stageOneCheckEVRRebuildPhase(env.Env, checkEVRCounterSelection{
		Name:           "nss",
		Config:         &projectconfig.ComponentConfig{Name: "nss"},
		Counter:        sources.DefaultReleaseCounter(),
		SelectionError: "reading Release tag from rendered spec: boom",
	}, "/staging")

	assert.Contains(t, phase.PreparationError, "reading Release tag")
}

// TestCheckEVRRebuildInputs_SkipsUnstagedRecords proves failed components are excluded from the
// batched rpmspec manifests without dropping the other components.
func TestCheckEVRRebuildInputs_SkipsUnstagedRecords(t *testing.T) {
	current := evr.EVR{Epoch: "0", Version: "1", Release: "1"}
	records := []*checkEVRRecord{
		{Component: "broken", Rebuild: &checkEVRRebuildPhase{CounterError: "boom"}},
		{Component: "unmanaged"},
		{Component: "nss", Rebuild: newRebuildPhase(current, current, checkEVRCompareEqual)},
	}

	currentInputs, rebuiltInputs := checkEVRRebuildInputs(records)

	require.Len(t, currentInputs, 1)
	require.Len(t, rebuiltInputs, 1)
	assert.Equal(t, "nss", currentInputs[0].Component)
	assert.True(t, strings.HasPrefix(rebuiltInputs[0].SpecPath, "/staging/rebuilt/"))
}

// TestCheckEVRRebuildComparisons_SkipsFailedExtractions proves a failed extraction never reaches
// labelCompare with a nil EVR.
func TestCheckEVRRebuildComparisons_SkipsFailedExtractions(t *testing.T) {
	records := []*checkEVRRecord{
		{Component: "broken", Rebuild: &checkEVRRebuildPhase{
			CurrentResult: &evr.Result{Component: "broken", Error: errors.New("rpmspec failed")},
			RebuiltResult: &evr.Result{Component: "broken", EVR: &evr.EVR{Epoch: "0", Version: "1", Release: "2"}},
		}},
		{Component: "unmanaged"},
		{Component: "nss", Rebuild: &checkEVRRebuildPhase{
			CurrentResult: &evr.Result{Component: "nss", EVR: &evr.EVR{Epoch: "0", Version: "1", Release: "1"}},
			RebuiltResult: &evr.Result{Component: "nss", EVR: &evr.EVR{Epoch: "0", Version: "1", Release: "2"}},
		}},
	}

	comparisons := checkEVRRebuildComparisons(records)

	require.Len(t, comparisons, 1)
	assert.Equal(t, "nss", comparisons[0].Component)
	assert.Equal(t, "1", comparisons[0].Previous.Release)
	assert.Equal(t, "2", comparisons[0].Current.Release)
}
