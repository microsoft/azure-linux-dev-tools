// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagePublishConfig_Validate(t *testing.T) {
	t.Parallel()

	validCases := []struct {
		name    string
		channel string
	}{
		{name: "empty channel is valid (means inherit)", channel: ""},
		{name: "simple channel name", channel: "base"},
		{name: "channel with hyphens", channel: "rpm-base"},
		{name: "channel with underscores", channel: "rpm_base"},
		{name: "reserved none value", channel: "none"},
		{name: "channel starting with dot", channel: ".hidden"},
		{name: "channel with internal dots", channel: "foo..bar"},
	}

	for _, testCase := range validCases {
		t.Run("RPMChannel/"+testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := projectconfig.PackagePublishConfig{RPMChannel: testCase.channel}
			assert.NoError(t, validator.New().Struct(&cfg))
		})

		t.Run("DebugInfoChannel/"+testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := projectconfig.PackagePublishConfig{DebugInfoChannel: testCase.channel}
			assert.NoError(t, validator.New().Struct(&cfg))
		})
	}

	invalidCases := []struct {
		name        string
		channel     string
		errContains string
	}{
		{name: "absolute path", channel: "/etc/passwd", errContains: "excludesall"},
		{name: "traversal with slash", channel: "../secret", errContains: "excludesall"},
		{name: "multi-segment path", channel: "foo/bar", errContains: "excludesall"},
		{name: "backslash separator", channel: `foo\bar`, errContains: "excludesall"},
		{name: "dot traversal", channel: "..", errContains: "'ne'"},
		{name: "single dot", channel: ".", errContains: "'ne'"},
	}

	for _, testCase := range invalidCases {
		t.Run("RPMChannel/"+testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := projectconfig.PackagePublishConfig{RPMChannel: testCase.channel}
			err := validator.New().Struct(&cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.errContains)
		})

		t.Run("DebugInfoChannel/"+testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := projectconfig.PackagePublishConfig{DebugInfoChannel: testCase.channel}
			err := validator.New().Struct(&cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.errContains)
		})
	}
}

func TestPackageGroupConfig_Validate_InvalidChannel(t *testing.T) {
	t.Parallel()

	t.Run("traversal channel in group default config is rejected", func(t *testing.T) {
		t.Parallel()

		file := projectconfig.ConfigFile{
			PackageGroups: map[string]projectconfig.PackageGroupConfig{
				"test-group": {
					Packages: []string{"curl"},
					DefaultPackageConfig: projectconfig.PackageConfig{
						Publish: projectconfig.PackagePublishConfig{RPMChannel: "../escape"},
					},
				},
			},
		}
		err := file.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "RPMChannel")
		assert.Contains(t, err.Error(), "excludesall")
	})

	t.Run("traversal debuginfo channel in group default config is rejected", func(t *testing.T) {
		t.Parallel()

		file := projectconfig.ConfigFile{
			PackageGroups: map[string]projectconfig.PackageGroupConfig{
				"test-group": {
					Packages: []string{"curl-debuginfo"},
					DefaultPackageConfig: projectconfig.PackageConfig{
						Publish: projectconfig.PackagePublishConfig{DebugInfoChannel: "../escape"},
					},
				},
			},
		}
		err := file.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DebugInfoChannel")
		assert.Contains(t, err.Error(), "excludesall")
	})
}

func TestPackageGroupConfig_Validate(t *testing.T) {
	t.Run("empty group is valid", func(t *testing.T) {
		group := projectconfig.PackageGroupConfig{}
		assert.NoError(t, group.Validate())
	})

	t.Run("group with packages is valid", func(t *testing.T) {
		group := projectconfig.PackageGroupConfig{
			Description: "development packages",
			Packages:    []string{"curl-devel", "python3-requests", "curl"},
		}
		assert.NoError(t, group.Validate())
	})

	t.Run("empty package name is invalid", func(t *testing.T) {
		group := projectconfig.PackageGroupConfig{
			Packages: []string{"curl-devel", ""},
		}
		err := group.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "packages[1]")
		assert.Contains(t, err.Error(), "must not be empty")
	})

	t.Run("duplicate package name within group is invalid", func(t *testing.T) {
		group := projectconfig.PackageGroupConfig{
			Packages: []string{"curl-devel", "wget2-devel", "curl-devel"},
		}
		err := group.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "curl-devel")
		assert.Contains(t, err.Error(), "more than once")
	})
}

