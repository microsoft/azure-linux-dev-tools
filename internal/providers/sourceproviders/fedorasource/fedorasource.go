// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../../../tools/mockgen/go.mod mockgen -source=fedorasource.go -destination=fedorasource_test/fedorasource_mocks.go -package=fedorasource_test --copyright_file=../../../../.license-preamble

package fedorasource

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
)

type FedoraSourceDownloader interface {
	// ExtractSourcesFromRepo processes a git repository by downloading any required
	// lookaside cache files into the repository directory.
	ExtractSourcesFromRepo(ctx context.Context, repoDir string, packageName string, lookasideBaseURI string) error
}

// FedoraSourceDownloaderImpl is an implementation of GitRepoExtractor.
type FedoraSourceDownloaderImpl struct {
	dryRunnable opctx.DryRunnable
	fileSystem  opctx.FS
	downloader  downloader.Downloader
	retryConfig retry.Config
}

// Ensure [FedoraSourceDownloaderImpl] implements [FedoraSourceDownloader].
var _ FedoraSourceDownloader = (*FedoraSourceDownloaderImpl)(nil)

// sourcesFilePattern matches lines in the Fedora/RHEL 'sources' file format.
// Example line: "SHA512 (example-1.0.tar.gz) = a1b2c3d4e5f6..."
// Capture groups:
//
//	1: Hash algorithm (e.g., "SHA512")
//	2: Filename (e.g., "example-1.0.tar.gz")
//	3: Hash value (e.g., "a1b2c3d4e5f6...")
var sourcesFilePattern = regexp.MustCompile(`^([A-Z0-9]+)\s+\(([^)]+)\)\s+=\s+([a-fA-F0-9]+)$`)

// sourcesFileLegacyPattern matches lines in the legacy sources file format.
// This is the older format used by some packages, typically with MD5 hashes.
// Example line: "7b74551e63f8ee6aab6fbc86676c0d37  zip30.tar.gz"
// Capture groups:
//
//	1: Hash value (e.g., "7b74551e63f8ee6aab6fbc86676c0d37")
//	2: Filename (e.g., "zip30.tar.gz")
var sourcesFileLegacyPattern = regexp.MustCompile(`^([a-fA-F0-9]+)\s+(\S+)$`)

// Capture group indexes for sourcesFilePattern.
const (
	sourcesPatternHashTypeIndex  = 1
	sourcesPatternFilenameIndex  = 2
	sourcesPatternHashValueIndex = 3
)

// Capture group indexes for sourcesFileLegacyPattern.
const (
	sourcesLegacyPatternHashValueIndex = 1
	sourcesLegacyPatternFilenameIndex  = 2
)

// sourceFileInfo contains metadata about a source file to be downloaded.
type sourceFileInfo struct {
	fileName     string
	uri          string
	hashType     fileutils.HashType
	expectedHash string
}

// NewFedoraRepoExtractorImpl creates a new instance of FedoraRepoExtractorImpl
// with the provided downloader.
func NewFedoraRepoExtractorImpl(
	dryRunnable opctx.DryRunnable,
	fileSystem opctx.FS,
	downloader downloader.Downloader,
	retryCfg retry.Config,
) (*FedoraSourceDownloaderImpl, error) {
	if fileSystem == nil {
		return nil, errors.New("filesystem cannot be nil")
	}

	if downloader == nil {
		return nil, errors.New("downloader cannot be nil")
	}

	if dryRunnable == nil {
		return nil, errors.New("dry runnable cannot be nil")
	}

	return &FedoraSourceDownloaderImpl{
		dryRunnable: dryRunnable,
		fileSystem:  fileSystem,
		downloader:  downloader,
		retryConfig: retryCfg,
	}, nil
}

// ExtractSourcesFromRepo processes the git repository by downloading any required
// lookaside cache files into the repository directory.
func (g *FedoraSourceDownloaderImpl) ExtractSourcesFromRepo(
	ctx context.Context, repoDir string, packageName string, lookasideBaseURI string,
) error {
	if repoDir == "" {
		return errors.New("repository directory cannot be empty")
	}

	if lookasideBaseURI == "" {
		return errors.New("lookaside base URI cannot be empty")
	}

	if err := verifyFedoraLookasideBaseURI(lookasideBaseURI); err != nil {
		return err
	}

	repoDirExists, err := fileutils.Exists(g.fileSystem, repoDir)
	if err != nil {
		return fmt.Errorf("failed to check if repository directory exists at %#q:\n%w", repoDir, err)
	}

	if !repoDirExists {
		return fmt.Errorf("repository directory does not exist at %#q, cloning failed", repoDir)
	}

	sourcesFilePath := filepath.Join(repoDir, "sources")

	sourcesExists, err := fileutils.Exists(g.fileSystem, sourcesFilePath)
	if err != nil {
		return fmt.Errorf("failed to check if sources file exists at %#q:\n%w", sourcesFilePath, err)
	}

	// If the sources file does not exist, there are no external sources to download
	if !sourcesExists {
		return nil
	}

	sourcesContent, err := fileutils.ReadFile(g.fileSystem, sourcesFilePath)
	if err != nil {
		return fmt.Errorf("failed to read sources file at %#q:\n%w", sourcesFilePath, err)
	}

	sourceFiles, err := parseSourcesFile(string(sourcesContent), packageName, lookasideBaseURI)
	if err != nil {
		return fmt.Errorf("failed to parse sources file at %#q:\n%w", sourcesFilePath, err)
	}

	err = g.downloadAndVerifySources(ctx, sourceFiles, repoDir)
	if err != nil {
		return fmt.Errorf("failed to download sources:\n%w", err)
	}

	return nil
}

