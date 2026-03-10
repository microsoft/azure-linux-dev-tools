// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reflectable

// Format for table output.
type Format int

const (
	// FormatTable emits the table in human-readable format (not machine-readable).
	FormatTable Format = iota
	// FormatMarkdown emits the table in Markdown format.
	FormatMarkdown
	// FormatCSV emits the table in CSV format.
	FormatCSV
)

// Options encapsulates options for formatting and displaying tables.
type Options struct {
	// Format for table output.
	Format Format
	// Maximum width for tables.
	MaxTableWidth int
	// Enable colorized output?
	ColorEnabled bool
}

// NewOptions constructs new Options with default values.
func NewOptions() *Options {
	return &Options{
		Format:        FormatTable,
		MaxTableWidth: 0,
	}
}

// WithFormat sets the format for table output.
func (o *Options) WithFormat(format Format) *Options {
	o.Format = format

	return o
}

// WithMaxTableWidth sets the maximum width for tables.
func (o *Options) WithMaxTableWidth(maxTableWidth int) *Options {
	o.MaxTableWidth = maxTableWidth

	return o
}

// WithColor enables or disables colorized output.
func (o *Options) WithColor(enabled bool) *Options {
	o.ColorEnabled = enabled

	return o
}
