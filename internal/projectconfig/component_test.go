// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComponentGroupConfigWithAbsolutePaths(t *testing.T) {
	const testRefDir = "/ref/dir"

	t.Run("empty", func(t *testing.T) {
		comp := projectconfig.ComponentGroupConfig{}
		absComp := comp.WithAbsolutePaths(testRefDir)

		require.Equal(t, comp, absComp)
	})

	t.Run("relative paths", func(t *testing.T) {
		comp := projectconfig.ComponentGroupConfig{
			SpecPathPatterns: []string{"dir/**/*.spec"},
		}

		absComp := comp.WithAbsolutePaths(testRefDir)

		assert.NotEqual(t, comp, absComp)
		assert.Equal(t, []string{"/ref/dir/dir/**/*.spec"}, absComp.SpecPathPatterns)
	})
}

func TestComponentConfigWithAbsolutePaths(t *testing.T) {
	const testRefDir = "/ref/dir"

	t.Run("empty", func(t *testing.T) {
		comp := projectconfig.ComponentConfig{}
		absComp := *comp.WithAbsolutePaths(testRefDir)

		require.Equal(t, comp, absComp)
	})

	t.Run("project file ptr", func(t *testing.T) {
		comp := projectconfig.ComponentConfig{
			SourceConfigFile: &projectconfig.ConfigFile{},
		}

		absComp := comp.WithAbsolutePaths(testRefDir)

		// We *require* that the SourceConfigFile pointer is aliased. Deep-copying it would
		// be cost-prohibitive and unnecessary.
		require.Equal(t, comp.SourceConfigFile, absComp.SourceConfigFile)
	})

	t.Run("relative paths", func(t *testing.T) {
		comp := projectconfig.ComponentConfig{
			Name: "test",
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeLocal,
				Path:       "file.spec",
			},
		}

		absComp := *comp.WithAbsolutePaths(testRefDir)

		assert.Equal(t, comp.Name, absComp.Name)
		assert.Equal(t, comp.Spec.SourceType, absComp.Spec.SourceType)
		assert.Equal(t, filepath.Join(testRefDir, comp.Spec.Path), absComp.Spec.Path)
	})

	t.Run("absolute paths", func(t *testing.T) {
		comp := projectconfig.ComponentConfig{
			Name: "test",
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeLocal,
				Path:       "/some/file.spec",
			},
		}

		absComp := *comp.WithAbsolutePaths(testRefDir)

		require.Equal(t, comp, absComp)
	})

	t.Run("overlays", func(t *testing.T) {
		comp := projectconfig.ComponentConfig{
			Name: "test",
			Overlays: []projectconfig.ComponentOverlay{
				{
					Type:   projectconfig.ComponentOverlayAddFile,
					Source: "somefile.txt",
				},
			},
		}

		absComp := *comp.WithAbsolutePaths(testRefDir)

		require.Equal(t, comp.Name, absComp.Name)
		require.Len(t, absComp.Overlays, 1)
		require.Equal(t, comp.Overlays[0].Type, absComp.Overlays[0].Type)
		require.Equal(t, filepath.Join(testRefDir, comp.Overlays[0].Source), absComp.Overlays[0].Source)
	})
}

