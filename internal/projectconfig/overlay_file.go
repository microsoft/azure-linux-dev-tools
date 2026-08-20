// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"slices"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
)

// ErrOverlayFilePerOverlayMetadata is returned when an overlay file declares
// `metadata` on an individual `[[overlays]]` entry. Each file represents one logical
// change; the file-level `[metadata]` block is the single source of truth and is
// stamped onto every overlay in the file at load time.
var ErrOverlayFilePerOverlayMetadata = errors.New(
	"per-overlay metadata is not allowed inside an overlay file; declare metadata once at the file level",
)

// ErrOverlayFileEmpty is returned when an overlay file decodes to zero overlays.
// Such a file is almost certainly a typo (e.g. `[overlays]` instead of `[[overlays]]`)
// and would otherwise silently contribute nothing.
var ErrOverlayFileEmpty = errors.New("overlay file declares no overlays")

// ErrOverlayFilesNoMatches is returned when a literal [ComponentConfig.OverlayFiles]
// path matches no files on disk. Glob patterns may intentionally match no files when
// inherited across many components, but a literal path with no match is almost always
// a typo.
var ErrOverlayFilesNoMatches = errors.New("overlay-files pattern matched no files")

// OverlayFile is the on-disk representation of a single overlay document. Each file
// represents one logical change: the file-level [OverlayFile.Metadata] is applied
// to every overlay in [OverlayFile.Overlays] at load time, after which the resulting
// overlays are appended to the owning component's [ComponentConfig.Overlays] slice.
type OverlayFile struct {
	// Metadata is the shared metadata for every overlay in this file. Required.
	Metadata OverlayMetadata `toml:"metadata"`

	// Overlays is the ordered list of overlays applied by this file. Must be non-empty.
	// Per-overlay `metadata` is not allowed — the file-level [OverlayFile.Metadata] is
	// the single source of truth.
	Overlays []ComponentOverlay `toml:"overlays"`
}

// ExpandResolvedOverlayFiles resolves a component's post-resolution
// [ComponentConfig.OverlayFiles] glob patterns, parses every matched file as an
// [OverlayFile], stamps the per-file metadata onto each overlay, appends the
// resulting overlays after inline overlays, and clears [ComponentConfig.OverlayFiles]
// so the returned config is not expanded twice.
func ExpandResolvedOverlayFiles(
	fs opctx.FS, component ComponentConfig, referenceDir string, permissiveConfigParsing bool,
) (ComponentConfig, error) {
	if len(component.OverlayFiles) == 0 {
		return component, nil
	}

	if referenceDir == "" && overlayFilesHasRelativePattern(component.OverlayFiles) {
		return ComponentConfig{}, fmt.Errorf(
			"component %#q has relative 'overlay-files' entries but no reference directory",
			component.Name,
		)
	}

	loaded, err := loadOverlayFiles(fs, referenceDir, component.OverlayFiles, permissiveConfigParsing)
	if err != nil {
		return ComponentConfig{}, fmt.Errorf("component %#q overlay-files:\n%w", component.Name, err)
	}

	component.Overlays = append(component.Overlays, loaded...)
	// Clear the glob list after expansion so later resolver paths do not append the same overlays again.
	component.OverlayFiles = nil

	return component, nil
}

func overlayFilesHasRelativePattern(patterns []string) bool {
	for _, pattern := range patterns {
		if !filepath.IsAbs(pattern) {
			return true
		}
	}

	return false
}

