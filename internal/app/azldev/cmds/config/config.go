// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// Called once when the app is initialized; registers any commands or callbacks with the app.
func OnAppInit(app *azldev.App) {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage tool configuration",
		Long: `Manage azldev tool configuration.

Use subcommands to inspect the resolved configuration or generate the
JSON schema used for validating TOML config files.`,
	}

	app.AddTopLevelCommand(cmd)
	generateSchemaOnAppInit(app, cmd)
	dumpConfigOnAppInit(app, cmd)
}
