// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
)

var (
	baseReleaseMacro = regexp.MustCompile(`%(?:\{\??baserelease\}|baserelease\b)`)
	baseReleaseLine  = regexp.MustCompile(`^[ \t]*%(?:global|define)[ \t]+baserelease[ \t]+\S`)
)

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
