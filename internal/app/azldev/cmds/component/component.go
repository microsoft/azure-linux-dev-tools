// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
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
	diffSourcesOnAppInit(app, cmd)
	listOnAppInit(app, cmd)
	prepareOnAppInit(app, cmd)
	queryOnAppInit(app, cmd)
	renderOnAppInit(app, cmd)
	updateOnAppInit(app, cmd)
}

// relativeLockDir returns the lock file directory relative to the project
// repository root. Uses the configured 'lock-dir' from the project config
// if set, falling back to the default [lockfile.LockDir].
func relativeLockDir(env *azldev.Env) string {
	if cfg := env.Config(); cfg != nil && cfg.Project.LockDir != "" {
		if rel, err := filepath.Rel(env.ProjectDir(), cfg.Project.LockDir); err == nil {
			return rel
		}
	}

	return lockfile.LockDir
}
