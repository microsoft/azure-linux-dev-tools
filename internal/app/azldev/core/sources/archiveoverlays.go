// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/archive"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/rootfs"
)

// applyArchiveOverlays groups archive overlays by target archive and processes
// them in order. Multiple overlays targeting the same archive are batched into
// a single extract/modify/repack cycle. File removals inside the archive reuse
// the same machinery as loose-file overlays ([applyNonSpecOverlay]).
func applyArchiveOverlays(
	dryRunnable opctx.DryRunnable,
	eventListener opctx.EventListener,
	sourcesDirPath string,
	overlays []projectconfig.ComponentOverlay,
) error {
	groups, err := groupOverlaysByArchive(overlays)
	if err != nil {
		return err
	}

	if len(groups) == 0 {
		return nil
	}

	operationCount := 0
	for _, group := range groups {
		operationCount += len(group.overlays)
	}

	event := eventListener.StartEvent("Applying archive overlays",
		"archives", len(groups),
		"operations", operationCount,
	)
	defer event.End()

	for _, group := range groups {
		if err := processArchive(dryRunnable, sourcesDirPath, group); err != nil {
			return fmt.Errorf("archive overlay failed for %#q:\n%w", group.archive, err)
		}
	}

	return nil
}

// archiveGroup holds overlays targeting the same archive, preserving order.
type archiveGroup struct {
	archive  string
	root     string
	overlays []projectconfig.ComponentOverlay
}

// groupOverlaysByArchive groups archive overlays by their
// [projectconfig.ComponentOverlay.Archive] field, preserving insertion order
// within each group and across groups. Non-archive overlays are silently skipped.
//
// The optional [projectconfig.ComponentOverlay.ArchiveRoot] override (mirroring
// rpmbuild's `%setup -n`) is reconciled per archive: all overlays targeting the
// same archive that set it must agree, otherwise the configuration is ambiguous
// and an error is returned.
func groupOverlaysByArchive(overlays []projectconfig.ComponentOverlay) ([]archiveGroup, error) {
	orderMap := make(map[string]int)

	var groups []archiveGroup

	for _, overlay := range overlays {
		if !overlay.ModifiesArchive() {
			continue
		}

		idx, exists := orderMap[overlay.Archive]
		if !exists {
			idx = len(groups)
			orderMap[overlay.Archive] = idx

			groups = append(groups, archiveGroup{archive: overlay.Archive})
		}

		if overlay.ArchiveRoot != "" {
			if groups[idx].root != "" && groups[idx].root != overlay.ArchiveRoot {
				return nil, fmt.Errorf(
					"conflicting %#q overrides for archive %#q: %#q vs %#q",
					"archive-root", overlay.Archive, groups[idx].root, overlay.ArchiveRoot,
				)
			}

			groups[idx].root = overlay.ArchiveRoot
		}

		groups[idx].overlays = append(groups[idx].overlays, overlay)
	}

	return groups, nil
}

