// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"fmt"
	"log/slog"
	"regexp"
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
// auto-bump: a bare integer (e.g. "1") or an integer followed by a
// dist macro (e.g. "5%{?dist}" or "5%{dist}"). Any other suffix — dotted
// segments, unknown macros, etc. — is rejected so the component must use
// 'release.calculation = "manual"'.
var staticReleasePattern = regexp.MustCompile(`^(\d+)(?:%\{\??dist\})?$`)

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

// bumpStaticRelease increments the leading integer in a static Release tag value
// by the given commit count.
func bumpStaticRelease(releaseValue string, commitCount int) (string, error) {
	matches := staticReleasePattern.FindStringSubmatch(releaseValue)
	if matches == nil {
		return "", fmt.Errorf("release value %#q does not start with an integer", releaseValue)
	}

	newCounter, err := incrementDecimalInteger(matches[1], commitCount)
	if err != nil {
		return "", fmt.Errorf("failed to bump release number from %#q:\n%w", releaseValue, err)
	}

	suffix := releaseValue[len(matches[1]):]

	return newCounter + suffix, nil
}

// tryBumpStaticRelease manages the Release tag based on the component's release
// calculation mode. It may bump, skip, or auto-detect depending on configuration:
//
//   - "manual":      no-op — component manages its own release numbering.
//   - "autorelease": no-op — rpmautospec resolves the release from git history.
//   - "static":      always bumps the configured or default release counter.
//   - "auto":        auto-detects from the spec's Release tag value; skips if
//     %autorelease is found, otherwise bumps the default release counter.
func (p *sourcePreparerImpl) tryBumpStaticRelease(
	component components.Component,
	sourcesDirPath string,
	commitCount int,
) error {
	release := component.GetConfig().Release
	calc := release.Calculation

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
		if release.Counter != nil {
			return p.applyReleaseCounter(component, sourcesDirPath, commitCount, *release.Counter)
		}

		return p.readAndBumpRelease(component, sourcesDirPath, commitCount, true)

	case projectconfig.ReleaseCalculationAuto:
		if release.Counter != nil {
			return p.applyReleaseCounter(component, sourcesDirPath, commitCount, *release.Counter)
		}

		return p.readAndBumpRelease(component, sourcesDirPath, commitCount, false)

	default:
		return fmt.Errorf("component %#q has unknown release calculation mode %#q",
			component.GetName(), calc)
	}
}

// readAndBumpRelease reads the Release tag from the spec and bumps its static integer.
// When requireStaticRelease is true (explicit static mode), encountering %autorelease
// produces an error telling the user to switch to 'release.calculation = "autorelease"'.
// When false (auto mode), specs using %autorelease are silently skipped.
func (p *sourcePreparerImpl) readAndBumpRelease(
	component components.Component,
	sourcesDirPath string,
	commitCount int,
	requireStaticRelease bool,
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
		if requireStaticRelease {
			return fmt.Errorf(
				"component %#q has 'release.calculation = \"static\"' but its Release tag "+
					"uses %%autorelease; set 'release.calculation = \"autorelease\"' instead",
				component.GetName())
		}

		slog.Debug("Spec uses %%autorelease; skipping static release bump",
			"component", component.GetName())

		return nil
	}

	if _, bumpErr := bumpStaticRelease(releaseValue, commitCount); bumpErr != nil {
		return fmt.Errorf(
			"component %#q has a non-standard Release tag value %#q that cannot be auto-bumped; "+
				"configure 'release.counter' to identify the counter, or set 'release.calculation = \"manual\"' "+
				"only when the component manages its own release numbering:\n%w",
			component.GetName(), releaseValue, bumpErr)
	}

	if err := p.applyReleaseCounter(component, sourcesDirPath, commitCount, DefaultReleaseCounter()); err != nil {
		return err
	}

	return nil
}

func (p *sourcePreparerImpl) applyReleaseCounter(
	component components.Component,
	sourcesDirPath string,
	commitCount int,
	counter projectconfig.ReleaseCounter,
) error {
	specPath, err := p.resolveSpecPath(component, sourcesDirPath)
	if err != nil {
		return err
	}

	change, err := applyReleaseCounterToFileInPlace(
		p.fs, counter, specPath, component.GetConfig().Build, commitCount)
	if err != nil {
		return fmt.Errorf(
			"applying release counter for component %#q:\n%w",
			component.GetName(), err)
	}

	slog.Info("Bumping release counter",
		"component", component.GetName(),
		"source", counter.Source,
		"oldCounter", change.OldValue,
		"newCounter", change.NewValue,
		"commitCount", commitCount)

	return nil
}
