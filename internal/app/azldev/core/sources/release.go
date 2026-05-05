// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
)

// autoreleasePattern matches the %autorelease macro invocation in a Release tag value.
// This covers:
//   - bare form: %autorelease
//   - braced form: %{autorelease}
//   - braced form with arguments: %{autorelease -e asan}
//   - conditional form (no fallback): %{?autorelease}
var autoreleasePattern = regexp.MustCompile(`%(\{[?]?autorelease($|[}\s])|autorelease($|\s))`)

// staticReleasePattern matches only the two Release tag forms we can safely
// auto-bump: a bare integer (e.g. "1") or an integer followed by the
// conditional dist macro (e.g. "5%{?dist}"). Any other suffix — dotted
// segments, unknown macros, etc. — is rejected so the component must use
// 'release.calculation = "manual"'.
var staticReleasePattern = regexp.MustCompile(`^(\d+)(%\{\?dist\})?$`)

// GetReleaseTagValue reads the Release tag value from the spec file at specPath.
// It returns the raw value string as written in the spec (e.g. "1%{?dist}" or "%autorelease").
// Returns [spec.ErrNoSuchTag] if no Release tag is found.
func GetReleaseTagValue(fs opctx.FS, specPath string) (string, error) {
	specFile, err := fs.Open(specPath)
	if err != nil {
		return "", fmt.Errorf("failed to open spec %#q:\n%w", specPath, err)
	}
	defer specFile.Close()

	openedSpec, err := spec.OpenSpec(specFile)
	if err != nil {
		return "", fmt.Errorf("failed to parse spec %#q:\n%w", specPath, err)
	}

	var releaseValue string

	err = openedSpec.VisitTagsPackage("", func(tagLine *spec.TagLine, _ *spec.Context) error {
		if strings.EqualFold(tagLine.Tag, "Release") {
			releaseValue = tagLine.Value
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to visit tags in spec %#q:\n%w", specPath, err)
	}

	if releaseValue == "" {
		return "", fmt.Errorf("release tag not found in spec %#q:\n%w", specPath, spec.ErrNoSuchTag)
	}

	return releaseValue, nil
}

// ReleaseUsesAutorelease reports whether the given Release tag value uses the
// %autorelease macro (either bare or braced form).
func ReleaseUsesAutorelease(releaseValue string) bool {
	return autoreleasePattern.MatchString(releaseValue)
}

// BumpStaticRelease increments the leading integer in a static Release tag value
// by the given commit count.
func BumpStaticRelease(releaseValue string, commitCount int) (string, error) {
	matches := staticReleasePattern.FindStringSubmatch(releaseValue)
	if matches == nil {
		return "", fmt.Errorf("release value %#q does not start with an integer", releaseValue)
	}

	currentRelease, err := strconv.Atoi(matches[1])
	if err != nil {
		return "", fmt.Errorf("failed to parse release number from %#q:\n%w", releaseValue, err)
	}

	newRelease := currentRelease + commitCount
	suffix := matches[2]

	return fmt.Sprintf("%d%s", newRelease, suffix), nil
}

// tryBumpStaticRelease manages the Release tag based on the component's release
// calculation mode. It may bump, skip, or auto-detect depending on configuration:
//
//   - "manual":      no-op — component manages its own release numbering.
//   - "autorelease": no-op — rpmautospec resolves the release from git history.
//   - "static":      always bumps the static integer release by commitCount.
//   - "auto":        auto-detects from the spec's Release tag value; skips if
//     %autorelease is found, otherwise bumps the static integer.
func (p *sourcePreparerImpl) tryBumpStaticRelease(
	component components.Component,
	sourcesDirPath string,
	commitCount int,
) error {
	calc := component.GetConfig().Release.Calculation

	switch calc {
	case projectconfig.ReleaseCalculationManual:
		slog.Debug("Component uses manual release calculation; skipping static release bump",
			"component", component.GetName())

		return nil

	case projectconfig.ReleaseCalculationAutorelease:
		slog.Debug("Component uses autorelease calculation; skipping static release bump",
			"component", component.GetName())

		return nil

	case projectconfig.ReleaseCalculationStatic:
		return p.bumpStaticRelease(component, sourcesDirPath, commitCount)

	case projectconfig.ReleaseCalculationAuto:
		return p.autoBumpStaticRelease(component, sourcesDirPath, commitCount)

	default:
		return fmt.Errorf("component %#q has unknown release calculation mode %#q",
			component.GetName(), calc)
	}
}

// autoBumpStaticRelease auto-detects the release mode from the spec's Release tag value.
// If the tag uses %autorelease, it's a no-op. Otherwise it attempts a static bump.
func (p *sourcePreparerImpl) autoBumpStaticRelease(
	component components.Component,
	sourcesDirPath string,
	commitCount int,
) error {
	specPath, err := p.resolveSpecPath(component, sourcesDirPath)
	if err != nil {
		return err
	}

	releaseValue, err := GetReleaseTagValue(p.fs, specPath)
	if err != nil {
		return fmt.Errorf("failed to read Release tag for component %#q:\n%w",
			component.GetName(), err)
	}

	if ReleaseUsesAutorelease(releaseValue) {
		slog.Debug("Spec uses %%autorelease; skipping static release bump",
			"component", component.GetName())

		return nil
	}

	return p.applyStaticBump(component, specPath, releaseValue, commitCount)
}

// bumpStaticRelease unconditionally bumps the static integer release.
func (p *sourcePreparerImpl) bumpStaticRelease(
	component components.Component,
	sourcesDirPath string,
	commitCount int,
) error {
	specPath, err := p.resolveSpecPath(component, sourcesDirPath)
	if err != nil {
		return err
	}

	releaseValue, err := GetReleaseTagValue(p.fs, specPath)
	if err != nil {
		return fmt.Errorf("failed to read Release tag for component %#q:\n%w",
			component.GetName(), err)
	}

	return p.applyStaticBump(component, specPath, releaseValue, commitCount)
}

// applyStaticBump bumps a static integer release value and writes the result back to the spec.
func (p *sourcePreparerImpl) applyStaticBump(
	component components.Component,
	specPath, releaseValue string,
	commitCount int,
) error {
	newRelease, err := BumpStaticRelease(releaseValue, commitCount)
	if err != nil {
		return fmt.Errorf(
			"component %#q has a non-standard Release tag value %#q that cannot be auto-bumped; "+
				"set 'release.calculation = \"manual\"' in the component configuration "+
				"and add a \"spec-set-tag\" overlay for the Release tag if needed:\n%w",
			component.GetName(), releaseValue, err)
	}

	slog.Info("Bumping static release",
		"component", component.GetName(),
		"oldRelease", releaseValue,
		"newRelease", newRelease,
		"commitCount", commitCount)

	overlay := projectconfig.ComponentOverlay{
		Type:  projectconfig.ComponentOverlayUpdateSpecTag,
		Tag:   "Release",
		Value: newRelease,
	}

	if err := ApplySpecOverlayToFileInPlace(p.fs, overlay, specPath); err != nil {
		return fmt.Errorf("failed to apply release bump overlay for component %#q:\n%w",
			component.GetName(), err)
	}

	return nil
}
