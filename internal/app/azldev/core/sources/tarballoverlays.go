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
	"regexp"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/tarball"
)

// applyTarballOverlays groups tarball overlays by target archive and processes
// them in order. Multiple overlays targeting the same tarball are batched into
// a single extract/modify/repack cycle. All operations (extract, modify, repack)
// are performed in pure Go on the host, except for patch application which
// shells out to the host's `patch` command.
func applyTarballOverlays(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	fs opctx.FS,
	eventListener opctx.EventListener,
	sourcesDirPath string,
	overlays []projectconfig.ComponentOverlay,
) error {
	groups := groupOverlaysByTarball(overlays)

	if len(groups) == 0 {
		return nil
	}

	event := eventListener.StartEvent("Applying tarball overlays",
		"tarballs", len(groups),
		"operations", len(overlays),
	)
	defer event.End()

	for _, group := range groups {
		if err := processTarball(ctx, cmdFactory, fs, sourcesDirPath, group); err != nil {
			return fmt.Errorf("tarball overlay failed for %#q:\n%w", group.tarball, err)
		}
	}

	return nil
}

// tarballGroup holds overlays targeting the same tarball, preserving order.
type tarballGroup struct {
	tarball  string
	overlays []projectconfig.ComponentOverlay
}

// groupOverlaysByTarball groups tarball overlays by their
// [projectconfig.ComponentOverlay.Tarball] field, preserving insertion order
// within each group and across groups. Non-tarball overlays are silently skipped.
func groupOverlaysByTarball(overlays []projectconfig.ComponentOverlay) []tarballGroup {
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

		groups[idx].overlays = append(groups[idx].overlays, overlay)
	}

	return groups
}

// processTarball extracts a tarball to a temp directory, applies all overlays,
// and deterministically repacks it in-place with the original compression.
func processTarball(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	fs opctx.FS,
	sourcesDirPath string,
	group tarballGroup,
) error {
	archivePath := filepath.Join(sourcesDirPath, group.tarball)

	// Create a temporary directory for extraction.
	workDir, err := os.MkdirTemp("", "tarball-overlay-")
	if err != nil {
		return fmt.Errorf("creating temp directory:\n%w", err)
	}

	defer func() {
		if removeErr := os.RemoveAll(workDir); removeErr != nil {
			slog.Warn("Failed to clean up tarball work directory", "error", removeErr)
		}
	}()

	// Detect compression and extract.
	compression, err := tarball.DetectCompression(group.tarball)
	if err != nil {
		return fmt.Errorf("detecting compression for %#q:\n%w", group.tarball, err)
	}

	if err := tarball.Extract(fs, archivePath, workDir, compression); err != nil {
		return fmt.Errorf("extracting tarball:\n%w", err)
	}

	// Determine the root of the extracted content. Most source tarballs have
	// a single top-level directory (e.g., "pkg-1.0/").
	extractRoot, err := tarball.ResolveExtractRoot(workDir)
	if err != nil {
		return fmt.Errorf("resolving extract root:\n%w", err)
	}

	// Apply each overlay operation in order.
	for _, overlay := range group.overlays {
		if err := applyTarballOperation(ctx, cmdFactory, fs, extractRoot, overlay); err != nil {
			return fmt.Errorf("applying %#q operation:\n%w", overlay.Type, err)
		}
	}

	// Deterministically repack the tarball in-place.
	if err := tarball.RepackDeterministic(fs, archivePath, workDir, compression); err != nil {
		return fmt.Errorf("repacking tarball:\n%w", err)
	}

	slog.Info("Tarball overlay applied", "tarball", group.tarball)

	return nil
}

