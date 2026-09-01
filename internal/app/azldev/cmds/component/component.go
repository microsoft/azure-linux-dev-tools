// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// Called once when the app is initialized; registers any commands or callbacks with the app.
func OnAppInit(app *azldev.App) {
	cmd := &cobra.Command{
		Use:     "component",
		Aliases: []string{"comp"},
		Short:   "Manage components",
		Long: `Manage components in an Azure Linux project.

Components are the primary unit of packaging — each corresponds to exactly one
RPM spec file. Building a component results in producing one or more RPM packages.
Use subcommands to add, list, query, build, and prepare sources for
components defined in the project configuration.`,
	}

	app.AddTopLevelCommand(cmd)
	addOnAppInit(app, cmd)
	buildOnAppInit(app, cmd)
	changedOnAppInit(app, cmd)
	diffSourcesOnAppInit(app, cmd)
	listOnAppInit(app, cmd)
	prepareOnAppInit(app, cmd)
	renderOnAppInit(app, cmd)

	// The commands that maintain resolved component state differ by mode: the
	// default mode maintains lock files, while lock-file-free mode maintains
	// generated upstream-commit config. Registering only the commands that
	// belong to the active mode keeps help, docs, and MCP tools honest.
	if app.WithoutLockfile() {
		legacyOnAppInit(app, cmd)
		refreshUpstreamCommitOnAppInit(app, cmd)
	} else {
		historyOnAppInit(app, cmd)
		queryOnAppInit(app, cmd)
		updateOnAppInit(app, cmd)
	}
}
