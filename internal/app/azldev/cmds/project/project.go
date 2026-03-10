// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package project

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// Called once when the app is initialized; registers any commands or callbacks with the app.
func OnAppInit(app *azldev.App) {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage Azure Linux projects",
		Long: `Manage Azure Linux projects.

Use subcommands to create a new project or initialize the current directory
as an Azure Linux project with a basic configuration.`,
	}

	app.AddTopLevelCommand(cmd)
	newOnAppInit(app, cmd)
	initOnAppInit(app, cmd)
}