func TestComponentGroupConfigWithAbsolutePaths_DefaultComponentConfig(t *testing.T) {
	const testRefDir = "/ref/dir"

	t.Run("default config with relative spec path", func(t *testing.T) {
		group := projectconfig.ComponentGroupConfig{
			Components: []string{"comp-a"},
			DefaultComponentConfig: projectconfig.ComponentConfig{
				Spec: projectconfig.SpecSource{
					SourceType: projectconfig.SpecSourceTypeLocal,
					Path:       "specs/test.spec",
				},
			},
		}

		absGroup := group.WithAbsolutePaths(testRefDir)

		// The default component config's spec path should be made absolute.
		assert.Equal(t, "/ref/dir/specs/test.spec", absGroup.DefaultComponentConfig.Spec.Path)

		// Members should be preserved.
		assert.Equal(t, []string{"comp-a"}, absGroup.Components)
	})

	t.Run("default config with empty fields", func(t *testing.T) {
		group := projectconfig.ComponentGroupConfig{
			Components:             []string{"comp-a"},
			DefaultComponentConfig: projectconfig.ComponentConfig{},
		}

		absGroup := group.WithAbsolutePaths(testRefDir)

		// Empty default config should remain empty.
		assert.Equal(t, projectconfig.ComponentConfig{}, absGroup.DefaultComponentConfig)
	})

	t.Run("default config with build settings", func(t *testing.T) {
		group := projectconfig.ComponentGroupConfig{
			DefaultComponentConfig: projectconfig.ComponentConfig{
				Build: projectconfig.ComponentBuildConfig{
					With:    []string{"tests"},
					Without: []string{"docs"},
				},
			},
		}

		absGroup := group.WithAbsolutePaths(testRefDir)

		// Build config should be preserved as-is (no paths to fix).
		assert.Equal(t, []string{"tests"}, absGroup.DefaultComponentConfig.Build.With)
		assert.Equal(t, []string{"docs"}, absGroup.DefaultComponentConfig.Build.Without)
	})
}

func TestMergeComponentUpdates(t *testing.T) {
	base := projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{
			Without: []string{"x", "y"},
		},
	}

	updates := projectconfig.ComponentConfig{
		Name: "test",
		Build: projectconfig.ComponentBuildConfig{
			Without: []string{"w"},
		},
	}

	err := base.MergeUpdatesFrom(&updates)
	require.NoError(t, err)
	require.Equal(t, "test", base.Name)
	require.Equal(t, []string{"x", "y", "w"}, base.Build.Without)
}

func TestAllowedSourceFilesHashTypes_MatchesJSONSchemaEnum(t *testing.T) {
	// Extract enum values from the jsonschema tag on
	// [projectconfig.SourceFileReference.HashType].
	field, ok := reflect.TypeOf(projectconfig.SourceFileReference{}).FieldByName("HashType")
	require.True(t, ok, "SourceFileReference must have a 'HashType' field")

	tag := field.Tag.Get("jsonschema")
	require.NotEmpty(t, tag, "HashType field must have a 'jsonschema' tag")

	// Parse "enum=X,enum=Y,..." entries from the tag.
	var schemaEnums []string

	for _, part := range strings.Split(tag, ",") {
		if strings.HasPrefix(part, "enum=") {
			schemaEnums = append(schemaEnums, strings.TrimPrefix(part, "enum="))
		}
	}

	assert.Len(t, schemaEnums, len(projectconfig.AllowedSourceFilesHashTypes),
		"number of 'enum=' entries in 'jsonschema' tag must match number of entries in 'AllowedSourceFilesHashTypes'")

	// Every enum value must be present in AllowedSourceFilesHashTypes.
	for _, enumVal := range schemaEnums {
		hashType := fileutils.HashType(enumVal)
		assert.True(t, projectconfig.AllowedSourceFilesHashTypes[hashType],
			"'jsonschema' enum value %#q is not in 'AllowedSourceFilesHashTypes'", enumVal)
	}
}

func TestReleaseCalculationValidation(t *testing.T) {
	validate := validator.New()

	// Empty (omitted) is valid — resolved to "auto" by the component resolver.
	require.NoError(t, validate.Struct(&projectconfig.ReleaseConfig{}))

	// Explicit "auto" is valid.
	require.NoError(t, validate.Struct(&projectconfig.ReleaseConfig{
		Calculation: projectconfig.ReleaseCalculationAuto,
	}))

	// Explicit "manual" is valid.
	require.NoError(t, validate.Struct(&projectconfig.ReleaseConfig{
		Calculation: projectconfig.ReleaseCalculationManual,
	}))

	// Invalid value is rejected.
	require.Error(t, validate.Struct(&projectconfig.ReleaseConfig{
		Calculation: "manaul",
	}))
}

