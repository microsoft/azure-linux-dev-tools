// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test private functions
package projectconfig

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandComponentTemplate_SingleAxis(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		DefaultComponentConfig: ComponentConfig{
			Build: ComponentBuildConfig{
				Defines: map[string]string{"base": "true"},
			},
		},
		Matrix: []MatrixAxis{
			{
				Axis: "kernel",
				Values: map[string]ComponentConfig{
					"6-6": {
						Build: ComponentBuildConfig{
							Defines: map[string]string{"kernel_version": "6.6.72"},
						},
					},
					"6-12": {
						Build: ComponentBuildConfig{
							Defines: map[string]string{"kernel_version": "6.12.8"},
						},
					},
				},
			},
		},
	}

	result, err := expandComponentTemplate("my-driver", &tmpl)
	require.NoError(t, err)
	assert.Len(t, result, 2)

	// Check both components exist with expected names.
	assert.Contains(t, result, "my-driver-6-12")
	assert.Contains(t, result, "my-driver-6-6")

	// Verify config layering: base + axis value.
	comp66 := result["my-driver-6-6"]
	assert.Equal(t, "my-driver-6-6", comp66.Name)
	assert.Equal(t, "true", comp66.Build.Defines["base"])
	assert.Equal(t, "6.6.72", comp66.Build.Defines["kernel_version"])

	comp612 := result["my-driver-6-12"]
	assert.Equal(t, "my-driver-6-12", comp612.Name)
	assert.Equal(t, "true", comp612.Build.Defines["base"])
	assert.Equal(t, "6.12.8", comp612.Build.Defines["kernel_version"])
}

func TestExpandComponentTemplate_TwoAxes(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		DefaultComponentConfig: ComponentConfig{
			Spec: SpecSource{
				SourceType: SpecSourceTypeLocal,
				Path:       "/specs/my-driver.spec",
			},
		},
		Matrix: []MatrixAxis{
			{
				Axis: "kernel",
				Values: map[string]ComponentConfig{
					"6-6": {
						Build: ComponentBuildConfig{
							Defines: map[string]string{"kernel_version": "6.6.72"},
						},
					},
					"6-12": {
						Build: ComponentBuildConfig{
							Defines: map[string]string{"kernel_version": "6.12.8"},
						},
					},
				},
			},
			{
				Axis: "toolchain",
				Values: map[string]ComponentConfig{
					"gcc13": {
						Build: ComponentBuildConfig{
							Defines: map[string]string{"gcc_version": "13"},
						},
					},
					"gcc14": {
						Build: ComponentBuildConfig{
							Defines: map[string]string{"gcc_version": "14"},
						},
					},
				},
			},
		},
	}

	result, err := expandComponentTemplate("my-driver", &tmpl)
	require.NoError(t, err)
	assert.Len(t, result, 4)

	// Verify all 4 cartesian product entries exist.
	expectedNames := []string{
		"my-driver-6-12-gcc13",
		"my-driver-6-12-gcc14",
		"my-driver-6-6-gcc13",
		"my-driver-6-6-gcc14",
	}

	for _, name := range expectedNames {
		assert.Contains(t, result, name)
	}

	// Verify one component has all layers applied.
	comp := result["my-driver-6-6-gcc14"]
	assert.Equal(t, "my-driver-6-6-gcc14", comp.Name)
	assert.Equal(t, SpecSourceTypeLocal, comp.Spec.SourceType)
	assert.Equal(t, "/specs/my-driver.spec", comp.Spec.Path)
	assert.Equal(t, "6.6.72", comp.Build.Defines["kernel_version"])
	assert.Equal(t, "14", comp.Build.Defines["gcc_version"])
}

func TestExpandComponentTemplate_ThreeAxes(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		Matrix: []MatrixAxis{
			{
				Axis: "a",
				Values: map[string]ComponentConfig{
					"a1": {},
					"a2": {},
				},
			},
			{
				Axis: "b",
				Values: map[string]ComponentConfig{
					"b1": {},
					"b2": {},
				},
			},
			{
				Axis: "c",
				Values: map[string]ComponentConfig{
					"c1": {},
					"c2": {},
					"c3": {},
				},
			},
		},
	}

	result, err := expandComponentTemplate("test", &tmpl)
	require.NoError(t, err)

	// 2 * 2 * 3 = 12 combinations.
	assert.Len(t, result, 12)

	// Verify a few specific names.
	assert.Contains(t, result, "test-a1-b1-c1")
	assert.Contains(t, result, "test-a2-b2-c3")
}

func TestExpandComponentTemplate_LaterAxisOverridesEarlier(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		DefaultComponentConfig: ComponentConfig{
			Build: ComponentBuildConfig{
				Defines: map[string]string{"shared": "default"},
			},
		},
		Matrix: []MatrixAxis{
			{
				Axis: "first",
				Values: map[string]ComponentConfig{
					"f1": {
						Build: ComponentBuildConfig{
							Defines: map[string]string{"shared": "from-first"},
						},
					},
				},
			},
			{
				Axis: "second",
				Values: map[string]ComponentConfig{
					"s1": {
						Build: ComponentBuildConfig{
							Defines: map[string]string{"shared": "from-second"},
						},
					},
				},
			},
		},
	}

	result, err := expandComponentTemplate("test", &tmpl)
	require.NoError(t, err)
	require.Len(t, result, 1)

	comp := result["test-f1-s1"]
	// Later axis ("second") should override earlier axis ("first").
	assert.Equal(t, "from-second", comp.Build.Defines["shared"])
}

