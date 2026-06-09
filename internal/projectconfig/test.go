// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"slices"

	"dario.cat/mergo"
	"github.com/invopop/jsonschema"
	orderedmap "github.com/pb33f/ordered-map/v2"
)

// TestType indicates the type of test framework used to run a test.
type TestType string

const (
	// TestTypePytest uses pytest to run static/offline validation checks.
	TestTypePytest TestType = "pytest"
	// TestTypeLisa uses LISA (Linux Integration Services Automation) to run
	// VM-level tests. LISA tests are not executed by azldev directly; the type
	// serves as metadata for external orchestration.
	TestTypeLisa TestType = "lisa"
)

// TestKind classifies the nature of a test for cost/expectation reporting.
// It is a closed multi-valued enum: a single test may legitimately combine
// kinds (e.g., a regression test that also gathers performance data).
type TestKind string

const (
	// TestKindFunctional indicates a functional / regression test: it asserts
	// behavior against an expected outcome. This is the typical default kind.
	TestKindFunctional TestKind = "functional"
	// TestKindPerformance indicates a test whose primary purpose is to measure
	// performance characteristics (throughput, latency, etc.) rather than to
	// assert pass/fail behavior.
	TestKindPerformance TestKind = "performance"
)

var (
	// ErrDuplicateTests is returned when duplicate conflicting test definitions are found.
	ErrDuplicateTests = errors.New("duplicate test")
	// ErrUnknownTestType is returned for unrecognized test types.
	ErrUnknownTestType = errors.New("unknown test type")
	// ErrUnknownTestKind is returned for unrecognized test kind values.
	ErrUnknownTestKind = errors.New("unknown test kind")
	// ErrMissingTestField is returned when a required test config field is missing.
	ErrMissingTestField = errors.New("missing required test field")
	// ErrUndefinedTest is returned when something references a test name that is not defined.
	ErrUndefinedTest = errors.New("undefined test reference")
	// ErrMismatchedTestSubtable is returned when a test config has a subtable
	// that does not match its declared type.
	ErrMismatchedTestSubtable = errors.New("mismatched test subtable")
	// ErrInvalidInstallMode is returned when a [PytestConfig.Install] value is not recognized.
	ErrInvalidInstallMode = errors.New("invalid install mode")
	// ErrInvalidTestRef is returned when a [TestRef] has neither or both of name/group set.
	ErrInvalidTestRef = errors.New("invalid test reference")
)

// TestConfig defines a single named unit of testing: one configuration of one
// runner / harness (e.g. pytest) with framework-specific options. Tests
// are referenced from images and components via [TestRef] entries, and may be
// bundled into [TestGroupConfig]s.
//
// JSONSchemaExtend uses a value receiver because the invopop/jsonschema library
// only invokes value-receiver methods when reflecting on the type; the rest of
// the methods use pointer receivers because they mutate.
//
//nolint:recvcheck // intentional mixed receivers (see comment above).
type TestConfig struct {
	// The test's name; not present in serialized TOML files (populated from the map key).
	Name string `toml:"-" json:"name" table:",sortkey"`

	// Description of the test.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this test"`

	// Type indicates the test framework to use.
	Type TestType `toml:"type" json:"type" jsonschema:"required,enum=pytest,enum=lisa,title=Type,description=Type of test framework (pytest or lisa)"`

	// Kind classifies the test's nature (functional, performance, ...). May be
	// multi-valued for tests that legitimately combine kinds (e.g., a regression
	// test that also gathers performance data). Optional; defaults to unspecified.
	Kind []TestKind `toml:"kind,omitempty" json:"kind,omitempty" jsonschema:"title=Kind,description=Classification of the test's nature (e.g. functional\\, performance). Closed enum; multi-valued."`

	// LongRunning marks a test that is expected to take a long time (hours).
	// Consumers (e.g., CI orchestration) can use this to schedule appropriately.
	// Optional; omitted when false. This is a declarative hint about cost, not
	// a configurable timeout.
	LongRunning bool `toml:"long-running,omitempty" json:"longRunning,omitempty" jsonschema:"title=Long running,description=Indicates that this test may take hours to complete. Hint about cost\\, not a configurable timeout."`

	// Pytest holds pytest-specific configuration. Required when Type is "pytest".
	Pytest *PytestConfig `toml:"pytest,omitempty" json:"pytest,omitempty" jsonschema:"title=Pytest config,description=Pytest-specific configuration (required when type is pytest)"`

	// Reference to the source config file that this definition came from; not present
	// in serialized files.
	SourceConfigFile *ConfigFile `toml:"-" json:"-" table:"-"`
}

