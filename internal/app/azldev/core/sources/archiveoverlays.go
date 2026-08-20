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
//
// It returns the names of the archives that were actually repacked. In dry-run
// mode no archive is repacked, so the returned slice is empty even when archive
// overlays were present.
func applyArchiveOverlays(
	dryRunnable opctx.DryRunnable,
	eventListener opctx.EventListener,
	sourcesDirPath string,
	overlays []projectconfig.ComponentOverlay,
) ([]string, error) {
	groups := groupOverlaysByArchive(overlays)

	if len(groups) == 0 {
		return nil, nil
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

	var repacked []string

	for _, group := range groups {
		didRepack, err := processArchive(dryRunnable, sourcesDirPath, group.archive, group.overlays)
		if err != nil {
			return nil, fmt.Errorf("archive overlay failed for %#q:\n%w", group.archive, err)
		}

		if didRepack {
			repacked = append(repacked, group.archive)
		}
	}

	return repacked, nil
}

// archiveGroup holds overlays targeting the same archive, preserving order.
type archiveGroup struct {
	archive  string
	overlays []projectconfig.ComponentOverlay
}

// groupOverlaysByArchive groups archive overlays by [projectconfig.ComponentOverlay.Archive],
// preserving insertion order within each group and across groups.
// Non-archive overlays are silently skipped.
func groupOverlaysByArchive(overlays []projectconfig.ComponentOverlay) []archiveGroup {
	orderMap := make(map[string]int)

	var groups []archiveGroup

	for _, overlay := range overlays {
		if !overlay.ModifiesArchive() {
			continue
		}

		archiveName := overlay.Archive

		idx, exists := orderMap[archiveName]
		if !exists {
			idx = len(groups)
			orderMap[archiveName] = idx

			groups = append(groups, archiveGroup{archive: archiveName})
		}

		groups[idx].overlays = append(groups[idx].overlays, overlay)
	}

	return groups
}

// processArchive extracts an archive to a temp directory, applies all overlays,
// and deterministically repacks it with the original compression, atomically
// replacing the original via a temp file + rename. It returns true when the
// archive was repacked. In dry-run mode it returns (false, nil) without touching
// the archive on disk.
func processArchive(
	dryRunnable opctx.DryRunnable,
	sourcesDirPath string,
	archiveName string,
	overlays []projectconfig.ComponentOverlay,
) (repacked bool, err error) {
	archivePath := filepath.Join(sourcesDirPath, archiveName)

	if dryRunnable.DryRun() {
		slog.Info("Dry run; would apply archive overlays",
			"archive", archiveName, "operations", len(overlays))

		return false, nil
	}

	// The [archive] package operates exclusively through OS primitives ([os.Root],
	// os.*), so extraction must use a genuine on-disk path regardless of the injected
	// FS implementation (which may be in-memory or otherwise non-OS-backed). Keep
	// that directory outside the sources tree: a process crash could otherwise leave
	// it behind for subsequent loose-file overlay globs to match.
	workDir, err := os.MkdirTemp("", "azldev-archive-overlay-")
	if err != nil {
		return false, fmt.Errorf("creating temp directory:\n%w", err)
	}

	defer func() {
		if removeErr := os.RemoveAll(workDir); removeErr != nil {
			slog.Warn("Failed to clean up archive work directory", "error", removeErr)
		}
	}()

	// Extract the archive; compression is inferred from the filename extension.
	// Fail on entry types we can't repack (hardlinks, devices, ...) so they
	// aren't silently dropped from the repacked archive.
	if err := archive.ExtractAuto(archivePath, workDir, archive.WithErrorOnUnsupportedEntry()); err != nil {
		return false, fmt.Errorf("extracting archive:\n%w", err)
	}

	// Determine the root of the extracted content. Most source archives unpack to
	// a single top-level directory (e.g., "pkg-1.0/"), which is used as the root.
	extractRoot, err := resolveExtractRoot(workDir)
	if err != nil {
		return false, fmt.Errorf("resolving extract root:\n%w", err)
	}

	// Confine an OS-backed FS to the extract root so file overlays reuse the same
	// machinery as loose-file overlays.
	extractFS, err := rootfs.New(extractRoot)
	if err != nil {
		return false, fmt.Errorf("confining FS to extract root:\n%w", err)
	}

	defer func() {
		if closeErr := extractFS.Close(); closeErr != nil {
			slog.Warn("Failed to close extract-root FS", "error", closeErr)
		}
	}()

	// Apply each overlay in order. Archive overlays (file-remove / file-search-replace,
	// see [projectconfig.ComponentOverlay.ModifiesArchive]) operate solely on the
	// destination tree, so extractFS is passed as both source and destination FS.
	for _, overlay := range overlays {
		if err := applyNonSpecOverlay(dryRunnable, extractFS, extractFS, overlay); err != nil {
			return false, fmt.Errorf("applying %#q operation:\n%w", overlay.Type, err)
		}
	}

	// Deterministically repack the archive, reusing the original compression, and
	// atomically replace the original (see [repackArchiveAtomic]).
	if err := repackArchiveAtomic(archivePath, archiveName, workDir); err != nil {
		return false, err
	}

	slog.Info("Archive overlay applied", "archive", archiveName)

	return true, nil
}

// repackArchiveAtomic deterministically repacks workDir into an archive that
// replaces archivePath, reusing the compression inferred from archiveName.
//
// To avoid corrupting the fetched source archive on a mid-write failure (disk
// full, permission error, etc.), it repacks to a temp file in the same directory
// and atomically renames it over the original only on success. Repacking directly
// over archivePath would truncate it first, leaving the workspace unrecoverable
// without refetching if the repack then failed.
func repackArchiveAtomic(archivePath, archiveName, workDir string) (err error) {
	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("stating original archive %#q:\n%w", archiveName, err)
	}

	originalPerm := archiveInfo.Mode().Perm()

	// Fall back to the extension only if the archive is empty (nothing to sniff).
	extComp, err := archive.DetectCompression(archiveName)
	if err != nil {
		return fmt.Errorf("detecting compression for %#q:\n%w", archiveName, err)
	}

	comp, err := archive.SniffCompressionFromFile(archivePath, extComp)
	if err != nil {
		return fmt.Errorf("sniffing compression for %#q:\n%w", archiveName, err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(archivePath), "."+filepath.Base(archiveName)+".repack-*")
	if err != nil {
		return fmt.Errorf("creating temp archive:\n%w", err)
	}

	tmpPath := tmpFile.Name()

	// Close the handle immediately; CreateDeterministicArchive truncates and
	// reopens this uniquely-created path.
	if closeErr := tmpFile.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("closing temp archive %#q:\n%w", tmpPath, closeErr)
	}

	// Clean up the temp file unless it was successfully renamed over the original.
	repackedOK := false

	defer func() {
		if !repackedOK {
			if removeErr := os.Remove(tmpPath); removeErr != nil && !os.IsNotExist(removeErr) {
				slog.Warn("Failed to clean up temp archive", "path", tmpPath, "error", removeErr)
			}
		}
	}()

	if err := archive.CreateDeterministicArchive(tmpPath, workDir, comp); err != nil {
		return fmt.Errorf("repacking archive:\n%w", err)
	}

	if err := os.Chmod(tmpPath, originalPerm); err != nil {
		return fmt.Errorf("restoring permissions on repacked archive %#q:\n%w", archiveName, err)
	}

	if err := os.Rename(tmpPath, archivePath); err != nil {
		return fmt.Errorf("replacing archive %#q with repacked archive:\n%w", archivePath, err)
	}

	repackedOK = true

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
