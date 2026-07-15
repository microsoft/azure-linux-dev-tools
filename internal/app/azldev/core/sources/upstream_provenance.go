// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// Macro names emitted to carry upstream Fedora provenance into a component's
// build. Specs may reference these (e.g. for SBAT) via %fedora_upstream_version
// and %fedora_upstream_release.
const (
	fedoraUpstreamVersionMacro = "fedora_upstream_version"
	fedoraUpstreamReleaseMacro = "fedora_upstream_release"
)

// distPlaceholder is the RPM %{?dist} token substituted with the resolved
// Fedora dist tag when expanding a pristine upstream Release value.
const distPlaceholder = "%{?dist}"

// FedoraDistTag returns the RPM %{?dist} expansion for a Fedora distro
// (".fc<releasever>", e.g. ".fc43"), or "" when the distro is not Fedora or the
// release version is not a plain integer. Fedora dist tags are always ".fc<N>";
// a non-numeric release version (e.g. "rawhide") has no well-defined dist tag,
// so provenance is disabled rather than emitting an invalid value like
// ".fcrawhide". Callers pass the result to [WithUpstreamProvenance].
func FedoraDistTag(distroName, releaseVer string) string {
	if !strings.EqualFold(distroName, "fedora") || !isNumericReleaseVer(releaseVer) {
		return ""
	}

	return ".fc" + releaseVer
}

// isNumericReleaseVer reports whether s is a non-empty string of ASCII digits,
// i.e. a Fedora numbered release like "43".
func isNumericReleaseVer(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// addUpstreamProvenanceMacros injects %fedora_upstream_version and
// %fedora_upstream_release into macros for Fedora upstream components. The
// values are read from the pristine upstream spec on disk (before overlays are
// applied) and the Release tag's %{?dist} token is expanded to the resolved
// Fedora dist tag.
//
// Best-effort: if the component is not a Fedora upstream, or the spec cannot be
// located or parsed, no macros are added. User-defined macros take precedence —
// an existing entry is never overwritten.
func (p *sourcePreparerImpl) addUpstreamProvenanceMacros(
	macros map[string]string, component components.Component, sourcesDir string,
) {
	// Only Fedora upstream components carry upstream provenance. Local and
	// SRPM components have no upstream spec; their own identity is already
	// available at build time via %{version}/%{release}.
	if p.upstreamDistTag == "" {
		return
	}

	config := component.GetConfig()

	// Opt-in: the macros are only useful to specs that reference them, so they
	// are emitted only for components that explicitly request them.
	if !config.Build.EmitUpstreamProvenance {
		return
	}

	if config.Spec.SourceType != projectconfig.SpecSourceTypeUpstream {
		return
	}

	specPath, err := findSpecInDir(p.fs, component, sourcesDir)
	if err != nil {
		// The user opted in via emit-upstream-provenance, so surface the
		// failure loudly enough to be visible in normal CI logs; still
		// non-fatal so the render/build proceeds without the macros.
		slog.Warn("Skipping upstream provenance macros; spec not found",
			"component", component.GetName(), "error", err)

		return
	}

	version, release, err := parseSpecVersionRelease(p.fs, specPath)
	if err != nil {
		slog.Warn("Skipping upstream provenance macros; failed to parse spec",
			"component", component.GetName(), "error", err)

		return
	}

	if version != "" {
		setMacroIfAbsent(macros, fedoraUpstreamVersionMacro, version)
	}

	if release != "" {
		setMacroIfAbsent(macros, fedoraUpstreamReleaseMacro,
			strings.ReplaceAll(release, distPlaceholder, p.upstreamDistTag))
	}
}

// setMacroIfAbsent sets macros[name]=value only when name is not already
// present, so user-defined macros (e.g. from 'build.defines') win.
func setMacroIfAbsent(macros map[string]string, name, value string) {
	if _, exists := macros[name]; !exists {
		macros[name] = value
	}
}

// parseSpecVersionRelease reads the Version and Release tags from the base
// package of the spec at specPath. Values are captured verbatim (no macro
// expansion beyond the caller's later %{?dist} substitution). Missing tags
// yield empty strings; it is not an error for a tag to be absent.
func parseSpecVersionRelease(fs opctx.FS, specPath string) (version, release string, err error) {
	data, err := fileutils.ReadFile(fs, specPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read spec %#q:\n%w", specPath, err)
	}

	parsed, err := spec.OpenSpec(bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf("failed to parse spec %#q:\n%w", specPath, err)
	}

	// VisitTagsPackage("") iterates tags in the base (unnamed) package, where
	// Name/Version/Release live for a well-formed spec.
	visitErr := parsed.VisitTagsPackage("", func(tagLine *spec.TagLine, _ *spec.Context) error {
		switch strings.ToLower(tagLine.Tag) {
		case "version":
			if version == "" {
				version = strings.TrimSpace(tagLine.Value)
			}
		case "release":
			if release == "" {
				release = strings.TrimSpace(tagLine.Value)
			}
		}

		return nil
	})
	if visitErr != nil {
		return "", "", fmt.Errorf("failed to scan spec tags in %#q:\n%w", specPath, visitErr)
	}

	return version, release, nil
}
