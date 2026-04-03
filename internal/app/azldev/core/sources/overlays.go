// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/rootfs"
	"github.com/samber/lo"
	"github.com/spf13/afero"
)

// Error to return when an overlay did not apply to its target (e.g., a search-and-replace
// overlay that found no matches).
var ErrOverlayDidNotApply = errors.New("overlay did not apply to target")

// isSpecFile returns true if the given file path refers to a spec file.
func isSpecFile(filePath string) bool {
	return strings.HasSuffix(filePath, ".spec")
}

// ApplyOverlayToSources applies the provided overlay to the specified spec and related sources.
// Files are mutated in-place. If an error occurs mid-way through applying multiple overlays,
// previously applied changes will remain. Callers should consider working on copies of source
// files if atomicity is required.
func ApplyOverlayToSources(
	dryRunnable opctx.DryRunnable,
	fs opctx.FS,
	overlay projectconfig.ComponentOverlay,
	sourcesDirPath, specPath string,
) error {
	// Apply the spec component, if any.
	if overlay.ModifiesSpec() {
		err := ApplySpecOverlayToFileInPlace(fs, overlay, specPath)
		if err != nil {
			return err
		}
	}

	// Apply the non-spec file component, if any.
	if !overlay.ModifiesNonSpecFiles() {
		return nil
	}

	// Create a destination filesystem confined to the sources directory.
	// For real filesystems (OsFs), this uses os.Root for atomic symlink safety.
	// For test filesystems (MemMapFs), we use BasePathFs to confine paths.
	destFS, err := newDestFS(fs, sourcesDirPath)
	if err != nil {
		return fmt.Errorf("failed to open sources directory root %#q:\n%w", sourcesDirPath, err)
	}

	// It's our responsibility to close destFS if it implements [io.Closer].
	defer func() {
		if closer, ok := destFS.(io.Closer); ok {
			_ = closer.Close()
		}
	}()

	return applyNonSpecOverlay(dryRunnable, fs, destFS, overlay)
}

// ApplySpecOverlayToFileInPlace applies the given overlay to the specified spec file.
// Changes are made in-place.
func ApplySpecOverlayToFileInPlace(fs opctx.FS, overlay projectconfig.ComponentOverlay, specPath string) error {
	specFile, err := fs.Open(specPath)
	if err != nil {
		return fmt.Errorf("failed to open spec %#q for reading:\n%w", specPath, err)
	}

	openedSpec, err := spec.OpenSpec(specFile)
	specFile.Close()

	if err != nil {
		return fmt.Errorf("failed to load spec %#q:\n%w", specPath, err)
	}

	err = ApplySpecOverlay(overlay, openedSpec)
	if err != nil {
		return fmt.Errorf("failed to apply overlay to spec %#q:\n%w", specPath, err)
	}

	slog.Debug("Saving updated spec", "specPath", specPath)

	specFile, err = fs.OpenFile(specPath, os.O_RDWR|os.O_TRUNC, fileperms.PrivateFile)
	if err != nil {
		return fmt.Errorf("failed to open spec %#q for writing:\n%w", specPath, err)
	}

	defer defers.HandleDeferError(specFile.Close, &err)

	err = openedSpec.Serialize(specFile)
	if err != nil {
		return fmt.Errorf("failed to serialize updated spec %#q:\n%w", specPath, err)
	}

	return nil
}

