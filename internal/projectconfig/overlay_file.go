// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"sort"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
)

// ErrOverlayFilePerOverlayMetadata is returned when a `.overlay.toml` file declares
// `metadata` on an individual `[[overlays]]` entry. Each file represents one logical
// change; the file-level `[metadata]` block is the single source of truth and is
// stamped onto every overlay in the file at load time.
var ErrOverlayFilePerOverlayMetadata = errors.New(
	"per-overlay metadata is not allowed inside a .overlay.toml file; declare metadata once at the file level",
)

// ErrOverlayFileEmpty is returned when a `.overlay.toml` file decodes to zero
// overlays. Such a file is almost certainly a typo (e.g. `[overlays]` instead of
// `[[overlays]]`) and would otherwise silently contribute nothing.
var ErrOverlayFileEmpty = errors.New("overlay file declares no overlays")

// ErrOverlayDirNoFiles is returned when a component's [ComponentConfig.OverlayDir]
// resolves to a directory that contains no `*.overlay.toml` files. A configured but
// empty overlay directory is almost always a misconfiguration (wrong path or missing
// files) and is surfaced as an error rather than silently contributing nothing.
var ErrOverlayDirNoFiles = errors.New("overlay-dir contains no *.overlay.toml files")

// ErrOverlayDirInDefaultConfig is returned when `overlay-dir` is set on a default
// component config. Overlay directories are resolved per concrete component before
// default configs are merged in, so a value set on a default would be silently
// ignored. Until overlay-dir is wired through the default-merge path, declaring it on
// a default config is rejected.
var ErrOverlayDirInDefaultConfig = errors.New(
	"overlay-dir is not supported on default-component-config; set it on individual components",
)

// overlayFileSuffix is the required filename suffix for files scanned from a
// component's [ComponentConfig.OverlayDir].
const overlayFileSuffix = "*.overlay.toml"

// OverlayFile is the on-disk representation of a single `.overlay.toml` document. Each
// file represents one logical change: the file-level [OverlayFile.Metadata] is applied
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

// applyOverlayDirs scans each component's [ComponentConfig.OverlayDir] (when set),
// parses every `*.overlay.toml` file in deterministic (filename) order, stamps the
// per-file metadata onto each overlay, and appends the resulting overlays to the
// component's [ComponentConfig.Overlays] slice. Inline overlays declared directly on
// the component come first; file-sourced overlays are appended after them.
//
// Called from [loadProjectConfigFile] after TOML decode but before [ConfigFile.Validate],
// so all per-overlay validation rules apply uniformly regardless of declaration site.
func applyOverlayDirs(fs opctx.FS, cfg *ConfigFile, permissiveConfigParsing bool) error {
	if err := rejectOverlayDirInDefaults(cfg); err != nil {
		return err
	}

	for componentName, component := range cfg.Components {
		if component.OverlayDir == "" {
			continue
		}

		absDir := makeAbsolute(cfg.dir, component.OverlayDir)

		loaded, err := loadOverlayDir(fs, absDir, permissiveConfigParsing)
		if err != nil {
			return fmt.Errorf("component %#q overlay-dir %q:\n%w", componentName, component.OverlayDir, err)
		}

		component.Overlays = append(component.Overlays, loaded...)
		cfg.Components[componentName] = component
	}

	return nil
}

// rejectOverlayDirInDefaults returns [ErrOverlayDirInDefaultConfig] if any default
// component config in cfg sets `overlay-dir`. Overlay directories are resolved per
// concrete component (see [applyOverlayDirs]) before default configs are merged, so a
// value declared on a default would be silently dropped. This stopgap surfaces the
// misconfiguration until overlay-dir is plumbed through the default-merge path.
func rejectOverlayDirInDefaults(cfg *ConfigFile) error {
	if cfg.DefaultComponentConfig != nil && cfg.DefaultComponentConfig.OverlayDir != "" {
		return fmt.Errorf("%w (project-level default-component-config)", ErrOverlayDirInDefaultConfig)
	}

	for name, group := range cfg.ComponentGroups {
		if group.DefaultComponentConfig.OverlayDir != "" {
			return fmt.Errorf("%w (component-group %#q)", ErrOverlayDirInDefaultConfig, name)
		}
	}

	for name, distro := range cfg.Distros {
		for version, versionDef := range distro.Versions {
			if versionDef.DefaultComponentConfig.OverlayDir != "" {
				return fmt.Errorf("%w (distro %#q version %#q)", ErrOverlayDirInDefaultConfig, name, version)
			}
		}
	}

	return nil
}

// loadOverlayDir parses every `*.overlay.toml` file in absDir (sorted by filename),
// validates each file's metadata, stamps the file metadata onto each overlay, and
// resolves overlay `source` paths relative to the overlay file.
func loadOverlayDir(
	fs opctx.FS, absDir string, permissiveConfigParsing bool,
) ([]ComponentOverlay, error) {
	pattern := path.Join(absDir, overlayFileSuffix)

	matches, err := fileutils.Glob(
		fs, pattern,
		doublestar.WithFailOnIOErrors(),
		doublestar.WithFailOnPatternNotExist(),
		doublestar.WithFilesOnly(),
	)

	switch {
	case errors.Is(err, doublestar.ErrPatternNotExist):
		return nil, fmt.Errorf("%w: %q", ErrOverlayDirNoFiles, absDir)
	case err != nil:
		return nil, fmt.Errorf("failed to scan for overlay files:\n%w", err)
	case len(matches) == 0:
		return nil, fmt.Errorf("%w: %q", ErrOverlayDirNoFiles, absDir)
	}

	sort.Strings(matches)

	var result []ComponentOverlay

	for _, overlayPath := range matches {
		fromFile, err := loadOverlayFile(fs, overlayPath, permissiveConfigParsing)
		if err != nil {
			return nil, err
		}

		result = append(result, fromFile...)
	}

	return result, nil
}

// loadOverlayFile parses a single `.overlay.toml` file, validates its metadata,
// stamps the metadata onto each overlay, and absolutizes per-overlay `source`
// paths relative to the file's directory.
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

	if err := ofile.Metadata.Validate(); err != nil {
		if !permissiveConfigParsing {
			return nil, fmt.Errorf("invalid [metadata] in overlay file %q:\n%w", overlayPath, err)
		}

		slog.Warn(
			"Overlay file metadata validation failed; continuing due to '--permissive-config'",
			"overlayFile", overlayPath,
			"error", err,
		)
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

		overlay.Metadata = ofile.Metadata.clone()
		overlay.Source = makeAbsolute(overlayDir, overlay.Source)
	}

	return ofile.Overlays, nil
}
