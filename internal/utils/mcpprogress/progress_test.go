// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mcpprogress_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/mcpprogress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddlewareProgress(t *testing.T) {
	for _, token := range []any{"build-token", float64(0), nil} {
		t.Run(tokenName(token), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := server.NewMCPServer("test", "1", server.WithToolHandlerMiddleware(mcpprogress.Middleware))
				session := server.NewInProcessSession("test", nil)
				session.Initialize()
				srv.AddTool(mcp.NewTool("slow"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					time.Sleep(25 * time.Second)

					return mcp.NewToolResultText("done"), nil
				})

				response := callTool(t, srv, session, token)
				result, ok := response.(mcp.JSONRPCResponse)
				require.True(t, ok, "unexpected response: %#v", response)
				assert.Equal(t, mcp.NewToolResultText("done"), result.Result)

				if token != nil {
					require.Len(t, session.ClientNotifications(), 2)

					for _, progress := range []float64{10, 20} {
						notification := <-session.ClientNotifications()
						assert.Equal(t, "notifications/progress", notification.Method)
						assert.Equal(t, map[string]any{
							"progressToken": token,
							"progress":      progress,
							"message":       "slow still running",
						}, notification.Params.AdditionalFields)
					}
				}

				time.Sleep(20 * time.Second)
				assert.Empty(t, session.ClientNotifications(), "no notifications after the handler returns")
			})
		})
	}
}

func TestMiddlewareStopsOnErrorAndPanic(t *testing.T) {
	for _, panicHandler := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panicHandler], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := server.NewMCPServer("test", "1", server.WithRecovery(),
					server.WithToolHandlerMiddleware(mcpprogress.Middleware))
				session := server.NewInProcessSession("test", nil)
				session.Initialize()
				srv.AddTool(mcp.NewTool("slow"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					time.Sleep(15 * time.Second)

					if panicHandler {
						panic("boom")
					}

					return nil, errors.New("boom")
				})

				response := callTool(t, srv, session, "token")
				result, ok := response.(mcp.JSONRPCError)
				require.True(t, ok, "unexpected response: %#v", response)
				assert.Contains(t, result.Error.Message, "boom")
				require.Len(t, session.ClientNotifications(), 1)
				<-session.ClientNotifications()
				time.Sleep(20 * time.Second)
				assert.Empty(t, session.ClientNotifications())
			})
		})
	}
}

func TestMiddlewareStopsOnCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		srv := server.NewMCPServer("test", "1", server.WithToolHandlerMiddleware(mcpprogress.Middleware))
		session := server.NewInProcessSession("test", nil)
		session.Initialize()
		srv.AddTool(mcp.NewTool("slow"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			time.Sleep(15 * time.Second)
			cancel()
			time.Sleep(20 * time.Second)

			return nil, ctx.Err()
		})

		message := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call",
			"params":{"name":"slow","_meta":{"progressToken":"token"}}}`)
		srv.HandleMessage(srv.WithContext(ctx, session), message)
		require.Len(t, session.ClientNotifications(), 1)
		<-session.ClientNotifications()
		time.Sleep(20 * time.Second)
		assert.Empty(t, session.ClientNotifications())
	})
}

func callTool(t *testing.T, srv *server.MCPServer, session *server.InProcessSession, token any) mcp.JSONRPCMessage {
	t.Helper()

	params := map[string]any{"name": "slow"}
	if token != nil {
		params["_meta"] = map[string]any{"progressToken": token}
	}

	raw, err := json.Marshal(map[string]any{
		"jsonrpc": mcp.JSONRPC_VERSION,
		"id":      1,
		"method":  "tools/call",
		"params":  params,
	})
	require.NoError(t, err)

	return srv.HandleMessage(srv.WithContext(t.Context(), session), raw)
}

func tokenName(token any) string {
	if token == nil {
		return "no-token"
	}

	if _, ok := token.(string); ok {
		return "string-token"
	}

	return "zero-token"
}