func (g *FedoraSourceDownloaderImpl) downloadAndVerifySources(
	ctx context.Context,
	sourceFiles []sourceFileInfo,
	repoDir string,
) error {
	sourcesTotal := len(sourceFiles)
	for sourceIndex, sourceFile := range sourceFiles {
		destFilePath := filepath.Join(repoDir, sourceFile.fileName)

		exists, err := fileutils.Exists(g.fileSystem, destFilePath)
		if err != nil {
			return fmt.Errorf("failed to check if file exists at %#q:\n%w", destFilePath, err)
		}

		if exists {
			slog.Debug("File already exists, skipping download", "fileName", sourceFile.fileName, "path", destFilePath)

			continue
		}

		slog.Info("Downloading source file...",
			"progress", fmt.Sprintf("%d/%d", sourceIndex+1, sourcesTotal),
			"fileName", sourceFile.fileName,
			"URI", sourceFile.uri,
			"destPath", destFilePath,
		)

		if err := retry.Do(ctx, g.retryConfig, func() error {
			// Remove any partially written file from a prior failed attempt.
			_ = g.fileSystem.Remove(destFilePath)

			if downloadErr := g.downloader.Download(ctx, sourceFile.uri, destFilePath); downloadErr != nil {
				return fmt.Errorf("failed to download from %#q to %#q:\n%w",
					sourceFile.uri, destFilePath, downloadErr)
			}

			if hashErr := g.validateDownloadedFile(destFilePath, sourceFile); hashErr != nil {
				return fmt.Errorf("hash validation failed for %#q:\n%w", sourceFile.fileName, hashErr)
			}

			return nil
		}); err != nil {
			return fmt.Errorf("failed to retrieve source file %#q:\n%w", sourceFile.fileName, err)
		}
	}

	return nil
}

func (g *FedoraSourceDownloaderImpl) validateDownloadedFile(
	filePath string,
	sourceFile sourceFileInfo,
) error {
	if err := fileutils.ValidateFileHash(
		g.dryRunnable,
		g.fileSystem,
		sourceFile.hashType,
		filePath,
		sourceFile.expectedHash,
	); err != nil {
		return fmt.Errorf("failed to validate file hash:\n%w", err)
	}

	return nil
}

// parseSourcesFile parses the content of a Fedora/RHEL sources file and returns
// the list of source files to download. It supports both the modern format
// (e.g., "SHA512 (file.tar.gz) = abc123...") and the legacy MD5 format
// (e.g., "abc123...  file.tar.gz").
func parseSourcesFile(content string, packageName string, lookasideBaseURI string) ([]sourceFileInfo, error) {
	sourceFiles := []sourceFileInfo{}

	// Parse and validate each line in the sources file
	lines := strings.Split(content, "\n")
	for lineNum, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var (
			hashType string
			fileName string
			hash     string
		)

		// Try modern format first
		matches := sourcesFilePattern.FindStringSubmatch(line)
		if matches != nil {
			hashType = matches[sourcesPatternHashTypeIndex]
			fileName = matches[sourcesPatternFilenameIndex]
			hash = matches[sourcesPatternHashValueIndex]
		} else {
			// Try legacy format before failing
			legacyMatches := sourcesFileLegacyPattern.FindStringSubmatch(line)
			if legacyMatches == nil {
				return nil, fmt.Errorf("invalid format in sources file at line %d: %#q", lineNum+1, line)
			}

			hash = legacyMatches[sourcesLegacyPatternHashValueIndex]
			fileName = legacyMatches[sourcesLegacyPatternFilenameIndex]

			// Legacy format historically only used MD5
			hashType = "MD5"
		}

		sourceURI := lookasideBaseURI
		sourceURI = strings.ReplaceAll(sourceURI, "$pkg", packageName)
		sourceURI = strings.ReplaceAll(sourceURI, "$filename", fileName)
		sourceURI = strings.ReplaceAll(sourceURI, "$hashtype", strings.ToLower(hashType))
		sourceURI = strings.ReplaceAll(sourceURI, "$hash", hash)

		sourceFiles = append(sourceFiles, sourceFileInfo{
			fileName:     fileName,
			uri:          sourceURI,
			hashType:     fileutils.HashType(hashType),
			expectedHash: hash,
		})
	}

	return sourceFiles, nil
}

func verifyFedoraLookasideBaseURI(lookasideBaseURI string) error {
	// Check for placeholder variables in the lookaside URI
	requiredPlaceholders := []string{"$pkg", "$filename", "$hashtype", "$hash"}
	for _, placeholder := range requiredPlaceholders {
		if !strings.Contains(lookasideBaseURI, placeholder) {
			return fmt.Errorf("lookaside base URI is missing required placeholder: %s", placeholder)
		}
	}

	return nil
}
