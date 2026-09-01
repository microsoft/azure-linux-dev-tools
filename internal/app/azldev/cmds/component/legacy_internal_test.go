// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyCommandsAreHiddenNoOps(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		args     []string
		expected string
	}{
		{
			name:     "update",
			command:  "update",
			args:     []string{"-a", "--bump", "curl"},
			expected: legacyUpdateMessage,
		},
		{
			name:     "history",
			command:  "history",
			args:     []string{"-a", "--include-bare", "-O", "json"},
			expected: legacyHistoryMessage,
		},
		{
			name:     "history alias",
			command:  "hist",
			args:     []string{"curl"},
			expected: legacyHistoryMessage,
		},
		{
			name:     "query",
			command:  "query",
			args:     []string{"-p", "curl", "--arch", "aarch64", "-O", "json"},
			expected: legacyQueryMessage,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := &cobra.Command{Use: "component"}
			legacyOnAppInit(nil, parent)

			var output bytes.Buffer
			parent.SetOut(&output)
			parent.SetErr(&output)
			parent.SetArgs(append([]string{test.command}, test.args...))

			require.NoError(t, parent.Execute())
			assert.Equal(t, test.expected+"\n", output.String())

			command, _, err := parent.Find([]string{test.command})
			require.NoError(t, err)
			assert.True(t, command.Hidden)
			assert.True(t, command.DisableFlagParsing)
		})
	}
}
