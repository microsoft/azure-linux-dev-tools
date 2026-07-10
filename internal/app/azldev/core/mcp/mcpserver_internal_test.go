// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mcp

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	mcpapi "github.com/mark3labs/mcp-go/mcp"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleToolCallMarksCommandError(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	root := &cobra.Command{Use: "azldev"}
	cmd := &cobra.Command{
		Use: "fail",
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("boom")
		},
	}
	root.AddCommand(cmd)

	result, err := handleToolCall(testEnv.Env, cmd)(t.Context(), mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{Arguments: map[string]any{}},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(mcpapi.TextContent)
	require.True(t, ok)
	assert.Equal(t, "boom", text.Text)
}

// TestCaptureStdoutLargeOutput is a regression guard for a pipe deadlock: capturing
// output larger than the OS pipe buffer (~64KB) must not block. A command such as
// 'config dump -f json' on a large distro emits >1MB; without a concurrent drain
// the write blocks and hangs the server. The timeout turns a regression into a
// clean failure instead of a hang.
func TestCaptureStdoutLargeOutput(t *testing.T) {
	want := strings.Repeat("x", 1<<20) // 1 MiB, well beyond the pipe buffer

	type result struct {
		out string
		err error
	}

	done := make(chan result, 1)

	go func() {
		out, err := captureStdout(func() error {
			_, writeErr := fmt.Fprint(os.Stdout, want)

			return writeErr
		})
		done <- result{out: out, err: err}
	}()

	select {
	case got := <-done:
		require.NoError(t, got.err)
		assert.Equal(t, want, got.out)
	case <-time.After(10 * time.Second):
		t.Fatal("captureStdout deadlocked on output larger than the pipe buffer")
	}
}

// TestCaptureStdoutReturnsFnError confirms captureStdout returns fn's error alongside
// whatever was written before it failed.
func TestCaptureStdoutReturnsFnError(t *testing.T) {
	out, err := captureStdout(func() error {
		fmt.Fprint(os.Stdout, "partial")

		return errors.New("boom")
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	assert.Equal(t, "partial", out)
}

func TestCaptureStdoutRestoresStdoutAfterPanic(t *testing.T) {
	origStdout := os.Stdout

	assert.PanicsWithValue(t, "boom", func() {
		_, _ = captureStdout(func() error {
			fmt.Fprint(os.Stdout, "partial")

			panic("boom")
		})
	})

	assert.Same(t, origStdout, os.Stdout)
}
