// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package reflectable implements facilities for formatting arbitrary Go values into
// human-readable tables, using reflection.
package reflectable

// FormatValue formats the given value as a table, using the go-pretty pretty table package.
func FormatValue(options *Options, value interface{}) (result string, err error) {
	writer := newPrettyWriter(options)

	FormatValueUsingWriter(writer, value)

	return writer.Finish(), nil
}
