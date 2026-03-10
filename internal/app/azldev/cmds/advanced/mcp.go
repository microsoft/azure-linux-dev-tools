// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package advanced

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/mcp"
	"github.com/spf13/cobra"
)

func mcpOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewMCPCmd())
}

// Constructs a [cobra.Command] for the 'mcp' command.
func NewMCPCmd() *cobra.Command {
	// We don't *require* a valid project configuration, but may use it if it's available.
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run in MCP server mode",
		Long: `Start azldev as a Model Context Protocol (MCP) server.

In this mode, azldev communicates over stdin/stdout using the MCP protocol,
exposing selected commands as tools that can be invoked by AI coding agents
and other MCP clients.`,
		Example: `  # Start the MCP server
  azldev advanced mcp`,
		RunE: (func(cmd *cobra.Command, args []string) error {
			env, err := azldev.GetEnvFromCommand(cmd)
			if err != nil {
				return fmt.Errorf("failed to get command environment:\n%w", err)
			}

			return mcp.RunMCPServer(env, cmd)
		}),
	}

	return cmd
}