// processArchive extracts an archive to a temp directory, applies all overlays,
// and deterministically repacks it in-place with the original compression.
func processArchive(
	dryRunnable opctx.DryRunnable,
	sourcesDirPath string,
	group archiveGroup,
) error {
	archivePath := filepath.Join(sourcesDirPath, group.archive)

	// Create a temporary directory for extraction directly on the real filesystem.
	// The [archive] package operates exclusively through OS primitives ([os.Root],
	// os.*), so the work directory must be a genuine on-disk path regardless of the
	// injected FS implementation. Using os.MkdirTemp here (instead of the injected
	// FS) makes that requirement explicit and keeps the path valid even when fs is
	// an in-memory or otherwise non-OS-backed FS (e.g., in tests or alternate runners).
	workDir, err := os.MkdirTemp("", "archive-overlay-")
	if err != nil {
		return fmt.Errorf("creating temp directory:\n%w", err)
	}

	defer func() {
		if removeErr := os.RemoveAll(workDir); removeErr != nil {
			slog.Warn("Failed to clean up archive work directory", "error", removeErr)
		}
	}()

	// Extract the archive; compression is inferred from the filename extension.
	if err := archive.ExtractAuto(archivePath, workDir); err != nil {
		return fmt.Errorf("extracting archive:\n%w", err)
	}

	// Determine the root of the extracted content. Most source archives have
	// a single top-level directory (e.g., "pkg-1.0/"); group.root overrides this
	// inference when set (mirrors rpmbuild's `%setup -n`).
	extractRoot, err := resolveExtractRoot(workDir, group.root)
	if err != nil {
		return fmt.Errorf("resolving extract root:\n%w", err)
	}

	// Confine an FS to the extract root so file overlays reuse the same machinery
	// as loose-file overlays. The extracted tree is always on the real filesystem
	// (written by the [archive] package), so root it on an OS-backed FS regardless
	// of the injected fs implementation.
	extractFS, err := rootfs.New(extractRoot)
	if err != nil {
		return fmt.Errorf("confining FS to extract root:\n%w", err)
	}

	defer func() {
		if closeErr := extractFS.Close(); closeErr != nil {
			slog.Warn("Failed to close extract-root FS", "error", closeErr)
		}
	}()

	// Apply each overlay operation in order. Archive overlays are restricted to
	// file-remove / file-search-replace (see [projectconfig.ComponentOverlay.ModifiesArchive]),
	// which operate solely on the destination tree, so the extract-root FS is passed as
	// both the source and destination FS — there is no component-source FS to read from.
	for _, overlay := range group.overlays {
		if err := applyNonSpecOverlay(dryRunnable, extractFS, extractFS, overlay); err != nil {
			return fmt.Errorf("applying %#q operation:\n%w", overlay.Type, err)
		}
	}

	// Deterministically repack the archive in-place, reusing the original compression.
	if err := archive.CreateDeterministicArchiveAuto(archivePath, workDir); err != nil {
		return fmt.Errorf("repacking archive:\n%w", err)
	}

	slog.Info("Archive overlay applied", "archive", group.archive)

	return nil
}

// resolveExtractRoot returns the effective root of an extracted archive.
// When rootOverride is set (the `%setup -n` equivalent), the named subdirectory
// of workDir is used; it must be a local path that exists as a directory. When
// rootOverride is empty, the root is inferred: if workDir contains exactly one
// entry and that entry is a directory (the common case for source archives like
// "pkg-1.0/"), that subdirectory is returned; otherwise workDir itself is
// returned.
func resolveExtractRoot(workDir, rootOverride string) (string, error) {
	if rootOverride != "" {
		// Defense in depth: validation already rejects non-local overrides, but
		// re-check before joining so a malformed value can never escape workDir.
		if !filepath.IsLocal(rootOverride) {
			return "", fmt.Errorf("archive root %#q is not a local path", rootOverride)
		}

		target := filepath.Join(workDir, rootOverride)

		info, err := os.Stat(target)
		if err != nil {
			return "", fmt.Errorf("archive root %#q not found after extraction:\n%w", rootOverride, err)
		}

		if !info.IsDir() {
			return "", fmt.Errorf("archive root %#q is not a directory", rootOverride)
		}

		return target, nil
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		return "", fmt.Errorf("reading extracted directory:\n%w", err)
	}

	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(workDir, entries[0].Name()), nil
	}

	return workDir, nil
}

// archiveNamesFromOverlays returns the unique archive filenames targeted by
// archive overlays in the given overlay list. Used by [updateSourcesFile] to
// determine which 'sources' entries need rehashing after overlay application.
func archiveNamesFromOverlays(overlays []projectconfig.ComponentOverlay) []string {
	seen := make(map[string]bool)

	var names []string

	for _, overlay := range overlays {
		if overlay.ModifiesArchive() && !seen[overlay.Archive] {
			seen[overlay.Archive] = true
			names = append(names, overlay.Archive)
		}
	}

	return names
}
