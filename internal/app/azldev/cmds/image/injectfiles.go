// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

// Options for adding components to the project configuration.
func injectFilesOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewImageInjectFilesCmd())
}

// Constructs a [cobra.Command] for the 'project new' command.
func NewImageInjectFilesCmd() *cobra.Command {
	options := &imageCustomizerOptions{}

	// We don't *require* a valid project configuration, but may use one if it's available.
	cmd := &cobra.Command{
		Use:   "inject-files",
		Short: "Injects files into a partition based on an inject-files.yaml file",
		Long: `Inject files into a disk image partition.

Uses Azure Linux Image Customizer to inject files into an existing disk
image based on an inject-files.yaml configuration file.`,
		Example: `  # Inject files into an image
  azldev image inject-files --image-file base.vhdx --image-config inject-files.yaml --output-path out/`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			return true, injectFileIntoImage(env, options)
		}),
	}

	cmd.Flags().StringVarP(&options.imageFile, "image-file", "", "",
		"Path of the base Azure Linux image which the customization will be applied to")
	cmd.Flags().StringVarP(&options.imageConfigFile, "image-config", "", "",
		"Path of the image customization config file")
	cmd.Flags().StringVar(&options.outputImageFormat, "output-image-format", "",
		"Format of output image ("+getImageCustomizerImageFormatsString()+")")
	cmd.Flags().StringVar(&options.outputPath, "output-path", "",
		"Path to write the customized image artifacts to. It can be a path to "+
			"a file if the output format is a single file format (e.g., vhd, qcow2), "+
			"or a path to a directory if the output format is a multi-file format (e.g., pxe-dir, pxe-tar)")

	_ = cmd.RegisterFlagCompletionFunc("output-image-format",
		func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return getImageCustomizerImageFormats(), cobra.ShellCompDirectiveDefault
		})

	// While --image-file and --output-path are not required by the Image
	// Customizer, we are requiring them in azldev so that we can deduce which
	// folders to mount into the container.
	// See: https://dev.azure.com/mariner-org/polar/_workitems/edit/15282
	_ = cmd.MarkFlagRequired("image-file")
	_ = cmd.MarkFlagRequired("image-config")
	_ = cmd.MarkFlagRequired("output-path")

	return cmd
}

func injectFileIntoImage(
	env *azldev.Env, options *imageCustomizerOptions,
) error {
	return runImageCustomizerContainer(env, imageCustomizerInjectCmd, options)
}
