// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"sort"
	"strings"

	"github.com/brunoga/deep"
)

// expandComponentTemplate expands a single component template into its cartesian product of
// components. Each expanded component is created by layering the template's default config with
// the selected axis value configs in matrix definition order.
func expandComponentTemplate(
	templateName string, tmpl *ComponentTemplateConfig,
) (map[string]ComponentConfig, error) {
	// Build the cartesian product of all axis values.
	combinations := cartesianProduct(tmpl.Matrix)

	result := make(map[string]ComponentConfig, len(combinations))

	for _, combo := range combinations {
		// Build the synthesized component name.
		nameParts := make([]string, 0, len(combo)+1)
		nameParts = append(nameParts, templateName)

		for _, selection := range combo {
			nameParts = append(nameParts, selection.valueName)
		}

		componentName := strings.Join(nameParts, "-")

		// Start from a deep copy of the template's default component config.
		expanded := deep.MustCopy(tmpl.DefaultComponentConfig)

		// Layer each axis value config in matrix definition order.
		for _, selection := range combo {
			valueCfg := deep.MustCopy(selection.valueCfg)

			if err := expanded.MergeUpdatesFrom(&valueCfg); err != nil {
				return nil, fmt.Errorf(
					"failed to merge axis %#q value %#q into expanded component %#q:\n%w",
					selection.axisName, selection.valueName, componentName, err,
				)
			}
		}

		// Set the component's identity fields.
		expanded.Name = componentName
		expanded.SourceConfigFile = tmpl.sourceConfigFile

		// Check for duplicate names within the same template expansion.
		if _, exists := result[componentName]; exists {
			return nil, fmt.Errorf(
				"component template %#q produces duplicate expanded component name %#q",
				templateName, componentName,
			)
		}

		result[componentName] = expanded
	}

	return result, nil
}

// axisSelection represents a single axis value chosen for one slot in the cartesian product.
type axisSelection struct {
	axisName  string
	valueName string
	valueCfg  ComponentConfig
}

// cartesianProduct generates all combinations of axis values across the given matrix axes.
// Each returned slice is one combination, with entries in the same order as the input axes.
func cartesianProduct(axes []MatrixAxis) [][]axisSelection {
	if len(axes) == 0 {
		return nil
	}

	// Start with a single empty combination.
	combinations := [][]axisSelection{{}}

	for _, axis := range axes {
		// Sort value names for deterministic expansion order within each axis.
		valueNames := make([]string, 0, len(axis.Values))
		for name := range axis.Values {
			valueNames = append(valueNames, name)
		}

		sort.Strings(valueNames)

		var newCombinations [][]axisSelection

		for _, combo := range combinations {
			for _, valueName := range valueNames {
				// Extend the existing combination with this axis value.
				extended := make([]axisSelection, len(combo), len(combo)+1)
				copy(extended, combo)

				extended = append(extended, axisSelection{
					axisName:  axis.Axis,
					valueName: valueName,
					valueCfg:  axis.Values[valueName],
				})

				newCombinations = append(newCombinations, extended)
			}
		}

		combinations = newCombinations
	}

	return combinations
}
