// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/archive"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/rootfs"
)

// applyTarballOverlays groups tarball overlays by target archive and processes
// them in order. Multiple overlays targeting the same tarball are batched into
// a single extract/modify/repack cycle. File modifications inside the archive
// reuse the same machinery as loose-file overlays ([applyNonSpecOverlay]); patch
// application shells out to the host's `patch` command.
func applyTarballOverlays(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	dryRunnable opctx.DryRunnable,
	fs opctx.FS,
	eventListener opctx.EventListener,
	sourcesDirPath string,
	overlays []projectconfig.ComponentOverlay,
) error {
	groups, err := groupOverlaysByTarball(overlays)
	if err != nil {
		return err
	}

	if len(groups) == 0 {
		return nil
	}

	event := eventListener.StartEvent("Applying tarball overlays",
		"tarballs", len(groups),
		"operations", len(overlays),
	)
	defer event.End()

	for _, group := range groups {
		if err := processTarball(ctx, cmdFactory, dryRunnable, fs, sourcesDirPath, group); err != nil {
			return fmt.Errorf("tarball overlay failed for %#q:\n%w", group.tarball, err)
		}
	}

	return nil
}

// tarballGroup holds overlays targeting the same tarball, preserving order.
type tarballGroup struct {
	tarball  string
	root     string
	overlays []projectconfig.ComponentOverlay
}

// groupOverlaysByTarball groups tarball overlays by their
// [projectconfig.ComponentOverlay.Tarball] field, preserving insertion order
// within each group and across groups. Non-tarball overlays are silently skipped.
//
// The optional [projectconfig.ComponentOverlay.TarballRoot] override (mirroring
// rpmbuild's `%setup -n`) is reconciled per tarball: all overlays targeting the
// same archive that set it must agree, otherwise the configuration is ambiguous
// and an error is returned.
func groupOverlaysByTarball(overlays []projectconfig.ComponentOverlay) ([]tarballGroup, error) {
	orderMap := make(map[string]int)

	var groups []tarballGroup

	for _, overlay := range overlays {
		if !overlay.ModifiesTarball() {
			continue
		}

		idx, exists := orderMap[overlay.Tarball]
		if !exists {
			idx = len(groups)
			orderMap[overlay.Tarball] = idx

			groups = append(groups, tarballGroup{tarball: overlay.Tarball})
		}

		if overlay.TarballRoot != "" {
			if groups[idx].root != "" && groups[idx].root != overlay.TarballRoot {
				return nil, fmt.Errorf(
					"conflicting %#q overrides for tarball %#q: %#q vs %#q",
					"tarball-root", overlay.Tarball, groups[idx].root, overlay.TarballRoot,
				)
			}

			groups[idx].root = overlay.TarballRoot
		}

		groups[idx].overlays = append(groups[idx].overlays, overlay)
	}

	return groups, nil
}

// processTarball extracts a tarball to a temp directory, applies all overlays,
// and deterministically repacks it in-place with the original compression.
func processTarball(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	dryRunnable opctx.DryRunnable,
	fs opctx.FS,
	sourcesDirPath string,
	group tarballGroup,
) error {
	archivePath := filepath.Join(sourcesDirPath, group.tarball)

	// Create a temporary directory for extraction. The injected FS is real-filesystem
	// backed in production, so the returned path is a genuine on-disk path usable by
	// the [archive] package and the host `patch` command.
	workDir, err := fileutils.MkdirTempInTempDir(fs, "tarball-overlay-")
	if err != nil {
		return fmt.Errorf("creating temp directory:\n%w", err)
	}

	defer func() {
		if removeErr := fs.RemoveAll(workDir); removeErr != nil {
			slog.Warn("Failed to clean up tarball work directory", "error", removeErr)
		}
	}()

	// Extract the archive; compression is inferred from the filename extension.
	if err := archive.ExtractAuto(archivePath, workDir); err != nil {
		return fmt.Errorf("extracting tarball:\n%w", err)
	}

	// Determine the root of the extracted content. Most source tarballs have
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

	// Apply each overlay operation in order.
	for _, overlay := range group.overlays {
		if err := applyTarballOperation(
			ctx, cmdFactory, dryRunnable, fs, extractFS, extractRoot, overlay,
		); err != nil {
			return fmt.Errorf("applying %#q operation:\n%w", overlay.Type, err)
		}
	}

	// Deterministically repack the tarball in-place, reusing the original compression.
	if err := archive.CreateDeterministicArchiveAuto(archivePath, workDir); err != nil {
		return fmt.Errorf("repacking tarball:\n%w", err)
	}

	slog.Info("Tarball overlay applied", "tarball", group.tarball)

	return nil
}

