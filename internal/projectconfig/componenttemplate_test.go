// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test private functions
package projectconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComponentTemplateConfig_Validate_Valid(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		Matrix: []MatrixAxis{
			{
				Axis: "kernel",
				Values: map[string]ComponentConfig{
					"6-6": {},
				},
			},
		},
	}

	err := tmpl.Validate()
	assert.NoError(t, err)
}

func TestComponentTemplateConfig_Validate_MultiAxis(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		Matrix: []MatrixAxis{
			{
				Axis: "kernel",
				Values: map[string]ComponentConfig{
					"6-6":  {},
					"6-12": {},
				},
			},
			{
				Axis: "toolchain",
				Values: map[string]ComponentConfig{
					"gcc13": {},
					"gcc14": {},
				},
			},
		},
	}

	err := tmpl.Validate()
	assert.NoError(t, err)
}

func TestComponentTemplateConfig_Validate_EmptyMatrix(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		Matrix: []MatrixAxis{},
	}

	err := tmpl.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one matrix axis")
}

func TestComponentTemplateConfig_Validate_NilMatrix(t *testing.T) {
	tmpl := ComponentTemplateConfig{}

	err := tmpl.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one matrix axis")
}

func TestComponentTemplateConfig_Validate_EmptyAxisName(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		Matrix: []MatrixAxis{
			{
				Axis:   "",
				Values: map[string]ComponentConfig{"v1": {}},
			},
		},
	}

	err := tmpl.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty axis name")
}

func TestComponentTemplateConfig_Validate_DuplicateAxisName(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		Matrix: []MatrixAxis{
			{
				Axis:   "kernel",
				Values: map[string]ComponentConfig{"6-6": {}},
			},
			{
				Axis:   "kernel",
				Values: map[string]ComponentConfig{"6-12": {}},
			},
		},
	}

	err := tmpl.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate matrix axis name")
}

func TestComponentTemplateConfig_Validate_EmptyValues(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		Matrix: []MatrixAxis{
			{
				Axis:   "kernel",
				Values: map[string]ComponentConfig{},
			},
		},
	}

	err := tmpl.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one value")
}

func TestComponentTemplateConfig_Validate_EmptyValueName(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		Matrix: []MatrixAxis{
			{
				Axis:   "kernel",
				Values: map[string]ComponentConfig{"": {}},
			},
		},
	}

	err := tmpl.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty value name")
}

func TestComponentTemplateConfig_WithAbsolutePaths(t *testing.T) {
	tmpl := ComponentTemplateConfig{
		DefaultComponentConfig: ComponentConfig{
			Spec: SpecSource{
				SourceType: SpecSourceTypeLocal,
				Path:       "my-driver.spec",
			},
		},
		Matrix: []MatrixAxis{
			{
				Axis: "kernel",
				Values: map[string]ComponentConfig{
					"6-6": {
						Spec: SpecSource{
							SourceType: SpecSourceTypeLocal,
							Path:       "override.spec",
						},
					},
				},
			},
		},
	}

	result := tmpl.WithAbsolutePaths("/project")

	assert.Equal(t, "/project/my-driver.spec", result.DefaultComponentConfig.Spec.Path)

	if assert.Contains(t, result.Matrix[0].Values, "6-6") {
		assert.Equal(t, "/project/override.spec", result.Matrix[0].Values["6-6"].Spec.Path)
	}

	// Verify original was not mutated.
	assert.Equal(t, "my-driver.spec", tmpl.DefaultComponentConfig.Spec.Path)
}
