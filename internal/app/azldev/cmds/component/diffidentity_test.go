// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"encoding/json"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeDiff(t *testing.T) {
	t.Run("all categories", func(t *testing.T) {
		base := map[string]string{
			"curl":    "sha256:aaa",
			"wget":    "sha256:bbb",
			"openssl": "sha256:ccc",
			"libold":  "sha256:fff",
		}
		head := map[string]string{
			"curl":    "sha256:aaa",
			"wget":    "sha256:ddd",
			"libfoo":  "sha256:eee",
			"openssl": "sha256:ccc",
		}

		report := component.ComputeDiff(base, head, false)

		assert.Equal(t, []string{"wget"}, report.Changed)
		assert.Equal(t, []string{"libfoo"}, report.Added)
		assert.Equal(t, []string{"libold"}, report.Removed)
		assert.Equal(t, []string{"curl", "openssl"}, report.Unchanged)
	})

	t.Run("removed component", func(t *testing.T) {
		base := map[string]string{
			"curl":   "sha256:aaa",
			"libfoo": "sha256:bbb",
		}
		head := map[string]string{
			"curl": "sha256:aaa",
		}

		report := component.ComputeDiff(base, head, false)

		assert.Empty(t, report.Changed)
		assert.Empty(t, report.Added)
		assert.Equal(t, []string{"libfoo"}, report.Removed)
		assert.Equal(t, []string{"curl"}, report.Unchanged)
	})

	t.Run("empty base", func(t *testing.T) {
		base := map[string]string{}
		head := map[string]string{
			"curl": "sha256:aaa",
			"wget": "sha256:bbb",
		}

		report := component.ComputeDiff(base, head, false)

		assert.Empty(t, report.Changed)
		assert.Equal(t, []string{"curl", "wget"}, report.Added)
		assert.Empty(t, report.Removed)
		assert.Empty(t, report.Unchanged)
	})

	t.Run("empty head", func(t *testing.T) {
		base := map[string]string{
			"curl": "sha256:aaa",
		}
		head := map[string]string{}

		report := component.ComputeDiff(base, head, false)

		assert.Empty(t, report.Changed)
		assert.Empty(t, report.Added)
		assert.Equal(t, []string{"curl"}, report.Removed)
		assert.Empty(t, report.Unchanged)
	})

	t.Run("both empty", func(t *testing.T) {
		report := component.ComputeDiff(map[string]string{}, map[string]string{}, false)

		assert.Empty(t, report.Changed)
		assert.Empty(t, report.Added)
		assert.Empty(t, report.Removed)
		assert.Empty(t, report.Unchanged)
	})

	t.Run("identical", func(t *testing.T) {
		both := map[string]string{
			"curl":    "sha256:aaa",
			"openssl": "sha256:bbb",
		}

		report := component.ComputeDiff(both, both, false)

		assert.Empty(t, report.Changed)
		assert.Empty(t, report.Added)
		assert.Empty(t, report.Removed)
		assert.Equal(t, []string{"curl", "openssl"}, report.Unchanged)
	})

	t.Run("sorted output", func(t *testing.T) {
		base := map[string]string{
			"zlib":    "sha256:aaa",
			"curl":    "sha256:bbb",
			"openssl": "sha256:ccc",
		}
		head := map[string]string{
			"zlib":    "sha256:xxx",
			"curl":    "sha256:yyy",
			"openssl": "sha256:ccc",
		}

		report := component.ComputeDiff(base, head, false)

		assert.Equal(t, []string{"curl", "zlib"}, report.Changed, "changed list should be sorted")
	})

	t.Run("changed only", func(t *testing.T) {
		base := map[string]string{
			"curl":    "sha256:aaa",
			"wget":    "sha256:bbb",
			"openssl": "sha256:ccc",
			"libold":  "sha256:fff",
		}
		head := map[string]string{
			"curl":    "sha256:aaa",
			"wget":    "sha256:ddd",
			"libfoo":  "sha256:eee",
			"openssl": "sha256:ccc",
		}

		report := component.ComputeDiff(base, head, true)

		assert.Equal(t, []string{"wget"}, report.Changed)
		assert.Equal(t, []string{"libfoo"}, report.Added)
		assert.Empty(t, report.Removed, "removed should be empty with changedOnly")
		assert.Empty(t, report.Unchanged, "unchanged should be empty with changedOnly")
	})
}

func TestDiffIdentities_MissingFile(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	_, err := component.DiffIdentities(testEnv.Env, "/nonexistent/base.json", "/nonexistent/head.json", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base identity file")
}

func TestDiffIdentities_MalformedJSON(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, "/base.json",
		[]byte("not valid json"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, "/head.json",
		[]byte(`[{"component":"a","fingerprint":"sha256:aaa"}]`), fileperms.PublicFile))

	_, err := component.DiffIdentities(testEnv.Env, "/base.json", "/head.json", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base identity file")
}

func TestDiffIdentities_ValidFiles(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, "/base.json",
		[]byte(`[{"component":"curl","fingerprint":"sha256:aaa"}]`), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, "/head.json",
		[]byte(`[{"component":"curl","fingerprint":"sha256:bbb"},{"component":"wget","fingerprint":"sha256:ccc"}]`),
		fileperms.PublicFile))

	result, err := component.DiffIdentities(testEnv.Env, "/base.json", "/head.json", false)
	require.NoError(t, err)

	// Default format is table, so we get []IdentityDiffResult.
	tableResults, ok := result.([]component.IdentityDiffResult)
	require.True(t, ok, "expected table results for default report format")
	require.Len(t, tableResults, 2)
}

func TestDiffIdentities_EmptyArray(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, "/base.json",
		[]byte(`[]`), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, "/head.json",
		[]byte(`[]`), fileperms.PublicFile))

	result, err := component.DiffIdentities(testEnv.Env, "/base.json", "/head.json", false)
	require.NoError(t, err)

	tableResults, ok := result.([]component.IdentityDiffResult)
	require.True(t, ok)
	assert.Empty(t, tableResults)
}

func TestDiffIdentities_JSONFormat(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Env.SetDefaultReportFormat(azldev.ReportFormatJSON)

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, "/base.json",
		[]byte(`[{"component":"curl","fingerprint":"sha256:aaa"}]`), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, "/head.json",
		[]byte(`[{"component":"curl","fingerprint":"sha256:bbb"},{"component":"wget","fingerprint":"sha256:ccc"}]`),
		fileperms.PublicFile))

	result, err := component.DiffIdentities(testEnv.Env, "/base.json", "/head.json", false)
	require.NoError(t, err)

	report, ok := result.(*component.IdentityDiffReport)
	require.True(t, ok, "expected IdentityDiffReport for JSON format")

	assert.Equal(t, []string{"curl"}, report.Changed)
	assert.Equal(t, []string{"wget"}, report.Added)
	assert.Empty(t, report.Removed)
	assert.Empty(t, report.Unchanged)

	// Verify JSON serialization produces [] not null for empty arrays.
	jsonBytes, err := json.Marshal(report)
	require.NoError(t, err)

	jsonStr := string(jsonBytes)
	assert.Contains(t, jsonStr, `"removed":[]`)
	assert.Contains(t, jsonStr, `"unchanged":[]`)
	assert.NotContains(t, jsonStr, "null")
}