// ApplySpecOverlay applies a spec-based overlay to an opened spec. An error is returned if a non-spec
// overlay is provided.
//
//nolint:cyclop,funlen // This function's complexity is inflated by the big switch over overlay types.
func ApplySpecOverlay(overlay projectconfig.ComponentOverlay, openedSpec *spec.Spec) error {
	//nolint:exhaustive // We intentionally ignore non-spec overlay types.
	switch overlay.Type {
	case projectconfig.ComponentOverlayAddSpecTag:
		err := openedSpec.AddTag(overlay.PackageName, overlay.Tag, overlay.Value)
		if err != nil {
			return fmt.Errorf("failed to add tag %#q to spec:\n%w", overlay.Tag, err)
		}
	case projectconfig.ComponentOverlayInsertSpecTag:
		err := openedSpec.InsertTag(overlay.PackageName, overlay.Tag, overlay.Value)
		if err != nil {
			return fmt.Errorf("failed to insert tag %#q into spec:\n%w", overlay.Tag, err)
		}
	case projectconfig.ComponentOverlayUpdateSpecTag:
		err := openedSpec.UpdateExistingTag(overlay.PackageName, overlay.Tag, overlay.Value)
		if err != nil {
			return fmt.Errorf("failed to update tag %#q in spec:\n%w", overlay.Tag, err)
		}
	case projectconfig.ComponentOverlaySetSpecTag:
		err := openedSpec.SetTag(overlay.PackageName, overlay.Tag, overlay.Value)
		if err != nil {
			return fmt.Errorf("failed to set tag %#q in spec:\n%w", overlay.Tag, err)
		}
	case projectconfig.ComponentOverlayRemoveSpecTag:
		err := openedSpec.RemoveTag(overlay.PackageName, overlay.Tag, overlay.Value)
		if err != nil {
			return fmt.Errorf("failed to remove tag %#q from spec:\n%w", overlay.Tag, err)
		}
	case projectconfig.ComponentOverlayPrependSpecLines:
		err := openedSpec.PrependLinesToSection(overlay.SectionName, overlay.PackageName, overlay.Lines)
		if err != nil {
			return fmt.Errorf("failed to prepend lines to spec:\n%w", err)
		}
	case projectconfig.ComponentOverlayAppendSpecLines:
		err := openedSpec.AppendLinesToSection(overlay.SectionName, overlay.PackageName, overlay.Lines)
		if err != nil {
			return fmt.Errorf("failed to append lines to spec:\n%w", err)
		}
	case projectconfig.ComponentOverlaySearchAndReplaceInSpec:
		err := openedSpec.SearchAndReplace(
			overlay.SectionName, overlay.PackageName, overlay.Regex, overlay.Replacement,
		)
		if err != nil {
			return fmt.Errorf("failed to search and replace in spec:\n%w", err)
		}
	case projectconfig.ComponentOverlayRemoveSection:
		err := openedSpec.RemoveSection(overlay.SectionName, overlay.PackageName)
		if err != nil {
			return fmt.Errorf("failed to remove section from spec:\n%w", err)
		}
	case projectconfig.ComponentOverlayAddPatch:
		destFilename := overlay.Filename
		if destFilename == "" {
			destFilename = filepath.Base(overlay.Source)
		}

		err := openedSpec.AddPatchEntry(overlay.PackageName, destFilename)
		if err != nil {
			return fmt.Errorf("failed to add patch entry to spec:\n%w", err)
		}
	case projectconfig.ComponentOverlayRemovePatch:
		err := openedSpec.RemovePatchEntry(overlay.Filename)
		if err != nil {
			return fmt.Errorf("failed to remove patch entry from spec:\n%w", err)
		}
	default:
		return fmt.Errorf("invalid overlay type found: %s", overlay.Type)
	}

	return nil
}

// applyNonSpecOverlay applies the given overlay to non-spec files in the component's source directory.
// Changes are made in-place. The dryRunnable parameter is currently only honored for file-add overlays
// (file copies); other overlay types always mutate files regardless of dry-run mode.
func applyNonSpecOverlay(
	dryRunnable opctx.DryRunnable, sourceFS, destFS opctx.FS, overlay projectconfig.ComponentOverlay,
) error {
	// Types whose `filename` is a literal filename (not a glob pattern).
	//nolint:exhaustive // Only literal-filename types are listed; all others use glob matching.
	switch overlay.Type {
	case projectconfig.ComponentOverlayAddFile:
		if isSpecFile(overlay.Filename) {
			return fmt.Errorf("non-spec overlay not supported on .spec file: %#q", overlay.Filename)
		}

		return applyNonSpecOverlayToFile(dryRunnable, sourceFS, destFS, overlay, overlay.Filename)
	case projectconfig.ComponentOverlayAddPatch:
		filename := overlay.Filename
		if filename == "" {
			filename = filepath.Base(overlay.Source)
		}

		return applyNonSpecOverlayToFile(dryRunnable, sourceFS, destFS, overlay, filename)
	default:
		// For all other cases, `filename` is treated as a glob pattern.
		return applyNonSpecOverlayToMatchingFiles(dryRunnable, sourceFS, destFS, overlay)
	}
}

func applyNonSpecOverlayToMatchingFiles(
	dryRunnable opctx.DryRunnable, sourceFS, destFS opctx.FS,
	overlay projectconfig.ComponentOverlay,
) error {
	// Treat the filename as a glob pattern. If multiple files are matched, ensure the overlay type supports
	// multiple files. Globbing returns pseudo-absolute paths (e.g., "/subdir/file.txt").
	matchedFilePaths, err := globNonSpecFiles(destFS, overlay.Filename)
	if err != nil {
		return fmt.Errorf("failed to glob for overlay filename %#q:\n%w",
			overlay.Filename, err)
	}

	if len(matchedFilePaths) == 0 {
		return fmt.Errorf("non-spec overlay filename pattern %#q did not match any files", overlay.Filename)
	}

	if len(matchedFilePaths) > 1 && !overlayTypeSupportsMultipleFiles(overlay.Type) {
		return fmt.Errorf("overlay type %s does not support multiple files, but filename pattern %#q matched %d files",
			overlay.Type, overlay.Filename, len(matchedFilePaths))
	}

	overlayAppliedToAtLeastOneFile := false

	for _, filePath := range matchedFilePaths {
		err = applyNonSpecOverlayToFile(dryRunnable, sourceFS, destFS, overlay, filePath)

		// Ignore if the overlay did not apply to this file, but we also won't
		// consider that a valid match.
		switch {
		case errors.Is(err, ErrOverlayDidNotApply):
		case err != nil:
			return fmt.Errorf("failed to apply overlay to file %#q:\n%w", filePath, err)
		default:
			overlayAppliedToAtLeastOneFile = true
		}
	}

	if !overlayAppliedToAtLeastOneFile {
		return fmt.Errorf(
			"non-spec overlay for filename %#q did not apply to any targets\n%w",
			overlay.Filename, ErrOverlayDidNotApply,
		)
	}

	return nil
}

