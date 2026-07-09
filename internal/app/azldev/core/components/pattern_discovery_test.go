// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const patternTestOverlayContent = `
[metadata]
category = "azl-branding-policy"

[[overlays]]
description = "Bump release"
type        = "spec-search-replace"
section     = "%install"
regex       = "unused"
`

// writeOverlayFile writes a minimal but valid overlay TOML at path.
func writeOverlayFile(t *testing.T, env *testutils.TestEnv, path string) {
	t.Helper()

	require.NoError(t, fileutils.MkdirAll(env.TestFS, dirOf(path)))
	require.NoError(t, fileutils.WriteFile(
		env.TestFS, path, []byte(patternTestOverlayContent), fileperms.PrivateFile,
	))
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}

	return "."
}

func TestFindAllComponents_PatternDiscoveryAtProjectScope(t *testing.T) {
	env := testutils.NewTestEnv(t)

	env.Config.DefaultComponentConfig.OverlayFiles = []string{
		"/project/comps/{component}/overlays/*.overlay.toml",
	}

	writeOverlayFile(t, env, "/project/comps/foo/overlays/0001.overlay.toml")
	writeOverlayFile(t, env, "/project/comps/foo/overlays/0002.overlay.toml")
	writeOverlayFile(t, env, "/project/comps/bar/overlays/0001.overlay.toml")

	all, err := components.NewResolver(env.Env).FindAllComponents()
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"foo", "bar"}, all.Names())

	foo, ok := all.TryGet("foo")
	require.True(t, ok)
	assert.Len(t, foo.GetConfig().Overlays, 2, "both foo overlay files should attach")

	bar, ok := all.TryGet("bar")
	require.True(t, ok)
	assert.Len(t, bar.GetConfig().Overlays, 1)
}

// Declared components with no 'overlay-files' inherit the project pattern; the
// placeholder is substituted with the component's own name at glob time.
func TestFindAllComponents_DeclaredComponentInheritsProjectPattern(t *testing.T) {
	env := testutils.NewTestEnv(t)

	env.Config.DefaultComponentConfig.OverlayFiles = []string{
		"/project/comps/{component}/overlays/*.overlay.toml",
	}

	writeOverlayFile(t, env, "/project/comps/foo/overlays/0001.overlay.toml")
	writeOverlayFile(t, env, "/project/comps/foo/overlays/0002.overlay.toml")

	env.Config.Components["foo"] = projectconfig.ComponentConfig{Name: "foo"}

	all, err := components.NewResolver(env.Env).FindAllComponents()
	require.NoError(t, err)

	foo, ok := all.TryGet("foo")
	require.True(t, ok)
	assert.Len(t, foo.GetConfig().Overlays, 2,
		"foo should inherit the project pattern with {component}=foo")
}

// A component that sets its own 'overlay-files' replaces the inherited project
// pattern wholesale; the placeholder pattern no longer applies.
func TestFindAllComponents_ComponentOverlayFilesReplacesInherited(t *testing.T) {
	env := testutils.NewTestEnv(t)

	env.Config.DefaultComponentConfig.OverlayFiles = []string{
		"/project/comps/{component}/overlays/*.overlay.toml",
	}

	// This file would match the inherited pattern (component=bar).
	writeOverlayFile(t, env, "/project/comps/bar/overlays/0001.overlay.toml")

	// But this file lives elsewhere and is the only one bar's config points to.
	writeOverlayFile(t, env, "/project/custom/bar/0001.overlay.toml")

	env.Config.Components["bar"] = projectconfig.ComponentConfig{
		Name:         "bar",
		OverlayFiles: []string{"/project/custom/bar/*.overlay.toml"},
	}

	all, err := components.NewResolver(env.Env).FindAllComponents()
	require.NoError(t, err)

	bar, ok := all.TryGet("bar")
	require.True(t, ok)
	assert.Len(t, bar.GetConfig().Overlays, 1,
		"bar overrides overlay-files wholesale; inherited pattern must not apply")
}

// Empty per-component 'overlay-files' disables the inherited project pattern.
func TestFindAllComponents_ComponentOverlayFilesEmptyDisablesInheritance(t *testing.T) {
	env := testutils.NewTestEnv(t)

	env.Config.DefaultComponentConfig.OverlayFiles = []string{
		"/project/comps/{component}/overlays/*.overlay.toml",
	}

	writeOverlayFile(t, env, "/project/comps/baz/overlays/0001.overlay.toml")

	env.Config.Components["baz"] = projectconfig.ComponentConfig{
		Name:         "baz",
		OverlayFiles: []string{},
	}

	all, err := components.NewResolver(env.Env).FindAllComponents()
	require.NoError(t, err)

	baz, ok := all.TryGet("baz")
	require.True(t, ok)
	assert.Empty(t, baz.GetConfig().Overlays,
		"empty overlay-files must disable inherited pattern")
}

func TestFindAllComponents_PatternDiscoverySameScopeCollisionErrors(t *testing.T) {
	env := testutils.NewTestEnv(t)

	env.Config.DefaultComponentConfig.OverlayFiles = []string{
		"/project/a/{component}/*.overlay.toml",
		"/project/b/{component}/*.overlay.toml",
	}

	writeOverlayFile(t, env, "/project/a/dup/0001.overlay.toml")
	writeOverlayFile(t, env, "/project/b/dup/0002.overlay.toml")

	_, err := components.NewResolver(env.Env).FindAllComponents()
	require.Error(t, err)
	require.ErrorIs(t, err, components.ErrPatternDiscoveryCollision,
		"expected ErrPatternDiscoveryCollision, got %v", err)
	assert.Contains(t, err.Error(), "dup")
}

// A pattern match whose captured segment isn't a safe component name
// (spaces, dot-directory, etc.) is skipped without failing overall discovery.
func TestFindAllComponents_PatternDiscoverySkipsUnsafeCapturedNames(t *testing.T) {
	env := testutils.NewTestEnv(t)

	env.Config.DefaultComponentConfig.OverlayFiles = []string{
		"/project/comps/{component}/overlays/*.overlay.toml",
	}

	// Well-formed match — should be discovered.
	writeOverlayFile(t, env, "/project/comps/openssl/overlays/0001.overlay.toml")

	// Unsafe captured segments — should be skipped without erroring the whole
	// discovery. These simulate directories a user might create that don't
	// correspond to valid component names.
	writeOverlayFile(t, env, "/project/comps/has space/overlays/0001.overlay.toml")
	writeOverlayFile(t, env, "/project/comps/./overlays/0001.overlay.toml")

	all, err := components.NewResolver(env.Env).FindAllComponents()
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"openssl"}, all.Names(),
		"only the safely-named directory should be discovered")
}
