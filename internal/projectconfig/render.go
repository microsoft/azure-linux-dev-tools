// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

// ComponentRenderConfig encapsulates configuration for rendering a component.
type ComponentRenderConfig struct {
	// SkipFileFilter, when true, disables the post-render file filter for this
	// component. Normally, rendered output is filtered to only include files
	// referenced by Source/Patch tags in the spec (as reported by spectool).
	// Some specs use macros that spectool cannot expand, causing referenced
	// files to be incorrectly removed. Setting this to true preserves all
	// files from the dist-git checkout.
	SkipFileFilter bool `toml:"skip-file-filter,omitempty" json:"skipFileFilter,omitempty" jsonschema:"title=Skip file filter,description=Disable post-render file filtering for specs with unexpandable macros in Source/Patch tags"`
}