func TestResolveComponentConfig(t *testing.T) {
	distroDefaults := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeUpstream,
		},
	}

	t.Run("no groups", func(t *testing.T) {
		comp := projectconfig.ComponentConfig{Name: "curl"}

		resolved, err := projectconfig.ResolveComponentConfig(
			comp, projectconfig.ComponentConfig{}, distroDefaults, nil, nil,
		)
		require.NoError(t, err)
		assert.Equal(t, "curl", resolved.Name)
		assert.Equal(t, projectconfig.SpecSourceTypeUpstream, resolved.Spec.SourceType)
	})

	t.Run("single group", func(t *testing.T) {
		groups := map[string]projectconfig.ComponentGroupConfig{
			"core": {
				DefaultComponentConfig: projectconfig.ComponentConfig{
					Spec: projectconfig.SpecSource{
						UpstreamCommit: "group-commit",
					},
				},
			},
		}
		comp := projectconfig.ComponentConfig{Name: "curl"}

		resolved, err := projectconfig.ResolveComponentConfig(
			comp, projectconfig.ComponentConfig{}, distroDefaults, groups, []string{"core"},
		)
		require.NoError(t, err)
		assert.Equal(t, "group-commit", resolved.Spec.UpstreamCommit)
		assert.Equal(t, projectconfig.SpecSourceTypeUpstream, resolved.Spec.SourceType)
	})

	t.Run("multi group sorted order", func(t *testing.T) {
		groups := map[string]projectconfig.ComponentGroupConfig{
			"alpha": {
				DefaultComponentConfig: projectconfig.ComponentConfig{
					Spec: projectconfig.SpecSource{UpstreamCommit: "alpha-commit"},
				},
			},
			"beta": {
				DefaultComponentConfig: projectconfig.ComponentConfig{
					Spec: projectconfig.SpecSource{UpstreamCommit: "beta-commit"},
				},
			},
		}
		comp := projectconfig.ComponentConfig{Name: "curl"}

		// Groups applied in sorted order: alpha then beta. beta wins.
		resolved, err := projectconfig.ResolveComponentConfig(
			comp, projectconfig.ComponentConfig{}, distroDefaults, groups, []string{"beta", "alpha"},
		)
		require.NoError(t, err)
		assert.Equal(t, "beta-commit", resolved.Spec.UpstreamCommit)
	})

	t.Run("component overrides group", func(t *testing.T) {
		groups := map[string]projectconfig.ComponentGroupConfig{
			"core": {
				DefaultComponentConfig: projectconfig.ComponentConfig{
					Spec: projectconfig.SpecSource{UpstreamCommit: "group-commit"},
				},
			},
		}
		comp := projectconfig.ComponentConfig{
			Name: "curl",
			Spec: projectconfig.SpecSource{UpstreamCommit: "comp-commit"},
		}

		resolved, err := projectconfig.ResolveComponentConfig(
			comp, projectconfig.ComponentConfig{}, distroDefaults, groups, []string{"core"},
		)
		require.NoError(t, err)
		assert.Equal(t, "comp-commit", resolved.Spec.UpstreamCommit)
	})

	t.Run("missing group errors", func(t *testing.T) {
		comp := projectconfig.ComponentConfig{Name: "curl"}

		_, err := projectconfig.ResolveComponentConfig(
			comp, projectconfig.ComponentConfig{}, distroDefaults, nil, []string{"nonexistent"},
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "component group not found")
	})

	t.Run("does not mutate inputs", func(t *testing.T) {
		groups := map[string]projectconfig.ComponentGroupConfig{
			"core": {
				DefaultComponentConfig: projectconfig.ComponentConfig{
					Spec: projectconfig.SpecSource{UpstreamCommit: "group-commit"},
				},
			},
		}
		comp := projectconfig.ComponentConfig{Name: "curl"}
		originalDefaults := distroDefaults

		_, err := projectconfig.ResolveComponentConfig(
			comp, projectconfig.ComponentConfig{}, distroDefaults, groups, []string{"core"},
		)
		require.NoError(t, err)
		assert.Equal(t, originalDefaults, distroDefaults, "distro defaults should not be mutated")
		assert.Empty(t, comp.Spec.UpstreamCommit, "component config should not be mutated")
	})
}

