// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
)

// commitResolver abstracts the ability to look up a commit by hash.
// This is satisfied by [*gogit.Repository] and can be replaced in tests.
type commitResolver interface {
	CommitObject(hash plumbing.Hash) (*object.Commit, error)
}

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

// GetVersionTagFromReader reads the Version tag value from a spec parsed from an [io.Reader].
// Returns the raw value string (e.g. "1.0.0" or "%{base_version}").
// Returns [spec.ErrNoSuchTag] if no Version tag is found.
func GetVersionTagFromReader(reader io.Reader) (string, error) {
	openedSpec, err := spec.OpenSpec(reader)
	if err != nil {
		return "", fmt.Errorf("failed to parse spec:\n%w", err)
	}

	var versionValue string

	err = openedSpec.VisitTagsPackage("", func(tagLine *spec.TagLine, _ *spec.Context) error {
		if strings.EqualFold(tagLine.Tag, "Version") {
			versionValue = tagLine.Value
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to visit tags:\n%w", err)
	}

	if versionValue == "" {
		return "", fmt.Errorf("version tag not found:\n%w", spec.ErrNoSuchTag)
	}

	return versionValue, nil
}

// getVersionAtUpstreamCommit reads the Version tag from a component's spec file
// at a specific upstream commit in the dist-git repository. The spec file is
// located by name (e.g. "package.spec") within the commit's tree.
func getVersionAtUpstreamCommit(
	resolver commitResolver,
	commitHash string,
	specFileName string,
) (string, error) {
	commitObj, err := resolver.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		return "", fmt.Errorf("failed to get commit %#q:\n%w", commitHash, err)
	}

	tree, err := commitObj.Tree()
	if err != nil {
		return "", fmt.Errorf("failed to get tree for commit %#q:\n%w", commitHash, err)
	}

	file, err := tree.File(specFileName)
	if err != nil {
		return "", fmt.Errorf("spec file %#q not found at commit %#q:\n%w", specFileName, commitHash, err)
	}

	reader, err := file.Reader()
	if err != nil {
		return "", fmt.Errorf("failed to read spec file %#q at commit %#q:\n%w", specFileName, commitHash, err)
	}
	defer reader.Close()

	return GetVersionTagFromReader(reader)
}

// CountCommitsSinceVersionChange determines how many synthetic commits should
// contribute to the static Release bump. It walks the [FingerprintChange] list
// (chronological, oldest first), reads the Version tag from the dist-git spec
// at each unique [FingerprintChange.UpstreamCommit], and finds the point where
// the Version last changed. Only synthetic commits after that point contribute
// to the count.
//
// When the Version tag cannot be read at a historical commit (e.g. because of
// a shallow clone, a force-push that rewrote upstream history, or a spec
// rename), the unresolvable commit is treated as a version boundary and the
// walk stops — only the already-counted commits contribute. This mirrors how
// [buildInterleavedSequence] drops orphaned commits rather than placing them on
// top of the synthetic history.
//
// When the resolver is nil (local components with no upstream repo), all
// changes are counted because there is no version history to consult.
func CountCommitsSinceVersionChange(
	resolver commitResolver,
	specFileName string,
	changes []FingerprintChange,
) int {
	if len(changes) == 0 {
		return 0
	}

	if resolver == nil {
		return len(changes)
	}

	// Walk changes newest-to-oldest, resolving the Version tag at each unique
	// upstream commit. Stop as soon as a version change is detected.
	versionCache := make(map[string]string)
	count := 0

	latestVersion := ""

	for idx := len(changes) - 1; idx >= 0; idx-- {
		hash := changes[idx].UpstreamCommit

		version, ok := versionCache[hash]
		if !ok {
			var err error

			version, err = getVersionAtUpstreamCommit(resolver, hash, specFileName)
			if err != nil {
				slog.Warn("Failed to read Version tag at upstream commit; treating as version boundary",
					"commit", hash, "error", err)

				break
			}

			versionCache[hash] = version
		}

		if latestVersion == "" {
			latestVersion = version
		}

		if version != latestVersion {
			break
		}

		count++
	}

	slog.Debug("Computed version-aware release bump count",
		"latestVersion", latestVersion,
		"totalChanges", len(changes),
		"sinceVersionChange", count)

	return count
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

// tryBumpStaticRelease checks whether the component's spec uses %autorelease.
// If not, it computes a version-aware bump count via [CountCommitsSinceVersionChange]
// and applies the change as an overlay to the spec file in-place.
//
// When the component's release calculation is "manual", this function is a no-op.
//
// When the spec uses %autorelease, this function is a no-op because rpmautospec
// already resolves the release number from git history.
//
// When the Release tag uses a non-standard value (not %autorelease and not a leading
// integer, e.g. %{pkg_release}), the component must set release.calculation to
// "manual", and likely define an explicit overlay that sets the Release tag.
// If a non-standard Release is found and release.calculation is not "manual",
// an error is returned.
func (p *sourcePreparerImpl) tryBumpStaticRelease(
	component components.Component,
	sourcesDirPath string,
	repo commitResolver,
	changes []FingerprintChange,
) error {
	if component.GetConfig().Release.Calculation == projectconfig.ReleaseCalculationManual {
		slog.Debug("Component uses manual release calculation; skipping static release bump",
			"component", component.GetName())

		return nil
	}

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

	// Only compute the version-aware commit count after confirming the spec
	// uses a static release, to avoid unnecessary git tree traversals for
	// components that use %autorelease or manual mode.
	specFileName := component.GetName() + ".spec"
	commitCount := CountCommitsSinceVersionChange(repo, specFileName, changes)

	newRelease, err := BumpStaticRelease(releaseValue, commitCount)
	if err != nil {
		// The Release tag does not start with an integer (e.g. %{pkg_release})
		// and the user did not set release.calculation to "manual".
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
