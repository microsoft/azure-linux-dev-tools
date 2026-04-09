// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components

import (
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// RenderedSpecDir returns the rendered spec output directory for a given component.
// The path is computed as {renderedSpecsDir}/{componentName}.
// Returns an empty string if renderedSpecsDir is not configured (empty).
// Returns an error if componentName is unsafe (absolute, contains path separators
// or traversal sequences).
func RenderedSpecDir(renderedSpecsDir, componentName string) (string, error) {
	if err := fileutils.ValidateFilename(componentName); err != nil {
		return "", fmt.Errorf("invalid component name for rendered spec dir:\n%w", err)
	}

	if renderedSpecsDir == "" {
		return "", nil
	}

	return filepath.Join(renderedSpecsDir, componentName), nil
}
