// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components

import "path/filepath"

// RenderedSpecDir returns the rendered spec output directory for a given component.
// The path is computed as {renderedSpecsDir}/{componentName}.
// Returns an empty string if renderedSpecsDir is not configured (empty).
func RenderedSpecDir(renderedSpecsDir, componentName string) string {
	if renderedSpecsDir == "" {
		return ""
	}

	return filepath.Join(renderedSpecsDir, componentName)
}
