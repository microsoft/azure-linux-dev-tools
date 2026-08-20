// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package azldev_cli wires together and runs the azldev command-line application.
//
// It is the entry point used by the azldev command (see
// github.com/microsoft/azure-linux-dev-tools/cmd/azldev); end users should
// install and run that command rather than importing this package directly.
package azldev_cli

import (
	"os"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/advanced"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/config"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/docs"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/image"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/pkg"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/project"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/repo"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/version"
)

// Main constructs the azldev CLI application, runs it with the process
// arguments, and exits the process with the resulting status code.
func Main() {
	// Instantiate the main CLI app instance.
	app := InstantiateApp()

	// Execute! We'll get back an exit code that we will exit with.
	ret := app.Execute(os.Args[1:])

	os.Exit(ret)
}

// InstantiateApp constructs a new instance of the azldev CLI application with
// all subcommands registered.
func InstantiateApp() *azldev.App {
	// Instantiate the main CLI application.
	app := azldev.NewApp(azldev.DefaultFileSystemFactory(), azldev.DefaultOSEnvFactory())

	// Give top level command packages an opportunity to register their commands (or in some cases,
	// request post-init callbacks).
	advanced.OnAppInit(app)
	component.OnAppInit(app)
	config.OnAppInit(app)
	docs.OnAppInit(app)
	image.OnAppInit(app)
	pkg.OnAppInit(app)
	project.OnAppInit(app)
	repo.OnAppInit(app)
	version.OnAppInit(app)

	return app
}
