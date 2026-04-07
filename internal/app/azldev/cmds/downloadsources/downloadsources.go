// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package downloadsources

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
	"github.com/spf13/cobra"
)

// DownloadSourcesOptions holds the options for the download-sources command.
type DownloadSourcesOptions struct {
	Directory         string
	OutputDir         string
	LookasideBaseURIs []string
	PackageName       string
}

// OnAppInit registers the download-sources command as a subcommand of the given parent.
func OnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewDownloadSourcesCmd())
}

// NewDownloadSourcesCmd creates the download-sources cobra command.
func NewDownloadSourcesCmd() *cobra.Command {
	var options DownloadSourcesOptions

	cmd := &cobra.Command{
		Use:   "download-sources",
		Short: "Download source files listed in a Fedora-format sources file",
		Long: `Download source files from a lookaside cache based on a Fedora-format
'sources' file in the specified directory.

The command reads the 'sources' file, resolves the lookaside cache URI from
the distro configuration, and downloads each listed file into the directory.
Files that already exist in the directory are skipped.

The package name is derived from the directory name and can be overridden
with --package-name. The directory must contain a 'sources' file.

This command can run without project configuration by providing
--lookaside-uri explicitly.`,
		Example: `  # Download sources in the current directory (package name derived from dir name)
  azldev advanced download-sources

  # Download sources from a specific directory
  azldev advanced download-sources -d ./path/to/curl/

  # Download sources to a different output directory
  azldev advanced download-sources -o /tmp/output

  # Download sources using explicit lookaside URIs
  azldev advanced download-sources \\
    --lookaside-uri https://example.com/cache1 \\
    --lookaside-uri https://example.com/cache2

  # Download sources without project configuration
  azldev advanced download-sources \\
    --lookaside-uri https://example.com/cache -d ./curl`,
		RunE: azldev.RunFuncWithoutRequiredConfig(func(env *azldev.Env) (interface{}, error) {
			if options.Directory == "" {
				options.Directory = "."
			}

			return nil, DownloadSources(env, &options)
		}),
	}

	cmd.Flags().StringVarP(&options.Directory, "dir", "d", "",
		"source directory containing the 'sources' file (defaults to current directory)")
	_ = cmd.MarkFlagDirname("dir")

	cmd.Flags().StringVarP(&options.OutputDir, "output-dir", "o", "",
		"output directory for downloaded files (defaults to source directory)")
	_ = cmd.MarkFlagDirname("output-dir")

	cmd.Flags().StringArrayVar(&options.LookasideBaseURIs, "lookaside-uri", nil,
		"explicit lookaside base URI(s) to use instead of the distro configuration (can be specified multiple times)")

	cmd.Flags().StringVar(&options.PackageName, "package-name", "",
		"explicit package name to use instead of deriving from the directory name")

	return cmd
}

// DownloadSources downloads source files from a lookaside cache into the specified directory.
func DownloadSources(env *azldev.Env, options *DownloadSourcesOptions) error {
	packageName, lookasideBaseURIs, err := resolveDownloadParams(env, options)
	if err != nil {
		return err
	}

	event := env.StartEvent("Downloading sources", "packageName", packageName)
	defer event.End()

	// Build retry config from environment.
	retryCfg := retry.DefaultConfig()
	if env.NetworkRetries() > 0 {
		retryCfg.MaxAttempts = env.NetworkRetries()
	}

	// Create the HTTP downloader and lookaside source downloader.
	httpDownloader, err := downloader.NewHTTPDownloader(env, env, env.FS())
	if err != nil {
		return fmt.Errorf("failed to create HTTP downloader:\n%w", err)
	}

	lookasideDownloader, err := fedorasource.NewFedoraRepoExtractorImpl(
		env, env.FS(), httpDownloader, retryCfg,
	)
	if err != nil {
		return fmt.Errorf("failed to create lookaside downloader:\n%w", err)
	}

	downloadDir, err := prepareDownloadDir(env, options)
	if err != nil {
		return err
	}

	// Try each lookaside base URI until one succeeds.
	var downloadErr error

	for _, uri := range lookasideBaseURIs {
		slog.Info("Trying lookaside base URI", "uri", uri)

		downloadErr = lookasideDownloader.ExtractSourcesFromRepo(
			env, downloadDir, packageName, uri, nil,
		)
		if downloadErr == nil {
			break
		}

		slog.Warn("Failed to download sources from lookaside URI",
			"uri", uri, "error", downloadErr)
	}

	if downloadErr != nil {
		return fmt.Errorf("failed to download sources from any lookaside URI:\n%w", downloadErr)
	}

	slog.Info("Sources downloaded successfully", "outputDir", downloadDir)

	return nil
}

