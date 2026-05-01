// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"encoding/hex"
	"errors"
	"fmt"

	"dario.cat/mergo"
)

// TestType indicates the type of test framework used to run a test suite.
type TestType string

const (
	// TestTypePytest uses pytest to run static/offline validation checks.
	TestTypePytest TestType = "pytest"
	// TestTypeLisa uses the LISA framework to run live VM tests.
	TestTypeLisa TestType = "lisa"
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
	// match its declared type.
	ErrMismatchedTestSubtable = errors.New("mismatched test subtable")
	// ErrInvalidInstallMode is returned when a [PytestConfig.Install] value is not recognized.
	ErrInvalidInstallMode = errors.New("invalid install mode")
	// ErrInvalidGitRef is returned when a git ref is not a valid hex commit SHA.
	ErrInvalidGitRef = errors.New("invalid git ref")
)

// TestSuiteConfig defines a named test suite.
type TestSuiteConfig struct {
	// The test suite's name; not present in serialized TOML files (populated from the map key).
	Name string `toml:"-" json:"name" table:",sortkey"`

	// Description of the test suite.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this test suite"`

	// Type indicates the test framework to use.
	Type TestType `toml:"type" json:"type" jsonschema:"required,enum=pytest lisa,title=Type,description=Type of test framework (pytest or lisa)"`

	// Pytest holds pytest-specific configuration. Required when Type is "pytest".
	Pytest *PytestConfig `toml:"pytest,omitempty" json:"pytest,omitempty" jsonschema:"title=Pytest config,description=Pytest-specific configuration (required when type is pytest)"`

	// Lisa holds LISA-specific configuration. Required when Type is "lisa".
	Lisa *LisaConfig `toml:"lisa,omitempty" json:"lisa,omitempty" jsonschema:"title=LISA config,description=LISA-specific configuration (required when type is lisa)"`

	// Reference to the source config file that this definition came from; not present
	// in serialized files.
	SourceConfigFile *ConfigFile `toml:"-" json:"-" table:"-"`
}

// PytestInstallMode specifies how Python dependencies are installed for a pytest suite.
type PytestInstallMode string

const (
	// PytestInstallPyproject installs dependencies from pyproject.toml using editable mode.
	// Returns an error if pyproject.toml is not found in the working directory.
	PytestInstallPyproject PytestInstallMode = "pyproject"
	// PytestInstallRequirements installs dependencies from requirements.txt.
	// Returns an error if requirements.txt is not found.
	PytestInstallRequirements PytestInstallMode = "requirements"
	// PytestInstallNone skips dependency installation entirely. This is the default
	// when [PytestConfig.Install] is not specified — pytest must already be available
	// in the venv (e.g., pre-installed, or installed by the test author out-of-band).
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
	// pytest. Defaults to "none" (no install) when not specified.
	Install PytestInstallMode `toml:"install,omitempty" json:"install,omitempty" jsonschema:"enum=pyproject,enum=requirements,enum=none,title=Install mode,description=How to install Python dependencies: pyproject\\, requirements\\, or none (default)"`
}

// LisaConfig holds configuration specific to LISA-based test suites.
type LisaConfig struct {
	// Framework identifies the git source for the LISA framework itself.
	Framework GitSourceConfig `toml:"framework" json:"framework" jsonschema:"required,title=Framework,description=Git source for the LISA framework"`

	// Runbook identifies the git source and path for the LISA runbook to run.
	Runbook LisaRunbookConfig `toml:"runbook" json:"runbook" jsonschema:"required,title=Runbook,description=Git source and path for the LISA runbook"`

	// PipPreInstall lists pip packages to install before the LISA framework itself.
	// This can be used to override framework version pins that conflict with the local
	// environment (e.g., installing a system-matching libvirt-python version).
	PipPreInstall []string `toml:"pip-pre-install,omitempty" json:"pipPreInstall,omitempty" jsonschema:"title=Pip pre-install,description=Pip packages to install before the framework (for overriding version pins)"`

	// PipExtras lists pip extras to install from the LISA framework package (e.g., "azure",
	// "legacy"). These are appended to the pip install command as pip install -e ".[extra1,extra2]".
	PipExtras []string `toml:"pip-extras,omitempty" json:"pipExtras,omitempty" jsonschema:"title=Pip extras,description=Pip extras to install from the LISA framework package"`

	// ExtraArgs is the list of additional arguments to pass to LISA. These are passed
	// verbatim after placeholder substitution. Supports {image-path}, {image-name},
	// and {capabilities} placeholders.
	ExtraArgs []string `toml:"extra-args,omitempty" json:"extraArgs,omitempty" jsonschema:"title=Extra arguments,description=Additional arguments passed to LISA. Supports {image-path} {image-name} {capabilities} placeholders."`
}

