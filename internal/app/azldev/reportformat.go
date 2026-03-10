// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev

import (
	"fmt"

	"github.com/spf13/pflag"
)

// Format of a results report.
type ReportFormat string

const (
	// ReportFormatTable is a human-readable tabular format.
	ReportFormatTable ReportFormat = "table"
	// ReportFormatCSV is a machine-readable, comma-separated value format.
	ReportFormatCSV ReportFormat = "csv"
	// ReportFormatJSON is a machine-readable JSON format.
	ReportFormatJSON ReportFormat = "json"
	// ReportFormatMarkdown is a markdown format intended for generating formatted readable reports.
	ReportFormatMarkdown ReportFormat = "markdown"
)

// Assert that ReportFormat implements the [pflag.Value] interface.
var _ pflag.Value = (*ReportFormat)(nil)

func (f *ReportFormat) String() string {
	return string(*f)
}

// Parses the format from a string; used by command-line parser.
func (f *ReportFormat) Set(value string) error {
	switch value {
	case "json":
		*f = ReportFormatJSON
	case "table":
		*f = ReportFormatTable
	case "markdown":
		*f = ReportFormatMarkdown
	case "csv":
		*f = ReportFormatCSV
	default:
		return fmt.Errorf("unsupported report format: %s", value)
	}

	return nil
}

// Returns a descriptive string used in command-line help.
func (f *ReportFormat) Type() string {
	return "fmt"
}
