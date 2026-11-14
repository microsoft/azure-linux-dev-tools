// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test unexported helpers.
package projectconfig

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOverlayFilesPlaceholder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   string
		wantErr bool
	}{
		{name: "empty", entry: "", wantErr: true},
		{name: "missing placeholder", entry: "comps/*/overlays/*.overlay.toml", wantErr: true},
		{name: "multiple placeholders", entry: "comps/{component}/{component}/x.toml", wantErr: true},
		{name: "placeholder is prefix substring", entry: "prefix-{component}/x.toml", wantErr: true},
		{name: "placeholder is suffix substring", entry: "{component}-suffix/x.toml", wantErr: true},
		{name: "backslash separator rejected", entry: "comps\\{component}\\x.toml", wantErr: true},
		{name: "glob metachar before placeholder", entry: "**/{component}/*.overlay.toml", wantErr: true},
		{name: "star before placeholder", entry: "comps*/{component}/*.overlay.toml", wantErr: true},
		{name: "brace alternatives before placeholder", entry: "{base,extra}/{component}/*.overlay.toml", wantErr: true},
		{name: "brace suffix rejected", entry: "comps/{component}/{base,extra}/*.overlay.toml", wantErr: true},
		{name: "brace parent alternative rejected", entry: "comps/{component}/{..,overlays}/*.overlay.toml", wantErr: true},
		{
			name: "nested brace alternatives rejected", entry: "comps/{component}/{base,{..,extra}}/*.overlay.toml",
			wantErr: true,
		},
		{
			name: "concatenated brace alternatives rejected", entry: "comps/{component}/{.,x}{.,y}/*.overlay.toml",
			wantErr: true,
		},
		{name: "unmatched opening brace rejected", entry: "comps/{component}/fix{.overlay.toml", wantErr: true},
		{name: "unmatched closing brace rejected", entry: "comps/{component}/fix}.overlay.toml", wantErr: true},
		{name: "whole segment at start", entry: "{component}/overlays/*.overlay.toml", wantErr: false},
		{name: "whole segment in middle", entry: "comps/{component}/overlays/*.overlay.toml", wantErr: false},
		{name: "placeholder at end rejected", entry: "comps/overlays/{component}", wantErr: true},
		{name: "placeholder followed by only slash rejected", entry: "comps/{component}/", wantErr: true},
		{name: "doublestar in suffix allowed", entry: "comps/{component}/**/*.overlay.toml", wantErr: false},
		{name: "absolute path", entry: "/project/base/comps/{component}/overlays/*.overlay.toml", wantErr: false},
		{name: "parent directory in prefix allowed", entry: "../comps/{component}/overlays/*.overlay.toml"},
		{name: "parent after placeholder rejected", entry: "comps/{component}/../overlays/*.overlay.toml", wantErr: true},
		{name: "nested parent rejected", entry: "comps/{component}/nested/../overlays/*.overlay.toml", wantErr: true},
		{name: "nested escape rejected", entry: "comps/{component}/nested/../../overlays/*.overlay.toml", wantErr: true},
		{name: "trailing parent rejected", entry: "comps/{component}/overlays/..", wantErr: true},
		{name: "two dots in filename allowed", entry: "comps/{component}/overlays/fix..overlay.toml"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := validateOverlayFilesPlaceholder(testCase.entry)
			if testCase.wantErr {
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidOverlayFilesEntry,
					"expected ErrInvalidOverlayFilesEntry, got %v", err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSubstituteOverlayFilesPlaceholder(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		"comps/foo/overlays/*.toml",
		substituteOverlayFilesPlaceholder("comps/{component}/overlays/*.toml", "foo"))

	// No placeholder → unchanged.
	assert.Equal(t,
		"overlays/*.toml",
		substituteOverlayFilesPlaceholder("overlays/*.toml", "foo"))
}

func TestLoadProjectConfig_OverlayFilesPlaceholderPaths(t *testing.T) {
	testCases := []struct {
		name     string
		pattern  string
		expected string
		wantErr  bool
		braces   bool
	}{
		{
			name:     "parent in placeholder prefix",
			pattern:  "../comps/{component}/overlays/*.overlay.toml",
			expected: "/test/comps/{component}/overlays/*.overlay.toml",
		},
		{
			name:    "parent removes placeholder",
			pattern: "comps/{component}/../overlays/*.overlay.toml",
			wantErr: true,
		},
		{
			name:    "nested parents remove placeholder",
			pattern: "comps/{component}/nested/../../overlays/*.overlay.toml",
			wantErr: true,
		},
		{
			name:    "parent after nested directory",
			pattern: "comps/{component}/nested/../overlays/*.overlay.toml",
			wantErr: true,
		},
		{
			name:     "plain legacy pattern unchanged",
			pattern:  "nested/../overlays/*.overlay.toml",
			expected: "nested/../overlays/*.overlay.toml",
		},
		{
			name:    "safe brace alternatives rejected",
			pattern: "comps/{component}/{base,extra}/*.overlay.toml",
			wantErr: true,
			braces:  true,
		},
		{
			name:    "brace parent bypass rejected",
			pattern: "comps/{component}/{..,overlays}/*.overlay.toml",
			wantErr: true,
			braces:  true,
		},
		{
			name:     "plain legacy brace pattern unchanged",
			pattern:  "{base,extra}/*.overlay.toml",
			expected: "{base,extra}/*.overlay.toml",
		},
	}

	for _, withoutLockfile := range []bool{false, true} {
		mode := "locked"
		if withoutLockfile {
			mode = "lockless"
		}

		t.Run(mode, func(t *testing.T) {
			for _, testCase := range testCases {
				t.Run(testCase.name, func(t *testing.T) {
					const projectDir = "/test/project"

					osEnv := testctx.NewTestOSEnv()
					osEnv.SetEnv("XDG_CONFIG_HOME", "/test/xdghome")
					ctx := testctx.NewCtx(testctx.WithOSEnv(osEnv))
					require.NoError(t, fileutils.MkdirAll(ctx.FS(), projectDir))
					require.NoError(t, fileutils.WriteFile(ctx.FS(),
						filepath.Join(projectDir, DefaultConfigFileName),
						[]byte("[default-component-config]\noverlay-files = [\""+testCase.pattern+"\"]\n"),
						fileperms.PublicFile))

					_, config, err := LoadProjectConfig(
						ctx.FS(), ctx.OSEnv(), projectDir, true, "/test/tmp", nil, false, withoutLockfile,
					)
					if testCase.wantErr {
						require.ErrorIs(t, err, ErrInvalidOverlayFilesEntry)

						if testCase.braces {
							require.ErrorContains(t, err, "must not contain braces after")
						} else {
							require.ErrorContains(t, err, "must not contain '..' path segments after")
						}

						return
					}

					require.NoError(t, err)
					assert.Equal(t, []string{testCase.expected}, config.DefaultComponentConfig.OverlayFiles)
				})
			}
		})
	}
}

func TestLoadAndResolveProjectConfig_OverlayFilesDefaultScopes(t *testing.T) {
	scopes := []struct {
		name    string
		lines   []string
		entries func(*ProjectConfig) []string
	}{
		{
			name:  "project",
			lines: []string{"[default-component-config]"},
			entries: func(config *ProjectConfig) []string {
				return config.DefaultComponentConfig.OverlayFiles
			},
		},
		{
			name: "distro",
			lines: []string{
				"[distros.test.versions.'1']",
				"release-ver = '1'",
				"[distros.test.versions.'1'.default-component-config]",
			},
			entries: func(config *ProjectConfig) []string {
				return config.Distros["test"].Versions["1"].DefaultComponentConfig.OverlayFiles
			},
		},
		{
			name:  "group",
			lines: []string{"[component-groups.test.default-component-config]"},
			entries: func(config *ProjectConfig) []string {
				return config.ComponentGroups["test"].DefaultComponentConfig.OverlayFiles
			},
		},
	}

	for _, scope := range scopes {
		t.Run(scope.name, func(t *testing.T) {
			for _, value := range []struct {
				name     string
				toml     string
				expected []string
			}{
				{name: "omitted"},
				{name: "plain-glob", toml: "['overlays/*.overlay.toml']", expected: []string{"overlays/*.overlay.toml"}},
				{name: "empty-list", toml: "[]", expected: []string{}},
				{
					name: "placeholder", toml: "['comps/{component}/overlays/*.overlay.toml']",
					expected: []string{"/project/comps/{component}/overlays/*.overlay.toml"},
				},
			} {
				t.Run(value.name, func(t *testing.T) {
					for _, mode := range []struct {
						name            string
						withoutLockfile bool
					}{
						{name: "default"},
						{name: "without-lockfile", withoutLockfile: true},
					} {
						t.Run(mode.name, func(t *testing.T) {
							ctx := testctx.NewCtx()
							configPath := filepath.Join("/project", DefaultConfigFileName)

							lines := append([]string{}, scope.lines...)
							if value.toml != "" {
								lines = append(lines, "overlay-files = "+value.toml)
							}

							require.NoError(t, fileutils.WriteFile(ctx.FS(), configPath,
								[]byte(strings.Join(lines, "\n")+"\n"), fileperms.PrivateFile))

							config, err := loadAndResolveProjectConfig(ctx.FS(),
								loadOptions{withoutLockfile: mode.withoutLockfile}, configPath)
							if scope.name != "project" && value.toml != "" {
								require.ErrorIs(t, err, ErrInvalidOverlayFilesEntry)
								require.ErrorContains(t, err, scope.name)
								assert.Nil(t, config)

								return
							}

							require.NoError(t, err)
							assert.Equal(t, value.expected, scope.entries(config))
						})
					}
				})
			}
		})
	}
}