func TestComponentPublishConfig_Validate(t *testing.T) {
	t.Parallel()

	validCases := []struct {
		name    string
		channel string
	}{
		{name: "empty channel is valid (means inherit)", channel: ""},
		{name: "simple channel name", channel: "rpms-base"},
		{name: "channel with hyphens", channel: "rpms-sdk-srpm"},
		{name: "channel with underscores", channel: "rpms_base"},
		{name: "reserved none value", channel: "none"},
	}

	for _, testCase := range validCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := projectconfig.ComponentPublishConfig{
				RPMChannel:       testCase.channel,
				SRPMChannel:      testCase.channel,
				DebugInfoChannel: testCase.channel,
			}
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
		{name: "backslash separator", channel: `foo\bar`, errContains: "excludesall"},
		{name: "dot traversal", channel: "..", errContains: "'ne'"},
		{name: "single dot", channel: ".", errContains: "'ne'"},
	}

	for _, testCase := range invalidCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := projectconfig.ComponentPublishConfig{RPMChannel: testCase.channel}
			err := validator.New().Struct(&cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.errContains)
		})
	}
}

func TestIsDebugInfoPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pkgName  string
		expected bool
	}{
		{"simple debuginfo suffix", "curl-debuginfo", true},
		{"simple debugsource suffix", "curl-debugsource", true},
		{"debuginfo with arch suffix", "kernel-debuginfo-common-x86_64", true},
		{"debugsource with extra segments", "glibc-debugsource-common", true},
		{"debuginfod is not debuginfo", "elfutils-debuginfod", false},
		{"debuginfod client is not debuginfo", "elfutils-debuginfod-client", false},
		{"debuginfod followed by debuginfo", "foo-debuginfod-debuginfo", true},
		{"plain binary package", "curl", false},
		{"package ending in info", "texinfo", false},
		{"package with debug prefix", "debug-tools", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, projectconfig.IsDebugInfoPackage(tt.pkgName))
		})
	}
}

