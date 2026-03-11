// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package dirdiff_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/dirdiff"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// change is a helper struct matching the JSON shape produced by [dirdiff.FileDiff.MarshalJSON].
type change struct {
	Line    int    `json:"line"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

// marshalChanges is a test helper that marshals a [dirdiff.FileDiff] with the given unified diff
// text and returns the parsed change records.
func marshalChanges(t *testing.T, unifiedDiff string) []change {
	t.Helper()

	fileDiff := dirdiff.FileDiff{
		Path:        "test",
		Status:      dirdiff.FileStatusModified,
		UnifiedDiff: unifiedDiff,
	}

	data, err := json.Marshal(fileDiff)
	require.NoError(t, err)

	var parsed struct {
		Changes []change `json:"changes"`
	}

	require.NoError(t, json.Unmarshal(data, &parsed))

	return parsed.Changes
}

func TestMarshalJSON_ContentLinesWithDashDashPrefix(t *testing.T) {
	// A removed line whose content starts with "-- " produces a diff line "--- ..."
	// which must NOT be confused with a file header.
	diff := strings.Join([]string{
		"--- a/query.sql",
		"+++ b/query.sql",
		"@@ -1,2 +1,1 @@",
		"--- this is a SQL comment",
		" SELECT 1;",
	}, "\n")

	changes := marshalChanges(t, diff)

	require.Len(t, changes, 1)
	assert.Equal(t, "remove", changes[0].Type)
	assert.Equal(t, "-- this is a SQL comment", changes[0].Content)
	assert.Equal(t, 1, changes[0].Line)
}

func TestMarshalJSON_ContentLinesWithPlusPlusPrefix(t *testing.T) {
	// An added line whose content starts with "++ " produces a diff line "+++ ..."
	// which must NOT be confused with a file header.
	diff := strings.Join([]string{
		"--- a/counter.txt",
		"+++ b/counter.txt",
		"@@ -1,1 +1,2 @@",
		" line one",
		"+++ increment counter",
	}, "\n")

	changes := marshalChanges(t, diff)

	require.Len(t, changes, 1)
	assert.Equal(t, "add", changes[0].Type)
	assert.Equal(t, "++ increment counter", changes[0].Content)
	assert.Equal(t, 2, changes[0].Line)
}

func TestMarshalJSON_NoNewlineMarkerIgnored(t *testing.T) {
	// The "\ No newline at end of file" marker must not advance line counters.
	diff := strings.Join([]string{
		"--- a/file.txt",
		"+++ b/file.txt",
		"@@ -1,2 +1,2 @@",
		"-old last line",
		`\ No newline at end of file`,
		"+new last line",
		`\ No newline at end of file`,
		" unchanged",
	}, "\n")

	changes := marshalChanges(t, diff)

	require.Len(t, changes, 2)

	assert.Equal(t, "remove", changes[0].Type)
	assert.Equal(t, "old last line", changes[0].Content)
	assert.Equal(t, 1, changes[0].Line)

	assert.Equal(t, "add", changes[1].Type)
	assert.Equal(t, "new last line", changes[1].Content)
	assert.Equal(t, 1, changes[1].Line)
}

func TestMarshalJSON_BasicDiff(t *testing.T) {
	// Sanity check: a straightforward one-line change still works.
	diff := strings.Join([]string{
		"--- a/hello.txt",
		"+++ b/hello.txt",
		"@@ -1,1 +1,1 @@",
		"-hello",
		"+world",
	}, "\n")

	changes := marshalChanges(t, diff)

	require.Len(t, changes, 2)
	assert.Equal(t, change{Line: 1, Type: "remove", Content: "hello"}, changes[0])
	assert.Equal(t, change{Line: 1, Type: "add", Content: "world"}, changes[1])
}
