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

func staticRenderHistory(
	env *azldev.Env, autospec renderAutorelease, dir, name, release string, history lastRender, editor spec.EditorMode,
) (resolved lastRender, err error) {
	if history.spec == "" || env.DryRun() || sources.ReleaseUsesAutorelease(release) {
		return history, nil
	}

	previous, err := spec.OpenSpec(
		strings.NewReader(history.spec), spec.WithEditor(editor), spec.WithPreservedReleaseFormatting())
	if err != nil {
		return history, fmt.Errorf("parsing committed release spec:\n%w", err)
	}

	previousRelease, err := previous.GetLastTag("", "Release")
	if err != nil {
		return history, fmt.Errorf("reading committed Release:\n%w", err)
	}

	if !sources.ReleaseUsesAutorelease(previousRelease) {
		return history, nil
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

	release, err = autospec.CalculateRelease(env, snapshot, filename)
	if err != nil {
		return history, fmt.Errorf("calculating committed autorelease:\n%w", err)
	}

	fields := strings.Fields(release)
	if len(fields) == 0 {
		return history, errors.New("calculating committed autorelease: empty release")
	}

	release = fields[len(fields)-1]
	if !autoreleaseNoDist.MatchString(previousRelease) {
		release += "%{?dist}"
	}

	if err := previous.SetTag("", "Release", release); err != nil {
		return history, fmt.Errorf("carrying calculated autorelease:\n%w", err)
	}

	var content strings.Builder
	if err := previous.Serialize(&content); err != nil {
		return history, fmt.Errorf("serializing committed release spec:\n%w", err)
	}

	resolved = history
	resolved.spec, err = staticRenderChangelog(env, autospec, snapshot, filename, content.String(), editor)

	return resolved, err
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

func carryStaticRenderRelease(
	env *azldev.Env, working *spec.Spec, release string,
	history lastRender, editor spec.EditorMode,
) error {
	if history.spec == "" || sources.ReleaseUsesAutorelease(release) || env.DryRun() {
		return nil
	}

	previous, err := spec.OpenSpec(strings.NewReader(history.spec), spec.WithEditor(editor))
	if err != nil {
		return fmt.Errorf("parsing committed release spec:\n%w", err)
	}

	oldRelease, err := previous.GetLastTag("", "Release")
	if err != nil {
		return fmt.Errorf("reading committed Release:\n%w", err)
	}

	if !baseReleaseMacro.MatchString(oldRelease) {
		if err := working.SetTag("", "Release", oldRelease); err != nil {
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
