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

// staticReleasePattern matches a leading integer in a static Release tag value,
// followed by an optional suffix (e.g. "%{?dist}").
var staticReleasePattern = regexp.MustCompile(`^(\d+)(.*)$`)

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

// HasUserReleaseOverlay reports whether the given overlay list contains an overlay
// that explicitly sets or updates the Release tag. This is used to determine whether
// a user has configured the component to handle a non-standard Release value
// (e.g. one using a custom macro like %{pkg_release}).
func HasUserReleaseOverlay(overlays []projectconfig.ComponentOverlay) bool {
	for _, overlay := range overlays {
		if !strings.EqualFold(overlay.Tag, "Release") {
			continue
		}

		if overlay.Type == projectconfig.ComponentOverlaySetSpecTag ||
			overlay.Type == projectconfig.ComponentOverlayUpdateSpecTag {
			return true
		}
	}

	return false
}

// tryBumpStaticRelease checks whether the component's spec uses %autorelease.
// If not, it bumps the static Release tag by commitCount and applies the change
// as an overlay to the spec file in-place. This ensures that components with static
// release numbers get deterministic version bumps matching the number of synthetic
// commits applied from the project repository.
//
// When the spec uses %autorelease, this function is a no-op because rpmautospec
// already resolves the release number from git history.
//
// When the Release tag uses a non-standard value (not %autorelease and not a leading
// integer, e.g. %{pkg_release}), the component must define an explicit overlay that
// sets the Release tag. If no such overlay exists, an error is returned.
func (p *sourcePreparerImpl) tryBumpStaticRelease(
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

	// Skip static release bump if the user has defined an explicit overlay for the Release tag.
	if HasUserReleaseOverlay(component.GetConfig().Overlays) {
		slog.Debug("Component has an explicit Release overlay; skipping static release bump",
			"component", component.GetName())

		return nil
	}

	newRelease, err := BumpStaticRelease(releaseValue, commitCount)
	if err != nil {
		// The Release tag does not start with an integer (e.g. %{pkg_release})
		// and the user did not provide an explicit overlay to set it.
		return fmt.Errorf(
			"component %#q has a non-standard Release tag value %#q that cannot be auto-bumped; "+
				"add a \"spec-set-tag\" overlay for the Release tag in the component configuration:\n%w",
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
