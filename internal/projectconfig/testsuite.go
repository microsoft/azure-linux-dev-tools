// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"

	"dario.cat/mergo"
)

// TestType indicates the type of test framework used to run a test suite.
type TestType string

const (
	// TestTypePytest uses pytest to run static/offline validation checks.
	TestTypePytest TestType = "pytest"
)

var (
	// ErrDuplicateTestSuites is returned when duplicate conflicting test suite definitions are found.
	ErrDuplicateTestSuites = errors.New("duplicate test suite")
	// ErrUnknownTestType is returned for unrecognized test types.
	ErrUnknownTestType = errors.New("unknown test type")
	// ErrMissingTestField is returned when a required test config field is missing.
	ErrMissingTestField = errors.New("missing required test field")
	// ErrUndefinedTestSuite is returned when an image references a test suite name that is not defined.
	ErrUndefinedTestSuite = errors.New("undefined test suite reference")
	// ErrMismatchedTestSubtable is returned when a test config has a subtable that does not
	// match its declared type. Currently only one test type (pytest) exists, so this cannot
	// trigger yet. When adding a new test type with its own subtable field, add cross-checks
	// in [TestSuiteConfig.Validate] to ensure only the matching subtable is populated.
	ErrMismatchedTestSubtable = errors.New("mismatched test subtable")
	// ErrInvalidInstallMode is returned when a [PytestConfig.Install] value is not recognized.
	ErrInvalidInstallMode = errors.New("invalid install mode")
)

// TestSuiteConfig defines a named test suite.
type TestSuiteConfig struct {
	// The test suite's name; not present in serialized TOML files (populated from the map key).
	Name string `toml:"-" json:"name" table:",sortkey"`

	// Description of the test suite.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this test suite"`

	// Type indicates the test framework to use.
	Type TestType `toml:"type" json:"type" jsonschema:"required,enum=pytest,title=Type,description=Type of test framework (pytest)"`

	// Pytest holds pytest-specific configuration. Required when Type is "pytest".
	Pytest *PytestConfig `toml:"pytest,omitempty" json:"pytest,omitempty" jsonschema:"title=Pytest config,description=Pytest-specific configuration (required when type is pytest)"`

	// Reference to the source config file that this definition came from; not present
	// in serialized files.
	SourceConfigFile *ConfigFile `toml:"-" json:"-" table:"-"`
}

// PytestInstallMode specifies how Python dependencies are installed for a pytest suite.
type PytestInstallMode string

const (
	// PytestInstallPyproject installs dependencies from pyproject.toml using editable mode.
	// Returns an error if pyproject.toml is not found in the working directory.
	// This is the default when [PytestConfig.Install] is not specified.
	PytestInstallPyproject PytestInstallMode = "pyproject"
	// PytestInstallRequirements installs dependencies from requirements.txt.
	// Returns an error if requirements.txt is not found.
	PytestInstallRequirements PytestInstallMode = "requirements"
	// PytestInstallNone skips dependency installation entirely.
	PytestInstallNone PytestInstallMode = "none"
)

// PytestConfig holds configuration specific to pytest-based test suites.
type PytestConfig struct {
	// WorkingDir is the directory to use as the current working directory when running pytest.
	// Relative paths are resolved against the config file's directory.
	WorkingDir string `toml:"working-dir,omitempty" json:"workingDir,omitempty" jsonschema:"title=Working directory,description=Directory to use as CWD when running pytest"`

	// TestPaths is the list of test file paths or directories to pass to pytest as positional
	// arguments. Glob patterns (e.g., cases/test_*.py) are expanded relative to WorkingDir.
	TestPaths []string `toml:"test-paths,omitempty" json:"testPaths,omitempty" jsonschema:"title=Test paths,description=Test file paths or directories passed to pytest. Glob patterns are expanded."`

	// ExtraArgs is the list of additional arguments to pass to pytest. These are passed
	// verbatim after placeholder substitution. Use {image-path} as a placeholder for the
	// image path, which will be substituted at runtime.
	ExtraArgs []string `toml:"extra-args,omitempty" json:"extraArgs,omitempty" jsonschema:"title=Extra arguments,description=Additional arguments passed to pytest. Use {image-path} as a placeholder for the image path."`

	// Install specifies how Python dependencies are installed into the venv before running
	// pytest. Defaults to "pyproject" when not specified.
	Install PytestInstallMode `toml:"install,omitempty" json:"install,omitempty" jsonschema:"enum=pyproject,enum=requirements,enum=none,title=Install mode,description=How to install Python dependencies: pyproject (default)\\, requirements\\, or none"`
}

