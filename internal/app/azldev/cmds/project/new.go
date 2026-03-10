// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package project

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectgen"
	"github.com/spf13/cobra"
)

func newOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewNewCmd())
}

// Constructs a [cobra.Command] for the 'project new' command.
func NewNewCmd() *cobra.Command {
	options := &projectgen.NewProjectOptions{}

	// We don't *require* a valid project configuration, but may use one if it's available.
	cmd := &cobra.Command{
		Use:   "new PATH",
		Short: "Create a new Azure Linux project with basic config",
		Long: `Create a new Azure Linux project at the specified path.

Sets up the directory structure and a minimal azldev.toml configuration
file. The path must not already contain a project.`,
		Example: `  # Create a new project
  azldev project new my-project

  # Create a new project in a specific directory
  azldev project new /home/user/projects/my-distro`,
		Args: cobra.ExactArgs(1),
		RunE: azldev.RunFuncWithoutRequiredConfigWithExtraArgs(
			func(env *azldev.Env, args []string) (results interface{}, err error) {
				return true, projectgen.CreateNewProject(env.FS(), args[0], options)
			}),
	}

	return cmd
}
