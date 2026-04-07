// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Testing unexported parseBatchJSON.
package sources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBatchJSON_Success(t *testing.T) {
	t.Parallel()

	stdout := `[{"name":"curl","specFiles":"Source0: curl-8.5.0.tar.xz\nPatch0: fix.patch","error":null}]`
	inputs := []ComponentInput{{Name: "curl", SpecFilename: "curl.spec"}}

	results, err := parseBatchJSON(stdout, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "curl", results[0].Name)
	require.NoError(t, results[0].Error)
	assert.Equal(t, []string{"curl-8.5.0.tar.xz", "fix.patch"}, results[0].SpecFiles)
}

func TestParseBatchJSON_RpmautospecFailed(t *testing.T) {
	t.Parallel()

	stdout := `[{"name":"broken","specFiles":"","error":"rpmautospec failed: could not process spec"}]`
	inputs := []ComponentInput{{Name: "broken", SpecFilename: "broken.spec"}}

	results, err := parseBatchJSON(stdout, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Error(t, results[0].Error)
	assert.Contains(t, results[0].Error.Error(), "rpmautospec failed")
	assert.Contains(t, results[0].Error.Error(), "could not process spec")
}

func TestParseBatchJSON_SpectoolFailed(t *testing.T) {
	t.Parallel()

	stdout := `[{"name":"badspec","specFiles":"","error":"spectool failed: query of specfile failed"}]`
	inputs := []ComponentInput{{Name: "badspec", SpecFilename: "badspec.spec"}}

	results, err := parseBatchJSON(stdout, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Error(t, results[0].Error)
	assert.Contains(t, results[0].Error.Error(), "spectool failed")
}

func TestParseBatchJSON_MissingComponent(t *testing.T) {
	t.Parallel()

	// JSON doesn't include a result for "ghost".
	stdout := `[]`
	inputs := []ComponentInput{{Name: "ghost", SpecFilename: "ghost.spec"}}

	results, err := parseBatchJSON(stdout, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Error(t, results[0].Error)
	assert.Contains(t, results[0].Error.Error(), "no result returned")
}

func TestParseBatchJSON_MultipleComponents(t *testing.T) {
	t.Parallel()

	stdout := `[
		{"name":"good","specFiles":"Source0: good-1.0.tar.gz","error":null},
		{"name":"bad","specFiles":"","error":"rpmautospec failed: boom"}
	]`

	inputs := []ComponentInput{
		{Name: "good", SpecFilename: "good.spec"},
		{Name: "bad", SpecFilename: "bad.spec"},
	}

	results, err := parseBatchJSON(stdout, inputs)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.NoError(t, results[0].Error)
	assert.Equal(t, []string{"good-1.0.tar.gz"}, results[0].SpecFiles)
	require.Error(t, results[1].Error)
	assert.Contains(t, results[1].Error.Error(), "boom")
}

func TestParseBatchJSON_InvalidJSON(t *testing.T) {
	t.Parallel()

	inputs := []ComponentInput{{Name: "any", SpecFilename: "any.spec"}}

	_, err := parseBatchJSON("not json{{{", inputs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing batch results JSON")
}
