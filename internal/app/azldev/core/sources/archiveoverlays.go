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
	groups := groupOverlaysByArchive(overlays)

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
	overlays []projectconfig.ComponentOverlay
}

// groupOverlaysByArchive groups archive overlays by the archive named in the first
// path segment of [projectconfig.ComponentOverlay.Filename] (see
// [projectconfig.ComponentOverlay.ArchiveTarget]), preserving insertion order within
// each group and across groups. Non-archive overlays are silently skipped.
//
// Each grouped overlay's Filename is rewritten to the in-archive glob (the archive
// prefix stripped) so that application globs relative to the extracted tree.
func groupOverlaysByArchive(overlays []projectconfig.ComponentOverlay) []archiveGroup {
	orderMap := make(map[string]int)

	var groups []archiveGroup

	for _, overlay := range overlays {
		if !overlay.ModifiesArchive() {
			continue
		}

		// ok is guaranteed true here: ModifiesArchive() implies ArchiveTarget() ok.
		archiveName, innerGlob, _ := overlay.ArchiveTarget()

		idx, exists := orderMap[archiveName]
		if !exists {
			idx = len(groups)
			orderMap[archiveName] = idx

			groups = append(groups, archiveGroup{archive: archiveName})
		}

		// Rewrite Filename to the in-archive glob so the in-archive application
		// (which globs relative to the extract root) does not see the archive prefix.
		scoped := overlay
		scoped.Filename = innerGlob
		groups[idx].overlays = append(groups[idx].overlays, scoped)
	}

	return groups
}

// processArchive extracts an archive to a temp directory, applies all overlays,
// and deterministically repacks it in-place with the original compression.
func processArchive(
	dryRunnable opctx.DryRunnable,
	sourcesDirPath string,
	group archiveGroup,
) error {
	archivePath := filepath.Join(sourcesDirPath, group.archive)

	if dryRunnable.DryRun() {
		slog.Info("Dry run; would apply archive overlays",
			"archive", group.archive, "operations", len(group.overlays))

		return nil
	}

	// The [archive] package operates exclusively through OS primitives ([os.Root],
	// os.*), so extraction must use a genuine on-disk path regardless of the injected
	// FS implementation (which may be in-memory or otherwise non-OS-backed).
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

	// Determine the root of the extracted content. Most source archives unpack to
	// a single top-level directory (e.g., "pkg-1.0/"), which is used as the root.
	extractRoot, err := resolveExtractRoot(workDir)
	if err != nil {
		return fmt.Errorf("resolving extract root:\n%w", err)
	}

	// Confine an OS-backed FS to the extract root so file overlays reuse the same
	// machinery as loose-file overlays.
	extractFS, err := rootfs.New(extractRoot)
	if err != nil {
		return fmt.Errorf("confining FS to extract root:\n%w", err)
	}

	defer func() {
		if closeErr := extractFS.Close(); closeErr != nil {
			slog.Warn("Failed to close extract-root FS", "error", closeErr)
		}
	}()

	// Apply each overlay in order. Archive overlays (file-remove / file-search-replace,
	// see [projectconfig.ComponentOverlay.ModifiesArchive]) operate solely on the
	// destination tree, so extractFS is passed as both source and destination FS.
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

// resolveExtractRoot returns the effective root of an extracted archive. If workDir
// contains exactly one entry and that entry is a directory (the common case for
// source archives like "pkg-1.0/"), that subdirectory is returned; otherwise workDir
// itself is returned.
func resolveExtractRoot(workDir string) (string, error) {
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
		if !overlay.ModifiesArchive() {
			continue
		}

		archiveName, _, _ := overlay.ArchiveTarget()
		if !seen[archiveName] {
			seen[archiveName] = true
			names = append(names, archiveName)
		}
	}

	return names
}