// applyTarballOperation dispatches a single overlay to the appropriate handler.
func applyTarballOperation(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	fs opctx.FS,
	extractRoot string,
	overlay projectconfig.ComponentOverlay,
) error {
	//nolint:exhaustive // Only tarball overlay types are valid here; the default catches the rest.
	switch overlay.Type {
	case projectconfig.ComponentOverlayTarballFileRemove:
		return tarballFileRemove(extractRoot, overlay.Filename)

	case projectconfig.ComponentOverlayTarballSearchReplace:
		return tarballSearchReplace(extractRoot, overlay.Filename, overlay.Regex, overlay.Replacement)

	case projectconfig.ComponentOverlayTarballPatch:
		stripLevel := 1

		if overlay.Value != "" {
			parsed, err := strconv.Atoi(overlay.Value)
			if err != nil {
				return fmt.Errorf("invalid strip level %#q:\n%w", overlay.Value, err)
			}

			stripLevel = parsed
		}

		return tarballApplyPatch(ctx, cmdFactory, fs, extractRoot, overlay.Source, stripLevel)

	default:
		return fmt.Errorf("unsupported tarball overlay type %#q", overlay.Type)
	}
}

// tarballFileRemove removes files matching a glob pattern from the extracted tree.
func tarballFileRemove(extractRoot, pattern string) error {
	matches, err := globFilesInDir(extractRoot, pattern)
	if err != nil {
		return err
	}

	if len(matches) == 0 {
		return fmt.Errorf("no files match pattern %#q:\n%w", pattern, ErrOverlayDidNotApply)
	}

	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to remove %#q:\n%w", path, err)
		}
	}

	return nil
}

// tarballSearchReplace applies regex search-and-replace to files matching a glob
// pattern inside the extracted tree.
func tarballSearchReplace(extractRoot, pattern, regex, replacement string) error {
	matches, err := globFilesInDir(extractRoot, pattern)
	if err != nil {
		return err
	}

	if len(matches) == 0 {
		return fmt.Errorf("no files match pattern %#q:\n%w", pattern, ErrOverlayDidNotApply)
	}

	compiled, err := regexp.Compile(regex)
	if err != nil {
		return fmt.Errorf("invalid regex %#q:\n%w", regex, err)
	}

	anyReplaced := false

	for _, path := range matches {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading %#q:\n%w", path, readErr)
		}

		newContent := compiled.ReplaceAll(content, []byte(replacement))
		if string(newContent) != string(content) {
			anyReplaced = true

			if writeErr := os.WriteFile(path, newContent, fileperms.PublicFile); writeErr != nil {
				return fmt.Errorf("writing %#q:\n%w", path, writeErr)
			}
		}
	}

	if !anyReplaced {
		return fmt.Errorf("regex %#q did not match any content in files matching %#q:\n%w",
			regex, pattern, ErrOverlayDidNotApply)
	}

	return nil
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

	// Write to a temp file on the real filesystem so the patch command can read it.
	tmpPatch, err := os.CreateTemp("", "tarball-patch-*.patch")
	if err != nil {
		return fmt.Errorf("creating temp patch file:\n%w", err)
	}

	defer os.Remove(tmpPatch.Name())

	if _, err := tmpPatch.Write(patchData); err != nil {
		tmpPatch.Close()

		return fmt.Errorf("writing temp patch file:\n%w", err)
	}

	tmpPatch.Close()

	var stderr strings.Builder

	rawCmd := exec.CommandContext(ctx, "patch",
		fmt.Sprintf("-p%d", stripLevel),
		"-i", tmpPatch.Name(),
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

// globFilesInDir finds files under root matching a glob pattern.
// Supports doublestar patterns (e.g., "**/*.md").
func globFilesInDir(root, pattern string) ([]string, error) {
	var matches []string

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("computing relative path for %#q:\n%w", path, relErr)
		}

		matched, matchErr := doublestar.Match(pattern, rel)
		if matchErr != nil {
			return fmt.Errorf("invalid glob pattern %#q:\n%w", pattern, matchErr)
		}

		if matched {
			matches = append(matches, path)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking directory for glob %#q:\n%w", pattern, err)
	}

	return matches, nil
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
