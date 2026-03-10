// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/mattn/go-isatty"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/reflectable"
	"github.com/spf13/cobra"
)

const (
	// CommandAnnotationRootOK is a [cobra.Command.Annotations] key used to indicate that a command
	// is allowed to be run as root.
	CommandAnnotationRootOK = "rootOK"
)

const (
	// CommandGroupMeta represents the command group for meta commands like help, completion, and version.
	CommandGroupMeta = "meta"
	// CommandGroupPrimary represents the command group for primary application commands.
	CommandGroupPrimary = "primary"
)

// Error to return when a command is invoked with invalid usage.
var ErrInvalidUsage = errors.New("invalid usage")

// CmdAnnotationMCPFuncType is the [cobra.Command] annotation key used to indicate that a command
// should be enabled in MCP server mode. The value associated with the key is ignored.
const CmdAnnotationMCPEnabled = "azldev.mcp.enabled"

type (
	cobraRunFuncType = func(command *cobra.Command, args []string) error

	// Type of a function for use with [RunFunc] and siblings.
	CmdFuncType = func(env *Env) (interface{}, error)

	// Type of a function for use with [RunFuncWithExtraArgs] and siblings.
	CmdWithExtraArgsFuncType = func(env *Env, extraArgs []string) (interface{}, error)
)

// Returns a function usable by an azldev command as a [cobra.Command] 'RunE' function. Rejects all
// positional arguments, retrieves the [Env], invokes the provided inner function with the
// right context objects, and then provides standard result reporting for the opaque result value
// returned by the inner function. Fails early if no project/configuration was loaded.
func RunFunc(innerFunc CmdFuncType) cobraRunFuncType {
	return runFuncInternal(func(env *Env, extraArgs []string) (interface{}, error) {
		if len(extraArgs) > 0 {
			return nil, fmt.Errorf("unexpected arguments: %v", extraArgs)
		}

		return innerFunc(env)
	}, true)
}

// Returns a function usable by an azldev command as a [cobra.Command] 'RunE' function. Rejects all
// positional arguments, retrieves the [Env], invokes the provided inner function with the
// right context objects, and then provides standard result reporting for the opaque result value
// returned by the inner function. Does *not* require valid project/configuration to have been
// loaded.
func RunFuncWithoutRequiredConfig(innerFunc CmdFuncType) cobraRunFuncType {
	return runFuncInternal(func(env *Env, extraArgs []string) (interface{}, error) {
		if len(extraArgs) > 0 {
			return nil, fmt.Errorf("unexpected arguments: %v", extraArgs)
		}

		return innerFunc(env)
	}, false)
}

// Returns a function usable by an azldev command as a `cobra.Command` 'RunE' function.
// Retrieves the `Env`, invokes the provided inner function with the right context
// objects and positional arguments, and then provides standard result reporting for
// the opaque result value returned by the inner function. Fails early if no
// project/configuration was loaded.
func RunFuncWithExtraArgs(innerFunc CmdWithExtraArgsFuncType) cobraRunFuncType {
	return runFuncInternal(innerFunc, true)
}

// Returns a function usable by an azldev command as a [cobra.Command] 'RunE' function.
// Retrieves the [Env], invokes the provided inner function with the right context
// objects and positional arguments, and then provides standard result reporting for
// the opaque result value returned by the inner function. Does *not* require valid
// project/configuration to have been loaded.
func RunFuncWithoutRequiredConfigWithExtraArgs(innerFunc CmdWithExtraArgsFuncType) cobraRunFuncType {
	return runFuncInternal(innerFunc, false)
}

