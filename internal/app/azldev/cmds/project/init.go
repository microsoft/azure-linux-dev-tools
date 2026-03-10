// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package project

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectgen"
	"github.com/spf13/cobra"
)

func initOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewInitCmd())
}

// Constructs a [cobra.Command] for the 'project init' command.
func NewInitCmd() *cobra.Command {
	options := &projectgen.NewProjectOptions{}

	// We don't *require* a valid project configuration, but may use one if it's available.
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the current working directory with a basic Azure Linux project config",
		Long: `Initialize the current working directory as an Azure Linux project.

Creates a minimal azldev.toml configuration file in the current directory.
This is equivalent to 'azldev project new .' but works in-place.`,
		Example: `  # Initialize the current directory
  cd my-project && azldev project init`,
		RunE: azldev.RunFuncWithoutRequiredConfig(
			func(env *azldev.Env) (results interface{}, err error) {
				var cwd string

				cwd, err = env.OSEnv().Getwd()
				if err != nil {
					return nil, fmt.Errorf("failed to get current working directory:\n%w", err)
				}

				err = projectgen.InitializeProject(env.FS(), cwd, options)
				if err != nil {
					return nil, fmt.Errorf("failed to initialize project:\n%w", err)
				}

				return true, nil
			}),
	}

	return cmd
}
