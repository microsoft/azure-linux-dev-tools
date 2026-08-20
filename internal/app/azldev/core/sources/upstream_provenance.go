// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
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

// distPlaceholder is the conditional RPM %{?dist} token substituted with the
// resolved Fedora dist tag when expanding a pristine upstream Release value.
const distPlaceholder = "%{?dist}"

// distPlaceholderPlain is the unconditional %{dist} form. It must be substituted
// too so a literal upstream Release like "5%{dist}" does not lazily expand
// against the Azure Linux buildroot's dist tag at build time.
const distPlaceholderPlain = "%{dist}"

// autoreleaseResolver resolves an rpmautospec %autorelease to a concrete Fedora
// release number by running rpmautospec in an isolated environment (a mock
// chroot in production). Abstracted so tests can substitute a fake without a
// real mock. Implemented by [*MockProcessor].
type autoreleaseResolver interface {
	// CalculateRelease returns the raw `rpmautospec calculate-release` output
	// for the spec named specFilename inside specHostDir (which must hold the
	// pristine .git history). The caller parses and validates the result.
	CalculateRelease(ctx context.Context, specHostDir, specFilename string) (string, error)
}

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
// applied). The Release tag's %{?dist} token is expanded to the resolved Fedora
// dist tag, and an rpmautospec %autorelease is resolved to the pristine Fedora
// release number (see resolveUpstreamRelease).
//
// Best-effort: if the component is not a Fedora upstream, or the spec cannot be
// located or parsed, no macros are added. User-defined macros take precedence —
// an existing entry is never overwritten.
func (p *sourcePreparerImpl) addUpstreamProvenanceMacros(
	ctx context.Context, macros map[string]string, component components.Component, sourcesDir string,
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

	resolvedRelease := p.resolveUpstreamRelease(ctx, component.GetName(), release, sourcesDir, specPath)
	if resolvedRelease != "" {
		setMacroIfAbsent(macros, fedoraUpstreamReleaseMacro, resolvedRelease)
	}
}

// resolveUpstreamRelease converts a pristine Release tag value into the value
// emitted for %fedora_upstream_release.
//
//   - A literal or in-spec-macro release has only its %{?dist} token replaced
//     with the resolved Fedora dist tag; other macros are left for lazy
//     expansion in the consuming spec.
//   - An rpmautospec %autorelease is resolved to a concrete number by running
//     `rpmautospec calculate-release` against the pristine dist-git checkout,
//     whose HEAD is the pinned upstream commit (this runs before azldev's
//     synthetic history is layered on, so the count matches Fedora's). The
//     git-derived number is combined with the resolved Fedora dist tag rather
//     than trusting the build root's %{?dist} (azldev's mock is Azure Linux, not
//     Fedora).
//
// Returns "" to signal that no release macro should be emitted — either the tag
// was empty, or it used %autorelease that could not be resolved (emitting the
// literal %autorelease would expand against the wrong history at build time).
func (p *sourcePreparerImpl) resolveUpstreamRelease(
	ctx context.Context, componentName, release, sourcesDir, specPath string,
) string {
	if release == "" {
		return ""
	}

	if !ReleaseUsesAutorelease(release) {
		return replaceDistTag(release, p.upstreamDistTag)
	}

	number := p.calculateAutorelease(ctx, componentName, sourcesDir, specPath)
	if number == "" {
		return ""
	}

	return number + p.upstreamDistTag
}

// replaceDistTag substitutes both the conditional (%{?dist}) and unconditional
// (%{dist}) dist macros in a Release value with the resolved Fedora dist tag.
// The conditional form is replaced first so the %{dist} pass does not match the
// inner {dist} of a %{?dist} token and leave a stray "%{?" behind.
func replaceDistTag(release, distTag string) string {
	release = strings.ReplaceAll(release, distPlaceholder, distTag)
	release = strings.ReplaceAll(release, distPlaceholderPlain, distTag)

	return release
}

// calculateAutorelease resolves an rpmautospec %autorelease to the pristine
// Fedora release number (without the dist tag) by running rpmautospec inside a
// mock chroot via the configured [autoreleaseResolver]. Best-effort: returns ""
// with a warning when the dist-git history is unavailable, no resolver is
// configured, the command fails, or the output is not a usable release number.
func (p *sourcePreparerImpl) calculateAutorelease(
	ctx context.Context, componentName, sourcesDir, specPath string,
) string {
	// rpmautospec needs the upstream .git history, which is only preserved when
	// dist-git creation is enabled (e.g. render/build/prepare-sources).
	if !p.withGitRepo {
		slog.Debug("Skipping %fedora_upstream_release resolution; dist-git history not available",
			"component", componentName)

		return ""
	}

	if p.autoreleaseResolver == nil {
		slog.Warn("Skipping %fedora_upstream_release; no mock processor available to resolve %autorelease",
			"component", componentName)

		return ""
	}

	out, err := p.autoreleaseResolver.CalculateRelease(ctx, sourcesDir, filepath.Base(specPath))
	if err != nil {
		slog.Warn("Skipping %fedora_upstream_release; rpmautospec calculate-release failed",
			"component", componentName, "error", err)

		return ""
	}

	// rpmautospec prints "Calculated release number: <release>"; the release is
	// the final whitespace-separated field.
	number := strings.TrimSpace(out)
	if fields := strings.Fields(number); len(fields) > 0 {
		number = fields[len(fields)-1]
	}

	if number == "" || number[0] < '0' || number[0] > '9' {
		slog.Warn("Skipping %fedora_upstream_release; rpmautospec did not return a valid release number",
			"component", componentName, "output", strings.TrimSpace(out))

		return ""
	}

	return number
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
