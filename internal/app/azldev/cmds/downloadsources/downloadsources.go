// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package downloadsources

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
	"github.com/spf13/cobra"
)

// DownloadSourcesOptions holds the options for the download-sources command.
type DownloadSourcesOptions struct {
	Directory           string
	OutputDir           string
	LookasideBaseURIs   []string
	ComponentName       string
	LookasideDownloader fedorasource.FedoraSourceDownloader
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

Either --component or --lookaside-uri must be provided:

  --component (-p)    Uses the component's distro configuration to resolve
                      the lookaside URI and package name.
  --lookaside-uri     Provides the URI explicitly (no project config needed).
                      Package name is derived from the directory name.`,
		Example: `  # Download sources for a component (uses component's distro config)
  azldev advanced download-sources -p curl

  # Download sources using explicit lookaside URI (no config needed)
  azldev advanced download-sources \
    --lookaside-uri 'https://example.com/$pkg/$filename/$hashtype/$hash/$filename'

  # Specify a different source directory
  azldev advanced download-sources -p curl -d ./path/to/sources/

  # Download to a different output directory
  azldev advanced download-sources -p curl -o /tmp/output

  # Try multiple lookaside URIs in order
  azldev advanced download-sources \
    --lookaside-uri 'https://cache1.example.com/$pkg/$filename/$hashtype/$hash/$filename' \
    --lookaside-uri 'https://cache2.example.com/$pkg/$filename/$hashtype/$hash/$filename'`,
		Annotations: map[string]string{
			azldev.CommandAnnotationRootOK: "true",
		},
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

	cmd.Flags().StringVarP(&options.ComponentName, "component", "p", "",
		"component name to resolve distro and package name from")

	cmd.Flags().StringArrayVar(&options.LookasideBaseURIs, "lookaside-uri", nil,
		"explicit lookaside base URI(s) to try in order, first success wins "+
			"(can be specified multiple times)")

	cmd.MarkFlagsOneRequired("component", "lookaside-uri")
	cmd.MarkFlagsMutuallyExclusive("component", "lookaside-uri")

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

	lookasideDownloader := options.LookasideDownloader
	if lookasideDownloader == nil {
		lookasideDownloader, err = createLookasideDownloader(env)
		if err != nil {
			return err
		}
	}

	// Build extract options.
	var extractOpts []fedorasource.ExtractOption
	if options.OutputDir != "" {
		extractOpts = append(extractOpts, fedorasource.WithOutputDir(options.OutputDir))
	}

	// Try each lookaside base URI until one succeeds.
	var downloadErr error

	for _, uri := range lookasideBaseURIs {
		slog.Info("Trying lookaside base URI", "uri", uri)

		uriErr := lookasideDownloader.ExtractSourcesFromRepo(
			env, options.Directory, packageName, uri, nil, extractOpts...,
		)
		if uriErr == nil {
			downloadErr = nil

			break
		}

		slog.Warn("Failed to download sources from lookaside URI",
			"uri", uri, "error", uriErr)

		downloadErr = errors.Join(downloadErr, uriErr)
	}

	if downloadErr != nil {
		return fmt.Errorf("failed to download sources from any lookaside URI:\n%w",
			downloadErr)
	}

	outputDir := options.Directory
	if options.OutputDir != "" {
		outputDir = options.OutputDir
	}

	absOutputDir, absErr := filepath.Abs(outputDir)
	if absErr != nil {
		absOutputDir = outputDir
	}

	slog.Info("Sources downloaded successfully", "outputDir", absOutputDir)

	return nil
}

// createLookasideDownloader builds the default [fedorasource.FedoraSourceDownloader]
// from the environment's network and filesystem configuration.
func createLookasideDownloader(env *azldev.Env) (fedorasource.FedoraSourceDownloader, error) {
	retryCfg := retry.DefaultConfig()
	if env.NetworkRetries() > 0 {
		retryCfg.MaxAttempts = env.NetworkRetries()
	}

	httpDownloader, err := downloader.NewHTTPDownloader(env, env, env.FS())
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP downloader:\n%w", err)
	}

	lookasideDownloader, err := fedorasource.NewFedoraRepoExtractorImpl(
		env, env.FS(), httpDownloader, retryCfg,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create lookaside downloader:\n%w", err)
	}

	return lookasideDownloader, nil
}

// resolveDownloadParams determines the package name and lookaside URIs.
// In component mode, both are resolved from the component's config.
// In standalone mode, lookaside URIs come from the flag and the package name
// is derived from the directory basename.
func resolveDownloadParams(
	env *azldev.Env, options *DownloadSourcesOptions,
) (packageName string, lookasideBaseURIs []string, err error) {
	if len(options.LookasideBaseURIs) == 0 && options.ComponentName == "" {
		return "", nil, errors.New(
			"either --component or --lookaside-uri must be provided")
	}

	// Standalone mode: --lookaside-uri provided.
	if len(options.LookasideBaseURIs) > 0 {
		packageName, err = resolvePackageNameFromDir(options)
		if err != nil {
			return "", nil, err
		}

		return packageName, options.LookasideBaseURIs, nil
	}

	// Component mode: --component provided.
	return resolveFromComponent(env, options)
}

// resolvePackageNameFromDir derives the package name from the directory basename.
func resolvePackageNameFromDir(options *DownloadSourcesOptions) (string, error) {
	absDir, err := filepath.Abs(options.Directory)
	if err != nil {
		return "", fmt.Errorf(
			"failed to resolve absolute path for %#q:\n%w",
			options.Directory, err)
	}

	packageName := filepath.Base(absDir)

	slog.Debug("Derived package name from directory name",
		"name", packageName, "dir", options.Directory)

	return packageName, nil
}

// resolveFromComponent resolves both the package name and lookaside URI
// from a component's configuration.
func resolveFromComponent(
	env *azldev.Env, options *DownloadSourcesOptions,
) (packageName string, lookasideBaseURIs []string, err error) {
	resolver := components.NewResolver(env)

	filter := &components.ComponentFilter{
		ComponentNamePatterns: []string{options.ComponentName},
	}

	comps, err := resolver.FindComponents(filter)
	if err != nil {
		return "", nil, fmt.Errorf("failed to resolve component %#q:\n%w",
			options.ComponentName, err)
	}

	if comps.Len() == 0 {
		return "", nil, fmt.Errorf("component %#q not found",
			options.ComponentName)
	}

	if comps.Len() != 1 {
		return "", nil, fmt.Errorf(
			"expected exactly one component for %#q, got %d",
			options.ComponentName, comps.Len())
	}

	component := comps.Components()[0]

	// Derive package name from the component's upstream-name or component name.
	packageName = component.GetName()
	if upstreamName := component.GetConfig().Spec.UpstreamName; upstreamName != "" {
		packageName = upstreamName
	}

	distro, err := sourceproviders.ResolveDistro(env, component)
	if err != nil {
		return "", nil, fmt.Errorf(
			"failed to resolve distro for component %#q:\n%w",
			options.ComponentName, err)
	}

	if distro.Definition.LookasideBaseURI == "" {
		return "", nil, fmt.Errorf(
			"no lookaside base URI configured for distro %#q",
			distro.Ref.Name)
	}

	return packageName, []string{distro.Definition.LookasideBaseURI}, nil
}