func TestResolveComponentConfig_ProjectDefaults(t *testing.T) {
	t.Parallel()

	t.Run("empty project and component returns zero-value config", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		comp := projectconfig.ComponentConfig{Name: "curl"}

		got, err := projectconfig.ResolveComponentConfig(
			comp, proj.DefaultComponentConfig, projectconfig.ComponentConfig{},
			proj.ComponentGroups, proj.GroupsByComponent[comp.Name],
		)
		require.NoError(t, err)
		assert.Empty(t, got.Publish.RPMChannel)
		assert.Empty(t, got.Publish.SRPMChannel)
		assert.Empty(t, got.Publish.DebugInfoChannel)
	})

	t.Run("project default applies when no other config matches", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel:       "rpms-sdk",
				SRPMChannel:      "rpms-sdk-srpm",
				DebugInfoChannel: "rpms-sdk-debuginfo",
			},
		}
		comp := projectconfig.ComponentConfig{Name: "curl"}

		got, err := projectconfig.ResolveComponentConfig(
			comp, proj.DefaultComponentConfig, projectconfig.ComponentConfig{},
			proj.ComponentGroups, proj.GroupsByComponent[comp.Name],
		)
		require.NoError(t, err)
		assert.Equal(t, "rpms-sdk", got.Publish.RPMChannel)
		assert.Equal(t, "rpms-sdk-srpm", got.Publish.SRPMChannel)
		assert.Equal(t, "rpms-sdk-debuginfo", got.Publish.DebugInfoChannel)
	})

	t.Run("component group overrides project default", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel:       "rpms-sdk",
				SRPMChannel:      "rpms-sdk-srpm",
				DebugInfoChannel: "rpms-sdk-debuginfo",
			},
		}
		proj.ComponentGroups = map[string]projectconfig.ComponentGroupConfig{
			"base-published": {
				Components: []string{"systemd", "bash"},
				DefaultComponentConfig: projectconfig.ComponentConfig{
					Publish: projectconfig.ComponentPublishConfig{
						RPMChannel:       "rpms-base",
						SRPMChannel:      "rpms-base-srpm",
						DebugInfoChannel: "rpms-base-debuginfo",
					},
				},
			},
		}
		proj.GroupsByComponent = map[string][]string{
			"systemd": {"base-published"},
			"bash":    {"base-published"},
		}

		comp := projectconfig.ComponentConfig{Name: "systemd"}
		got, err := projectconfig.ResolveComponentConfig(
			comp, proj.DefaultComponentConfig, projectconfig.ComponentConfig{},
			proj.ComponentGroups, proj.GroupsByComponent[comp.Name],
		)
		require.NoError(t, err)
		assert.Equal(t, "rpms-base", got.Publish.RPMChannel)
		assert.Equal(t, "rpms-base-srpm", got.Publish.SRPMChannel)
		assert.Equal(t, "rpms-base-debuginfo", got.Publish.DebugInfoChannel)
	})

	t.Run("component not in group inherits project default", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel:  "rpms-sdk",
				SRPMChannel: "rpms-sdk-srpm",
			},
		}
		proj.ComponentGroups = map[string]projectconfig.ComponentGroupConfig{
			"base-published": {
				Components: []string{"systemd"},
				DefaultComponentConfig: projectconfig.ComponentConfig{
					Publish: projectconfig.ComponentPublishConfig{
						RPMChannel: "rpms-base",
					},
				},
			},
		}
		proj.GroupsByComponent = map[string][]string{
			"systemd": {"base-published"},
		}

		comp := projectconfig.ComponentConfig{Name: "curl"}
		got, err := projectconfig.ResolveComponentConfig(
			comp, proj.DefaultComponentConfig, projectconfig.ComponentConfig{},
			proj.ComponentGroups, proj.GroupsByComponent[comp.Name],
		)
		require.NoError(t, err)
		assert.Equal(t, "rpms-sdk", got.Publish.RPMChannel)
		assert.Equal(t, "rpms-sdk-srpm", got.Publish.SRPMChannel)
	})

	t.Run("component own publish config overrides all defaults", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel:       "rpms-sdk",
				SRPMChannel:      "rpms-sdk-srpm",
				DebugInfoChannel: "rpms-sdk-debuginfo",
			},
		}
		comp := projectconfig.ComponentConfig{
			Name: "special-comp",
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel: "rpms-custom",
			},
		}

		got, err := projectconfig.ResolveComponentConfig(
			comp, proj.DefaultComponentConfig, projectconfig.ComponentConfig{},
			proj.ComponentGroups, proj.GroupsByComponent[comp.Name],
		)
		require.NoError(t, err)
		assert.Equal(t, "rpms-custom", got.Publish.RPMChannel)
		assert.Equal(t, "rpms-sdk-srpm", got.Publish.SRPMChannel)
		assert.Equal(t, "rpms-sdk-debuginfo", got.Publish.DebugInfoChannel)
	})

	t.Run("partial group override preserves other fields from project default", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel:       "rpms-sdk",
				SRPMChannel:      "rpms-sdk-srpm",
				DebugInfoChannel: "rpms-sdk-debuginfo",
			},
		}
		proj.ComponentGroups = map[string]projectconfig.ComponentGroupConfig{
			"base-published": {
				Components: []string{"bash"},
				DefaultComponentConfig: projectconfig.ComponentConfig{
					Publish: projectconfig.ComponentPublishConfig{
						RPMChannel: "rpms-base",
						// SRPMChannel and DebugInfoChannel not set — should inherit.
					},
				},
			},
		}
		proj.GroupsByComponent = map[string][]string{
			"bash": {"base-published"},
		}

		comp := projectconfig.ComponentConfig{Name: "bash"}
		got, err := projectconfig.ResolveComponentConfig(
			comp, proj.DefaultComponentConfig, projectconfig.ComponentConfig{},
			proj.ComponentGroups, proj.GroupsByComponent[comp.Name],
		)
		require.NoError(t, err)
		assert.Equal(t, "rpms-base", got.Publish.RPMChannel)
		assert.Equal(t, "rpms-sdk-srpm", got.Publish.SRPMChannel)
		assert.Equal(t, "rpms-sdk-debuginfo", got.Publish.DebugInfoChannel)
	})

	t.Run("build config is also inherited from defaults", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Build: projectconfig.ComponentBuildConfig{
				Without: []string{"docs"},
			},
		}

		comp := projectconfig.ComponentConfig{Name: "curl"}
		got, err := projectconfig.ResolveComponentConfig(
			comp, proj.DefaultComponentConfig, projectconfig.ComponentConfig{},
			proj.ComponentGroups, proj.GroupsByComponent[comp.Name],
		)
		require.NoError(t, err)
		assert.Equal(t, []string{"docs"}, got.Build.Without)
	})
}

