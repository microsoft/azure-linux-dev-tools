// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"

	"github.com/brunoga/deep"
)

// ComponentTemplateConfig defines a component template that produces multiple component variants
// via a matrix of axes. Each axis contributes named values; the template expands into the
// cartesian product of all axis values, yielding one [ComponentConfig] per combination.
type ComponentTemplateConfig struct {
	// A human-friendly description of this component template.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this component template"`

	// Default configuration applied to every expanded component before axis-specific overrides.
	DefaultComponentConfig ComponentConfig `toml:"default-component-config,omitempty" json:"defaultComponentConfig,omitempty" jsonschema:"title=Default component configuration,description=Default component config applied to every expanded variant before axis overrides"`

	// Ordered list of matrix axes. Each axis defines a dimension with named values;
	// the cartesian product of all axes determines the set of expanded components.
	// Axis configs are applied in array order, so later axes override earlier ones.
	Matrix []MatrixAxis `toml:"matrix" json:"matrix" validate:"required,min=1,dive" jsonschema:"required,minItems=1,title=Matrix axes,description=Ordered list of matrix axes whose cartesian product defines the expanded component variants"`

	// Internal: name assigned during loading (not serialized).
	name string `toml:"-" json:"-"`

	// Internal: reference to the source config file (not serialized).
	sourceConfigFile *ConfigFile `toml:"-" json:"-"`
}

// MatrixAxis defines a single dimension of a component template's matrix.
// It has a name (the axis identifier) and a map of named values, each containing
// a partial [ComponentConfig] that is merged into the expanded component.
type MatrixAxis struct {
	// Name of this axis (e.g., "kernel", "toolchain").
	Axis string `toml:"axis" json:"axis" validate:"required" jsonschema:"required,title=Axis name,description=Name of this matrix axis (e.g. kernel or toolchain)"`

	// Named values for this axis. Each key is a value name that appears in the
	// synthesized component name; each value is a partial [ComponentConfig] merged
	// into the expanded component.
	Values map[string]ComponentConfig `toml:"values" json:"values" validate:"required,min=1,dive" jsonschema:"required,minProperties=1,title=Axis values,description=Named values for this axis; each value is a partial ComponentConfig merged into the expanded component"`
}

// Validate checks that the component template configuration is internally consistent.
func (t ComponentTemplateConfig) Validate() error {
	if len(t.Matrix) == 0 {
		return errors.New("component template must have at least one matrix axis")
	}

	seenAxes := make(map[string]struct{}, len(t.Matrix))

	for i, axis := range t.Matrix {
		if axis.Axis == "" {
			return fmt.Errorf("matrix axis %d has an empty axis name", i+1)
		}

		if _, seen := seenAxes[axis.Axis]; seen {
			return fmt.Errorf("duplicate matrix axis name %#q", axis.Axis)
		}

		seenAxes[axis.Axis] = struct{}{}

		if len(axis.Values) == 0 {
			return fmt.Errorf("matrix axis %#q must have at least one value", axis.Axis)
		}

		for valueName := range axis.Values {
			if valueName == "" {
				return fmt.Errorf("matrix axis %#q has an empty value name", axis.Axis)
			}
		}
	}

	return nil
}

// WithAbsolutePaths returns a copy of the component template config with relative file paths
// converted to absolute file paths (relative to referenceDir).
func (t ComponentTemplateConfig) WithAbsolutePaths(referenceDir string) ComponentTemplateConfig {
	result := deep.MustCopy(t)

	result.DefaultComponentConfig = *(result.DefaultComponentConfig.WithAbsolutePaths(referenceDir))

	for i, axis := range result.Matrix {
		for valueName, valueCfg := range axis.Values {
			result.Matrix[i].Values[valueName] = *(valueCfg.WithAbsolutePaths(referenceDir))
		}
	}

	return result
}
