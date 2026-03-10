// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

/*
Package reflectable provides facilities for formatting arbitrary Go values into human-readable tables
using reflection. It supports multiple output formats and can handle complex Go data structures
including structs, slices, maps, and primitive types.

# Responsibilities

This package is responsible for:

  - Using Go reflection to introspect arbitrary values and their types.
  - Converting Go values into tabular representations with configurable formatting options.
  - Supporting multiple output formats including human-readable tables, Markdown tables, and CSV.
  - Providing struct field customization through tags for display names, alignment, sorting, and visibility.
  - Handling complex data structures like nested structs, slices of structs, and maps.

# Usage Context

This package is used by command implementations that need to display structured data to users
in a consistent, readable format. It enables commands to output query results, configuration data,
and other structured information in tables that can be easily consumed by both humans and tools.

The main entry point is [FormatValue], which takes formatting options and an arbitrary Go value
and returns a formatted string representation.

# Example Usage

	// Basic usage
	options := reflectable.NewOptions()
	result, err := reflectable.FormatValue(options, someStruct)

	// Struct field customization via tags
	type MyStruct struct {
		LongerFieldName string `table:"name,omitempty"`
	}
*/
package reflectable
