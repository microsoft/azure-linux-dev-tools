// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// Called once when the app is initialized; registers any commands or callbacks with the app.
func OnAppInit(app *azldev.App) {
	app.AddTopLevelCommand(newComponentCmd(app))
}

func newComponentCmd(app *azldev.App) *cobra.Command {
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

	addSpecEditorOption(cmd)
	addOnAppInit(app, cmd)
	buildOnAppInit(app, cmd)
	changedOnAppInit(app, cmd)
	diffSourcesOnAppInit(app, cmd)
	distGitRepoOnAppInit(app, cmd)
	listOnAppInit(app, cmd)
	prepareOnAppInit(app, cmd)
	renderOnAppInit(app, cmd)
	testOnAppInit(app, cmd)
	metadataOnAppInit(app, cmd)

	historyOnAppInit(app, cmd)
	queryOnAppInit(app, cmd)
	updateOnAppInit(app, cmd)
	refreshUpstreamCommitOnAppInit(app, cmd)
	makeLocklessOnAppInit(app, cmd)

	return cmd
}