func TestResolvePackagePublishChannel(t *testing.T) {
	t.Parallel()

	t.Run("binary package gets binary channel from resolved component", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel:       "rpms-sdk",
				DebugInfoChannel: "rpms-sdk-debuginfo",
			},
		}

		resolved, err := projectconfig.ResolveComponentConfig(
			projectconfig.ComponentConfig{Name: "curl"},
			proj.DefaultComponentConfig,
			projectconfig.ComponentConfig{},
			proj.ComponentGroups,
			proj.GroupsByComponent["curl"],
		)
		require.NoError(t, err)

		channel, err := projectconfig.ResolvePackagePublishChannel("curl", &resolved, &proj)
		require.NoError(t, err)
		assert.Equal(t, "rpms-sdk", channel)
	})

	t.Run("debuginfo package gets debuginfo channel from resolved component", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel:       "rpms-sdk",
				DebugInfoChannel: "rpms-sdk-debuginfo",
			},
		}

		resolved, err := projectconfig.ResolveComponentConfig(
			projectconfig.ComponentConfig{Name: "curl"},
			proj.DefaultComponentConfig,
			projectconfig.ComponentConfig{},
			proj.ComponentGroups,
			proj.GroupsByComponent["curl"],
		)
		require.NoError(t, err)

		channel, err := projectconfig.ResolvePackagePublishChannel("curl-debuginfo", &resolved, &proj)
		require.NoError(t, err)
		assert.Equal(t, "rpms-sdk-debuginfo", channel)
	})

	t.Run("no config returns empty channel", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()

		resolved, err := projectconfig.ResolveComponentConfig(
			projectconfig.ComponentConfig{Name: "curl"},
			proj.DefaultComponentConfig,
			projectconfig.ComponentConfig{},
			proj.ComponentGroups,
			proj.GroupsByComponent["curl"],
		)
		require.NoError(t, err)

		channel, err := projectconfig.ResolvePackagePublishChannel("curl", &resolved, &proj)
		require.NoError(t, err)
		assert.Empty(t, channel)
	})

	t.Run("debuginfo with arch suffix uses debuginfo channel", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel:       "rpms-base",
				DebugInfoChannel: "rpms-base-debuginfo",
			},
		}

		resolved, err := projectconfig.ResolveComponentConfig(
			projectconfig.ComponentConfig{Name: "kernel"},
			proj.DefaultComponentConfig,
			projectconfig.ComponentConfig{},
			proj.ComponentGroups,
			proj.GroupsByComponent["kernel"],
		)
		require.NoError(t, err)

		// kernel-debuginfo-common-x86_64 has "-debuginfo" as a middle segment, not a suffix.
		channel, err := projectconfig.ResolvePackagePublishChannel(
			"kernel-debuginfo-common-x86_64", &resolved, &proj,
		)
		require.NoError(t, err)
		assert.Equal(t, "rpms-base-debuginfo", channel)
	})
}