// applyTarballOperation dispatches a single overlay against the extracted tree.
// The dedicated tarball-patch type shells out to the host `patch` command; all
// other (archive-scoped) types reuse [applyNonSpecOverlay], operating on the
// extract-root FS exactly as they would on the loose sources tree.
func applyTarballOperation(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	dryRunnable opctx.DryRunnable,
	sourceFS, extractFS opctx.FS,
	extractRoot string,
	overlay projectconfig.ComponentOverlay,
) error {
	if overlay.Type == projectconfig.ComponentOverlayTarballPatch {
		stripLevel := 1

		if overlay.Value != "" {
			parsed, err := strconv.Atoi(overlay.Value)
			if err != nil {
				return fmt.Errorf("invalid strip level %#q:\n%w", overlay.Value, err)
			}

			stripLevel = parsed
		}

		return tarballApplyPatch(ctx, cmdFactory, sourceFS, extractRoot, overlay.Source, stripLevel)
	}

	return applyNonSpecOverlay(dryRunnable, sourceFS, extractFS, overlay)
}

// tarballApplyPatch applies a unified diff patch to the extracted tree by
// shelling out to the host's `patch` command.
func tarballApplyPatch(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	fs opctx.FS,
	extractRoot, patchSource string,
	stripLevel int,
) error {
	if !cmdFactory.CommandInSearchPath("patch") {
		return errors.New("'patch' command not found in PATH; " +
			"install the 'patch' package to use tarball-patch overlays")
	}

	// Read the patch file content via the abstract FS (supports both real and test FSs).
	patchData, err := fileutils.ReadFile(fs, patchSource)
	if err != nil {
		return fmt.Errorf("reading patch file %#q:\n%w", patchSource, err)
	}

	// Stage the patch in a temp dir so the host `patch` command can read it. The
	// injected FS is real-filesystem backed in production, so the path is on disk.
	tmpDir, err := fileutils.MkdirTempInTempDir(fs, "tarball-patch-")
	if err != nil {
		return fmt.Errorf("creating temp patch directory:\n%w", err)
	}

	defer func() {
		if removeErr := fs.RemoveAll(tmpDir); removeErr != nil {
			slog.Warn("Failed to clean up tarball patch directory", "error", removeErr)
		}
	}()

	tmpPatchPath := filepath.Join(tmpDir, "overlay.patch")
	if err := fileutils.WriteFile(fs, tmpPatchPath, patchData, fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing temp patch file:\n%w", err)
	}

	var stderr strings.Builder

	rawCmd := exec.CommandContext(ctx, "patch",
		fmt.Sprintf("-p%d", stripLevel),
		"-i", tmpPatchPath,
	)
	rawCmd.Dir = extractRoot
	rawCmd.Stderr = &stderr

	cmd, err := cmdFactory.Command(rawCmd)
	if err != nil {
		return fmt.Errorf("creating patch command:\n%w", err)
	}

	if runErr := cmd.Run(ctx); runErr != nil {
		return fmt.Errorf("patch failed:\n%s\n%w", stderr.String(), runErr)
	}

	return nil
}

// resolveExtractRoot returns the effective root of an extracted tarball.
// When rootOverride is set (the `%setup -n` equivalent), the named subdirectory
// of workDir is used; it must be a local path that exists as a directory. When
// rootOverride is empty, the root is inferred: if workDir contains exactly one
// entry and that entry is a directory (the common case for source tarballs like
// "pkg-1.0/"), that subdirectory is returned; otherwise workDir itself is
// returned.
func resolveExtractRoot(workDir, rootOverride string) (string, error) {
	if rootOverride != "" {
		// Defense in depth: validation already rejects non-local overrides, but
		// re-check before joining so a malformed value can never escape workDir.
		if !filepath.IsLocal(rootOverride) {
			return "", fmt.Errorf("tarball root %#q is not a local path", rootOverride)
		}

		target := filepath.Join(workDir, rootOverride)

		info, err := os.Stat(target)
		if err != nil {
			return "", fmt.Errorf("tarball root %#q not found after extraction:\n%w", rootOverride, err)
		}

		if !info.IsDir() {
			return "", fmt.Errorf("tarball root %#q is not a directory", rootOverride)
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

// tarballNamesFromOverlays returns the unique tarball filenames targeted by
// tarball overlays in the given overlay list. Used by [updateSourcesFile] to
// determine which 'sources' entries need rehashing after overlay application.
func tarballNamesFromOverlays(overlays []projectconfig.ComponentOverlay) []string {
	seen := make(map[string]bool)

	var names []string

	for _, overlay := range overlays {
		if overlay.ModifiesTarball() && !seen[overlay.Tarball] {
			seen[overlay.Tarball] = true
			names = append(names, overlay.Tarball)
		}
	}

	return names
}