func overlayTypeSupportsMultipleFiles(overlayType projectconfig.ComponentOverlayType) bool {
	return overlayType == projectconfig.ComponentOverlayPrependLinesToFile ||
		overlayType == projectconfig.ComponentOverlaySearchAndReplaceInFile ||
		overlayType == projectconfig.ComponentOverlayRemoveFile ||
		overlayType == projectconfig.ComponentOverlayRemovePatch
}

// applyNonSpecOverlayToFile applies the given overlay to the specified non-spec file.
// Changes are made in-place. ErrOverlayDidNotApply may be returned if the overlay did not
// apply to the target file.
func applyNonSpecOverlayToFile(
	dryRunnable opctx.DryRunnable, sourceFS, destFS opctx.FS,
	overlay projectconfig.ComponentOverlay, filePath string,
) error {
	//nolint:exhaustive // We intentionally ignore non-spec overlay types.
	switch overlay.Type {
	case projectconfig.ComponentOverlayPrependLinesToFile:
		err := prependLinesToFile(destFS, filePath, overlay.Lines)
		if err != nil {
			return fmt.Errorf("failed to prepend lines to file %#q:\n%w", filePath, err)
		}
	case projectconfig.ComponentOverlaySearchAndReplaceInFile:
		err := searchAndReplaceInFile(destFS, filePath, overlay.Regex, overlay.Replacement)
		if err != nil {
			return fmt.Errorf("failed to search and replace in file %#q:\n%w", filePath, err)
		}
	case projectconfig.ComponentOverlayAddFile, projectconfig.ComponentOverlayAddPatch:
		err := addNonSpecFile(dryRunnable, sourceFS, destFS, overlay, filePath)
		if err != nil {
			return err
		}
	case projectconfig.ComponentOverlayRemoveFile, projectconfig.ComponentOverlayRemovePatch:
		err := destFS.Remove(filePath)
		if err != nil {
			return fmt.Errorf("failed to remove file %#q:\n%w", filePath, err)
		}
	case projectconfig.ComponentOverlayRenameFile:
		// Compute new path in the same directory as the source.
		newFilePath := filepath.Join(filepath.Dir(filePath), overlay.Replacement)

		err := destFS.Rename(filePath, newFilePath)
		if err != nil {
			return fmt.Errorf("failed to rename file %#q to %#q:\n%w", filePath, newFilePath, err)
		}
	default:
		return fmt.Errorf("invalid overlay type found: %s", overlay.Type)
	}

	return nil
}

// addNonSpecFile adds a new file to the specified path in the filesystem for the given overlay.
func addNonSpecFile(
	dryRunnable opctx.DryRunnable, sourceFS, destFS opctx.FS,
	overlay projectconfig.ComponentOverlay, filePath string,
) error {
	slog.Debug("Adding file", "source", overlay.Source, "destination", filePath)

	exists, err := fileutils.Exists(destFS, filePath)
	if err != nil {
		return fmt.Errorf("failed to check for existence of file %#q:\n%w", filePath, err)
	}

	if exists {
		return fmt.Errorf("cannot add overlay file %#q because it already exists", filePath)
	}

	// Copy from sourceFS to destFS.
	err = fileutils.CopyFileCrossFS(
		dryRunnable,
		sourceFS, overlay.Source,
		destFS, filePath,
		fileutils.CopyFileOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to copy overlay file from %#q to %#q:\n%w", overlay.Source, filePath, err)
	}

	return nil
}

// prependLinesToFile prepends the given lines to the specified file, mutating the file in-place.
func prependLinesToFile(destFS opctx.FS, filePath string, lines []string) error {
	slog.Debug("Prepending lines to file", "filePath", filePath, "lines", lines)

	// As a precaution, make sure we're not being asked to modify a .spec file. To update a spec,
	// a spec-specific overlay should be used.
	if isSpecFile(filePath) {
		return fmt.Errorf("file prepending not supported on .spec file %#q", filePath)
	}

	// Get original file permissions to preserve them after writing.
	fileInfo, err := destFS.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to stat file %#q to preserve permissions:\n%w", filePath, err)
	}

	originalPerm := fileInfo.Mode().Perm()

	fileContents, err := fileutils.ReadFile(destFS, filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %#q to apply overlay:\n%w", filePath, err)
	}

	newContents := append([]byte(strings.Join(lines, "\n")+"\n"), fileContents...)

	if err := fileutils.WriteFile(destFS, filePath, newContents, originalPerm); err != nil {
		return fmt.Errorf("failed to write file %#q to apply overlay:\n%w", filePath, err)
	}

	return nil
}

