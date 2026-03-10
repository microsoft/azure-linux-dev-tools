// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package advanced

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// Called once when the app is initialized; registers any commands or callbacks with the app.
func OnAppInit(app *azldev.App) {
	cmd := &cobra.Command{
		Use:     "advanced",
		Aliases: []string{"adv"},
		Short:   "Advanced operations",
		Long: `Advanced operations for power users and automation.

These commands provide low-level access to mock, MCP server mode,
and direct file downloads. They are hidden from the default help
output but fully supported.`,
		Hidden: true,
	}

	app.AddTopLevelCommand(cmd)
	mcpOnAppInit(app, cmd)
	mockOnAppInit(app, cmd)
	wgetOnAppInit(app, cmd)
}
