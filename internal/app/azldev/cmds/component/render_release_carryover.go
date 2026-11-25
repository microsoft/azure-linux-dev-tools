// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

var (
	baseReleaseMacro   = regexp.MustCompile(`%(?:\{\??baserelease\}|baserelease\b)`)
	baseReleaseLine    = regexp.MustCompile(`^[ \t]*%(?:global|define)[ \t]+baserelease[ \t]+\S`)
	autoChangelogMacro = regexp.MustCompile(`%(?:\{\??autochangelog\}|autochangelog\b)`)
	autoreleaseNoDist  = regexp.MustCompile(`(?:^|\s)-n(?:\s|\}|$)`)
)

// ErrMultipleReleaseTags is returned when one spec package has more than one
// 'Release' tag.
var ErrMultipleReleaseTags = errors.New("more than one 'Release' tag in a package")

// ErrMissingReleaseTag is returned when a spec's main package has no 'Release' tag.
var ErrMissingReleaseTag = errors.New("missing 'Release' tag in the main package")

// releaseTags maps each spec package name ("" for the main package) to the
// value of its single 'Release' tag.
type releaseTags map[string]string

// singleReleaseTags returns the 'Release' value of each package, requiring
// exactly one 'Release' tag in the main package and at most one in each
// subpackage. Tags inside conditionals count; commented-out lines do not.
func singleReleaseTags(opened *spec.Spec) (releaseTags, error) {
	var packages []string

	values := make(map[string][]string)

	err := opened.VisitTags(func(tagLine *spec.TagLine, ctx *spec.Context) error {
		if !strings.EqualFold(tagLine.Tag, "Release") {
			return nil
		}

		pkg := ctx.CurrentSection.Package
		if _, seen := values[pkg]; !seen {
			packages = append(packages, pkg)
		}

		values[pkg] = append(values[pkg], tagLine.Value)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading 'Release' tags:\n%w", err)
	}

	if len(values[""]) == 0 {
		return nil, ErrMissingReleaseTag
	}

	tags := make(releaseTags, len(values))

	for _, pkg := range packages {
		if count := len(values[pkg]); count > 1 {
			return nil, fmt.Errorf("%s has %d 'Release' tags; a spec may have one 'Release' tag in the main "+
				"package and at most one per subpackage, so use overlays to remove or comment out the "+
				"extra 'Release:' lines:\n%w", describeReleasePackage(pkg), count, ErrMultipleReleaseTags)
		}

		tags[pkg] = values[pkg][0]
	}

	return tags, nil
}

func describeReleasePackage(pkg string) string {
	if pkg == "" {
		return "main package"
	}

	return fmt.Sprintf("subpackage %#q", pkg)
}

// committedReleaseTags parses the committed render and returns its validated
// 'Release' tags.
func committedReleaseTags(history lastRender, editor spec.EditorMode) (releaseTags, error) {
	previous, err := spec.OpenSpec(strings.NewReader(history.spec), spec.WithEditor(editor))
	if err != nil {
		return nil, fmt.Errorf("parsing committed release spec:\n%w", err)
	}

	tags, err := singleReleaseTags(previous)
	if err != nil {
		return nil, fmt.Errorf("checking committed 'Release' tags:\n%w", err)
	}

	return tags, nil
}

// committedMainUsesAutorelease reports whether the committed render's final
// main 'Release' uses '%autorelease', without validating the tag count.
func committedMainUsesAutorelease(history lastRender, editor spec.EditorMode) (bool, error) {
	previous, err := spec.OpenSpec(strings.NewReader(history.spec), spec.WithEditor(editor))
	if err != nil {
		return false, fmt.Errorf("parsing committed release spec:\n%w", err)
	}

	release, err := previous.GetLastTag("", "Release")
	if err != nil {
		return false, fmt.Errorf("reading committed Release:\n%w", err)
	}

	return sources.ReleaseUsesAutorelease(release), nil
}

// staticRenderHistory resolves a committed '%autorelease' render for an
// upstream spec that now uses a static 'Release'. When the version is
// unchanged, every committed '%autorelease' 'Release' tag is replaced by the
// calculated release; the '%autochangelog' is expanded in either case.
func staticRenderHistory(
	env *azldev.Env, autospec renderAutorelease, dir, name, release string, history lastRender,
	versionChanged bool, editor spec.EditorMode,
) (resolved lastRender, err error) {
	if history.spec == "" || env.DryRun() || sources.ReleaseUsesAutorelease(release) {
		return history, nil
	}

	var committed releaseTags

	if versionChanged {
		// The committed Release is not carried, so only its '%autochangelog' needs history.
		usesAutorelease, err := committedMainUsesAutorelease(history, editor)
		if err != nil || !usesAutorelease || !autoChangelogMacro.MatchString(history.spec) {
			return history, err
		}
	} else {
		committed, err = committedReleaseTags(history, editor)
		if err != nil {
			return history, err
		}

		if !sources.ReleaseUsesAutorelease(committed[""]) {
			return history, nil
		}
	}

	scratch, err := fileutils.MkdirTemp(env.FS(), filepath.Dir(dir), "azldev-render-static-")
	if err != nil {
		return history, fmt.Errorf("creating committed autorelease scratch directory:\n%w", err)
	}
	defer fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), scratch, &err)

	snapshot := filepath.Join(scratch, "dist-git")
	if _, err := CreateComponentDistGitRepo(env, &DistGitRepoOptions{
		Component: name, OutputDir: snapshot,
	}); err != nil {
		return history, fmt.Errorf("extracting committed autorelease history:\n%w", err)
	}

	filename := name + ".spec"
	content := history.spec

	if !versionChanged {
		content, err = calculateCommittedAutorelease(env, autospec, snapshot, filename, history, committed, editor)
		if err != nil {
			return history, err
		}
	}

	resolved = history
	resolved.spec, err = staticRenderChangelog(env, autospec, snapshot, filename, content, editor)

	return resolved, err
}

