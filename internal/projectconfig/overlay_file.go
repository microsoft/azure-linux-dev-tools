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

// ErrOverlayFilesNoMatches is returned when one of a component's
// [ComponentConfig.OverlayFiles] glob patterns matches no files on disk. A configured
// glob that matches nothing is almost always a misconfiguration (wrong path or
// missing files) and is surfaced as an error rather than silently contributing
// nothing.
var ErrOverlayFilesNoMatches = errors.New("overlay-files pattern matched no files")

// ErrOverlayFilesInDefaultConfig is returned when `overlay-files` is set on a
// default component config. Overlay file globs are resolved per concrete component
// before default configs are merged in, so a value set on a default would be
// silently ignored. Until overlay-files is wired through the default-merge path,
// declaring it on a default config is rejected.
var ErrOverlayFilesInDefaultConfig = errors.New(
	"overlay-files is not supported on default-component-config; set it on individual components",
)

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

// applyOverlayFiles resolves each component's [ComponentConfig.OverlayFiles] glob
// patterns (when set), parses every matched file as an [OverlayFile] in deterministic
// order, stamps the per-file metadata onto each overlay, and appends the resulting
// overlays to the component's [ComponentConfig.Overlays] slice. Inline overlays
// declared directly on the component come first; file-sourced overlays are appended
// after them.
//
// Called from [loadProjectConfigFile] after TOML decode but before [ConfigFile.Validate],
// so all per-overlay validation rules apply uniformly regardless of declaration site.
func applyOverlayFiles(fs opctx.FS, cfg *ConfigFile, permissiveConfigParsing bool) error {
	if err := rejectOverlayFilesInDefaults(cfg); err != nil {
		return err
	}

	for componentName, component := range cfg.Components {
		if len(component.OverlayFiles) == 0 {
			continue
		}

		loaded, err := loadOverlayFiles(fs, cfg.dir, component.OverlayFiles, permissiveConfigParsing)
		if err != nil {
			return fmt.Errorf("component %#q overlay-files:\n%w", componentName, err)
		}

		component.Overlays = append(component.Overlays, loaded...)
		cfg.Components[componentName] = component
	}

	return nil
}

// rejectOverlayFilesInDefaults returns [ErrOverlayFilesInDefaultConfig] if any default
// component config in cfg sets `overlay-files`. Overlay file globs are resolved per
// concrete component (see [applyOverlayFiles]) before default configs are merged, so a
// value declared on a default would be silently dropped. This stopgap surfaces the
// misconfiguration until overlay-files is plumbed through the default-merge path.
func rejectOverlayFilesInDefaults(cfg *ConfigFile) error {
	if cfg.DefaultComponentConfig != nil && len(cfg.DefaultComponentConfig.OverlayFiles) > 0 {
		return fmt.Errorf("%w (project-level default-component-config)", ErrOverlayFilesInDefaultConfig)
	}

	for name, group := range cfg.ComponentGroups {
		if len(group.DefaultComponentConfig.OverlayFiles) > 0 {
			return fmt.Errorf("%w (component-group %#q)", ErrOverlayFilesInDefaultConfig, name)
		}
	}

	for name, distro := range cfg.Distros {
		for version, versionDef := range distro.Versions {
			if len(versionDef.DefaultComponentConfig.OverlayFiles) > 0 {
				return fmt.Errorf("%w (distro %#q version %#q)", ErrOverlayFilesInDefaultConfig, name, version)
			}
		}
	}

	return nil
}

// loadOverlayFiles resolves each glob pattern (relative to referenceDir if not
// already absolute), parses every matched overlay file, validates each file's
// metadata, stamps the file metadata onto each overlay, and resolves overlay
// `source` paths relative to the overlay file.
//
// Matches are concatenated in the order patterns are declared; within a single
// pattern, matches are applied in filename (lexicographic) order, using the full
// path as a tie-breaker when filenames match. Duplicate matches across patterns
// are de-duplicated, preserving first occurrence. Each pattern is required to
// match at least one file.
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
			doublestar.WithFailOnPatternNotExist(),
			doublestar.WithFilesOnly(),
		)

		switch {
		case errors.Is(err, doublestar.ErrPatternNotExist):
			return nil, fmt.Errorf("%w: %q", ErrOverlayFilesNoMatches, pattern)
		case err != nil:
			return nil, fmt.Errorf("failed to scan for overlay files matching %q:\n%w", pattern, err)
		case len(matches) == 0:
			return nil, fmt.Errorf("%w: %q", ErrOverlayFilesNoMatches, pattern)
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
	}

	return ofile.Overlays, nil
}
