// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package advanced

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
	"github.com/spf13/cobra"
)

func wgetOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewWgetCmd())
}

// Options controlling how a file is downloaded from an internet source.
type DownloadOptions struct {
	uri            string
	outputFilePath string
}

// Constructs a [cobra.Command] for the 'wget' command.
func NewWgetCmd() *cobra.Command {
	options := &DownloadOptions{}

	// We don't *require* a valid project configuration, but may use one if it's available.
	cmd := &cobra.Command{
		Use:   "wget URI",
		Short: "Download files via https",
		Long: `Download a file from an HTTPS URI to a local path.

This is a simple download utility that respects azldev's network retry
configuration. It is primarily used internally but can be invoked directly.`,
		Example: `  # Download a file
  azldev advanced wget --uri https://example.com/file.tar.gz -o ./file.tar.gz`,
		RunE: azldev.RunFuncWithoutRequiredConfig(func(env *azldev.Env) (results interface{}, err error) {
			return true, Download(env, options)
		}),
	}

	cmd.Flags().StringVar(&options.uri, "uri", "", "URI to download")
	cmd.Flags().StringVarP(&options.outputFilePath, "output", "o", "", "Path to output file")

	_ = cmd.MarkFlagRequired("uri")
	_ = cmd.MarkFlagRequired("output")

	return cmd
}

// Performs the file download requested by options.
func Download(env *azldev.Env, options *DownloadOptions) error {
	httpDownloader, err := downloader.NewHTTPDownloader(env, env, env.FS())
	if err != nil {
		return fmt.Errorf("failed to create downloader:\n%w", err)
	}

	err = httpDownloader.Download(env, options.uri, options.outputFilePath)
	if err != nil {
		return fmt.Errorf("failed to download '%s' to '%s':\n%w", options.uri, options.outputFilePath, err)
	}

	return nil
}
