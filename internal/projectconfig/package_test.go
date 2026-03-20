// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackageGroupConfig_Validate(t *testing.T) {
	t.Run("empty group is valid", func(t *testing.T) {
		group := projectconfig.PackageGroupConfig{}
		assert.NoError(t, group.Validate())
	})

	t.Run("group with valid patterns is valid", func(t *testing.T) {
		group := projectconfig.PackageGroupConfig{
			Description:     "development packages",
			PackagePatterns: []string{"*-devel", "python3-*", "curl"},
		}
		assert.NoError(t, group.Validate())
	})

	t.Run("empty pattern string is invalid", func(t *testing.T) {
		group := projectconfig.PackageGroupConfig{
			PackagePatterns: []string{"*-devel", ""},
		}
		err := group.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "package-patterns[1]")
		assert.Contains(t, err.Error(), "must not be empty")
	})

	t.Run("malformed glob is invalid", func(t *testing.T) {
		group := projectconfig.PackageGroupConfig{
			PackagePatterns: []string{"[invalid"},
		}
		err := group.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid glob")
	})
}

func TestPackageConfig_MergeUpdatesFrom(t *testing.T) {
	t.Run("non-zero other overrides zero base", func(t *testing.T) {
		base := projectconfig.PackageConfig{}
		other := projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{Channel: "build"},
		}
		require.NoError(t, base.MergeUpdatesFrom(&other))
		assert.Equal(t, "build", base.Publish.Channel)
	})

	t.Run("non-zero other overrides non-zero base", func(t *testing.T) {
		base := projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{Channel: "build"},
		}
		other := projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{Channel: "base"},
		}
		require.NoError(t, base.MergeUpdatesFrom(&other))
		assert.Equal(t, "base", base.Publish.Channel)
	})

	t.Run("zero other does not override non-zero base", func(t *testing.T) {
		base := projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{Channel: "build"},
		}
		other := projectconfig.PackageConfig{}
		require.NoError(t, base.MergeUpdatesFrom(&other))
		assert.Equal(t, "build", base.Publish.Channel)
	})
}

