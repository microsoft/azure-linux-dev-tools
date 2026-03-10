// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev

import (
	"encoding/json"
	"errors"
	"fmt"

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
	})
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
	})
}

// Returns a function usable by an azldev command as a `cobra.Command` 'RunE' function.
// Retrieves the `Env`, invokes the provided inner function with the right context
// objects and positional arguments, and then provides standard result reporting for
// the opaque result value returned by the inner function. Fails early if no
// project/configuration was loaded.
func RunFuncWithExtraArgs(innerFunc CmdWithExtraArgsFuncType) cobraRunFuncType {
	return runFuncInternal(innerFunc)
}

// Returns a function usable by an azldev command as a [cobra.Command] 'RunE' function.
// Retrieves the [Env], invokes the provided inner function with the right context
// objects and positional arguments, and then provides standard result reporting for
// the opaque result value returned by the inner function. Does *not* require valid
// project/configuration to have been loaded.
func RunFuncWithoutRequiredConfigWithExtraArgs(innerFunc CmdWithExtraArgsFuncType) cobraRunFuncType {
	return runFuncInternal(innerFunc)
}

func runFuncInternal(innerFunc CmdWithExtraArgsFuncType) cobraRunFuncType {
	return func(command *cobra.Command, args []string) error {
		// If we got down here, then make sure we don't display usage unless we are certain
		// it's a usage error.
		command.SilenceUsage = true

		env, err := GetEnvFromCommand(command)
		if err != nil {
			return err
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
	return reportResultsAsJSON(env, results)
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
