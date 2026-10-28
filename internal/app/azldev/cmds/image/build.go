// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/imagebuilder"
	"github.com/spf13/cobra"
)

func buildOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewImageBuildCmd())
}

// Constructs a [cobra.Command] for the 'image build' command.
func NewImageBuildCmd() *cobra.Command {
	options := &imagebuilder.ImageBuildOptions{}

	cmd := &cobra.Command{
		Use:   "build [image-name]",
		Short: "Build an image using kiwi-ng",
		Long: `Build an image using kiwi-ng.

The image must be defined in the project configuration with a kiwi definition type.
This command invokes kiwi-ng via sudo to build the image. Built image artifacts
are placed in the project output directory.

The selected distro version's 'kiwi-config-override' file is passed to kiwi-ng with
'--config'.`,
		Example: `  # Build an image by name
  azldev image build my-image

  # Build for a specific architecture
  azldev image build my-image --arch aarch64

  # Build with a local RPM repository
  azldev image build my-image --local-repo ./base/out`,
		Args: cobra.MaximumNArgs(1),
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			if len(args) > 0 {
				options.ImageName = args[0]
			}

			return imagebuilder.BuildImage(env, options)
		}),
		ValidArgsFunction: generateImageNameCompletions,
	}

	cmd.Flags().Var(&options.TargetArch, "arch",
		"Target architecture to build for (e.g., x86_64 or aarch64). Defaults to the host architecture if not specified)")
	cmd.Flags().StringArrayVar(&options.LocalRepoPaths, "local-repo", []string{},
		"Paths to local repositories to include during build (can be specified multiple times)")
	cmd.Flags().StringArrayVar(&options.RemoteRepoPaths, "remote-repo", []string{},
		"URIs to remote repositories (http:// or https://) to include during build (can be specified multiple times)")
	cmd.Flags().BoolVar(&options.NoRemoteRepoGpgCheck, "remote-repo-no-gpgcheck", false,
		"Disable GPG checking for all remote repositories specified via --remote-repo (not for production use)")
	cmd.Flags().BoolVar(&options.RemoteRepoIncludeInImage, "remote-repo-include-in-image", false,
		"Include all remote repositories specified via --remote-repo in the built image's repository setup")

	return cmd
}
