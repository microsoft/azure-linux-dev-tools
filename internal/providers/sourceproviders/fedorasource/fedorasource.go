// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../../../tools/mockgen/go.mod mockgen -source=fedorasource.go -destination=fedorasource_test/fedorasource_mocks.go -package=fedorasource_test --copyright_file=../../../../.license-preamble

package fedorasource

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/downloader"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
)

// SourcesFileName is the name of the Fedora/RHEL 'sources' metadata file.
const SourcesFileName = "sources"

type FedoraSourceDownloader interface {
	// ExtractSourcesFromRepo processes a git repository by downloading any required
	// lookaside cache files into the repository directory. Files whose names appear
	// in skipFilenames are not downloaded (e.g., files already fetched separately).
	// Optional [ExtractOption] values can override default behavior.
	ExtractSourcesFromRepo(
		ctx context.Context, repoDir string, packageName string,
		lookasideBaseURI string, skipFilenames []string,
		opts ...ExtractOption,
	) error
}

// extractOptions holds optional configuration for [ExtractSourcesFromRepo].
type extractOptions struct {
	outputDir string
}

// ExtractOption is a functional option for [ExtractSourcesFromRepo].
type ExtractOption func(*extractOptions)

// WithOutputDir specifies a separate directory for downloaded files.
// When set, the sources file is read from repoDir but files are
// downloaded into outputDir instead.
func WithOutputDir(dir string) ExtractOption {
	return func(o *extractOptions) {
		o.outputDir = dir
	}
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

// SourcesFileEntry represents a parsed entry from a Fedora/RHEL sources file.
// This struct is used for both reading existing sources files and generating new entries.
type SourcesFileEntry struct {
	Filename string
	HashType fileutils.HashType
	Hash     string
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
	ctx context.Context, repoDir string, packageName string, lookasideBaseURI string, skipFileNames []string,
	opts ...ExtractOption,
) error {
	if repoDir == "" {
		return errors.New("repository directory cannot be empty")
	}

	if lookasideBaseURI == "" {
		return errors.New("lookaside base URI cannot be empty")
	}

	// Apply functional options.
	var options extractOptions

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(&options)
	}

	repoDirExists, err := fileutils.Exists(g.fileSystem, repoDir)
	if err != nil {
		return fmt.Errorf("failed to check if repository directory exists at %#q:\n%w", repoDir, err)
	}

	if !repoDirExists {
		return fmt.Errorf("repository directory does not exist at %#q, cloning failed", repoDir)
	}

	sourcesFilePath := filepath.Join(repoDir, SourcesFileName)

	sourcesExists, err := fileutils.Exists(g.fileSystem, sourcesFilePath)
	if err != nil {
		return fmt.Errorf("failed to check if sources file exists at %#q:\n%w", sourcesFilePath, err)
	}

	// If the sources file does not exist, there are no external sources to download.
	if !sourcesExists {
		slog.Info("No 'sources' file found, nothing to download", "dir", repoDir)

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

	skipSet := make(map[string]bool, len(skipFileNames))
	for _, name := range skipFileNames {
		skipSet[name] = true
	}

	// Determine where to write downloaded files.
	destDir := repoDir
	if options.outputDir != "" {
		destDir = options.outputDir

		if err := fileutils.MkdirAll(g.fileSystem, destDir); err != nil {
			return fmt.Errorf("failed to create output directory %#q:\n%w", destDir, err)
		}
	}

	err = g.downloadAndVerifySources(ctx, sourceFiles, destDir, skipSet)
	if err != nil {
		return fmt.Errorf("failed to download sources:\n%w", err)
	}

	return nil
}

func (g *FedoraSourceDownloaderImpl) downloadAndVerifySources(
	ctx context.Context,
	sourceFiles []sourceFileInfo,
	repoDir string,
	skipSet map[string]bool,
) error {
	sourcesTotal := len(sourceFiles)
	for sourceIndex, sourceFile := range sourceFiles {
		if skipSet[sourceFile.fileName] {
			slog.Debug("File already provided, skipping lookaside download",
				"fileName", sourceFile.fileName)

			continue
		}

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
			// Remove any bad file left by the final failed attempt so subsequent
			// callers (e.g. retrying with a different URI) don't see it as valid.
			_ = g.fileSystem.Remove(destFilePath)

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
	entries, err := ReadSourcesFileEntries(content)
	if err != nil {
		return nil, err
	}

	sourceFiles := make([]sourceFileInfo, 0, len(entries))

	for _, entry := range entries {
		sourceURI, err := BuildLookasideURL(
			lookasideBaseURI, packageName, entry.Filename,
			string(entry.HashType), entry.Hash,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build lookaside URL for file %#q:\n%w", entry.Filename, err)
		}

		// Validate the filename to prevent path traversal attacks from crafted sources entries.
		if err := fileutils.ValidateFilename(entry.Filename); err != nil {
			return nil, fmt.Errorf("unsafe filename in sources file %#q:\n%w", entry.Filename, err)
		}

		sourceFiles = append(sourceFiles, sourceFileInfo{
			fileName:     entry.Filename,
			uri:          sourceURI,
			hashType:     entry.HashType,
			expectedHash: entry.Hash,
		})
	}

	return sourceFiles, nil
}

// ReadSourcesFileEntries parses the content of a Fedora/RHEL sources file and returns
// the list of source file entries. It supports both the modern format
// (e.g., "SHA512 (file.tar.gz) = abc123...") and the legacy MD5 format
// (e.g., "abc123...  file.tar.gz").
func ReadSourcesFileEntries(content string) ([]SourcesFileEntry, error) {
	lines := strings.Split(content, "\n")

	entries := make([]SourcesFileEntry, 0, len(lines))
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
			hashType = string(fileutils.HashTypeMD5)
		}

		entries = append(entries, SourcesFileEntry{
			Filename: fileName,
			HashType: fileutils.HashType(hashType),
			Hash:     hash,
		})
	}

	return entries, nil
}