func TestExpandComponentTemplate_SourceConfigFilePropagated(t *testing.T) {
	configFile := &ConfigFile{sourcePath: "/project/azldev.toml"}

	tmpl := ComponentTemplateConfig{
		sourceConfigFile: configFile,
		Matrix: []MatrixAxis{
			{
				Axis: "kernel",
				Values: map[string]ComponentConfig{
					"6-6": {},
				},
			},
		},
	}

	result, err := expandComponentTemplate("my-driver", &tmpl)
	require.NoError(t, err)

	comp := result["my-driver-6-6"]
	assert.Same(t, configFile, comp.SourceConfigFile)
}

func TestCartesianProduct_Empty(t *testing.T) {
	result := cartesianProduct(nil)
	assert.Nil(t, result)

	result = cartesianProduct([]MatrixAxis{})
	assert.Nil(t, result)
}

func TestCartesianProduct_SingleAxisSingleValue(t *testing.T) {
	axes := []MatrixAxis{
		{
			Axis: "a",
			Values: map[string]ComponentConfig{
				"v1": {},
			},
		},
	}

	result := cartesianProduct(axes)
	require.Len(t, result, 1)
	require.Len(t, result[0], 1)
	assert.Equal(t, "a", result[0][0].axisName)
	assert.Equal(t, "v1", result[0][0].valueName)
}

// Test full pipeline integration: template expansion through loadAndResolveProjectConfig.
func TestLoadAndResolveProjectConfig_ComponentTemplate(t *testing.T) {
	const configContents = `
[component-templates.my-driver]
description = "Test template"

[component-templates.my-driver.default-component-config]
spec = { type = "local", path = "my-driver.spec" }

[[component-templates.my-driver.matrix]]
axis = "kernel"
[component-templates.my-driver.matrix.values.6-6]
[component-templates.my-driver.matrix.values.6-12]

[[component-templates.my-driver.matrix]]
axis = "toolchain"
[component-templates.my-driver.matrix.values.gcc13]
[component-templates.my-driver.matrix.values.gcc14]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	// Should have 4 expanded components.
	assert.Len(t, config.Components, 4)
	assert.Contains(t, config.Components, "my-driver-6-6-gcc13")
	assert.Contains(t, config.Components, "my-driver-6-6-gcc14")
	assert.Contains(t, config.Components, "my-driver-6-12-gcc13")
	assert.Contains(t, config.Components, "my-driver-6-12-gcc14")

	// Verify spec path is resolved relative to the config dir.
	comp := config.Components["my-driver-6-6-gcc13"]
	assert.Equal(t, SpecSourceTypeLocal, comp.Spec.SourceType)
	assert.Equal(t, filepath.Join(filepath.Dir(testConfigPath), "my-driver.spec"), comp.Spec.Path)
}

// Test that a template expansion colliding with an explicit component produces an error.
func TestLoadAndResolveProjectConfig_ComponentTemplate_CollisionWithExplicit(t *testing.T) {
	const configContents = `
[components.my-driver-6-6]

[component-templates.my-driver]

[[component-templates.my-driver.matrix]]
axis = "kernel"
[component-templates.my-driver.matrix.values.6-6]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.ErrorIs(t, err, ErrDuplicateComponents)
	assert.Nil(t, config)
}

// Test that template with build defines are layered correctly.
func TestLoadAndResolveProjectConfig_ComponentTemplate_WithBuildDefines(t *testing.T) {
	const configContents = `
[component-templates.my-driver]

[component-templates.my-driver.default-component-config.build]
defines = { base_macro = "base_value" }

[[component-templates.my-driver.matrix]]
axis = "kernel"
[component-templates.my-driver.matrix.values.6-6.build]
defines = { kernel_version = "6.6.72" }

[[component-templates.my-driver.matrix]]
axis = "toolchain"
[component-templates.my-driver.matrix.values.gcc14.build]
defines = { gcc_version = "14" }
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	require.Contains(t, config.Components, "my-driver-6-6-gcc14")

	comp := config.Components["my-driver-6-6-gcc14"]
	assert.Equal(t, "base_value", comp.Build.Defines["base_macro"])
	assert.Equal(t, "6.6.72", comp.Build.Defines["kernel_version"])
	assert.Equal(t, "14", comp.Build.Defines["gcc_version"])
}

// Test that a template with an empty matrix fails validation.
func TestLoadAndResolveProjectConfig_ComponentTemplate_EmptyMatrix(t *testing.T) {
	const configContents = `
[component-templates.my-driver]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.Error(t, err)
	assert.Nil(t, config)
}

// Test that templates and regular components coexist.
func TestLoadAndResolveProjectConfig_ComponentTemplate_MixedWithComponents(t *testing.T) {
	const configContents = `
[components.curl]

[component-templates.my-driver]

[[component-templates.my-driver.matrix]]
axis = "kernel"
[component-templates.my-driver.matrix.values.6-6]
[component-templates.my-driver.matrix.values.6-12]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	// 1 explicit component + 2 expanded from template.
	assert.Len(t, config.Components, 3)
	assert.Contains(t, config.Components, "curl")
	assert.Contains(t, config.Components, "my-driver-6-6")
	assert.Contains(t, config.Components, "my-driver-6-12")
}