func TestResolvePackageConfig(t *testing.T) {
	makeProj := func(groups map[string]projectconfig.PackageGroupConfig) *projectconfig.ProjectConfig {
		proj := projectconfig.NewProjectConfig()
		proj.PackageGroups = groups

		return &proj
	}

	baseProj := makeProj(map[string]projectconfig.PackageGroupConfig{
		"debug-packages": {
			PackagePatterns: []string{"*-debuginfo", "*-debugsource"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{Channel: "none"},
			},
		},
		"build-time-deps": {
			PackagePatterns: []string{"*-devel", "*-static"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{Channel: "build"},
			},
		},
	})

	testCases := []struct {
		name            string
		pkgName         string
		compDefault     projectconfig.PackageConfig
		compPackages    map[string]projectconfig.PackageConfig
		expectedChannel string
	}{
		{
			name:            "unmatched package returns zero channel",
			pkgName:         "curl",
			expectedChannel: "",
		},
		{
			name:            "devel package matched by group pattern",
			pkgName:         "curl-devel",
			expectedChannel: "build",
		},
		{
			name:            "debuginfo package matched by group pattern",
			pkgName:         "gcc-debuginfo",
			expectedChannel: "none",
		},
		{
			name:    "component default overrides group default",
			pkgName: "gcc-devel",
			compDefault: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{Channel: "base"},
			},
			expectedChannel: "base",
		},
		{
			name:    "component default applies when no group matches",
			pkgName: "curl",
			compDefault: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{Channel: "none"},
			},
			expectedChannel: "none",
		},
		{
			name:    "exact package override takes priority over group and component default",
			pkgName: "curl-devel",
			compDefault: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{Channel: "none"},
			},
			compPackages: map[string]projectconfig.PackageConfig{
				"curl-devel": {Publish: projectconfig.PackagePublishConfig{Channel: "base"}},
			},
			expectedChannel: "base",
		},
		{
			name:    "exact package override takes priority over group with no component default",
			pkgName: "curl-devel",
			compPackages: map[string]projectconfig.PackageConfig{
				"curl-devel": {Publish: projectconfig.PackagePublishConfig{Channel: "base"}},
			},
			expectedChannel: "base",
		},
		{
			name:    "non-matching exact package entry does not affect result",
			pkgName: "curl-devel",
			compPackages: map[string]projectconfig.PackageConfig{
				"curl": {Publish: projectconfig.PackagePublishConfig{Channel: "base"}},
			},
			expectedChannel: "build", // from group
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			comp := &projectconfig.ComponentConfig{
				Name:                 "test-component",
				DefaultPackageConfig: testCase.compDefault,
				Packages:             testCase.compPackages,
			}

			got, err := projectconfig.ResolvePackageConfig(testCase.pkgName, comp, baseProj)
			require.NoError(t, err)
			assert.Equal(t, testCase.expectedChannel, got.Publish.Channel)
		})
	}

	t.Run("groups applied in alphabetical order - later-named overrides earlier-named", func(t *testing.T) {
		// "zzz-group" is alphabetically later than "aaa-group", so its channel wins.
		proj := makeProj(map[string]projectconfig.PackageGroupConfig{
			"aaa-group": {
				PackagePatterns: []string{"*-devel"},
				DefaultPackageConfig: projectconfig.PackageConfig{
					Publish: projectconfig.PackagePublishConfig{Channel: "build"},
				},
			},
			"zzz-group": {
				PackagePatterns: []string{"curl-*"},
				DefaultPackageConfig: projectconfig.PackageConfig{
					Publish: projectconfig.PackagePublishConfig{Channel: "base"},
				},
			},
		})

		comp := &projectconfig.ComponentConfig{Name: "curl"}
		got, err := projectconfig.ResolvePackageConfig("curl-devel", comp, proj)
		require.NoError(t, err)
		assert.Equal(t, "base", got.Publish.Channel)
	})

	t.Run("empty project config returns zero-value PackageConfig", func(t *testing.T) {
		proj := projectconfig.NewProjectConfig()
		comp := &projectconfig.ComponentConfig{Name: "curl"}

		got, err := projectconfig.ResolvePackageConfig("curl", comp, &proj)
		require.NoError(t, err)
		assert.Empty(t, got.Publish.Channel)
	})

	t.Run("project default applies when no other config matches", func(t *testing.T) {
		proj := projectconfig.NewProjectConfig()
		proj.DefaultPackageConfig = projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{Channel: "base"},
		}

		comp := &projectconfig.ComponentConfig{Name: "curl"}
		got, err := projectconfig.ResolvePackageConfig("curl", comp, &proj)
		require.NoError(t, err)
		assert.Equal(t, "base", got.Publish.Channel)
	})

	t.Run("package group overrides project default", func(t *testing.T) {
		proj := projectconfig.NewProjectConfig()
		proj.DefaultPackageConfig = projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{Channel: "base"},
		}
		proj.PackageGroups = map[string]projectconfig.PackageGroupConfig{
			"debug-packages": {
				PackagePatterns: []string{"*-debuginfo"},
				DefaultPackageConfig: projectconfig.PackageConfig{
					Publish: projectconfig.PackagePublishConfig{Channel: "none"},
				},
			},
		}

		comp := &projectconfig.ComponentConfig{Name: "gcc"}
		got, err := projectconfig.ResolvePackageConfig("gcc-debuginfo", comp, &proj)
		require.NoError(t, err)
		assert.Equal(t, "none", got.Publish.Channel)
	})

	t.Run("component default overrides project default", func(t *testing.T) {
		proj := projectconfig.NewProjectConfig()
		proj.DefaultPackageConfig = projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{Channel: "base"},
		}

		comp := &projectconfig.ComponentConfig{
			Name: "build-id-helper",
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{Channel: "none"},
			},
		}

		got, err := projectconfig.ResolvePackageConfig("build-id-helper-tool", comp, &proj)
		require.NoError(t, err)
		assert.Equal(t, "none", got.Publish.Channel)
	})

	t.Run("per-package override takes priority over project default", func(t *testing.T) {
		proj := projectconfig.NewProjectConfig()
		proj.DefaultPackageConfig = projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{Channel: "base"},
		}

		comp := &projectconfig.ComponentConfig{
			Name: "curl",
			Packages: map[string]projectconfig.PackageConfig{
				"curl-devel": {Publish: projectconfig.PackagePublishConfig{Channel: "none"}},
			},
		}

		got, err := projectconfig.ResolvePackageConfig("curl-devel", comp, &proj)
		require.NoError(t, err)
		assert.Equal(t, "none", got.Publish.Channel)
	})
}
