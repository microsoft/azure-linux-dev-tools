// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
)

// autoreleasePattern matches the %autorelease macro invocation in a Release tag value.
// This covers both the bare form (%autorelease) and the braced form (%{autorelease}).
var autoreleasePattern = regexp.MustCompile(`%(\{autorelease\}|autorelease($|\s))`)

// autochangelogPattern matches the %autochangelog macro invocation in a changelog section.
// This covers both the bare form (%autochangelog) and the braced form (%{autochangelog}).
var autochangelogPattern = regexp.MustCompile(`%(\{autochangelog\}|autochangelog($|\s))`)

// staticReleasePattern matches a leading integer in a static Release tag value,
// followed by an optional suffix (e.g. "%{?dist}").
var staticReleasePattern = regexp.MustCompile(`^(\d+)(.*)$`)

// GetReleaseTagValue reads the Release tag value from the spec file at specPath.
// It returns the raw value string as written in the spec (e.g. "1%{?dist}" or "%autorelease").
// Returns [spec.ErrNoSuchTag] if no Release tag is found.
func GetReleaseTagValue(fs opctx.FS, specPath string) (string, error) {
	return GetSpecTagValue(fs, specPath, "Release")
}

// GetSpecTagValue reads the value of the given tag from the spec file at specPath.
// The tag comparison is case-insensitive. Returns the raw value string as written in the spec.
// Returns [spec.ErrNoSuchTag] if the tag is not found.
func GetSpecTagValue(fs opctx.FS, specPath, tag string) (string, error) {
	specFile, err := fs.Open(specPath)
	if err != nil {
		return "", fmt.Errorf("failed to open spec %#q:\n%w", specPath, err)
	}
	defer specFile.Close()

	openedSpec, err := spec.OpenSpec(specFile)
	if err != nil {
		return "", fmt.Errorf("failed to parse spec %#q:\n%w", specPath, err)
	}

	var value string

	err = openedSpec.VisitTagsPackage("", func(tagLine *spec.TagLine, _ *spec.Context) error {
		if strings.EqualFold(tagLine.Tag, tag) {
			value = tagLine.Value
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to visit tags in spec %#q:\n%w", specPath, err)
	}

	if value == "" {
		return "", fmt.Errorf("tag %#q not found in spec %#q:\n%w", tag, specPath, spec.ErrNoSuchTag)
	}

	return value, nil
}

// ChangelogUsesAutochangelog reports whether the spec file at specPath uses the
// %autochangelog macro in its %changelog section. Returns false if the spec has
// no %changelog section.
func ChangelogUsesAutochangelog(fs opctx.FS, specPath string) (bool, error) {
	specFile, err := fs.Open(specPath)
	if err != nil {
		return false, fmt.Errorf("failed to open spec %#q:\n%w", specPath, err)
	}
	defer specFile.Close()

	openedSpec, err := spec.OpenSpec(specFile)
	if err != nil {
		return false, fmt.Errorf("failed to parse spec %#q:\n%w", specPath, err)
	}

	var found bool

	err = openedSpec.Visit(func(ctx *spec.Context) error {
		if ctx.Target.TargetType != spec.SectionLineTarget {
			return nil
		}

		if ctx.CurrentSection.SectName != "%changelog" {
			return nil
		}

		if ctx.RawLine != nil && autochangelogPattern.MatchString(*ctx.RawLine) {
			found = true
		}

		return nil
	})
	if err != nil {
		return false, fmt.Errorf("failed to visit spec %#q:\n%w", specPath, err)
	}

	return found, nil
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

// tryApplyReleaseAndChangelog examines the component's spec to determine which
// combination of %autorelease and %autochangelog macros (if any) it uses, then
// applies the appropriate release bump and/or changelog updates.
//
// When the Release tag uses a non-standard value (not %autorelease and not a leading
// integer, e.g. %{pkg_release}), the component must define an explicit overlay that
// sets the Release tag. If no such overlay exists, an error is returned.
func (p *sourcePreparerImpl) tryApplyReleaseAndChangelog(
	component components.Component,
	sourcesDirPath string,
	commits []CommitMetadata,
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

	usesAutorelease := ReleaseUsesAutorelease(releaseValue)
	hasUserOverlay := HasUserReleaseOverlay(component.GetConfig().Overlays)

	usesAutochangelog, err := ChangelogUsesAutochangelog(p.fs, specPath)
	if err != nil {
		return fmt.Errorf("failed to check for %%autochangelog in component %#q:\n%w",
			component.GetName(), err)
	}

	slog.Debug("Applying release and changelog updates",
		"component", component.GetName(),
		"usesAutorelease", usesAutorelease,
		"usesAutochangelog", usesAutochangelog,
		"hasUserReleaseOverlay", hasUserOverlay)

	// Bump release if the spec uses a static Release tag and the user hasn't
	// provided an explicit Release overlay. When an overlay is present, the user
	// is managing the Release value themselves.
	if !usesAutorelease && !hasUserOverlay {
		if err := p.applyReleaseBump(component, specPath, releaseValue, len(commits)); err != nil {
			return err
		}
	}

	// Add changelog entries if the spec uses a static %changelog section.
	if !usesAutochangelog {
		if err := p.addChangelogEntries(component, specPath, commits); err != nil {
			return err
		}
	}

	return nil
}

// applyReleaseBump increments the static Release tag by commitCount and applies
// the change as an overlay to the spec file in-place.
func (p *sourcePreparerImpl) applyReleaseBump(
	component components.Component,
	specPath, releaseValue string,
	commitCount int,
) error {
	newRelease, err := BumpStaticRelease(releaseValue, commitCount)
	if err != nil {
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

// addChangelogEntries adds one changelog entry per commit to the spec file.
// Commits are applied in chronological order (oldest first). Each entry uses the
// commit's author, email, timestamp, and first line of the commit message.
func (p *sourcePreparerImpl) addChangelogEntries(
	component components.Component,
	specPath string,
	commits []CommitMetadata,
) (err error) {
	versionValue, err := GetSpecTagValue(p.fs, specPath, "Version")
	if err != nil {
		return fmt.Errorf("failed to read Version tag for component %#q:\n%w",
			component.GetName(), err)
	}

	releaseValue, err := GetReleaseTagValue(p.fs, specPath)
	if err != nil {
		return fmt.Errorf("failed to read Release tag for component %#q:\n%w",
			component.GetName(), err)
	}

	specFile, err := p.fs.Open(specPath)
	if err != nil {
		return fmt.Errorf("failed to open spec %#q:\n%w", specPath, err)
	}

	openedSpec, err := spec.OpenSpec(specFile)
	specFile.Close()

	if err != nil {
		return fmt.Errorf("failed to parse spec %#q:\n%w", specPath, err)
	}

	// Ensure the spec has a %changelog section.
	hasChangelog, err := openedSpec.HasSection("%changelog")
	if err != nil {
		return fmt.Errorf("failed to check for %%changelog section in spec %#q:\n%w", specPath, err)
	}

	if !hasChangelog {
		slog.Info("No %%changelog section found; adding empty section",
			"component", component.GetName())

		openedSpec.InsertLinesAt([]string{"", "%changelog"}, openedSpec.LineCount())
	}

	// Add entries in reverse chronological order (newest first) since each
	// AddChangelogEntry inserts after the %changelog header.
	// The commits slice is in chronological order (oldest first), so iterate in reverse.
	for i := len(commits) - 1; i >= 0; i-- {
		commit := commits[i]
		commitTime := time.Unix(commit.Timestamp, 0).UTC()
		detail := firstLine(commit.Message)

		slog.Debug("Adding changelog entry",
			"component", component.GetName(),
			"author", commit.Author,
			"message", detail)

		if err := openedSpec.AddChangelogEntry(
			commit.Author, commit.AuthorEmail,
			versionValue, releaseValue,
			commitTime, []string{detail},
		); err != nil {
			return fmt.Errorf("failed to add changelog entry for component %#q:\n%w",
				component.GetName(), err)
		}
	}

	// Write updated spec back to disk.
	outFile, err := p.fs.OpenFile(specPath, os.O_RDWR|os.O_TRUNC, fileperms.PrivateFile)
	if err != nil {
		return fmt.Errorf("failed to open spec %#q for writing:\n%w", specPath, err)
	}

	defer defers.HandleDeferError(outFile.Close, &err)

	if err := openedSpec.Serialize(outFile); err != nil {
		return fmt.Errorf("failed to write spec %#q:\n%w", specPath, err)
	}

	return nil
}

// firstLine returns the first line of a multi-line string.
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}

	return s
}
