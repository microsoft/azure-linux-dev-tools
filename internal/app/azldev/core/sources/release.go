// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

const (
	rpmDevBumpspecIdentity = "Azure Linux Packaging Team <azurelinux@microsoft.com>"
	rpmDevBumpspecComment  = "- rebuilt"
	rpmDevBumpspecDate     = "Mon Jan 06 2025"
)

type releaseStrategy uint8

const (
	releaseStrategyLegacy releaseStrategy = iota
	releaseStrategyRPMDevBumpspec
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
var staticReleasePattern = regexp.MustCompile(`^(\d+)(%\{\??dist\})?$`)

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

func (p *sourcePreparerImpl) tryBumpRelease(
	ctx context.Context, component components.Component, sourcesDirPath string, changes []FingerprintChange,
) error {
	switch p.releaseStrategy {
	case releaseStrategyLegacy:
		return p.tryBumpStaticRelease(component, sourcesDirPath, len(changes))
	case releaseStrategyRPMDevBumpspec:
		return p.tryBumpRPMDevBumpspec(ctx, component, sourcesDirPath, changes)
	default:
		return fmt.Errorf("component %#q has unknown release strategy %d", component.GetName(), p.releaseStrategy)
	}
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
		return p.readAndBumpRelease(component, sourcesDirPath, commitCount, true)

	case projectconfig.ReleaseCalculationAuto:
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

//nolint:cyclop,funlen // The transaction boundaries preserve all four release modes and failure paths.
func (p *sourcePreparerImpl) tryBumpRPMDevBumpspec(
	ctx context.Context, component components.Component, sourcesDirPath string, changes []FingerprintChange,
) (err error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("release bump cancelled before execution:\n%w", err)
	}

	if len(changes) == 0 {
		return nil
	}

	calc := component.GetConfig().Release.Calculation
	switch calc {
	case projectconfig.ReleaseCalculationManual, projectconfig.ReleaseCalculationAutorelease:
		return nil
	case projectconfig.ReleaseCalculationAuto, projectconfig.ReleaseCalculationStatic:
	default:
		return fmt.Errorf("component %#q has unknown release calculation mode %#q", component.GetName(), calc)
	}

	specPath, err := p.resolveSpecPath(component, sourcesDirPath)
	if err != nil {
		return err
	}

	releaseValue, err := GetReleaseTagValue(p.fs, specPath)
	if err != nil {
		return fmt.Errorf("failed to read Release tag for component %#q:\n%w", component.GetName(), err)
	}

	if ReleaseUsesAutorelease(releaseValue) {
		if calc == projectconfig.ReleaseCalculationStatic {
			return fmt.Errorf("component %#q has 'release.calculation = \"static\"' but its Release tag uses %%autorelease; "+
				"set 'release.calculation = \"autorelease\"' instead", component.GetName())
		}

		return nil
	}

	if p.bumpspecCtx == nil || p.bumpspecScratchDir == "" {
		return fmt.Errorf("component %#q requires rpmdev-bumpspec release handling, but its host runner is not configured",
			component.GetName())
	}

	if err := fileutils.MkdirAll(p.bumpspecCtx.FS(), p.bumpspecScratchDir); err != nil {
		return fmt.Errorf("failed to create rpmdev-bumpspec scratch parent %#q:\n%w", p.bumpspecScratchDir, err)
	}

	originalBytes, err := fileutils.ReadFile(p.fs, specPath)
	if err != nil {
		return fmt.Errorf("failed to save original spec %#q before release bumps:\n%w", specPath, err)
	}

	defer func() {
		if err == nil {
			return
		}

		if restoreErr := fileutils.WriteFile(p.fs, specPath, originalBytes, fileperms.PublicFile); restoreErr != nil {
			err = fmt.Errorf("release bump failed and restoring original spec %#q also failed:\n%w",
				specPath, errors.Join(err, restoreErr))
		}
	}()

	for operation := range changes {
		homeDir, mkErr := fileutils.MkdirTemp(p.bumpspecCtx.FS(), p.bumpspecScratchDir, "azldev-bumpspec-")
		if mkErr != nil {
			return fmt.Errorf("failed to create rpmdev-bumpspec scratch directory:\n%w", mkErr)
		}

		request := RPMDevBumpspecRequest{
			SpecPath: specPath, Comment: rpmDevBumpspecComment, Identity: rpmDevBumpspecIdentity,
			Datestamp: rpmDevBumpspecDate, HomeDir: homeDir, Build: component.GetConfig().Build,
			TargetArch: p.bumpspecTargetArch,
		}
		runErr := RunRPMDevBumpspec(p.bumpspecCtx, request) //nolint:contextcheck

		cleanupErr := p.bumpspecCtx.FS().RemoveAll(homeDir)
		if runErr != nil {
			if cleanupErr != nil {
				return fmt.Errorf("failed release bump operation %d and scratch cleanup:\n%w",
					operation+1, errors.Join(runErr, cleanupErr))
			}

			return fmt.Errorf("failed to bump release for component %#q at operation %d:\n%w",
				component.GetName(), operation+1, runErr)
		}

		if cleanupErr != nil {
			return fmt.Errorf("failed to clean rpmdev-bumpspec scratch directory %#q:\n%w", homeDir, cleanupErr)
		}
	}

	slog.Info("Bumped release with rpmdev-bumpspec", "component", component.GetName(), "count", len(changes))

	return nil
}