// PytestInstallMode specifies how Python dependencies are installed for a pytest test.
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

// PytestConfig holds configuration specific to pytest-based tests.
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

// Validate checks that the test config has valid type-specific required fields and that
// only the matching subtable is present.
func (t *TestConfig) Validate() error {
	if t.Type == "" {
		return fmt.Errorf("%w: test %#q is missing required field 'type'",
			ErrMissingTestField, t.Name)
	}

	if err := validateTestKinds(t.Name, t.Kind); err != nil {
		return err
	}

	switch t.Type {
	case TestTypePytest:
		if t.Pytest == nil {
			return fmt.Errorf("%w: test %#q of type %#q requires a [pytest] subtable",
				ErrMissingTestField, t.Name, t.Type)
		}

		if err := t.Pytest.Validate(); err != nil {
			return fmt.Errorf("test %#q: %w", t.Name, err)
		}

	case TestTypeLisa:
		// LISA is an external test framework not executed by azldev.
		// Tests of this type serve as metadata for external orchestration (e.g. control tower).
		if t.Pytest != nil {
			return fmt.Errorf(
				"%w: test %#q of type %#q cannot include subtable 'pytest'",
				ErrMismatchedTestSubtable, t.Name, t.Type,
			)
		}

	default:
		return fmt.Errorf("%w: %#q (test: %#q)", ErrUnknownTestType, t.Type, t.Name)
	}

	return nil
}

// validateTestKinds checks that every kind value is a recognized [TestKind] and
// flags duplicates (kept order-preserving by reporting the first dup encountered).
func validateTestKinds(testName string, kinds []TestKind) error {
	seen := make(map[TestKind]bool, len(kinds))

	for _, k := range kinds {
		if !k.isValid() {
			return fmt.Errorf("%w: %#q (test: %#q); allowed values: %#q, %#q",
				ErrUnknownTestKind, k, testName, TestKindFunctional, TestKindPerformance)
		}

		if seen[k] {
			return fmt.Errorf("test %#q has duplicate kind %#q", testName, k)
		}

		seen[k] = true
	}

	return nil
}

