// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package docs

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// Called once when the app is initialized; registers any commands or callbacks with the app.
func OnAppInit(app *azldev.App) {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Documentation commands",
		Long: `Commands for generating azldev documentation.

Currently supports generating Markdown reference pages from the CLI
command tree, suitable for inclusion in the user guide.`,
	}

	app.AddTopLevelCommand(cmd)
	mdOnAppInit(app, cmd)
}
