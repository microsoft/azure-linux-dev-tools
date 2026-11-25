// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocklessBumpspecComment(t *testing.T) {
	for _, testCase := range []struct {
		name, message, changes, want string
	}{
		{name: "blank", changes: " \n", want: "temporary commit message"},
		{name: "single change", changes: "abc123 Fix upstream", want: "temporary commit message\n- abc123 Fix upstream"},
		{
			name:    "multiple changes with trailing newline",
			changes: "abc123 Fix upstream\ndef456 Add feature\n",
			want:    "temporary commit message\n- abc123 Fix upstream\n- def456 Add feature",
		},
		{name: "message without changes", message: "Rebuild for foo", want: "Rebuild for foo"},
		{
			name: "message with changes", message: "Rebuild for foo", changes: "abc123 Fix upstream",
			want: "Rebuild for foo\n- abc123 Fix upstream",
		},
		{name: "blank message", message: "  ", changes: "abc123", want: "temporary commit message\n- abc123"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, locklessBumpspecComment(testCase.message, testCase.changes))
		})
	}
}