func runFuncInternal(innerFunc CmdWithExtraArgsFuncType, requireConfig bool) cobraRunFuncType {
	return func(command *cobra.Command, args []string) error {
		// If we got down here, then make sure we don't display usage unless we are certain
		// it's a usage error.
		command.SilenceUsage = true

		env, err := GetEnvFromCommand(command)
		if err != nil {
			return err
		}

		if requireConfig && (env.Config() == nil || env.ProjectDir() == "") {
			slog.Warn(
				"!!! Unable to find and load valid Azure Linux project configuration.\n\n" +
					"Please either use the -C option to specify a path to the root directory " +
					"of your Azure Linux project/repo, or else run this tool from within a directory " +
					"tree that contains an 'azldev.toml' file at its root.\n\n" +
					"Most commands will not function correctly without a valid configuration.\n\n" +
					"------------------------------------------------------------------\n",
			)

			return errors.New("a valid project and configuration are required to execute this command")
		}

		results, err := innerFunc(env, args)
		if err != nil {
			// If it was a more complicated usage error not caught by cobra, then re-enable auto-usage
			// display.
			if errors.Is(err, ErrInvalidUsage) {
				command.SilenceUsage = false
			}

			return err
		}

		return reportResults(env, results)
	}
}

// Helper to retrieve the [Env] from the context of a [cobra.Command].
func GetEnvFromCommand(cmd *cobra.Command) (*Env, error) {
	ctx := cmd.Context()
	if ctx == nil {
		return nil, errors.New("unexpected: nil context")
	}

	env, ok := ctx.(*Env)
	if !ok {
		return nil, errors.New("unexpected: incorrect context type")
	}

	return env, nil
}

// ExportAsMCPTool updates the provided command (and all descendant commands),
// opting it into being advertised as a tool in MCP server mode.
func ExportAsMCPTool(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}

	// The value doesn't matter.
	cmd.Annotations[CmdAnnotationMCPEnabled] = "true"

	// If the command has subcommands, then recursively opt them in as well.
	for _, subCmd := range cmd.Commands() {
		ExportAsMCPTool(subCmd)
	}
}

// Displays the results of a command in the appropriate format to stdout.
func reportResults(env *Env, results interface{}) error {
	switch env.defaultReportFormat {
	case ReportFormatMarkdown:
		return reportResultsViaReflectable(env, results, reflectable.FormatMarkdown)
	case ReportFormatCSV:
		return reportResultsViaReflectable(env, results, reflectable.FormatCSV)
	case ReportFormatJSON:
		return reportResultsAsJSON(env, results)
	case ReportFormatTable:
		fallthrough
	default:
		return reportResultsViaReflectable(env, results, reflectable.FormatTable)
	}
}

func reportResultsViaReflectable(env *Env, results interface{}, format reflectable.Format) (err error) {
	// Don't bother formatting well-known simple values that aren't meaningful to humans.
	if results == nil || results == true || results == false {
		return nil
	}

	options := createReflectableOptions(env, format)

	formatted, err := reflectable.FormatValue(options, results)
	if err != nil {
		return fmt.Errorf("failed to format results:\n%w", err)
	}

	// Only write to stdout if we have something to write. This avoids an unnecessary
	// bare newline being printed when there are no results to report.
	if formatted != "" {
		fmt.Fprintf(env.ReportFile(), "%s\n", formatted)
	}

	return nil
}

// createReflectableOptions computes the set of options that we should use for formatting.
func createReflectableOptions(env *Env, format reflectable.Format) *reflectable.Options {
	options := reflectable.NewOptions().WithFormat(format)
	tty := isatty.IsTerminal(os.Stdout.Fd())
	color := false

	// Figure out if we should ask reflectable to use color.
	switch env.ColorMode() {
	case ColorModeAlways:
		color = true
	case ColorModeNever:
		break
	case ColorModeAuto:
		fallthrough
	default:
		color = tty
	}

	options = options.WithColor(color)

	// If we know that stdout is a terminal, then try to auto-fit within the terminal
	// width. In all other cases, don't constrain the table width.
	if tty {
		terminalWidth, _, _ := term.GetSize(os.Stdout.Fd())
		if terminalWidth != 0 {
			options = options.WithMaxTableWidth(terminalWidth)
		}
	}

	return options
}

// Displays the results of a command to stdout in JSON format.
func reportResultsAsJSON(env *Env, results interface{}) error {
	jsonBytes, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal command results to JSON:\n%w", err)
	}

	if len(jsonBytes) > 0 {
		fmt.Fprintf(env.ReportFile(), "%s\n", jsonBytes)
	}

	return nil
}
