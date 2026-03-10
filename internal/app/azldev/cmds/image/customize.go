// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// Options for adding components to the project configuration.
func customizeOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewImageCustomizeCmd())
}

// Constructs a [cobra.Command] for the 'project new' command.
func NewImageCustomizeCmd() *cobra.Command {
	options := &imageCustomizerOptions{}

	// We don't *require* a valid project configuration, but may use one if it's available.
	cmd := &cobra.Command{
		Use:   "customize",
		Short: "Customizes a pre-built Azure Linux image",
		Long: `Customize a pre-built Azure Linux image using Azure Linux Image Customizer.

The customization is driven by a YAML config file that specifies packages
to install, files to add, and other modifications. The base image can be
a local file or a container tag from MCR.`,
		Example: `  # Customize a local image file
  azldev image customize --image-file base.vhdx --image-config config.yaml --output-path out/

  # Customize from a container tag
  azldev image customize --image-tag mcr.microsoft.com/azurelinux/base:4.0 \
    --image-config config.yaml --output-path out/`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			return true, customizeImage(env, options)
		}),
	}

	cmd.Flags().StringVarP(&options.imageFile, "image-file", "", "",
		"Path of the base Azure Linux image to be customized. Cannot be specified with --image-tag")
	cmd.Flags().StringVarP(&options.imageTag, "image-tag", "", "",
		"Container tag for the MCR base Azure Linux image to be downloaded and customized."+
			" Cannot be specified with --image-file")
	cmd.Flags().StringVarP(&options.imageConfigFile, "image-config", "", "",
		"Path of the image customization config file")
	cmd.Flags().StringVar(&options.outputImageFormat, "output-image-format", "",
		"Format of output image ("+getImageCustomizerImageFormatsString()+")")
	cmd.Flags().StringVar(&options.outputPath, "output-path", "",
		"Path to write the customized image artifacts to. It can be a path to"+
			" a file if the output format is a single file format (e.g., vhd, qcow2),"+
			" or a path to a directory if the output format is a multi-file format (e.g., pxe-dir, pxe-tar)")
	cmd.Flags().StringSliceVar(&options.rpmSources, "rpm-source", []string{},
		"Path to a RPM repo config file or a directory containing RPMs")
	cmd.Flags().BoolVar(&options.disableBaseImageRpmRepos, "disable-base-image-rpm-repos", false,
		"Disable the base image's RPM repos as an RPM source")
	cmd.Flags().StringVar(&options.packageSnapshotTime, "package-snapshot-time", "",
		"Only packages published before this snapshot time will be available during customization."+
			" Supports 'YYYY-MM-DD' or full RFC3339 timestamp (e.g., 2024-05-20T23:59:59Z)")

	_ = cmd.RegisterFlagCompletionFunc("output-image-format",
		func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return getImageCustomizerImageFormats(), cobra.ShellCompDirectiveDefault
		})

	// While --image-file and --output-path are not required by the Image
	// Customizer, we are requiring them in azldev so that we can deduce which
	// folders to mount into the container.
	// See: https://dev.azure.com/mariner-org/polar/_workitems/edit/15282
	_ = cmd.MarkFlagRequired("image-config")
	_ = cmd.MarkFlagRequired("output-path")

	cmd.MarkFlagsMutuallyExclusive("image-file", "image-tag")
	cmd.MarkFlagsOneRequired("image-file", "image-tag")

	return cmd
}

func customizeImage(
	env *azldev.Env, options *imageCustomizerOptions,
) error {
	return runImageCustomizerContainer(env, imageCustomizerCustomizeCmd, options)
}