func TestPackageConfig_MergeUpdatesFrom(t *testing.T) {
	t.Run("non-zero other overrides zero base", func(t *testing.T) {
		base := projectconfig.PackageConfig{}
		other := projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{RPMChannel: "build"},
		}
		require.NoError(t, base.MergeUpdatesFrom(&other))
		assert.Equal(t, "build", base.Publish.RPMChannel)
	})

	t.Run("non-zero other overrides non-zero base", func(t *testing.T) {
		base := projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{RPMChannel: "build"},
		}
		other := projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{RPMChannel: "base"},
		}
		require.NoError(t, base.MergeUpdatesFrom(&other))
		assert.Equal(t, "base", base.Publish.RPMChannel)
	})

	t.Run("zero other does not override non-zero base", func(t *testing.T) {
		base := projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{RPMChannel: "build"},
		}
		other := projectconfig.PackageConfig{}
		require.NoError(t, base.MergeUpdatesFrom(&other))
		assert.Equal(t, "build", base.Publish.RPMChannel)
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
			Packages: []string{"gcc-debuginfo", "curl-debuginfo", "curl-debugsource"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "none"},
			},
		},
		"build-time-deps": {
			Packages: []string{"curl-devel", "curl-static", "gcc-devel"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "build"},
			},
		},
	})

	testCases := []struct {
		name            string
		pkgName         string
		compPackages    map[string]projectconfig.PackageConfig
		expectedChannel string
	}{
		{
			name:            "package not in any group returns zero channel",
			pkgName:         "curl",
			expectedChannel: "",
		},
		{
			name:            "package listed in build-time-deps group gets build channel",
			pkgName:         "curl-devel",
			expectedChannel: "build",
		},
		{
			name:            "package listed in debug-packages group gets none channel",
			pkgName:         "gcc-debuginfo",
			expectedChannel: "none",
		},
		{
			name:    "exact package override takes priority over group",
			pkgName: "curl-devel",
			compPackages: map[string]projectconfig.PackageConfig{
				"curl-devel": {Publish: projectconfig.PackagePublishConfig{RPMChannel: "base"}},
			},
			expectedChannel: "base",
		},
		{
			name:    "exact package override with no group match",
			pkgName: "curl",
			compPackages: map[string]projectconfig.PackageConfig{
				"curl": {Publish: projectconfig.PackagePublishConfig{RPMChannel: "base"}},
			},
			expectedChannel: "base",
		},
		{
			name:    "unrelated exact package entry does not affect result",
			pkgName: "curl-devel",
			compPackages: map[string]projectconfig.PackageConfig{
				"curl": {Publish: projectconfig.PackagePublishConfig{RPMChannel: "base"}},
			},
			expectedChannel: "build", // from group
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			comp := &projectconfig.ComponentConfig{
				Name:     "test-component",
				Packages: testCase.compPackages,
			}

			got, err := projectconfig.ResolvePackageConfig(testCase.pkgName, comp, baseProj)
			require.NoError(t, err)
			assert.Equal(t, testCase.expectedChannel, got.Publish.RPMChannel)
		})
	}

	t.Run("package group default-package-config is applied", func(t *testing.T) {
		proj := makeProj(map[string]projectconfig.PackageGroupConfig{
			"my-group": {
				Packages: []string{"curl-devel"},
				DefaultPackageConfig: projectconfig.PackageConfig{
					Publish: projectconfig.PackagePublishConfig{RPMChannel: "build"},
				},
			},
		})

		comp := &projectconfig.ComponentConfig{Name: "curl"}
		got, err := projectconfig.ResolvePackageConfig("curl-devel", comp, proj)
		require.NoError(t, err)
		assert.Equal(t, "build", got.Publish.RPMChannel)
	})

	t.Run("empty project config returns zero-value PackageConfig", func(t *testing.T) {
		proj := projectconfig.NewProjectConfig()
		comp := &projectconfig.ComponentConfig{Name: "curl"}

		got, err := projectconfig.ResolvePackageConfig("curl", comp, &proj)
		require.NoError(t, err)
		assert.Empty(t, got.Publish.RPMChannel)
	})

	t.Run("project default applies when no other config matches", func(t *testing.T) {
		proj := projectconfig.NewProjectConfig()
		proj.DefaultPackageConfig = projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{RPMChannel: "base"},
		}

		comp := &projectconfig.ComponentConfig{Name: "curl"}
		got, err := projectconfig.ResolvePackageConfig("curl", comp, &proj)
		require.NoError(t, err)
		assert.Equal(t, "base", got.Publish.RPMChannel)
	})

	t.Run("package group overrides project default", func(t *testing.T) {
		proj := projectconfig.NewProjectConfig()
		proj.DefaultPackageConfig = projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{RPMChannel: "base"},
		}
		proj.PackageGroups = map[string]projectconfig.PackageGroupConfig{
			"debug-packages": {
				Packages: []string{"gcc-debuginfo"},
				DefaultPackageConfig: projectconfig.PackageConfig{
					Publish: projectconfig.PackagePublishConfig{RPMChannel: "none"},
				},
			},
		}

		comp := &projectconfig.ComponentConfig{Name: "gcc"}
		got, err := projectconfig.ResolvePackageConfig("gcc-debuginfo", comp, &proj)
		require.NoError(t, err)
		assert.Equal(t, "none", got.Publish.RPMChannel)
	})

	t.Run("per-package override takes priority over project default", func(t *testing.T) {
		proj := projectconfig.NewProjectConfig()
		proj.DefaultPackageConfig = projectconfig.PackageConfig{
			Publish: projectconfig.PackagePublishConfig{RPMChannel: "base"},
		}

		comp := &projectconfig.ComponentConfig{
			Name: "curl",
			Packages: map[string]projectconfig.PackageConfig{
				"curl-devel": {Publish: projectconfig.PackagePublishConfig{RPMChannel: "none"}},
			},
		}

		got, err := projectconfig.ResolvePackageConfig("curl-devel", comp, &proj)
		require.NoError(t, err)
		assert.Equal(t, "none", got.Publish.RPMChannel)
	})
}
