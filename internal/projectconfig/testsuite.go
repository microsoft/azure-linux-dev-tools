// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import "errors"

var (
	// ErrDuplicateTestSuites is returned when duplicate conflicting test suite definitions are found.
	ErrDuplicateTestSuites = errors.New("duplicate test suite")
	// ErrUndefinedTestSuite is returned when an image references a test suite name that is not defined.
	ErrUndefinedTestSuite = errors.New("undefined test suite reference")
)

// TestSuiteConfig defines a named test suite.
type TestSuiteConfig struct {
	// The test suite's name; not present in serialized TOML files (populated from the map key).
	Name string `toml:"-" json:"name" table:",sortkey"`

	// Description of the test suite.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this test suite"`

	// Reference to the source config file that this definition came from; not present
	// in serialized files.
	SourceConfigFile *ConfigFile `toml:"-" json:"-" table:"-"`
}
