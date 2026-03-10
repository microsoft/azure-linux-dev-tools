// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Performs the file download requested by options.
func RunMCPServer(env *azldev.Env, cmd *cobra.Command) error {
	srv := server.NewMCPServer(cmd.Short, "1.0.0",
		server.WithLogging(),
		server.WithRecovery(),
		server.WithResourceCapabilities(true, true),
		server.WithToolCapabilities(true),
	)

	toolCount := 0

	// Look through all leaf commands in the command hierarchy; register a tool for each command
	// that has opted into being exported to our MCP server.
	leaves := getLeafCommands(cmd.Root())
	for _, leaf := range leaves {
		if _, mcpEnabled := leaf.Annotations[azldev.CmdAnnotationMCPEnabled]; mcpEnabled {
			addToolForCmd(env, srv, leaf)

			toolCount++
		}
	}

	slog.Info("Registered MCP tools", "count", toolCount)

	stdioServer := server.NewStdioServer(srv)

	slog.Info("Starting MCP server")

	// Run the server until canceled.
	err := stdioServer.Listen(cmd.Context(), os.Stdin, os.Stdout)
	if err != nil {
		return fmt.Errorf("failed to start MCP server:\n%w", err)
	}

	return nil
}

//
// Below code is adapted from the sources in the mcp-cobra package, which was
// authored by PlusLemon and available for distribution under the MIT license.
//
//     https://github.com/PlusLemon/mcp-cobra/blob/main/mcp/mcp.go
//     Captured from git commit e12d5b446b388f7291899866a3ada5ebc85b3bce on 5 June 2025.
//

func addToolForCmd(env *azldev.Env, srv *server.MCPServer, leaf *cobra.Command) {
	fullPath := getFullCommandPath(leaf)
	toolName := strings.Join(fullPath, "-")

	toolDesc := leaf.Short
	if toolDesc == "" {
		toolDesc = leaf.Long
	}

	var toolOptions []mcp.ToolOption

	toolOptions = append(toolOptions, mcp.WithDescription(toolDesc))

	flags := getAllFlagDefs(leaf)
	for _, flag := range flags {
		var propOptions []mcp.PropertyOption

		// Skip hidden flags.
		if flag.Hidden {
			continue
		}

		// Assume a flag with no default value is required.
		if flag.DefValue == "" {
			propOptions = append(propOptions, mcp.Required())
		}

		propOptions = append(propOptions, mcp.Description(flag.Usage))

		switch flag.Value.Type() {
		case "string":
			propOptions = append(propOptions, mcp.DefaultString(flag.DefValue))
			toolOptions = append(toolOptions, mcp.WithString(flag.Name, propOptions...))

		case "int":
			defaultValue, _ := strconv.ParseFloat(flag.DefValue, 64)
			propOptions = append(propOptions, mcp.DefaultNumber(defaultValue))
			toolOptions = append(toolOptions, mcp.WithNumber(flag.Name, propOptions...))

		case "bool":
			defaultValue, _ := strconv.ParseBool(flag.DefValue)
			propOptions = append(propOptions, mcp.DefaultBool(defaultValue))
			toolOptions = append(toolOptions, mcp.WithBoolean(flag.Name, propOptions...))

		case "float32", "float64":
			defaultValue, _ := strconv.ParseFloat(flag.DefValue, 64)
			propOptions = append(propOptions, mcp.DefaultNumber(defaultValue))
			toolOptions = append(toolOptions, mcp.WithNumber(flag.Name, propOptions...))

		default:
			toolOptions = append(toolOptions, mcp.WithString(flag.Name, propOptions...))
		}
	}

	tool := mcp.NewTool(toolName, toolOptions...)

	slog.Info("Registering tool", "name", toolName, "description", toolDesc)

	srv.AddTool(tool, handleToolCall(env, leaf))
}

func handleToolCall(
	env *azldev.Env, cmd *cobra.Command,
) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		slog.Info("Invoking tool", "tool", cmd.Name(), "params", request.Params.Arguments)

		fullArgs := getFullCommandPath(cmd)

		// Cast Arguments to map[string]any for proper handling
		args, ok := request.Params.Arguments.(map[string]any)
		if !ok {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					mcp.NewTextContent("Invalid arguments provided to tool: " + cmd.Name()),
				},
			}, nil
		}

		for key, val := range args {
			if key == "args" {
				continue
			}

			fullArgs = append(fullArgs, fmt.Sprintf("--%s=%v", key, val))
		}

		if argsList, ok := args["args"].([]any); ok {
			for _, arg := range argsList {
				fullArgs = append(fullArgs, fmt.Sprintf("%v", arg))
			}
		}

		slog.Info("Executing command", "args", fullArgs)
		cmd.Root().SetArgs(fullArgs)

		reader, writer, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create pipe for command output:\n%w", err)
		}

		origStdout := os.Stdout
		os.Stdout = writer

		env.SetReportFile(writer)

		err = cmd.Root().Execute()

		os.Stdout = origStdout

		if err != nil {
			slog.Error("Error executing command", "error", err)

			return mcp.NewToolResultText(err.Error()), nil
		}

		writer.Close()

		capturedText, _ := io.ReadAll(reader)

		return mcp.NewToolResultText(string(capturedText)), nil
	}
}

func getLeafCommands(cmd *cobra.Command) []*cobra.Command {
	var leaves []*cobra.Command
	if len(cmd.Commands()) == 0 {
		leaves = append(leaves, cmd)
	} else {
		for _, sub := range cmd.Commands() {
			leaves = append(leaves, getLeafCommands(sub)...)
		}
	}

	return leaves
}

func getFullCommandPath(cmd *cobra.Command) []string {
	if cmd.Parent() == nil {
		// ignore the root command
		return []string{}
	}

	parentPath := getFullCommandPath(cmd.Parent())

	return append(parentPath, cmd.Name())
}

func getAllFlagDefs(cmd *cobra.Command) []*pflag.Flag {
	flagsByName := make(map[string]*pflag.Flag)

	// Compose a map containing all flags by name, ensuring that we get all local and inherited flags
	// (and don't have duplicates).
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		flagsByName[f.Name] = f
	})

	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		flagsByName[f.Name] = f
	})

	// Extract the flag values and sort them by name for consistent output.
	flags := lo.Values(flagsByName)

	sort.Slice(flags, func(i, j int) bool {
		return flags[i].Name < flags[j].Name
	})

	return flags
}
