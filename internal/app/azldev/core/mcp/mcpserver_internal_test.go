// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	mcpapi "github.com/mark3labs/mcp-go/mcp"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStdioServerServesToolsDuringSubscription(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	root := &cobra.Command{Use: "azldev"}
	cmd := &cobra.Command{
		Use: "echo",
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(testEnv.Env.ReportFile(), "ready")

			return err
		},
	}
	azldev.ExportAsReadOnlyMCPTool(cmd)
	root.AddCommand(cmd)

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	closePipes := func() {
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
	}
	stopClosing := context.AfterFunc(ctx, closePipes)
	done := make(chan error, 1)

	go func() {
		done <- newStdioServer(testEnv.Env, root).Listen(ctx, stdinReader, stdoutWriter)
	}()

	t.Cleanup(func() {
		cancel()
		stopClosing()
		closePipes()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("stdio server did not stop after cancellation")
		}
	})

	type response struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}

	responses := make(chan response)
	readErrors := make(chan error, 1)

	go func() {
		decoder := json.NewDecoder(stdoutReader)

		for {
			var message response
			if err := decoder.Decode(&message); err != nil {
				readErrors <- err

				return
			}

			select {
			case responses <- message:
			case <-ctx.Done():
				return
			}
		}
	}()

	encoder := json.NewEncoder(stdinWriter)
	send := func(requestID, method string, params map[string]any) {
		t.Helper()

		params["_meta"] = map[string]any{
			mcpapi.MetaKeyProtocolVersion:    mcpapi.ProtocolVersion20260728,
			mcpapi.MetaKeyClientCapabilities: map[string]any{},
		}
		require.NoError(t, encoder.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      requestID,
			"method":  method,
			"params":  params,
		}))
	}
	receive := func() response {
		t.Helper()

		select {
		case message := <-responses:
			require.Empty(t, message.Error)

			return message
		case err := <-readErrors:
			t.Fatalf("reading stdio response: %v", err)
		case <-ctx.Done():
			t.Fatal("stdio request timed out while subscription was open")
		}

		return response{}
	}

	send("discover", "server/discover", map[string]any{})

	discovered := receive()
	require.Equal(t, "discover", discovered.ID)

	var discovery mcpapi.DiscoverResult
	require.NoError(t, json.Unmarshal(discovered.Result, &discovery))
	require.Contains(t, discovery.SupportedVersions, mcpapi.ProtocolVersion20260728)

	send("subscription", "subscriptions/listen", map[string]any{
		"notifications": map[string]any{
			"toolsListChanged":     true,
			"resourcesListChanged": true,
		},
	})
	require.Equal(t, string(mcpapi.MethodNotificationSubscriptionsAcknowledged), receive().Method)

	for range 2 {
		send("list", "tools/list", map[string]any{})

		listed := receive()
		require.Equal(t, "list", listed.ID)

		var tools mcpapi.ListToolsResult
		require.NoError(t, json.Unmarshal(listed.Result, &tools))
		require.Len(t, tools.Tools, 1)
		assert.Equal(t, "echo", tools.Tools[0].Name)

		send("call", "tools/call", map[string]any{
			"name":      "echo",
			"arguments": map[string]any{},
		})

		called := receive()
		require.Equal(t, "call", called.ID)

		var result mcpapi.CallToolResult
		require.NoError(t, json.Unmarshal(called.Result, &result))
		require.False(t, result.IsError)
		require.Len(t, result.Content, 1)
		text, ok := result.Content[0].(mcpapi.TextContent)
		require.True(t, ok)
		assert.Equal(t, "ready", text.Text)
	}
}

func TestHandleToolCallMarksCommandError(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	root := &cobra.Command{Use: "azldev"}
	cmd := &cobra.Command{
		Use: "fail",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprint(os.Stdout, "partial")

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
	assert.Equal(t, "partial\nboom", text.Text)
}

func TestHandleToolCallRestoresReportFile(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	reportOutput := &bytes.Buffer{}
	testEnv.Env.SetReportFile(reportOutput)

	root := &cobra.Command{Use: "azldev"}
	cmd := &cobra.Command{
		Use: "report",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprint(testEnv.Env.ReportFile(), "captured")

			return nil
		},
	}
	root.AddCommand(cmd)

	result, err := handleToolCall(testEnv.Env, cmd)(t.Context(), mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{Arguments: map[string]any{}},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(mcpapi.TextContent)
	require.True(t, ok)
	assert.Equal(t, "captured", text.Text)
	assert.Same(t, reportOutput, testEnv.Env.ReportFile())
	_, writeErr := fmt.Fprint(testEnv.Env.ReportFile(), "later")
	require.NoError(t, writeErr)
	assert.Equal(t, "later", reportOutput.String())
}

func TestHandleToolCallRestoresReportFileAfterPanic(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	reportOutput := &bytes.Buffer{}
	testEnv.Env.SetReportFile(reportOutput)

	root := &cobra.Command{Use: "azldev"}
	cmd := &cobra.Command{
		Use: "panic",
		Run: func(_ *cobra.Command, _ []string) {
			panic("boom")
		},
	}
	root.AddCommand(cmd)
	handler := handleToolCall(testEnv.Env, cmd)

	assert.PanicsWithValue(t, "boom", func() {
		_, _ = handler(t.Context(), mcpapi.CallToolRequest{
			Params: mcpapi.CallToolParams{Arguments: map[string]any{}},
		})
	})

	assert.Same(t, reportOutput, testEnv.Env.ReportFile())
	_, writeErr := fmt.Fprint(testEnv.Env.ReportFile(), "later")
	require.NoError(t, writeErr)
	assert.Equal(t, "later", reportOutput.String())
}

func TestHandleToolCallResetsCommandFlags(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	root := &cobra.Command{Use: "azldev"}

	var (
		value string
		items []string
	)

	cmd := &cobra.Command{
		Use: "show",
		RunE: func(command *cobra.Command, _ []string) error {
			fmt.Fprintf(os.Stdout, "%s:%t|%s:%t",
				value, command.Flags().Changed("value"),
				strings.Join(items, ","), command.Flags().Changed("item"))

			return nil
		},
	}
	cmd.Flags().StringVar(&value, "value", "default", "value to print")
	cmd.Flags().StringArrayVar(&items, "item", []string{"base"}, "item to print")
	root.AddCommand(cmd)
	handler := handleToolCall(testEnv.Env, cmd)

	first, err := handler(t.Context(), mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{Arguments: map[string]any{
			"value": "first",
			"item":  "one",
		}},
	})
	require.NoError(t, err)
	require.Len(t, first.Content, 1)
	firstText, isText := first.Content[0].(mcpapi.TextContent)
	require.True(t, isText)
	assert.Equal(t, "first:true|one:true", firstText.Text)

	second, err := handler(t.Context(), mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{Arguments: map[string]any{}},
	})
	require.NoError(t, err)
	require.Len(t, second.Content, 1)
	secondText, isText := second.Content[0].(mcpapi.TextContent)
	require.True(t, isText)
	assert.Equal(t, "default:false|base:false", secondText.Text)

	third, err := handler(t.Context(), mcpapi.CallToolRequest{
		Params: mcpapi.CallToolParams{Arguments: map[string]any{"item": "two"}},
	})
	require.NoError(t, err)
	require.Len(t, third.Content, 1)
	thirdText, isText := third.Content[0].(mcpapi.TextContent)
	require.True(t, isText)
	assert.Equal(t, "default:false|two:true", thirdText.Text)
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