// GitSourceConfig identifies a git repository at a specific commit.
type GitSourceConfig struct {
	// GitURL is the URL of the git repository.
	GitURL string `toml:"git-url" json:"gitUrl" jsonschema:"required,title=Git URL,description=URL of the git repository"`

	// Ref is the commit SHA to check out. Must be a full hex commit hash.
	Ref string `toml:"ref" json:"ref" jsonschema:"required,title=Ref,description=Commit SHA to check out (full hex hash)"`
}

// Validate checks that the [GitSourceConfig] has required fields and a valid ref.
func (g *GitSourceConfig) Validate(context string) error {
	if g.GitURL == "" {
		return fmt.Errorf("%w: %s requires 'git-url'", ErrMissingTestField, context)
	}

	if g.Ref == "" {
		return fmt.Errorf("%w: %s requires 'ref'", ErrMissingTestField, context)
	}

	if err := validateCommitSHA(g.Ref); err != nil {
		return fmt.Errorf("%s: %w", context, err)
	}

	return nil
}

// LisaRunbookConfig identifies a LISA runbook within a git repository.
type LisaRunbookConfig struct {
	GitSourceConfig `toml:",inline"`

	// Path is the path to the runbook YAML file within the repository.
	Path string `toml:"path" json:"path" jsonschema:"required,title=Path,description=Path to the runbook YAML file within the repository"`
}

// Validate checks that the [LisaRunbookConfig] has required fields.
func (r *LisaRunbookConfig) Validate(context string) error {
	if err := r.GitSourceConfig.Validate(context); err != nil {
		return err
	}

	if r.Path == "" {
		return fmt.Errorf("%w: %s requires 'path'", ErrMissingTestField, context)
	}

	return nil
}

// validateCommitSHA checks that s is a valid full-length hex commit SHA (40 characters).
func validateCommitSHA(ref string) error {
	const commitSHALength = 40

	if len(ref) != commitSHALength {
		return fmt.Errorf("%w: expected %d hex characters, got %d: %#q",
			ErrInvalidGitRef, commitSHALength, len(ref), ref)
	}

	if _, err := hex.DecodeString(ref); err != nil {
		return fmt.Errorf("%w: not a valid hex string: %#q", ErrInvalidGitRef, ref)
	}

	return nil
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

		if t.Lisa != nil {
			return fmt.Errorf("%w: test suite %#q of type %#q must not have a [lisa] subtable",
				ErrMismatchedTestSubtable, t.Name, t.Type)
		}

	case TestTypeLisa:
		if t.Lisa == nil {
			return fmt.Errorf("%w: test suite %#q of type %#q requires a [lisa] subtable",
				ErrMissingTestField, t.Name, t.Type)
		}

		frameworkContext := fmt.Sprintf("test suite %#q lisa.framework", t.Name)
		if err := t.Lisa.Framework.Validate(frameworkContext); err != nil {
			return err
		}

		runbookContext := fmt.Sprintf("test suite %#q lisa.runbook", t.Name)
		if err := t.Lisa.Runbook.Validate(runbookContext); err != nil {
			return err
		}

		if t.Pytest != nil {
			return fmt.Errorf("%w: test suite %#q of type %#q must not have a [pytest] subtable",
				ErrMismatchedTestSubtable, t.Name, t.Type)
		}

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
			PytestInstallNone,
		)
	}

	// When the effective install mode requires a working directory, 'working-dir'
	// must be specified. The default mode is 'none' (no install) and so requires
	// nothing; only an explicitly-set install mode that performs work needs the dir.
	if p.EffectiveInstallMode() != PytestInstallNone && p.WorkingDir == "" {
		return fmt.Errorf(
			"%w: 'working-dir' is required when install mode is %#q",
			ErrMissingTestField, p.EffectiveInstallMode(),
		)
	}

	return nil
}

// EffectiveInstallMode returns the install mode, defaulting to [PytestInstallNone] when
// the field is not set.
func (p *PytestConfig) EffectiveInstallMode() PytestInstallMode {
	if p.Install == "" {
		return PytestInstallNone
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
			TestPaths:  append([]string(nil), t.Pytest.TestPaths...),
			ExtraArgs:  append([]string(nil), t.Pytest.ExtraArgs...),
			Install:    t.Pytest.Install,
		}
	}

	if t.Lisa != nil {
		result.Lisa = &LisaConfig{
			Framework:     t.Lisa.Framework,
			Runbook:       t.Lisa.Runbook,
			PipPreInstall: t.Lisa.PipPreInstall,
			PipExtras:     t.Lisa.PipExtras,
			ExtraArgs:     t.Lisa.ExtraArgs,
		}
	}

	return result
}
