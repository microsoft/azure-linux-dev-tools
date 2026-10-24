// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package mcpprogress keeps long-running MCP tool calls active with progress notifications.
package mcpprogress

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const progressInterval = 10 * time.Second

// Middleware reports progress while a tool handler is running.
func Middleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if request.Params.Meta == nil || request.Params.Meta.ProgressToken == nil {
			return next(ctx, request)
		}

		srv := server.ServerFromContext(ctx)
		if srv == nil {
			return nil, errors.New("cannot report MCP progress without a server in the request context")
		}

		heartbeatCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})

		go func() {
			defer close(done)

			reportProgress(heartbeatCtx, srv, request)
		}()

		defer func() {
			cancel()
			<-done
		}()

		return next(ctx, request)
	}
}

func reportProgress(ctx context.Context, srv *server.MCPServer, request mcp.CallToolRequest) {
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()

	for progress := progressInterval.Seconds(); ; progress += progressInterval.Seconds() {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}

			err := srv.SendNotificationToClient(ctx, "notifications/progress", map[string]any{
				"progressToken": request.Params.Meta.ProgressToken,
				"progress":      progress,
				"message":       request.Params.Name + " still running",
			})
			if err != nil {
				slog.WarnContext(ctx, "Failed to send MCP progress", "tool", request.Params.Name, "error", err)

				return
			}
		}
	}
}
