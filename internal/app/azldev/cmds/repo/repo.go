// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repo

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// OnAppInit is called once when the app is initialized; registers the "repo" command tree.
func OnAppInit(app *azldev.App) {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Query published RPM repositories",
		Long: `Query published RPM repositories.

Subcommands wrap 'dnf repoquery' against an Azure Linux published repo URL
(e.g. an azl4-dev blob storage endpoint) and bucket the results into the
on-disk layout expected by downstream tooling.`,
	}

	app.AddTopLevelCommand(cmd)
	queryOnAppInit(app, cmd)
	diffOnAppInit(app, cmd)
}