// searchAndReplaceInFile applies a regex-based search-and-replace to the given file, mutating
// the file in-place. The replacement is performed literally; regex capture group references like $1
// are not expanded. If no matches are found, the returned error has ErrOverlayDidNotApply in its
// chain.
func searchAndReplaceInFile(destFS opctx.FS, filePath, regex, replacement string) error {
	slog.Debug("Searching and replacing in file", "filePath", filePath, "regex", regex, "replacement", replacement)

	// As a precaution, make sure we're not being asked to modify a .spec file. To update a spec,
	// a spec-specific overlay should be used.
	if isSpecFile(filePath) {
		return fmt.Errorf("file search-and-replace not supported on .spec file %#q", filePath)
	}

	compiledRe, err := regexp.Compile(regex)
	if err != nil {
		return fmt.Errorf("failed to compile regex %#q:\n%w", regex, err)
	}

	// Get original file permissions to preserve them after writing.
	fileInfo, err := destFS.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to stat file %#q to preserve permissions:\n%w", filePath, err)
	}

	originalPerm := fileInfo.Mode().Perm()

	fileContents, err := fileutils.ReadFile(destFS, filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %#q to apply overlay:\n%w", filePath, err)
	}

	// Perform the replacement. ReplaceAllLiteral treats the replacement as literal text
	// (no capture group expansion).
	newContents := compiledRe.ReplaceAllLiteral(fileContents, []byte(replacement))

	// Return an error if no replacements were made.
	if bytes.Equal(newContents, fileContents) {
		return fmt.Errorf("%w: regex %#q does not match content in file %#q",
			ErrOverlayDidNotApply, regex, filepath.Base(filePath),
		)
	}

	if err := fileutils.WriteFile(destFS, filePath, newContents, originalPerm); err != nil {
		return fmt.Errorf("failed to write file %#q to apply overlay:\n%w", filePath, err)
	}

	return nil
}

// globNonSpecFiles finds all non-spec files in the destination filesystem using the given
// glob pattern. Double-star globbing is supported; symlinks are not followed during traversal
// (defense in depth; the destFS provides the primary symlink protection).
func globNonSpecFiles(destFS opctx.FS, pattern string) ([]string, error) {
	candidatePaths, err := fileutils.Glob(destFS, pattern,
		doublestar.WithFailOnIOErrors(),
		doublestar.WithFilesOnly(),
		doublestar.WithNoFollow())
	if err != nil {
		return nil, fmt.Errorf("failed to glob for files using pattern %#q:\n%w", pattern, err)
	}

	// Filter out .spec files and .git directory contents. Paths are already in pseudo-absolute format.
	// The .git directory is excluded because overlay glob patterns like "**/*" must not match
	// git internal files (packfiles, objects, etc.) which are read-only and binary.
	return lo.Filter(candidatePaths, func(path string, _ int) bool {
		return !isSpecFile(path) && !isGitInternalPath(path)
	}), nil
}

// isGitInternalPath returns true if the given path is inside a .git directory.
// Handles both root-level (.git/HEAD) and nested (subdir/.git/objects) paths.
func isGitInternalPath(path string) bool {
	return strings.HasPrefix(path, ".git/") || strings.Contains(path, "/.git/")
}

// newDestFS creates a destination filesystem confined to the given root directory.
// For real filesystems (OsFs), it uses os.Root via rootfs.RootFs for atomic symlink safety.
// For test filesystems (MemMapFs), it uses afero.BasePathFs to confine paths.
// Callers should check if the returned FS implements [io.Closer] and call Close() if so.
func newDestFS(sourceFS opctx.FS, rootPath string) (opctx.FS, error) {
	switch sourceFS.(type) {
	case *afero.OsFs:
		// Use RootFs for production: provides atomic symlink-escape protection via os.Root.
		fs, err := rootfs.New(rootPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create RootFs for overlay destination:\n%w", err)
		}

		return fs, nil

	case *afero.MemMapFs:
		// For MemMapFs and other test filesystems, use BasePathFs to confine operations.
		// BasePathFs prepends the base path to all operations, providing logical confinement.
		return afero.NewBasePathFs(sourceFS, rootPath), nil

	default:
		return nil, errors.New("unsupported filesystem type for overlay destination")
	}
}
