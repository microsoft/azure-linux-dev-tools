// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runHistory runs `azldev component history` with the given args and returns
// parsed JSON results. Fails the test on any error. Decodes into the real
// [componentcmds.HistoryResult] so the test stays in sync with the command's
// schema automatically.
func runHistory(t *testing.T, azldevBin, projectDir string, extraArgs ...string) []componentcmds.HistoryResult {
	t.Helper()

	args := []string{"-C", projectDir, "--no-default-config", "component", "history"}
	args = append(args, extraArgs...)
	args = append(args, "-q", "-O", "json")

	cmd := exec.CommandContext(t.Context(), azldevBin, args...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "azldev failed: %s", string(out))

	var results []componentcmds.HistoryResult
	require.NoError(t, json.Unmarshal(out, &results), "failed to parse JSON: %s", string(out))

	return results
}

// historyMap converts a slice of [componentcmds.HistoryResult] into a map keyed
// by component name.
func historyMap(results []componentcmds.HistoryResult) map[string]componentcmds.HistoryResult {
	m := make(map[string]componentcmds.HistoryResult, len(results))
	for _, r := range results {
		m[r.Name] = r
	}

	return m
}

// TestComponentHistory_Smoke exercises the `azldev component history` command
// end-to-end with a real git repository, verifying that:
//   - customized components are reported with their customization count and items,
//   - bare components are excluded by default,
//   - `--include-bare` brings the bare components back into the output.
//
// This is a smoke test — it doesn't validate every metric, just that the command
// runs, emits valid JSON, and respects the most common filtering flag.
func TestComponentHistory_Smoke(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	azldevBin, projectDir := setupProjectWithGit(t,
		[]*projecttest.TestSpec{
			projecttest.NewSpec(
				projecttest.WithName("curl"),
				projecttest.WithVersion("8.0.0"),
				projecttest.WithRelease("1%{?dist}"),
				projecttest.WithBuildArch(projecttest.NoArch),
			),
		},
		[]*projectconfig.ComponentConfig{
			{
				// curl has explicit customizations so it should appear by default.
				Name: "curl",
				Spec: projectconfig.SpecSource{
					SourceType: projectconfig.SpecSourceTypeLocal,
					Path:       filepath.Join("specs", "curl", "curl.spec"),
				},
				Build: projectconfig.ComponentBuildConfig{
					With: []string{"feature-a"},
				},
			},
			{
				// bash is truly bare: no Spec, no Build, no anything. The
				// collectors emit zero items so it gets filtered out unless
				// --include-bare is passed.
				Name: "bash",
			},
		},
		nil,
	)

	// Commit everything so the project has a git history for toml-commit counting.
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "initial")

	// Seed two commits to curl's lock file with distinct fingerprints so the
	// fp-change details path has something to report.
	writeFileInDir(t, projectDir, "locks/curl.lock",
		`version = 1`+"\n"+`input-fingerprint = "sha256:curl-v1"`+"\n")
	gitInDir(t, projectDir, "add", "locks/curl.lock")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "curl: initial lock")

	writeFileInDir(t, projectDir, "locks/curl.lock",
		`version = 1`+"\n"+`input-fingerprint = "sha256:curl-v2"`+"\n")
	gitInDir(t, projectDir, "add", "locks/curl.lock")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "curl: bump fingerprint")

	// Default run: bare components are filtered out.
	results := runHistory(t, azldevBin, projectDir, "-a")
	rm := historyMap(results)

	require.Contains(t, rm, "curl", "customized component should be reported by default")
	require.NotContains(t, rm, "bash", "bare component should be filtered out by default")

	curl := rm["curl"]
	// curl's config sets exactly two fingerprintable fields: Build.With and the
	// explicit Spec.SourceType=local. Pin the exact count and Kinds so a
	// regression in either collector (or an accidental extra emission) is caught
	// rather than masked by a loose >= comparison.
	assert.Equal(t, 2, curl.Customizations, "curl should have exactly two customizations")
	assert.ElementsMatch(t, []string{"build.with", "spec.source-type"}, customizationKinds(curl),
		"curl's customization Kinds should be exactly build.with and spec.source-type")
	assert.NotEmpty(t, curl.TomlPath, "curl's source TOML path should be populated")
	// Both components are defined in the single azldev.toml, so it is shared and
	// the one initial commit that created it is the only one touching it.
	assert.True(t, curl.SharedToml, "curl shares azldev.toml with bash")
	assert.Equal(t, 1, curl.TomlCommits, "only the initial commit touched the shared azldev.toml")

	// Fingerprint-change details: should include both lock commits with full
	// author / message metadata sourced from the synthetic-distgit
	// FingerprintChange type via the local DTO copy.
	require.Equal(t, 2, curl.FingerprintChanges, "expected two fingerprint changes")
	require.Len(t, curl.FingerprintChangeDetails, curl.FingerprintChanges,
		"FingerprintChangeDetails length must match FingerprintChanges count")

	for i, change := range curl.FingerprintChangeDetails {
		assert.NotEmpty(t, change.Hash, "change[%d].Hash should be populated", i)
		assert.NotEmpty(t, change.Author, "change[%d].Author should be populated", i)
		assert.NotEmpty(t, change.Message, "change[%d].Message should be populated", i)
		assert.Positive(t, change.Timestamp, "change[%d].Timestamp should be populated", i)
	}

	// With --include-bare both components show up. With more than one result,
	// FingerprintChangeDetails is suppressed in JSON output to keep responses
	// bounded on -a runs (count is still populated).
	results = runHistory(t, azldevBin, projectDir, "-a", "--include-bare")
	rm = historyMap(results)

	require.Contains(t, rm, "curl", "customized component should still be reported with --include-bare")
	require.Contains(t, rm, "bash", "bare component should be reported with --include-bare")
	assert.Equal(t, 0, rm["bash"].Customizations, "bash has no customizations")
	assert.Equal(t, 2, rm["curl"].FingerprintChanges,
		"FingerprintChanges count should still be populated on multi-result runs")
	assert.Nil(t, rm["curl"].FingerprintChangeDetails,
		"FingerprintChangeDetails should be suppressed when more than one component is reported")

	// Explicit single-component query for a bare component: --include-bare
	// is force-disabled so the user gets the row they asked for.
	results = runHistory(t, azldevBin, projectDir, "bash")
	rm = historyMap(results)

	require.Contains(t, rm, "bash",
		"explicit positional name should override --include-bare and return the row")
	assert.Equal(t, 0, rm["bash"].Customizations)

	// Explicit single-component query for curl: even though curl shares its TOML,
	// being the only surviving row means FingerprintChangeDetails is retained
	// (the multi-result suppression only kicks in with >1 row).
	results = runHistory(t, azldevBin, projectDir, "curl")
	rm = historyMap(results)
	require.Contains(t, rm, "curl")
	require.Len(t, results, 1, "explicit single-component query returns exactly one row")
	assert.Len(t, rm["curl"].FingerprintChangeDetails, 2,
		"single surviving row retains its FingerprintChangeDetails")

	// --shared=omit without an explicit selection drops shared-TOML rows. Both
	// curl and bash live in the shared azldev.toml, so the omit run is empty.
	results = runHistory(t, azldevBin, projectDir, "-a", "--include-bare", "--shared=omit")
	rm = historyMap(results)
	assert.NotContains(t, rm, "curl", "--shared=omit drops shared-TOML rows without explicit selection")
	assert.NotContains(t, rm, "bash", "--shared=omit drops shared-TOML rows without explicit selection")

	// An explicit positional selection overrides --shared=omit: the user asked
	// for curl by name, so they get it back even though its TOML is shared.
	results = runHistory(t, azldevBin, projectDir, "curl", "--shared=omit")
	rm = historyMap(results)
	require.Contains(t, rm, "curl",
		"explicit selection overrides --shared=omit")
}

// customizationKinds returns the set of CustomizationItem Kinds in a result,
// deduplicated, for order-independent assertions.
func customizationKinds(r componentcmds.HistoryResult) []string {
	seen := make(map[string]bool, len(r.CustomizationItems))
	kinds := make([]string, 0, len(r.CustomizationItems))

	for _, item := range r.CustomizationItems {
		if seen[item.Kind] {
			continue
		}

		seen[item.Kind] = true

		kinds = append(kinds, item.Kind)
	}

	return kinds
}
