// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	serverName      = "Azldev Mage Builder"
	serverVersion   = "0.0.1"
	minArgsRequired = 2
)

func main() {
	// Get project root directory from command line argument
	if len(os.Args) < minArgsRequired {
		fmt.Fprintf(os.Stderr, "Usage: %#q <project-root-directory>\n", os.Args[0])
		os.Exit(1)
	}

	projectRootDir := os.Args[1]

	fmt.Fprintf(os.Stderr, "Starting MCP server: %#q v%#q\n", serverName, serverVersion)
	fmt.Fprintf(os.Stderr, "Project root directory: %#q\n", projectRootDir)

	// Create server with options
	mcpServer := server.NewMCPServer(
		serverName,
		serverVersion,
		server.WithToolCapabilities(true),
		server.WithRecovery(),
		server.WithLogging(),
	)

	// Register tools
	fmt.Fprintf(os.Stderr, "Registering MCP tools...\n")
	mcpServer.AddTool(buildTool(), buildHandler(projectRootDir))
	mcpServer.AddTool(unitTool(), unitHandler(projectRootDir))
	mcpServer.AddTool(generateTool(), generateHandler(projectRootDir))
	mcpServer.AddTool(docsTool(), docsHandler(projectRootDir))
	mcpServer.AddTool(checkAllTool(), checkAllHandler(projectRootDir))
	mcpServer.AddTool(fixAllTool(), fixAllHandler(projectRootDir))
	mcpServer.AddTool(scenarioUpdateTool(), scenarioUpdateHandler(projectRootDir))
	mcpServer.AddTool(scenarioTool(), scenarioHandler(projectRootDir))
	mcpServer.AddTool(mutationTool(), mutationHandler(projectRootDir))
	mcpServer.AddTool(allTool(), allHandler(projectRootDir))
	fmt.Fprintf(os.Stderr, "Registered 10 MCP tools\n")

	// Start the server
	fmt.Fprintf(os.Stderr, "Starting MCP server on stdio...\n")

	err := server.ServeStdio(mcpServer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}

// Tool definitions - Read-only/Safe.
func buildTool() mcp.Tool {
	return mcp.Tool{
		Name: "mage_build",
		Description: "Build Go binaries for the azldev project. This compiles the main azldev CLI tool and " +
			"any other Go executables defined in cmd/. Use this when you need to verify that code changes " +
			"compile successfully, or when preparing binaries for testing or distribution. The build process " +
			"includes code generation and dependency resolution.",
		InputSchema: verboseSchema(),
	}
}

func unitTool() mcp.Tool {
	return mcp.Tool{
		Name: "mage_unit",
		Description: "Run all unit tests for the azldev project. This executes fast, isolated tests that don't " +
			"require external dependencies or filesystem access. Unit tests use helpers under the " +
			"internal/global/testctx directory for in-memory operations. Use this frequently during development " +
			"to verify your changes don't break existing functionality. This is much faster than scenario tests.",
		InputSchema: verboseSchema(),
	}
}

func generateTool() mcp.Tool {
	return mcp.Tool{
		Name: "mage_generate",
		Description: "Run code generation for the azldev project. This executes all //go:generate directives to " +
			"create generated Go code, including string enums, mocks, and other auto-generated files. Code " +
			"generation runs automatically during build and test, but you can run it standalone to update " +
			"generated files after making changes to source code that affects generation.",
		InputSchema: verboseSchema(),
	}
}

func docsTool() mcp.Tool {
	return mcp.Tool{
		Name: "mage_docs",
		Description: "Regenerate CLI reference docs and JSON schema for the azldev project. This builds the " +
			"binary and then runs it to produce markdown CLI docs under docs/user/reference/cli/ and the " +
			"JSON schema at schemas/azldev.schema.json. Use this after changing Cobra command descriptions, " +
			"flags, or config structs that affect the schema.",
		InputSchema: verboseSchema(),
	}
}

func checkAllTool() mcp.Tool {
	return mcp.Tool{
		Name: "mage_check_all",
		Description: "Run comprehensive quality checks on the azldev codebase including linting, formatting " +
			"verification, static analysis, and license compliance. This validates code style, identifies " +
			"potential bugs, and ensures adherence to project standards. Use this to verify your changes " +
			"meet quality requirements before submitting. Does not modify files.",
		InputSchema: verboseSchema(),
	}
}

// Tool definitions - Modifying.
func fixAllTool() mcp.Tool {
	return mcp.Tool{
		Name: "mage_fix_all",
		Description: "Automatically fix code quality issues in the azldev project including formatting, import " +
			"organization, and simple linting violations. This modifies source files to resolve issues that " +
			"can be auto-corrected. Use this as the FIRST step when addressing linter errors - it handles " +
			"most formatting and style issues automatically, leaving only semantic problems that require " +
			"manual attention. WARNING: This modifies files in place.",
		InputSchema: verboseSchema(),
	}
}

// Tool definitions - Slow/Resource Intensive.
func scenarioTool() mcp.Tool {
	return mcp.Tool{
		Name: "mage_scenario",
		Description: "Run comprehensive scenario tests for the azldev project. These tests verify end-to-end " +
			"functionality by executing the actual azldev CLI with various configurations and validating " +
			"outputs against stored snapshots. Scenario tests are slower than unit tests as they involve " +
			"real file I/O, process execution, and complex scenarios. Use this to verify that changes work " +
			"correctly in realistic usage scenarios. WARNING: This will take several minutes and some " +
			"tests access external resources via network. Transient failures may occur.",
		InputSchema: verboseSchema(),
	}
}

func scenarioUpdateTool() mcp.Tool {
	return mcp.Tool{
		Name: "mage_scenario_update",
		Description: "Update test snapshots for the azldev scenario tests. When scenario test expectations " +
			"change due to legitimate code changes, this updates the stored expected outputs in " +
			"scenario/__snapshots__/ to match new behavior. Use this when scenario tests fail because " +
			"expected output has changed (not due to bugs). Only run after verifying the new output is " +
			"correct. WARNING: This modifies test snapshot files. This will take several minutes and some " +
			"tests access external resources via network. Transient failures may occur.",
		InputSchema: verboseSchema(),
	}
}

func mutationTool() mcp.Tool {
	return mcp.Tool{
		Name: "mage_mutation",
		Description: "Run mutation testing (gremlins) to assess how thoroughly UNIT tests actually verify " +
			"behavior (not just line coverage). It introduces small changes ('mutants') into the source and " +
			"reports whether the tests catch them: a KILLED mutant is caught, a LIVED mutant is a real test " +
			"gap, and NOT COVERED means no test exercises the code. Provide EITHER 'path' to mutation-test a " +
			"package (e.g. './internal/rpm', or './' for the whole repo, which takes a few minutes) OR 'diff' " +
			"to mutate only the lines changed versus a git ref (e.g. 'main') - the fastest way to check a " +
			"branch's new code. The scenario/ and magefiles/ trees and generated mocks are excluded, and " +
			"scenario tests are never run. The output lists only the mutants worth acting on (LIVED and " +
			"NOT COVERED) plus a summary; a full JSON report covering every mutant is written to " +
			"out/mutation-report.json - read that file for complete results. Some LIVED mutants are " +
			"'equivalent' and cannot be killed, so inspect before changing tests. It is slower than unit " +
			"tests (it recompiles and reruns tests per mutant), so prefer scoping with 'path' or 'diff'.",
		InputSchema: mutationSchema(),
	}
}

func allTool() mcp.Tool {
	return mcp.Tool{
		Name: "mage_all",
		Description: "Execute the complete azldev build and test pipeline including code generation, compilation, " +
			"unit tests, scenario tests, and all quality checks. This is the comprehensive validation that " +
			"runs in CI/CD to ensure all changes are production-ready. Use this before submitting major " +
			"changes or when you want complete confidence that everything works. WARNING: This may take " +
			"multiple minutes as it runs the full scenario test suite.",
		InputSchema: verboseSchema(),
	}
}

// Common schema for verbose flag.
func verboseSchema() mcp.ToolInputSchema {
	return mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"verbose": map[string]any{
				"type":        "boolean",
				"description": "Enable verbose output",
				"default":     false,
			},
		},
	}
}

