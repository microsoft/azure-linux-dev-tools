// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
)

//go:generate go tool -modfile=../../../tools/mockgen/go.mod mockgen -package=sourceproviders_test -destination=sourceproviders_test/sourcemanager_mocks.go github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders SourceManager

// Provider is an abstract interface implemented by a source provider.
type Provider interface{}

// FileSourceProvider is an abstract interface implemented by a source provider that can retrieve individual
// source files.
type FileSourceProvider interface {
	Provider

	// GetFiles retrieves the specified source files and places them in the provided directory. If a file
	// is not known to (or handled by) the providers, the error will be (or will wrap) ErrNotFound.
	GetFiles(ctx context.Context, fileRefs []projectconfig.SourceFileReference, destDirPath string) error
}

// ComponentSourceProvider is an abstract interface implemented by a source provider that can retrieve the
// full file contents of a given component.
type ComponentSourceProvider interface {
	Provider

	// GetComponent retrieves the `.spec` for the specified component along with any sidecar
	// files stored along with it, placing the fetched files in the provided directory.
	GetComponent(ctx context.Context, component components.Component, destDirPath string) error
}

// SourceManager is an abstract interface for a facility that can fetch arbitrary component sources.
type SourceManager interface {
	// FetchFiles fetches the given source files, placing the files in the provided directory.
	FetchFiles(ctx context.Context, component components.Component, destDirPath string) error

	// FetchComponent fetches an entire upstream component, including its `.spec` file and any sidecar files.
	FetchComponent(ctx context.Context, component components.Component, destDirPath string) error
}

// DefaultDistroResolver is a function type that returns the project's default distro definition.
type DefaultDistroResolver func() (projectconfig.DistroDefinition, projectconfig.DistroVersionDefinition, error)

type sourceManager struct {
	// Upstream component providers (can have multiple, e.g., different RPM repos)
	upstreamComponentProviders []ComponentSourceProvider

	// File providers for individual files
	fileProviders []FileSourceProvider

	// Lookaside downloader for fetching source tarballs from upstream caches
	lookasideDownloader fedorasource.FedoraSourceDownloader

	// Retry configuration for network operations
	retryConfig retry.Config

	// Dependencies extracted from environment
	dryRunnable           opctx.DryRunnable
	eventListener         opctx.EventListener
	cmdFactory            opctx.CmdFactory
	fs                    opctx.FS
	distroResolver        DistroResolver
	defaultDistroResolver DefaultDistroResolver
}

var _ SourceManager = (*sourceManager)(nil)

func NewSourceManager(env *azldev.Env) (SourceManager, error) {
	if env == nil {
		return nil, errors.New("environment cannot be nil")
	}

	// Build retry config from environment
	retryCfg := retry.DefaultConfig()
	if env.NetworkRetries() > 0 {
		retryCfg.MaxAttempts = env.NetworkRetries()
	}

	// Extract dependencies from environment
	manager := &sourceManager{
		upstreamComponentProviders: make([]ComponentSourceProvider, 0),
		fileProviders:              make([]FileSourceProvider, 0),
		retryConfig:                retryCfg,
		dryRunnable:                env,
		eventListener:              env,
		cmdFactory:                 env,
		fs:                         env.FS(),
		distroResolver:             env.ResolveDistroRef,
		defaultDistroResolver:      env.Distro,
	}

	// Create lookaside downloader for fetching source tarballs
	err := manager.createLookasideDownloader()
	if err != nil {
		slog.Warn("Failed to create lookaside downloader; lookaside downloads will be disabled", "error", err)
	}

	// Create component providers
	err = manager.createComponentProviders()
	if err != nil {
		return nil, fmt.Errorf("failed to create source manager component providers:\n%w", err)
	}

	// Ensure at least one provider was created successfully
	if len(manager.upstreamComponentProviders) == 0 &&
		len(manager.fileProviders) == 0 {
		slog.Debug("No upstream source providers could be created; only local components will be supported")
	}

	return manager, nil
}

// createComponentProviders creates all component providers we may need.
func (m *sourceManager) createComponentProviders() error {
	// Create Git component provider with all required dependencies
	gitProvider, err := m.createGitContentsProvider()
	if err != nil {
		slog.Warn("Failed to setup Git component provider", "error", err)

		return fmt.Errorf("configuration for cloning components from Git failed:\n%w", err)
	}

	m.upstreamComponentProviders = append(m.upstreamComponentProviders, gitProvider)

	slog.Debug("Registered Git component provider")

	return nil
}