// loadOverlayFiles resolves each glob pattern (relative to referenceDir if not
// already absolute), parses every matched overlay file, validates each file's
// metadata, stamps the file metadata onto each overlay, and resolves overlay
// `source` paths relative to the overlay file.
//
// Matches are concatenated in the order patterns are declared; within a single
// pattern, matches are applied in filename (lexicographic) order, using the full
// path as a tie-breaker when filenames match. Duplicate matches across patterns
// are de-duplicated, preserving first occurrence. Glob patterns that match no
// files contribute no overlays; literal paths must match a file.
func loadOverlayFiles(
	fs opctx.FS, referenceDir string, patterns []string, permissiveConfigParsing bool,
) ([]ComponentOverlay, error) {
	var (
		ordered []string
		seen    = make(map[string]bool)
	)

	for _, pattern := range patterns {
		absPattern := pattern
		if !filepath.IsAbs(absPattern) {
			absPattern = path.Join(referenceDir, absPattern)
		}

		matches, err := fileutils.Glob(
			fs, absPattern,
			doublestar.WithFailOnIOErrors(),
			doublestar.WithFilesOnly(),
		)

		switch {
		case err != nil:
			return nil, fmt.Errorf("failed to scan for overlay files matching %q:\n%w", pattern, err)
		case len(matches) == 0 && !containsPattern(pattern):
			return nil, fmt.Errorf("%w: %q", ErrOverlayFilesNoMatches, pattern)
		case len(matches) == 0:
			continue
		}

		slices.SortFunc(matches, func(left, right string) int {
			if result := cmp.Compare(filepath.Base(left), filepath.Base(right)); result != 0 {
				return result
			}

			return cmp.Compare(left, right)
		})

		for _, match := range matches {
			if seen[match] {
				continue
			}

			seen[match] = true
			ordered = append(ordered, match)
		}
	}

	var result []ComponentOverlay

	for _, overlayPath := range ordered {
		fromFile, err := loadOverlayFile(fs, overlayPath, permissiveConfigParsing)
		if err != nil {
			return nil, err
		}

		result = append(result, fromFile...)
	}

	return result, nil
}

// loadOverlayFile parses a single overlay file, validates its metadata, stamps the
// metadata onto each overlay, and absolutizes per-overlay `source` paths relative
// to the file's directory.
func loadOverlayFile(
	fs opctx.FS, overlayPath string, permissiveConfigParsing bool,
) ([]ComponentOverlay, error) {
	file, err := fs.Open(overlayPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open overlay file %q:\n%w", overlayPath, err)
	}
	defer file.Close()

	decoder := toml.NewDecoder(file)

	if !permissiveConfigParsing {
		decoder.DisallowUnknownFields()
	}

	var ofile OverlayFile
	if err := decoder.Decode(&ofile); err != nil {
		return nil, fmt.Errorf("failed to parse overlay file %q:\n%w", overlayPath, err)
	}

	// In permissive mode, drop invalid file-level metadata so overlays still load
	// cleanly; otherwise stamping the bad metadata would cause overlay-level
	// validation to re-fail later with a misleading location.
	stampedMetadata := &ofile.Metadata
	if err := ofile.Metadata.Validate(); err != nil {
		if !permissiveConfigParsing {
			return nil, fmt.Errorf("invalid [metadata] in overlay file %q:\n%w", overlayPath, err)
		}

		slog.Warn(
			"Overlay file metadata validation failed; dropping metadata and continuing due to '--permissive-config'",
			"overlayFile", overlayPath,
			"error", err,
		)

		stampedMetadata = nil
	}

	if len(ofile.Overlays) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrOverlayFileEmpty, overlayPath)
	}

	overlayDir := filepath.Dir(overlayPath)

	for idx := range ofile.Overlays {
		overlay := &ofile.Overlays[idx]

		if overlay.Metadata != nil {
			return nil, fmt.Errorf("%w (overlay %d in %q)", ErrOverlayFilePerOverlayMetadata, idx+1, overlayPath)
		}

		overlay.Metadata = stampedMetadata.clone()

		overlay.Source = makeAbsolute(overlayDir, overlay.Source)
		if err := overlay.Validate(); err != nil {
			return nil, fmt.Errorf("invalid overlay %d in overlay file %q:\n%w", idx+1, overlayPath, err)
		}
	}

	return ofile.Overlays, nil
}