func TestResolvePackagePublishChannel_PackageGroupOverrides(t *testing.T) {
	t.Parallel()

	t.Run("package group overrides component binary channel", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel:       "rpms-base",
				SRPMChannel:      "rpms-base-srpm",
				DebugInfoChannel: "rpms-base-debuginfo",
			},
		}
		proj.ComponentGroups = map[string]projectconfig.ComponentGroupConfig{
			"base-published": {
				Components: []string{"cmake"},
				DefaultComponentConfig: projectconfig.ComponentConfig{
					Publish: projectconfig.ComponentPublishConfig{
						RPMChannel:       "rpms-base",
						DebugInfoChannel: "rpms-base-debuginfo",
					},
				},
			},
		}
		proj.GroupsByComponent = map[string][]string{
			"cmake": {"base-published"},
		}
		proj.PackageGroups = map[string]projectconfig.PackageGroupConfig{
			"sdk-published": {
				Packages: []string{"cmake-gui"},
				DefaultPackageConfig: projectconfig.PackageConfig{
					Publish: projectconfig.PackagePublishConfig{
						RPMChannel:       "rpms-sdk",
						DebugInfoChannel: "rpms-sdk-debuginfo",
					},
				},
			},
		}

		resolved, err := projectconfig.ResolveComponentConfig(
			projectconfig.ComponentConfig{Name: "cmake"},
			proj.DefaultComponentConfig,
			projectconfig.ComponentConfig{},
			proj.ComponentGroups,
			proj.GroupsByComponent["cmake"],
		)
		require.NoError(t, err)

		// cmake-gui is in sdk-published package group — should get sdk channels.
		channel, err := projectconfig.ResolvePackagePublishChannel("cmake-gui", &resolved, &proj)
		require.NoError(t, err)
		assert.Equal(t, "rpms-sdk", channel)

		// cmake (not in package group) should get base channels from component group.
		channel, err = projectconfig.ResolvePackagePublishChannel("cmake", &resolved, &proj)
		require.NoError(t, err)
		assert.Equal(t, "rpms-base", channel)
	})

	t.Run("package group overrides component debuginfo channel", func(t *testing.T) {
		t.Parallel()

		proj := projectconfig.NewProjectConfig()
		proj.DefaultComponentConfig = projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				DebugInfoChannel: "rpms-base-debuginfo",
			},
		}
		proj.PackageGroups = map[string]projectconfig.PackageGroupConfig{
			"sdk-debug": {
				Packages: []string{"cmake-gui-debuginfo"},
				DefaultPackageConfig: projectconfig.PackageConfig{
					Publish: projectconfig.PackagePublishConfig{
						DebugInfoChannel: "rpms-sdk-debuginfo",
					},
				},
			},
		}

		resolved, err := projectconfig.ResolveComponentConfig(
			projectconfig.ComponentConfig{Name: "cmake"},
			proj.DefaultComponentConfig,
			projectconfig.ComponentConfig{},
			proj.ComponentGroups,
			proj.GroupsByComponent["cmake"],
		)
		require.NoError(t, err)

		channel, err := projectconfig.ResolvePackagePublishChannel("cmake-gui-debuginfo", &resolved, &proj)
		require.NoError(t, err)
		assert.Equal(t, "rpms-sdk-debuginfo", channel)
	})
}