func (m *sourceManager) FetchFiles(
	ctx context.Context,
	component components.Component,
	destDirPath string,
) error {
	sourceFiles := component.GetConfig().SourceFiles
	if len(sourceFiles) == 0 {
		slog.Debug("No source files to fetch for component", "component", component.GetName())

		return nil
	}

	httpDownloader, err := downloader.NewHTTPDownloader(m.dryRunnable, m.eventListener, m.fs)
	if err != nil {
		return fmt.Errorf("failed to create HTTP downloader:\n%w", err)
	}

	for i := range sourceFiles {
		fileRef := &sourceFiles[i]

		err := m.fetchSourceFile(ctx, httpDownloader, fileRef, destDirPath)
		if err != nil {
			return fmt.Errorf("failed to fetch source file %#q:\n%w", fileRef.Filename, err)
		}
	}

	return nil
}

// fetchSourceFile downloads a source file from its configured URL.
func (m *sourceManager) fetchSourceFile(
	ctx context.Context,
	httpDownloader downloader.Downloader,
	fileRef *projectconfig.SourceFileReference,
	destDirPath string,
) error {
	if fileRef.Origin.Uri == "" {
		return fmt.Errorf("no URL configured for source file \n%#q", fileRef.Filename)
	}

	// Validate filename to prevent path traversal vulnerabilities
	if err := validateFilename(fileRef.Filename); err != nil {
		return fmt.Errorf("invalid source file reference:\n%w", err)
	}

	destPath := filepath.Join(destDirPath, fileRef.Filename)

	sourceExists, err := fileutils.Exists(m.fs, destPath)
	if err != nil {
		return fmt.Errorf("failed to check existence of destination file %#q:\n%w", destPath, err)
	}

	if sourceExists {
		slog.Debug("Source file already exists, skipping download",
			"filename", fileRef.Filename,
			"path", destPath)

		return nil
	}

	switch fileRef.Origin.Type {
	case projectconfig.OriginTypeURI:
		slog.Info("Downloading source file from URL",
			"filename", fileRef.Filename,
			"origin", fileRef.Origin.Uri,
			"destination", destPath)

		err = retry.Do(ctx, m.retryConfig, func() error {
			// Remove any partially written file from a prior failed attempt.
			_ = m.fs.Remove(destPath)

			downloadErr := httpDownloader.Download(ctx, fileRef.Origin.Uri, destPath)
			if downloadErr != nil {
				return fmt.Errorf("failed to download %#q from %#q:\n%w",
					fileRef.Filename, fileRef.Origin.Uri, downloadErr)
			}

			if fileRef.Hash != "" && fileRef.HashType != "" {
				hashErr := fileutils.ValidateFileHash(m.dryRunnable, m.fs, fileRef.HashType, destPath, fileRef.Hash)
				if hashErr != nil {
					return fmt.Errorf("hash validation failed for %#q:\n%w", fileRef.Filename, hashErr)
				}
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to retrieve source file %#q:\n%w", fileRef.Filename, err)
		}

	default:
		return fmt.Errorf("unsupported origin type %#q for source file %#q", fileRef.Origin.Type, fileRef.Filename)
	}

	return nil
}

func (m *sourceManager) FetchComponent(ctx context.Context, component components.Component, destDirPath string) error {
	if component.GetName() == "" {
		return errors.New("component name is empty")
	}

	sourceType := component.GetConfig().Spec.SourceType

	switch sourceType {
	case projectconfig.SpecSourceTypeLocal, projectconfig.SpecSourceTypeUnspecified:
		return m.fetchLocalComponent(ctx, component, destDirPath)

	case projectconfig.SpecSourceTypeUpstream:
		return m.fetchUpstreamComponent(ctx, component, destDirPath)
	}

	return fmt.Errorf("spec for component %#q not found in any configured provider",
		component.GetName())
}

func (m *sourceManager) fetchLocalComponent(
	ctx context.Context, component components.Component, destDirPath string,
) error {
	err := FetchLocalComponent(m.dryRunnable, m.eventListener, m.fs, component, destDirPath, false)
	if err != nil {
		return fmt.Errorf("failed to fetch local component %#q:\n%w",
			component.GetName(), err)
	}

	// Download source files from lookaside cache if available
	err = m.downloadLookasideSources(ctx, component, destDirPath)
	if err != nil {
		return fmt.Errorf("failed to download lookaside sources for component %#q:\n%w",
			component.GetName(), err)
	}

	return nil
}

// resolveLookasideURI finds a lookaside base URI for the component.
// It checks the component's upstream distro first, then falls back to the project default.
// Each distro is followed up the chain until one with a lookaside URI is found.
func (m *sourceManager) resolveLookasideURI(component components.Component) string {
	// Try component's upstream distro first
	if ref := component.GetConfig().Spec.UpstreamDistro; ref.Name != "" {
		if uri := m.getLookasideURIFromDistroChain(ref); uri != "" {
			return uri
		}
	}

	// Fall back to project's default distro
	if m.defaultDistroResolver == nil {
		return ""
	}

	distroDef, distroVersionDef, err := m.defaultDistroResolver()
	if err != nil {
		return ""
	}

	return m.getLookasideURIFromDistro(distroDef, distroVersionDef)
}

// getLookasideURIFromDistroChain resolves a distro reference and follows the chain
// to find a lookaside URI.
func (m *sourceManager) getLookasideURIFromDistroChain(ref projectconfig.DistroReference) string {
	distroDef, distroVersionDef, err := m.distroResolver(ref)
	if err != nil {
		return ""
	}

	return m.getLookasideURIFromDistro(distroDef, distroVersionDef)
}

// getLookasideURIFromDistro returns the lookaside URI from the distro, or follows
// the upstream chain if the distro doesn't have one.
func (m *sourceManager) getLookasideURIFromDistro(
	distroDef projectconfig.DistroDefinition,
	distroVersionDef projectconfig.DistroVersionDefinition,
) string {
	if distroDef.LookasideBaseURI != "" {
		return distroDef.LookasideBaseURI
	}

	// Follow upstream chain
	upstreamRef := distroVersionDef.DefaultComponentConfig.Spec.UpstreamDistro
	if upstreamRef.Name == "" {
		return ""
	}

	return m.getLookasideURIFromDistroChain(upstreamRef)
}

// downloadLookasideSources downloads source tarballs from a lookaside cache for the given component.
// It resolves the appropriate lookaside URI from the distro configuration and uses the component's
// upstream name (if set) as the package name for the lookaside lookup.
// Returns nil if no lookaside downloader or URI is available.
func (m *sourceManager) downloadLookasideSources(
	ctx context.Context, component components.Component, destDirPath string,
) error {
	if m.lookasideDownloader == nil {
		return nil
	}

	lookasideURI := m.resolveLookasideURI(component)
	if lookasideURI == "" {
		return nil
	}

	// Determine the package name to use for the lookaside lookup
	packageName := component.GetName()
	if upstreamName := component.GetConfig().Spec.UpstreamName; upstreamName != "" {
		packageName = upstreamName
	}

	err := m.lookasideDownloader.ExtractSourcesFromRepo(ctx, destDirPath, packageName, lookasideURI)
	if err != nil {
		return fmt.Errorf("failed to extract sources from lookaside cache:\n%w", err)
	}

	return nil
}

func (m *sourceManager) fetchUpstreamComponent(
	ctx context.Context, component components.Component, destDirPath string,
) error {
	if len(m.upstreamComponentProviders) == 0 {
		return fmt.Errorf("no upstream component origins configured for component %#q",
			component.GetName())
	}

	var lastError error

	// Try each upstream component provider, until one succeeds
	for _, provider := range m.upstreamComponentProviders {
		err := provider.GetComponent(ctx, component, destDirPath)
		if err == nil {
			slog.Debug("Successfully fetched upstream component",
				"component", component.GetName(),
				"provider", fmt.Sprintf("%T", provider))

			return nil
		}

		lastError = err
	}

	// If we tried providers but none succeeded
	return fmt.Errorf("failed to fetch upstream component %#q:\n%w",
		component.GetName(), lastError)
}

func (m *sourceManager) createLookasideDownloader() error {
	httpDownloader, err := downloader.NewHTTPDownloader(m.dryRunnable, m.eventListener, m.fs)
	if err != nil {
		return fmt.Errorf("failed to create HTTP downloader:\n%w", err)
	}

	extractor, err := fedorasource.NewFedoraRepoExtractorImpl(
		m.dryRunnable,
		m.fs,
		httpDownloader,
		m.retryConfig,
	)
	if err != nil {
		return fmt.Errorf("failed to create lookaside downloader:\n%w", err)
	}

	m.lookasideDownloader = extractor

	return nil
}

func (m *sourceManager) createGitContentsProvider() (*FedoraSourcesProviderImpl, error) {
	gitProvider, err := git.NewGitProviderImpl(m.eventListener, m.cmdFactory)
	if err != nil {
		return nil, fmt.Errorf("failed to create git provider:\n%w", err)
	}

	if m.lookasideDownloader == nil {
		return nil, errors.New("lookaside downloader is required for Git component provider")
	}

	return NewFedoraSourcesProviderImpl(
		m.fs,
		m.dryRunnable,
		gitProvider,
		m.lookasideDownloader,
		m.distroResolver,
		m.retryConfig,
	)
}

// validateFilename ensures a filename is safe for use as a destination path.
// It rejects filenames that could escape the destination directory via path traversal.
func validateFilename(filename string) error {
	if filename == "" {
		return errors.New("filename cannot be empty")
	}

	// Check for absolute paths
	if filepath.IsAbs(filename) {
		return fmt.Errorf("filename %#q cannot be an absolute path", filename)
	}

	cleaned := filepath.Clean(filename)
	if cleaned != filename {
		return fmt.Errorf("filename %#q contains path traversal elements", filename)
	}

	if filepath.Base(filename) != filename {
		return fmt.Errorf("filename %#q must be a simple filename without directory components", filename)
	}

	return nil
}