// prepareDownloadDir resolves the download directory, verifies the 'sources' file exists,
// and copies it to the output directory if needed.
func prepareDownloadDir(env *azldev.Env, options *DownloadSourcesOptions) (string, error) {
	sourceDir, err := filepath.Abs(options.Directory)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path for source directory %#q:\n%w", options.Directory, err)
	}

	// Verify the 'sources' file exists before attempting downloads.
	sourcesFilePath := filepath.Join(sourceDir, "sources")

	sourcesExists, err := fileutils.Exists(env.FS(), sourcesFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to check for sources file at %#q:\n%w", sourcesFilePath, err)
	}

	if !sourcesExists {
		return "", fmt.Errorf("no 'sources' file found in %#q", sourceDir)
	}

	downloadDir := sourceDir

	if options.OutputDir != "" {
		downloadDir, err = filepath.Abs(options.OutputDir)
		if err != nil {
			return "", fmt.Errorf("failed to resolve absolute path for output directory %#q:\n%w", options.OutputDir, err)
		}
	}

	// If downloading to a different directory, copy the 'sources' file there.
	if downloadDir != sourceDir {
		dstPath := filepath.Join(downloadDir, "sources")

		if err := fileutils.MkdirAll(env.FS(), downloadDir); err != nil {
			return "", fmt.Errorf("failed to create output directory %#q:\n%w", downloadDir, err)
		}

		if err := fileutils.CopyFile(env, env.FS(), sourcesFilePath, dstPath, fileutils.CopyFileOptions{}); err != nil {
			return "", fmt.Errorf("failed to copy sources file to output directory:\n%w", err)
		}
	}

	return downloadDir, nil
}

// resolveDownloadParams determines the package name and lookaside URIs.
func resolveDownloadParams(
	env *azldev.Env, options *DownloadSourcesOptions,
) (packageName string, lookasideBaseURIs []string, err error) {
	packageName, err = resolvePackageName(options)
	if err != nil {
		return "", nil, err
	}

	if len(options.LookasideBaseURIs) > 0 {
		return packageName, options.LookasideBaseURIs, nil
	}

	lookasideBaseURI, err := resolveLookasideURI(env)
	if err != nil {
		return "", nil, err
	}

	return packageName, []string{lookasideBaseURI}, nil
}

// resolvePackageName determines the package name from the --package-name flag or directory name.
func resolvePackageName(options *DownloadSourcesOptions) (string, error) {
	if options.PackageName != "" {
		return options.PackageName, nil
	}

	absDir, err := filepath.Abs(options.Directory)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path for %#q:\n%w", options.Directory, err)
	}

	packageName := filepath.Base(absDir)

	slog.Debug("Derived package name from directory name", "name", packageName, "dir", options.Directory)

	return packageName, nil
}

// resolveLookasideURI finds the lookaside base URI by checking the default distro first,
// then following the upstream distro reference if needed.
func resolveLookasideURI(env *azldev.Env) (string, error) {
	distroDef, distroVersionDef, err := env.Distro()
	if err != nil {
		return "", fmt.Errorf("failed to resolve default distro:\n%w", err)
	}

	// If the default distro itself has a lookaside URI, use it directly.
	if distroDef.LookasideBaseURI != "" {
		return distroDef.LookasideBaseURI, nil
	}

	// Otherwise, follow the upstream distro reference from the default component config.
	upstreamRef := distroVersionDef.DefaultComponentConfig.Spec.UpstreamDistro
	if upstreamRef.Name == "" {
		return "", errors.New("no lookaside base URI configured for the default distro, " +
			"and no upstream distro reference found; use --lookaside-uri to specify one")
	}

	upstreamDef, _, err := env.ResolveDistroRef(upstreamRef)
	if err != nil {
		return "", fmt.Errorf("failed to resolve upstream distro %#q:\n%w", upstreamRef.Name, err)
	}

	if upstreamDef.LookasideBaseURI == "" {
		return "", fmt.Errorf("no lookaside base URI configured for upstream distro %#q", upstreamRef.Name)
	}

	return upstreamDef.LookasideBaseURI, nil
}