// isValid returns whether the kind is a recognized [TestKind] value.
func (k TestKind) isValid() bool {
	switch k {
	case TestKindFunctional, TestKindPerformance:
		return true
	default:
		return false
	}
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

// MergeUpdatesFrom updates the test config with overrides present in other.
func (t *TestConfig) MergeUpdatesFrom(other *TestConfig) error {
	err := mergo.Merge(t, other, mergo.WithOverride, mergo.WithAppendSlice)
	if err != nil {
		return fmt.Errorf("failed to merge test config:\n%w", err)
	}

	return nil
}

// WithAbsolutePaths returns a copy of the test config with relative file paths converted
// to absolute paths (relative to referenceDir).
func (t *TestConfig) WithAbsolutePaths(referenceDir string) *TestConfig {
	result := &TestConfig{
		Name:             t.Name,
		Description:      t.Description,
		Type:             t.Type,
		Kind:             slices.Clone(t.Kind),
		LongRunning:      t.LongRunning,
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

	return result
}

// TestRef is a reference from an image or component to either a single named test
// or a named test-group. Exactly one of [TestRef.Name] / [TestRef.Group] must be set.
//
// Using a structured ref (rather than a bare string) lets the same association
// list ergonomically mix tests and groups, and leaves room for per-ref metadata
// (e.g., "required vs optional") to be added without a breaking config change.
//
// JSONSchemaExtend uses a value receiver because the invopop/jsonschema library
// only invokes value-receiver methods when reflecting on the type; [TestRef.Validate]
// uses a pointer receiver for consistency with the rest of this package.
//
//nolint:recvcheck // intentional mixed receivers (see comment above).
type TestRef struct {
	// Name references a single [TestConfig] by key.
	Name string `toml:"name,omitempty" json:"name,omitempty" jsonschema:"title=Name,description=Name of a test (key in [tests]); mutually exclusive with 'group'"`

	// Group references a [TestGroupConfig] by key.
	Group string `toml:"group,omitempty" json:"group,omitempty" jsonschema:"title=Group,description=Name of a test-group (key in [test-groups]); mutually exclusive with 'name'"`
}

// Validate checks that exactly one of Name/Group is set on the [TestRef].
func (r *TestRef) Validate(context string) error {
	if r.Name == "" && r.Group == "" {
		return fmt.Errorf("%w: %s must set either 'name' or 'group'",
			ErrInvalidTestRef, context)
	}

	if r.Name != "" && r.Group != "" {
		return fmt.Errorf(
			"%w: %s sets both 'name' = %#q and 'group' = %#q; exactly one is allowed",
			ErrInvalidTestRef, context, r.Name, r.Group,
		)
	}

	return nil
}

// validateTestRefs runs [TestRef.Validate] on every entry and joins errors.
func validateTestRefs(context string, refs []TestRef) error {
	var errs []error

	for i, ref := range refs {
		entryContext := fmt.Sprintf("%s entry %d", context, i)
		if err := ref.Validate(entryContext); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// orderedMapWithConst returns an ordered map containing a single property whose
// schema is `{ "const": value }`. Helper for building `If` schemas that match a
// specific discriminator value.
func orderedMapWithConst(key string, value any) *orderedmap.OrderedMap[string, *jsonschema.Schema] {
	m := orderedmap.New[string, *jsonschema.Schema]()
	m.Set(key, &jsonschema.Schema{Const: value})

	return m
}

// JSONSchemaExtend tightens the generated schema for [TestRef] so editors can
// flag refs that set neither or both of name/group, or empty/whitespace-only
// values. Mirrors the runtime constraints in [TestRef.Validate]; the runtime
// validator is the source of truth.
func (TestRef) JSONSchemaExtend(schema *jsonschema.Schema) {
	// Exactly one of name|group. Encoded as oneOf with `required` on each and
	// `not` excluding the other, which forbids both `{}` and `{name, group}`.
	schema.OneOf = []*jsonschema.Schema{
		{Required: []string{"name"}, Not: &jsonschema.Schema{Required: []string{"group"}}},
		{Required: []string{"group"}, Not: &jsonschema.Schema{Required: []string{"name"}}},
	}

	// Reject empty / leading-whitespace identifiers in both name and group.
	minLen := uint64(1)

	if schema.Properties != nil {
		if nameProp, ok := schema.Properties.Get("name"); ok && nameProp != nil {
			nameProp.MinLength = &minLen
			nameProp.Pattern = `^\S`
		}

		if groupProp, ok := schema.Properties.Get("group"); ok && groupProp != nil {
			groupProp.MinLength = &minLen
			groupProp.Pattern = `^\S`
		}
	}
}

// JSONSchemaExtend tightens [TestConfig] so the framework-specific subtable
// must match the declared `type`, and so the `kind` field is a closed,
// duplicate-free enum. Mirrors the runtime checks in [TestConfig.Validate]
// (subtable/type match) and [validateTestKinds] (closed enum + no duplicates);
// the runtime validator is the source of truth.
//
//   - type=pytest requires `pytest`;
//   - type=lisa forbids `pytest` (no subtable today).
func (TestConfig) JSONSchemaExtend(schema *jsonschema.Schema) {
	// Constrain `kind` items to the closed [TestKind] enum and forbid duplicates,
	// mirroring validateTestKinds. The struct tag only sets the array's title and
	// description; the item-level enum has to be applied here.
	if schema.Properties != nil {
		if kindProp, ok := schema.Properties.Get("kind"); ok && kindProp != nil {
			kindProp.UniqueItems = true

			if kindProp.Items != nil {
				kindProp.Items.Enum = []any{
					string(TestKindFunctional),
					string(TestKindPerformance),
				}
			}
		}
	}

	onlyPytest := &jsonschema.Schema{
		Required: []string{"pytest"},
	}

	noSubtable := &jsonschema.Schema{
		Not: &jsonschema.Schema{Required: []string{"pytest"}},
	}

	schema.AllOf = append(schema.AllOf,
		&jsonschema.Schema{
			If:   &jsonschema.Schema{Properties: orderedMapWithConst("type", string(TestTypePytest))},
			Then: onlyPytest,
		},
		&jsonschema.Schema{
			If:   &jsonschema.Schema{Properties: orderedMapWithConst("type", string(TestTypeLisa))},
			Then: noSubtable,
		},
	)
}
