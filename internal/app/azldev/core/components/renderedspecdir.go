// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// RenderedSpecDir returns the rendered spec output directory for a given component.
// Components are organized by the lowercase first letter of their name:
// {renderedSpecsDir}/{letter}/{componentName} (e.g., "SPECS/c/curl").
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

	prefix := strings.ToLower(componentName[:1])

	return filepath.Join(renderedSpecsDir, prefix, componentName), nil
}

// RenderedSpecDirAliasName returns the URL-encoded form of componentName intended
// for use as a sibling alias (typically a symlink) alongside the real rendered
// directory. This is a temporary compatibility shim — see the workaround block
// in `RenderComponents` for the full rationale and TODO link. It is NOT part of
// the canonical rendered-spec directory contract and should be removed once the
// downstream build system decodes SCM URL fragments correctly.
//
// Encoder choice: [url.QueryEscape] is used deliberately, in conscious divergence
// from the [url.PathEscape] convention elsewhere in this codebase (e.g.
// fedorasource). Of the two stdlib encoders, only QueryEscape escapes '+' as
// '%2B', and '+' is the only RPM-name character that triggers the downstream
// path mismatch we need to bridge (PathEscape would emit a literal '+', the
// alias name would equal the real name, and no symlink would be written).
// Component names with '%' literal are assumed not to occur (RPM convention);
// no real-world packages exercise this case.
//
// Returns "" when no alias is needed (the encoded form equals componentName).
func RenderedSpecDirAliasName(componentName string) string {
	encoded := url.QueryEscape(componentName)
	if encoded == componentName {
		return ""
	}

	return encoded
}
