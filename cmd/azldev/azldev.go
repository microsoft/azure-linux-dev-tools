// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package main

import (
	"os"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/docs"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/version"
)

func main() {
	// Instantiate the main CLI app instance.
	app := InstantiateApp()

	// Execute! We'll get back an exit code that we will exit with.
	ret := app.Execute(os.Args[1:])

	os.Exit(ret)
}

// Constructs a new instance of the main CLI application, with all subcommands registered.
func InstantiateApp() *azldev.App {
	// Instantiate the main CLI application.
	app := azldev.NewApp(azldev.DefaultFileSystemFactory(), azldev.DefaultOSEnvFactory())

	// Give top level command packages an opportunity to register their commands (or in some cases,
	// request post-init callbacks).
	docs.OnAppInit(app)
	version.OnAppInit(app)

	return app
}