// FormatSourcesEntry formats a sources file entry in the modern Fedora/RHEL format.
// Example output: "SHA512 (example-1.0.tar.gz) = a1b2c3d4e5f6..."
//
// The [hashType] must be a canonical [fileutils.HashType] constant.
// The hash value is included as-is without case normalization.
func FormatSourcesEntry(filename string, hashType fileutils.HashType, hash string) string {
	return fmt.Sprintf("%s (%s) = %s", string(hashType), filename, hash)
}

// Lookaside URI template placeholders supported by [BuildLookasideURL].
const (
	// PlaceholderPkg is replaced with the package name.
	PlaceholderPkg = "$pkg"
	// PlaceholderFilename is replaced with the source file name.
	PlaceholderFilename = "$filename"
	// PlaceholderHashType is replaced with the lowercase hash algorithm (e.g., "sha512").
	PlaceholderHashType = "$hashtype"
	// PlaceholderHash is replaced with the hash value.
	PlaceholderHash = "$hash"
)

// validateAbsoluteURL parses uri and verifies it is an absolute URL with a
// non-empty scheme and host. The label parameter is used in error messages to
// identify the URL's purpose (e.g. "lookaside", "dist-git").
func validateAbsoluteURL(uri, label string) error {
	u, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("resulting %s URL is not valid:\n%w", label, err)
	}

	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("resulting %s URL %#q is missing scheme or host", label, uri)
	}

	return nil
}

// BuildLookasideURL constructs a lookaside cache URL by substituting placeholders in the
// URI template with the provided values. Supported placeholders are [PlaceholderPkg],
// [PlaceholderFilename], [PlaceholderHashType], and [PlaceholderHash].
// Placeholders not present in the template are simply ignored.
//
// Substituted values are URL path-escaped via [url.PathEscape] so that reserved
// characters such as /, ?, #, and % do not alter the URL structure.
//
// Returns an error if any of the provided values contain a placeholder string, as this
// would cause ambiguous substitution results depending on replacement order, or if the
// resulting URL is not valid.
func BuildLookasideURL(template, packageName, fileName, hashType, hash string) (string, error) {
	// allPlaceholders lists all supported lookaside URI template placeholders.
	allPlaceholders := []string{PlaceholderPkg, PlaceholderFilename, PlaceholderHashType, PlaceholderHash}
	hashType = strings.ToLower(hashType)

	// Normalize hashType to lowercase since that is the form actually substituted.
	hashType = strings.ToLower(hashType)

	for _, v := range []string{packageName, fileName, hashType, hash} {
		for _, p := range allPlaceholders {
			if strings.Contains(v, p) {
				return "", fmt.Errorf("value %#q contains placeholder %#q, which would cause ambiguous substitution", v, p)
			}
		}
	}

	uri := template
	uri = strings.ReplaceAll(uri, PlaceholderPkg, url.PathEscape(packageName))
	uri = strings.ReplaceAll(uri, PlaceholderFilename, url.PathEscape(fileName))
	uri = strings.ReplaceAll(uri, PlaceholderHashType, url.PathEscape(hashType))
	uri = strings.ReplaceAll(uri, PlaceholderHash, url.PathEscape(hash))

	if err := validateAbsoluteURL(uri, "lookaside"); err != nil {
		return "", err
	}

	return uri, nil
}

// BuildDistGitURL constructs a dist-git repository URL by substituting the
// [PlaceholderPkg] placeholder in the URI template with the provided package name.
//
// The package name is URL path-escaped via [url.PathEscape] so that reserved
// characters such as /, ?, #, and % do not alter the URL structure.
//
// Returns an error if the package name contains a placeholder string, or if the
// resulting URL is not valid.
func BuildDistGitURL(template, packageName string) (string, error) {
	if strings.Contains(packageName, PlaceholderPkg) {
		return "", fmt.Errorf("package name %#q contains placeholder %#q, which would cause ambiguous substitution",
			packageName, PlaceholderPkg)
	}

	uri := strings.ReplaceAll(template, PlaceholderPkg, url.PathEscape(packageName))

	if err := validateAbsoluteURL(uri, "dist-git"); err != nil {
		return "", err
	}

	return uri, nil
}
