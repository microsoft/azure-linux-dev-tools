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
			projecttest.NewSpec(
				projecttest.WithName("bash"),
				projecttest.WithVersion("5.2.0"),
				projecttest.WithRelease("1%{?dist}"),
				projecttest.WithBuildArch(projecttest.NoArch),
			),
		},
		[]*projectconfig.ComponentConfig{
			{
				// curl has a customization (build.with), so it should appear by default.
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
				// bash is bare (no customizations) — filtered out unless --include-bare.
				Name: "bash",
				Spec: projectconfig.SpecSource{
					SourceType: projectconfig.SpecSourceTypeLocal,
					Path:       filepath.Join("specs", "bash", "bash.spec"),
				},
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
	assert.GreaterOrEqual(t, curl.Customizations, 1, "curl should have at least one customization")
	assert.NotEmpty(t, curl.CustomizationItems, "curl should have customization items in JSON output")
	assert.NotEmpty(t, curl.TomlPath, "curl's source TOML path should be populated")
	assert.GreaterOrEqual(t, curl.TomlCommits, 1, "curl's TOML should have at least one commit")

	// fp-change details: should include both lock commits with full author /
	// message metadata sourced from the synthetic-distgit FingerprintChange type.
	require.Equal(t, 2, curl.FpChanges, "expected two fingerprint changes")
	require.Len(t, curl.FpChangeDetails, curl.FpChanges,
		"FpChangeDetails length must match FpChanges count")

	for i, change := range curl.FpChangeDetails {
		assert.NotEmpty(t, change.Hash, "change[%d].Hash should be populated", i)
		assert.NotEmpty(t, change.Author, "change[%d].Author should be populated", i)
		assert.NotEmpty(t, change.Message, "change[%d].Message should be populated", i)
		assert.Positive(t, change.Timestamp, "change[%d].Timestamp should be populated", i)
	}

	// With --include-bare both components show up.
	results = runHistory(t, azldevBin, projectDir, "-a", "--include-bare")
	rm = historyMap(results)

	require.Contains(t, rm, "curl", "customized component should still be reported with --include-bare")
	require.Contains(t, rm, "bash", "bare component should be reported with --include-bare")
	assert.Equal(t, 0, rm["bash"].Customizations, "bash has no customizations")
}
