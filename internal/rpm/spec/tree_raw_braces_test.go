// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec //nolint:testpackage // Tests access unexported parser tree types.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTreeKeepsRawBracesInsideExpandBody(t *testing.T) {
	lines := []string{
		"%global date_corpus %{expand:",
		"for date in 2024-02-29 2025-02-28; do",
		`  if { test "${date#????-??-??}" = "$date"; }; then`,
		"    %if 0",
		"    printf '%s\\n' %{date}",
		"    %endif",
		"  fi",
		"done",
		"}",
		"%build",
		"echo %{date_corpus}",
	}

	tree, err := parseTree(lines)

	require.NoError(t, err)
	assert.Equal(t, lines, serializeTree(tree))
	require.Len(t, tree.Children, 2)
	require.Len(t, tree.Children[0].Children, 1)
	assert.Equal(t, lines[:9], tree.Children[0].Children[0].Lines)
}