// calculateCommittedAutorelease returns the committed spec with each
// '%autorelease' 'Release' tag replaced by the calculated release, keeping
// each tag's own '-n' (no '%{?dist}') handling.
func calculateCommittedAutorelease(
	env *azldev.Env, autospec renderAutorelease, snapshot, filename string, history lastRender,
	committed releaseTags, editor spec.EditorMode,
) (string, error) {
	calculated, err := autospec.CalculateRelease(env, snapshot, filename)
	if err != nil {
		return "", fmt.Errorf("calculating committed autorelease:\n%w", err)
	}

	fields := strings.Fields(calculated)
	if len(fields) == 0 {
		return "", errors.New("calculating committed autorelease: empty release")
	}

	calculated = fields[len(fields)-1]

	previous, err := spec.OpenSpec(
		strings.NewReader(history.spec), spec.WithEditor(editor), spec.WithPreservedReleaseFormatting())
	if err != nil {
		return "", fmt.Errorf("parsing committed release spec:\n%w", err)
	}

	for pkg, release := range committed {
		if !sources.ReleaseUsesAutorelease(release) {
			continue
		}

		value := calculated
		if !autoreleaseNoDist.MatchString(release) {
			value += "%{?dist}"
		}

		if err := previous.UpdateExistingTag(pkg, "Release", value); err != nil {
			return "", fmt.Errorf("carrying calculated autorelease:\n%w", err)
		}
	}

	var content strings.Builder
	if err := previous.Serialize(&content); err != nil {
		return "", fmt.Errorf("serializing committed release spec:\n%w", err)
	}

	return content.String(), nil
}

func staticRenderChangelog(
	env *azldev.Env, autospec renderAutorelease, dir, filename, content string, editor spec.EditorMode,
) (string, error) {
	if !autoChangelogMacro.MatchString(content) {
		return content, nil
	}

	changelog, err := autospec.GenerateChangelog(env, dir, filename)
	if err != nil {
		return "", fmt.Errorf("generating committed autorelease changelog:\n%w", err)
	}

	start, end, err := renderChangelogBounds([]byte(content), editor)
	if err != nil {
		return "", err
	}

	return content[:start] + "%changelog\n" + changelog + content[end:], nil
}

// carryStaticRenderRelease carries the committed static 'Release' values onto
// the upstream spec when the version is unchanged. The main 'Release' is
// carried, or its '%baserelease' definition when it uses one; each static
// subpackage 'Release' is carried from the committed subpackage of the same
// name, keeping the upstream value when the committed render lacks one.
func carryStaticRenderRelease(
	env *azldev.Env, working *spec.Spec, upstream releaseTags,
	history lastRender, editor spec.EditorMode,
) error {
	if history.spec == "" || sources.ReleaseUsesAutorelease(upstream[""]) || env.DryRun() {
		return nil
	}

	previous, err := spec.OpenSpec(strings.NewReader(history.spec), spec.WithEditor(editor))
	if err != nil {
		return fmt.Errorf("parsing committed release spec:\n%w", err)
	}

	committed, err := singleReleaseTags(previous)
	if err != nil {
		return fmt.Errorf("checking committed 'Release' tags:\n%w", err)
	}

	if err := carrySubpackageReleases(working, upstream, committed); err != nil {
		return err
	}

	if !baseReleaseMacro.MatchString(committed[""]) {
		if err := working.UpdateExistingTag("", "Release", committed[""]); err != nil {
			return fmt.Errorf("carrying committed Release:\n%w", err)
		}

		return nil
	}

	_, definition, err := baseReleaseDefinition(previous)
	if err != nil {
		return fmt.Errorf("reading committed 'baserelease' definition:\n%w", err)
	}

	line, _, err := baseReleaseDefinition(working)
	if err != nil {
		return fmt.Errorf("finding upstream 'baserelease' definition:\n%w", err)
	}

	working.ReplaceLine(line, definition)

	return nil
}

// carrySubpackageReleases copies each committed static subpackage 'Release'
// onto the upstream static 'Release' of the subpackage with the same name.
func carrySubpackageReleases(working *spec.Spec, upstream, committed releaseTags) error {
	for pkg, release := range upstream {
		oldRelease, found := committed[pkg]
		if pkg == "" || !found || sources.ReleaseUsesAutorelease(release) ||
			sources.ReleaseUsesAutorelease(oldRelease) {
			continue
		}

		if err := working.UpdateExistingTag(pkg, "Release", oldRelease); err != nil {
			return fmt.Errorf("carrying committed Release for %s:\n%w", describeReleasePackage(pkg), err)
		}
	}

	return nil
}

func baseReleaseDefinition(opened *spec.Spec) (line int, definition string, err error) {
	var matches int

	err = opened.Visit(func(ctx *spec.Context) error {
		if ctx.CurrentSection.SectName == "" && ctx.RawLine != nil && baseReleaseLine.MatchString(*ctx.RawLine) {
			line, definition = ctx.CurrentLineNum, *ctx.RawLine
			matches++
		}

		return nil
	})
	if err != nil {
		return 0, "", fmt.Errorf("finding 'baserelease' macro:\n%w", err)
	}

	if matches != 1 {
		return 0, "", fmt.Errorf("expected one 'baserelease' definition, found %d", matches)
	}

	return line, definition, nil
}
