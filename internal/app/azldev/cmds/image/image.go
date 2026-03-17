// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// Called once when the app is initialized; registers any commands or callbacks with the app.
func OnAppInit(app *azldev.App) {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage Azure Linux images",
		Long: `Manage Azure Linux images.

Use subcommands to build, customize, boot, and inspect images defined in
the project configuration. Images are typically built with kiwi-ng and
can be customized using Azure Linux Image Customizer.`,
	}

	app.AddTopLevelCommand(cmd)
	bootOnAppInit(app, cmd)
	buildOnAppInit(app, cmd)
	customizeOnAppInit(app, cmd)
	injectFilesOnAppInit(app, cmd)
	listOnAppInit(app, cmd)
	testOnAppInit(app, cmd)
}
