// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// collectCustomizations gathers all customization items declared on the
// component's config into a uniform list. Items are emitted in a stable
// order (overlays first in declared order; then build, spec, release,
// packages, source-files in field order) so the output is deterministic.
func collectCustomizations(name string, config *projectconfig.ComponentConfig) []CustomizationItem {
	if config == nil {
		return nil
	}

	items := make([]CustomizationItem, 0)

	items = appendOverlayItems(items, config.Overlays)
	items = appendBuildItems(items, config.Build)
	items = appendSpecItems(items, name, config.Spec)
	items = appendReleaseItems(items, config.Release)
	items = appendRenderItems(items, config.Render)
	items = appendPackageItems(items, config.Packages)
	items = appendSourceFileItems(items, config.SourceFiles)

	return items
}

// appendRenderItems flags non-default render-config customizations.
func appendRenderItems(
	items []CustomizationItem, render projectconfig.ComponentRenderConfig,
) []CustomizationItem {
	if render.SkipFileFilter {
		items = append(items, CustomizationItem{
			Kind:  "render.skip-file-filter",
			Value: strconv.FormatBool(true),
		})
	}

	return items
}

// appendOverlayItems converts each overlay into a CustomizationItem.
func appendOverlayItems(
	items []CustomizationItem, overlays []projectconfig.ComponentOverlay,
) []CustomizationItem {
	for i := range overlays {
		overlay := &overlays[i]

		items = append(items, CustomizationItem{
			Kind:        string(overlay.Type),
			Value:       overlaySummary(overlay),
			Description: overlay.Description,
		})
	}

	return items
}

// overlaySummary returns a short human-readable identification of an overlay,
// suitable for the Value field of a CustomizationItem.
func overlaySummary(overlay *projectconfig.ComponentOverlay) string {
	switch {
	case overlay.Tag != "" && overlay.Value != "":
		return fmt.Sprintf("%s=%s", overlay.Tag, overlay.Value)
	case overlay.Tag != "":
		return overlay.Tag
	case overlay.EffectiveSourceName() != "":
		return overlay.EffectiveSourceName()
	case overlay.Filename != "":
		return overlay.Filename
	case overlay.SectionName != "":
		return overlay.SectionName
	case overlay.Regex != "":
		return overlay.Regex
	default:
		return ""
	}
}

// appendBuildItems converts non-default build-config fields into items.
func appendBuildItems(
	items []CustomizationItem, build projectconfig.ComponentBuildConfig,
) []CustomizationItem {
	for _, flag := range build.With {
		items = append(items, CustomizationItem{Kind: "build.with", Value: flag})
	}

	for _, flag := range build.Without {
		items = append(items, CustomizationItem{Kind: "build.without", Value: flag})
	}

	// Sort define keys so iteration order is deterministic.
	defineKeys := make([]string, 0, len(build.Defines))
	for key := range build.Defines {
		defineKeys = append(defineKeys, key)
	}

	sort.Strings(defineKeys)

	for _, key := range defineKeys {
		items = append(items, CustomizationItem{
			Kind:  "build.defines",
			Value: fmt.Sprintf("%s=%s", key, build.Defines[key]),
		})
	}

	for _, macro := range build.Undefines {
		items = append(items, CustomizationItem{Kind: "build.undefines", Value: macro})
	}

	if build.EmitUpstreamProvenance {
		items = append(items, CustomizationItem{
			Kind:  "build.emit-upstream-provenance",
			Value: strconv.FormatBool(true),
		})
	}

	if build.Check.Skip {
		items = append(items, CustomizationItem{
			Kind:        "build.check.skip",
			Value:       strconv.FormatBool(true),
			Description: build.Check.SkipReason,
		})
	}

	return items
}

// appendSpecItems captures spec-source customizations relative to the
// inherited default. We cannot perfectly know the inherited default without
// re-resolving, but we can flag the cases that are unambiguous (commit pin,
// upstream-name renamed away from the component name, upstream-distro set).
func appendSpecItems(
	items []CustomizationItem, name string, spec projectconfig.SpecSource,
) []CustomizationItem {
	// Only surface SourceType when explicitly set in the raw per-component
	// config -- components that inherit from group defaults leave it empty,
	// so this avoids inflating the customization count for every component.
	if spec.SourceType != "" {
		items = append(items, CustomizationItem{
			Kind:  "spec.source-type",
			Value: string(spec.SourceType),
		})
	}

	if spec.UpstreamCommit != "" {
		items = append(items, CustomizationItem{
			Kind:  "spec.upstream-commit",
			Value: spec.UpstreamCommit,
		})
	}

	if spec.UpstreamName != "" && spec.UpstreamName != name {
		items = append(items, CustomizationItem{
			Kind:  "spec.upstream-name",
			Value: spec.UpstreamName,
		})
	}

	// Both Name and Version are real build inputs (only Snapshot carries
	// fingerprint:"-"), so a version-only pin is a genuine customization.
	if spec.UpstreamDistro.Name != "" || spec.UpstreamDistro.Version != "" {
		items = append(items, CustomizationItem{
			Kind:  "spec.upstream-distro",
			Value: spec.UpstreamDistro.String(),
		})
	}

	return items
}

// appendReleaseItems flags non-default release-calculation modes.
func appendReleaseItems(
	items []CustomizationItem, release projectconfig.ReleaseConfig,
) []CustomizationItem {
	if release.Calculation == "" || release.Calculation == projectconfig.ReleaseCalculationAuto {
		return items
	}

	return append(items, CustomizationItem{
		Kind:  "release.calculation",
		Value: string(release.Calculation),
	})
}

// appendPackageItems emits one item per binary package override.
func appendPackageItems(
	items []CustomizationItem, packages map[string]projectconfig.PackageConfig,
) []CustomizationItem {
	if len(packages) == 0 {
		return items
	}

	keys := make([]string, 0, len(packages))
	for key := range packages {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		items = append(items, CustomizationItem{Kind: "packages", Value: key})
	}

	return items
}

// appendSourceFileItems emits one item per declared source-file reference,
// plus distinct items for high-signal toggles: the ReplaceUpstream flag (which
// actively masks a same-named upstream source), the Script field (custom
// generation script), and MockPackages (chroot deps for that script).
func appendSourceFileItems(
	items []CustomizationItem, sourceFiles []projectconfig.SourceFileReference,
) []CustomizationItem {
	for _, sourceFile := range sourceFiles {
		items = append(items, CustomizationItem{Kind: "source-files", Value: sourceFile.Filename})

		if sourceFile.ReplaceUpstream {
			items = append(items, CustomizationItem{
				Kind:  "source-files.replace-upstream",
				Value: sourceFile.Filename,
			})
		}

		if sourceFile.Origin.Script != "" {
			items = append(items, CustomizationItem{
				Kind:  "source-files.script",
				Value: sourceFile.Origin.Script,
			})
		}

		for _, pkg := range sourceFile.Origin.MockPackages {
			items = append(items, CustomizationItem{
				Kind:  "source-files.mock-packages",
				Value: pkg,
			})
		}

		for _, input := range sourceFile.Origin.Inputs {
			items = append(items, CustomizationItem{
				Kind:  "source-files.inputs",
				Value: input,
			})
		}
	}

	return items
}