func TestResolvePackagePublishChannel_FullScenario(t *testing.T) {
	t.Parallel()

	// Reproduce the user's TOML config:
	// Project default: everything goes to SDK channels
	// Component group "base-published": upgrades some components to base channels
	// Package group "sdk-published": overrides specific sub-packages back to SDK
	proj := projectconfig.NewProjectConfig()
	proj.DefaultComponentConfig = projectconfig.ComponentConfig{
		Publish: projectconfig.ComponentPublishConfig{
			RPMChannel:       "rpms-sdk",
			SRPMChannel:      "rpms-sdk-srpm",
			DebugInfoChannel: "rpms-sdk-debuginfo",
		},
	}
	proj.ComponentGroups = map[string]projectconfig.ComponentGroupConfig{
		"base-published": {
			Components: []string{"systemd", "bash", "cmake"},
			DefaultComponentConfig: projectconfig.ComponentConfig{
				Publish: projectconfig.ComponentPublishConfig{
					RPMChannel:       "rpms-base",
					SRPMChannel:      "rpms-base-srpm",
					DebugInfoChannel: "rpms-base-debuginfo",
				},
			},
		},
	}
	proj.GroupsByComponent = map[string][]string{
		"systemd": {"base-published"},
		"bash":    {"base-published"},
		"cmake":   {"base-published"},
	}
	proj.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"sdk-published": {
			Packages: []string{"cmake-gui"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{
					RPMChannel:       "rpms-sdk",
					DebugInfoChannel: "rpms-sdk-debuginfo",
				},
			},
		},
	}

	// systemd is in base-published → SRPM channel comes from resolved Publish.
	systemdResolved, err := projectconfig.ResolveComponentConfig(
		projectconfig.ComponentConfig{Name: "systemd"},
		proj.DefaultComponentConfig,
		projectconfig.ComponentConfig{},
		proj.ComponentGroups,
		proj.GroupsByComponent["systemd"],
	)
	require.NoError(t, err)
	assert.Equal(t, "rpms-base-srpm", systemdResolved.Publish.SRPMChannel)

	// systemd binary → rpms-base (from component group).
	channel, err := projectconfig.ResolvePackagePublishChannel("systemd", &systemdResolved, &proj)
	require.NoError(t, err)
	assert.Equal(t, "rpms-base", channel)

	// systemd-debuginfo → rpms-base-debuginfo (from component group).
	channel, err = projectconfig.ResolvePackagePublishChannel("systemd-debuginfo", &systemdResolved, &proj)
	require.NoError(t, err)
	assert.Equal(t, "rpms-base-debuginfo", channel)

	// cmake-gui (in sdk-published package group) → rpms-sdk (overrides base).
	cmakeResolved, err := projectconfig.ResolveComponentConfig(
		projectconfig.ComponentConfig{Name: "cmake"},
		proj.DefaultComponentConfig,
		projectconfig.ComponentConfig{},
		proj.ComponentGroups,
		proj.GroupsByComponent["cmake"],
	)
	require.NoError(t, err)

	channel, err = projectconfig.ResolvePackagePublishChannel("cmake-gui", &cmakeResolved, &proj)
	require.NoError(t, err)
	assert.Equal(t, "rpms-sdk", channel)

	// cmake SRPM → rpms-base-srpm (from component group).
	assert.Equal(t, "rpms-base-srpm", cmakeResolved.Publish.SRPMChannel)

	// cmake (main binary, not in package group) → rpms-base.
	channel, err = projectconfig.ResolvePackagePublishChannel("cmake", &cmakeResolved, &proj)
	require.NoError(t, err)
	assert.Equal(t, "rpms-base", channel)

	// A component NOT in any group (e.g., "python3") → SDK defaults.
	pythonResolved, err := projectconfig.ResolveComponentConfig(
		projectconfig.ComponentConfig{Name: "python3"},
		proj.DefaultComponentConfig,
		projectconfig.ComponentConfig{},
		proj.ComponentGroups,
		proj.GroupsByComponent["python3"],
	)
	require.NoError(t, err)

	channel, err = projectconfig.ResolvePackagePublishChannel("python3", &pythonResolved, &proj)
	require.NoError(t, err)
	assert.Equal(t, "rpms-sdk", channel)

	assert.Equal(t, "rpms-sdk-srpm", pythonResolved.Publish.SRPMChannel)
}
