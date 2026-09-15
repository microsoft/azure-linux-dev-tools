// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

// ComponentRenderConfig encapsulates configuration for rendering a component.
type ComponentRenderConfig struct {
	// SkipFileFilter is retained for compatibility and ignored. Render always
	// preserves every file in the prepared dist-git dir.
	SkipFileFilter bool `toml:"skip-file-filter,omitempty" json:"skipFileFilter,omitempty" jsonschema:"title=Skip file filter,description=Deprecated compatibility setting; ignored because render always preserves every file in the dist-git dir" fingerprint:"-"`
}
