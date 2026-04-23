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
			comp, distroDefaults, nil, nil,
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
			comp, distroDefaults, groups, []string{"core"},
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
			comp, distroDefaults, groups, []string{"beta", "alpha"},
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
			comp, distroDefaults, groups, []string{"core"},
		)
		require.NoError(t, err)
		assert.Equal(t, "comp-commit", resolved.Spec.UpstreamCommit)
	})

	t.Run("missing group errors", func(t *testing.T) {
		comp := projectconfig.ComponentConfig{Name: "curl"}

		_, err := projectconfig.ResolveComponentConfig(
			comp, distroDefaults, nil, []string{"nonexistent"},
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
			comp, distroDefaults, groups, []string{"core"},
		)
		require.NoError(t, err)
		assert.Equal(t, originalDefaults, distroDefaults, "distro defaults should not be mutated")
		assert.Empty(t, comp.Spec.UpstreamCommit, "component config should not be mutated")
	})
}