// mutationSchema is the input schema for the mage_mutation tool. Exactly one of
// 'path' or 'diff' must be supplied; this is enforced by the handler, since JSON
// schema can't cleanly express "exactly one of".
func mutationSchema() mcp.ToolInputSchema {
	return mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]any{
			"path": map[string]any{
				"type": "string",
				"description": "Package path to mutation-test, e.g. './internal/rpm' (or './' for the whole " +
					"repo). Provide either 'path' or 'diff', not both.",
			},
			"diff": map[string]any{
				"type": "string",
				"description": "Git branch or commit to diff against, e.g. 'main' or 'HEAD~3'. Only lines changed versus " +
					"this ref are mutated. Provide either 'path' or 'diff', not both.",
			},
			"verbose": map[string]any{
				"type":        "boolean",
				"description": "Enable verbose output",
				"default":     false,
			},
		},
	}
}

// Handler creation functions.
func buildHandler(projectRootDir string) server.ToolHandlerFunc {
	return createMageHandler(projectRootDir, "build")
}

func unitHandler(projectRootDir string) server.ToolHandlerFunc {
	return createMageHandler(projectRootDir, "unit")
}

func generateHandler(projectRootDir string) server.ToolHandlerFunc {
	return createMageHandler(projectRootDir, "generate")
}

func docsHandler(projectRootDir string) server.ToolHandlerFunc {
	return createMageHandler(projectRootDir, "docs")
}

func checkAllHandler(projectRootDir string) server.ToolHandlerFunc {
	return createMageHandler(projectRootDir, "check", "all")
}

func fixAllHandler(projectRootDir string) server.ToolHandlerFunc {
	return createMageHandler(projectRootDir, "fix", "all")
}