// Validate checks that the test suite config has valid type-specific required fields and that
// only the matching subtable is present.
func (t *TestSuiteConfig) Validate() error {
	if t.Type == "" {
		return fmt.Errorf("%w: test suite %#q is missing required field 'type'",
			ErrMissingTestField, t.Name)
	}

	switch t.Type {
	case TestTypePytest:
		if t.Pytest == nil {
			return fmt.Errorf("%w: test suite %#q of type %#q requires a [pytest] subtable",
				ErrMissingTestField, t.Name, t.Type)
		}

		if err := t.Pytest.Validate(); err != nil {
			return fmt.Errorf("test suite %#q: %w", t.Name, err)
		}

		// NOTE: When adding a new test type with its own subtable field (e.g., Lisa *LisaConfig),
		// add a mismatch check here:
		//   if t.Lisa != nil { return fmt.Errorf("%w: ...", ErrMismatchedTestSubtable) }
		// and add the symmetric check in the new type's case branch.

	default:
		return fmt.Errorf("%w: %#q (test suite: %#q)", ErrUnknownTestType, t.Type, t.Name)
	}

	return nil
}

// Validate checks that the [PytestConfig] fields are valid.
func (p *PytestConfig) Validate() error {
	if p.Install != "" && !p.Install.isValid() {
		return fmt.Errorf(
			"%w: %#q; allowed values: %#q, %#q, %#q (or omit for default %#q)",
			ErrInvalidInstallMode, p.Install,
			PytestInstallPyproject, PytestInstallRequirements, PytestInstallNone,
			PytestInstallPyproject,
		)
	}

	// When 'install' is explicitly set to a mode that requires a working directory,
	// 'working-dir' must also be specified.
	if p.Install != "" && p.Install != PytestInstallNone && p.WorkingDir == "" {
		return fmt.Errorf(
			"%w: 'working-dir' is required when 'install' is %#q",
			ErrMissingTestField, p.Install,
		)
	}

	return nil
}

// EffectiveInstallMode returns the install mode, defaulting to [PytestInstallPyproject] when
// the field is not set.
func (p *PytestConfig) EffectiveInstallMode() PytestInstallMode {
	if p.Install == "" {
		return PytestInstallPyproject
	}

	return p.Install
}

// isValid returns whether the mode is a recognized [PytestInstallMode] value.
func (m PytestInstallMode) isValid() bool {
	switch m {
	case PytestInstallPyproject, PytestInstallRequirements, PytestInstallNone:
		return true
	default:
		return false
	}
}

// MergeUpdatesFrom updates the test suite config with overrides present in other.
func (t *TestSuiteConfig) MergeUpdatesFrom(other *TestSuiteConfig) error {
	err := mergo.Merge(t, other, mergo.WithOverride, mergo.WithAppendSlice)
	if err != nil {
		return fmt.Errorf("failed to merge test suite config:\n%w", err)
	}

	return nil
}

// WithAbsolutePaths returns a copy of the test suite config with relative file paths converted
// to absolute paths (relative to referenceDir).
func (t *TestSuiteConfig) WithAbsolutePaths(referenceDir string) *TestSuiteConfig {
	result := &TestSuiteConfig{
		Name:             t.Name,
		Description:      t.Description,
		Type:             t.Type,
		SourceConfigFile: t.SourceConfigFile,
	}

	if t.Pytest != nil {
		result.Pytest = &PytestConfig{
			WorkingDir: makeAbsolute(referenceDir, t.Pytest.WorkingDir),
			TestPaths:  t.Pytest.TestPaths,
			ExtraArgs:  t.Pytest.ExtraArgs,
			Install:    t.Pytest.Install,
		}
	}

	return result
}
