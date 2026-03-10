// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev

import (
	"fmt"

	"github.com/spf13/pflag"
)

// Colorization mode for the application.
type ColorMode string

const (
	// ColorModeAlways indicates that color should always be used in output.
	ColorModeAlways ColorMode = "always"
	// ColorModeAuto indicates that color should be used if the output is a terminal.
	ColorModeAuto ColorMode = "auto"
	// ColorModeNever indicates that color should never be used in output.
	ColorModeNever ColorMode = "never"
)

// Assert that ColorMode implements the [pflag.Value] interface.
var _ pflag.Value = (*ColorMode)(nil)

func (f *ColorMode) String() string {
	return string(*f)
}

// Parses the format from a string; used by command-line parser.
func (f *ColorMode) Set(value string) error {
	switch value {
	case "always":
		*f = ColorModeAlways
	case "auto":
		*f = ColorModeAuto
	case "never":
		*f = ColorModeNever
	case "":
		*f = ColorModeAuto
	default:
		// Default to auto but still return an error.
		*f = ColorModeAuto

		return fmt.Errorf("unsupported color mode: %s", value)
	}

	return nil
}

// Returns a descriptive string used in command-line help.
func (f *ColorMode) Type() string {
	return "mode"
}
