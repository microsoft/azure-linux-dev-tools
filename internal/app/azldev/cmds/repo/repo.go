// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package repo implements the `azldev repo` top-level command, which exposes
// thin wrappers over the system dnf that auto-discover RPM repos
// under one or more URL prefixes.
package repo

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// OnAppInit registers the `repo` command tree with app.
func OnAppInit(app *azldev.App) {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Inspect and manage RPM repositories",
		Long: `Inspect and manage RPM repositories.

Subcommands operate over upstream RPM repos described by an
rpm-repo-set-template (e.g. the built-in "azl-standard" layout) expanded
under one or more URL prefixes.`,
	}

	app.AddTopLevelCommand(cmd)
	queryOnAppInit(app, cmd)
}