func scenarioUpdateHandler(projectRootDir string) server.ToolHandlerFunc {
	return createMageHandler(projectRootDir, "scenarioUpdate")
}

func scenarioHandler(projectRootDir string) server.ToolHandlerFunc {
	return createMageHandler(projectRootDir, "scenario")
}

func allHandler(projectRootDir string) server.ToolHandlerFunc {
	return createMageHandler(projectRootDir, "all")
}

// mutationHandler runs the mage_mutation tool. It routes to 'mage mutation
// <path>' or 'mage mutationDiff <diff>' depending on which argument is set;
// exactly one of 'path' or 'diff' is required.
func mutationHandler(projectRootDir string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fmt.Fprintf(os.Stderr, "Handling tool request: %s\n", request.Params.Name)

		args, ok := toArgsMap(request.Params.Arguments)
		if !ok {
			return invalidArgsResult(request.Params.Name), nil
		}

		path := trimmedStringArg(args, "path")
		diff := trimmedStringArg(args, "diff")

		switch {
		case path != "" && diff != "":
			return mutationArgError("provide either 'path' or 'diff', not both"), nil
		case diff != "":
			return runMageTool(ctx, projectRootDir, getVerboseFlag(args), "mutationDiff", diff), nil
		case path != "":
			return runMageTool(ctx, projectRootDir, getVerboseFlag(args), "mutation", path), nil
		default:
			return mutationArgError("provide a 'path' (e.g. './internal/rpm') or a 'diff' ref (e.g. 'main')"), nil
		}
	}
}

// trimmedStringArg returns the trimmed string value of args[key], or "" if it is
// absent or not a string.
func trimmedStringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)

	return strings.TrimSpace(value)
}

// mutationArgError builds the error result returned when the mage_mutation
// arguments are missing or contradictory.
func mutationArgError(detail string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.NewTextContent("The 'mage_mutation' tool requires you to " + detail + "."),
		},
	}
}

// Generic handler creator.
func createMageHandler(projectRootDir string, mageArgs ...string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fmt.Fprintf(os.Stderr, "Handling tool request: %s with args: %v\n", request.Params.Name, mageArgs)

		args, ok := toArgsMap(request.Params.Arguments)
		if !ok {
			return invalidArgsResult(request.Params.Name), nil
		}

		return runMageTool(ctx, projectRootDir, getVerboseFlag(args), mageArgs...), nil
	}
}

// toArgsMap normalizes a tool call's raw arguments to a map. MCP clients may omit
// arguments entirely (a nil value), which is valid for tools whose parameters are
// all optional; that is treated as an empty map. Only a non-nil value of an
// unexpected (non-object) type is rejected.
func toArgsMap(raw any) (map[string]any, bool) {
	if raw == nil {
		return map[string]any{}, true
	}

	args, ok := raw.(map[string]any)

	return args, ok
}

// invalidArgsResult builds the error result returned when a tool's arguments
// cannot be parsed.
func invalidArgsResult(toolName string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.NewTextContent("Invalid arguments provided to tool: " + toolName),
		},
	}
}

// runMageTool invokes mage with the given arguments and converts the result
// into an MCP tool result.
func runMageTool(
	ctx context.Context,
	projectRootDir string,
	verbose bool,
	mageArgs ...string,
) *mcp.CallToolResult {
	fmt.Fprintf(os.Stderr, "Running mage command: mage %s (verbose=%v)\n", strings.Join(mageArgs, " "), verbose)

	output, err := callMage(ctx, projectRootDir, verbose, mageArgs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Mage command failed: %v\n", err)

		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				mcp.NewTextContent("Error running mage command: " + err.Error()),
				mcp.NewTextContent("Mage command output:\n\n" + output),
			},
		}
	}

	fmt.Fprintf(os.Stderr, "Mage command completed successfully\n")

	return &mcp.CallToolResult{
		IsError: false,
		Content: []mcp.Content{
			mcp.NewTextContent("Mage command output:\n\n" + output),
		},
	}
}

func getVerboseFlag(args map[string]any) bool {
	if args == nil {
		return false
	}

	if verbose, ok := args["verbose"].(bool); ok {
		return verbose
	}

	return false
}

func callMage(ctx context.Context, projectRootDir string, verbose bool, args ...string) (string, error) {
	// Build the command - use "go run magefile.go" to invoke mage
	cmdArgs := []string{"run", "magefile.go"}
	if verbose {
		cmdArgs = append(cmdArgs, "-v")
	}

	cmdArgs = append(cmdArgs, args...)

	fmt.Fprintf(os.Stderr, "Executing from directory: %#q\n", projectRootDir)
	fmt.Fprintf(os.Stderr, "Running: 'go %s'\n", strings.Join(cmdArgs, " "))

	cmd := exec.CommandContext(ctx, "go", cmdArgs...)
	cmd.Dir = projectRootDir

	out, err := cmd.CombinedOutput()
	outString := strings.TrimSpace(string(out))

	if err != nil {
		return outString, fmt.Errorf("mage command failed: %w", err)
	}

	return outString, nil
}
