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
	"go.szostok.io/version"
)

// RunMCPServer starts the azldev MCP server, advertising the exported commands as
// tools over stdio until the context is canceled.
func RunMCPServer(env *azldev.Env, cmd *cobra.Command) error {
	srv := server.NewMCPServer(cmd.Root().Name(), version.Get().Version,
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

	// handleToolCall runs the shared cobra command tree and swaps the process-global
	// os.Stdout to capture output, so two handlers must not run at once. mcp-go
	// dispatches tool calls to a worker pool (default 5) -- an agent firing parallel
	// skill/tool calls would hit it -- so pin the pool to a single worker and let calls
	// serialize instead of corrupting each other's output.
	server.WithWorkerPoolSize(1)(stdioServer)

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

	// Mirror our read-only annotation (set by [azldev.ExportAsReadOnlyMCPTool]) into the MCP tool
	// schema so that clients can treat and auto-approve the tool as non-mutating.
	if _, readOnly := leaf.Annotations[azldev.CmdAnnotationMCPReadOnly]; readOnly {
		toolOptions = append(toolOptions, mcp.WithReadOnlyHintAnnotation(true))
	}

	flags := getAllFlagDefs(leaf)
	for _, flag := range flags {
		var propOptions []mcp.PropertyOption

		// Skip hidden flags.
		if flag.Hidden {
			continue
		}

		// Mirror cobra's required-flag annotation (set by cmd.MarkFlagRequired) into the MCP
		// tool schema. Flags without this annotation are exposed as optional, even if their
		// default is the type's zero value.
		if _, required := flag.Annotations[cobra.BashCompOneRequiredFlag]; required {
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
	flagDefaults := captureFlagDefaults(getAllFlagDefs(cmd))

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

		if err := restoreFlagDefaults(flagDefaults, args); err != nil {
			return nil, fmt.Errorf("failed to reset command flags:\n%w", err)
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

		// LIMITATION: the [azldev.Env] here is created once when the MCP server starts and is reused
		// for every tool call. Per-call global flags parsed by this Execute (e.g. '--dry-run',
		// '--project') update the App's flag fields but are NOT re-threaded into this reused Env, so
		// env.DryRun() and the loaded config still reflect the server's startup values. As a result a
		// mutating tool invoked with '--dry-run=true' would still write. Until the MCP rework gives
		// each call its own Env, only expose commands that do not modify managed project state
		// (specs, locks, config) on their own: read-only commands (marked with
		// [azldev.ExportAsReadOnlyMCPTool]), or ones that write solely to a caller-provided output
		// path, such as 'docs markdown' or 'component diff-sources --output-file'.
		capturedText, execErr := captureStdout(func() error {
			env.SetReportFile(os.Stdout) // os.Stdout is the capture pipe for the duration of this call

			return cmd.Root().Execute()
		})
		if execErr != nil {
			slog.Error("Error executing command", "error", execErr)

			errorText := capturedText
			if errorText != "" && !strings.HasSuffix(errorText, "\n") {
				errorText += "\n"
			}

			errorText += execErr.Error()

			result := mcp.NewToolResultText(errorText)
			result.IsError = true

			return result, nil
		}

		return mcp.NewToolResultText(capturedText), nil
	}
}

type flagDefault struct {
	flag        *pflag.Flag
	value       string
	sliceValues []string
	changed     bool
}

func (value flagDefault) restore(supplied bool) error {
	if sliceValue, ok := value.flag.Value.(pflag.SliceValue); ok {
		sliceValues := value.sliceValues
		if supplied {
			sliceValues = nil
		}

		if err := sliceValue.Replace(sliceValues); err != nil {
			return fmt.Errorf("failed to restore slice value:\n%w", err)
		}

		return nil
	}

	if err := value.flag.Value.Set(value.value); err != nil {
		return fmt.Errorf("failed to restore value:\n%w", err)
	}

	return nil
}

func captureFlagDefaults(flags []*pflag.Flag) []flagDefault {
	defaults := make([]flagDefault, 0, len(flags))

	for _, flag := range flags {
		value := flagDefault{
			flag:    flag,
			value:   flag.Value.String(),
			changed: flag.Changed,
		}
		if sliceValue, ok := flag.Value.(pflag.SliceValue); ok {
			value.sliceValues = append([]string(nil), sliceValue.GetSlice()...)
		}

		defaults = append(defaults, value)
	}

	return defaults
}

func restoreFlagDefaults(defaults []flagDefault, args map[string]any) error {
	for _, value := range defaults {
		_, supplied := args[value.flag.Name]
		if err := value.restore(supplied); err != nil {
			return fmt.Errorf("failed to reset flag %#q:\n%w", value.flag.Name, err)
		}

		value.flag.Changed = value.changed
	}

	return nil
}

// captureStdout runs action with os.Stdout redirected to a pipe and returns everything
// written to it. The pipe is drained by a concurrent goroutine so that output larger
// than the OS pipe buffer (~64KB, e.g. 'config dump' on a large distro) does not block
// the write and hang the caller. Not safe for concurrent use: it mutates the global
// os.Stdout, so callers must serialize (the MCP server pins its worker pool to one).
func captureStdout(action func() error) (string, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("failed to create pipe for command output:\n%w", err)
	}

	origStdout := os.Stdout
	os.Stdout = writer

	captured := make(chan []byte, 1)

	go func() {
		data, _ := io.ReadAll(reader)
		captured <- data
	}()

	cleanedUp := false
	cleanup := func() string {
		if cleanedUp {
			return ""
		}

		cleanedUp = true
		os.Stdout = origStdout
		_ = writer.Close() // signal EOF so the drain goroutine finishes
		output := <-captured
		_ = reader.Close()

		return string(output)
	}

	defer func() {
		_ = cleanup()
	}()

	actionErr := action()
	output := cleanup()

	return output, actionErr
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
