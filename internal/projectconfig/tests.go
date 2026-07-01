// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"

	"github.com/invopop/jsonschema"
	orderedmap "github.com/pb33f/ordered-map/v2"
)

func orderedMapWithConst(key string, value any) *orderedmap.OrderedMap[string, *jsonschema.Schema] {
	m := orderedmap.New[string, *jsonschema.Schema]()
	m.Set(key, &jsonschema.Schema{Const: value})

	return m
}

// TestDefinition is the new-shape [tests.X] declaration: one configuration of one
// runner/harness with framework-specific options. Framework subtables are kept as
// loosely-typed maps so the resolver can evolve their schemas without requiring
// matching struct changes here.
type TestDefinition struct {
	// Type identifies the framework/runner. Required, and constrained to the
	// closed enum in the schema tag at the schema layer. The loader still
	// accepts unknown values permissively; the resolver is the source of truth.
	Type string `toml:"type" json:"type" jsonschema:"required,title=Type,description=Test framework type,enum=pytest,enum=lisa,enum=tmt"`

	// Human-readable description.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this test"`

	// Kind hints at what the test exercises (e.g. functional, performance).
	Kind []string `toml:"kind,omitempty" json:"kind,omitempty" jsonschema:"title=Kind,description=Test kind hints (e.g. functional or performance)"`

	// LongRunning hints to schedulers/policy that this test may take a long time.
	LongRunning bool `toml:"long-running,omitempty" json:"longRunning,omitempty" jsonschema:"title=Long running,description=Hints that this test may run for hours"`

	// RequiredCapabilities lists capability tokens an image must declare to be a
	// valid target for this test. Tokens are matched against [ImageCapabilities].
	RequiredCapabilities []string `toml:"required-capabilities,omitempty" json:"requiredCapabilities,omitempty" jsonschema:"title=Required capabilities,description=Capability tokens the image must declare"`

	// Framework-specific subtables. Kept untyped so framework schema can evolve
	// independently of the dev-tools type definitions.
	Lisa   map[string]any `toml:"lisa,omitempty"   json:"lisa,omitempty"   jsonschema:"title=LISA config,description=LISA-specific configuration"`
	Tmt    map[string]any `toml:"tmt,omitempty"    json:"tmt,omitempty"    jsonschema:"title=TMT config,description=TMT-specific configuration"`
	Pytest map[string]any `toml:"pytest,omitempty" json:"pytest,omitempty" jsonschema:"title=Pytest config,description=pytest-specific configuration"`
}

// TestGroup is a [test-groups.X] declaration: a named bundle of test references that
// images or components can target via a single name.
type TestGroup struct {
	// Human-readable description.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this test group"`

	// Tests is the ordered list of test or nested-group references that make up
	// the group's membership.
	Tests []TestRef `toml:"tests,omitempty" json:"tests,omitempty" jsonschema:"title=Tests,description=Member references (each is either {name=...} or {group=...})"`
}

// TestRef is a reference to either a test (by name) or another group (by name).
// Exactly one of Name or Group should be set; semantic validation is the resolver's
// responsibility.
type TestRef struct {
	// Name references a [tests.X] entry.
	Name string `toml:"name,omitempty" json:"name,omitempty" jsonschema:"title=Name,description=Name of a test (mutually exclusive with group)"`

	// Group references a [test-groups.X] entry.
	Group string `toml:"group,omitempty" json:"group,omitempty" jsonschema:"title=Group,description=Name of a test group (mutually exclusive with name)"`
}

// ComponentTestsConfig holds the new-shape per-component tests block:
//
//	tests.tests = [{ name = "..." }, { group = "..." }]
type ComponentTestsConfig struct {
	// Tests is the list of test or test-group references that apply to the component.
	Tests []TestRef `toml:"tests,omitempty" json:"tests,omitempty" jsonschema:"title=Tests,description=Per-component test or test-group references"`
}

// Validate checks that exactly one framework subtable is set and it matches Type.
func (t TestDefinition) Validate(testName string) error {
	if t.Type == "" {
		return fmt.Errorf("%w: test %#q is missing required field 'type'", ErrMissingTestField, testName)
	}

	type testTypeRule struct {
		required   string
		disallowed []string
	}

	typeRules := map[string]testTypeRule{
		"pytest": {required: "pytest", disallowed: []string{"lisa", "tmt"}},
		"lisa":   {required: "lisa", disallowed: []string{"pytest", "tmt"}},
		"tmt":    {required: "tmt", disallowed: []string{"pytest", "lisa"}},
	}

	rule, ok := typeRules[t.Type]
	if !ok {
		return fmt.Errorf("%w: %#q (test: %#q)", ErrUnknownTestType, t.Type, testName)
	}

	subtableLengths := map[string]int{
		"pytest": len(t.Pytest),
		"lisa":   len(t.Lisa),
		"tmt":    len(t.Tmt),
	}

	if subtableLengths[rule.required] == 0 {
		return fmt.Errorf(
			"%w: test %#q of type %#q requires a [%s] subtable",
			ErrMissingTestField,
			testName,
			t.Type,
			rule.required,
		)
	}

	for _, subtable := range rule.disallowed {
		if subtableLengths[subtable] > 0 {
			return fmt.Errorf(
				"%w: test %#q of type %#q cannot include subtable '%s'",
				ErrMismatchedTestSubtable,
				testName,
				t.Type,
				subtable,
			)
		}
	}

	return nil
}

// JSONSchemaExtend tightens [TestDefinition] so the framework-specific subtable
// must match the declared type.
func (TestDefinition) JSONSchemaExtend(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}

	onlyPytest := &jsonschema.Schema{
		Required: []string{"pytest"},
		Not: &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
			{Required: []string{"lisa"}},
			{Required: []string{"tmt"}},
		}},
	}

	onlyLisa := &jsonschema.Schema{
		Required: []string{"lisa"},
		Not: &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
			{Required: []string{"pytest"}},
			{Required: []string{"tmt"}},
		}},
	}

	onlyTmt := &jsonschema.Schema{
		Required: []string{"tmt"},
		Not: &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
			{Required: []string{"pytest"}},
			{Required: []string{"lisa"}},
		}},
	}

	schema.AllOf = append(schema.AllOf,
		&jsonschema.Schema{If: &jsonschema.Schema{Properties: orderedMapWithConst("type", "pytest")}, Then: onlyPytest},
		&jsonschema.Schema{If: &jsonschema.Schema{Properties: orderedMapWithConst("type", "lisa")}, Then: onlyLisa},
		&jsonschema.Schema{If: &jsonschema.Schema{Properties: orderedMapWithConst("type", "tmt")}, Then: onlyTmt},
	)
}

// JSONSchemaExtend tightens the generated schema for [TestRef] so editors can
// flag refs that set neither or both of name/group, or empty/whitespace-only values.
// The runtime resolver ([ErrInvalidTestRef]) is the source of truth; this keeps the schema in sync.
func (TestRef) JSONSchemaExtend(schema *jsonschema.Schema) {
	// Exactly one of name|group: encoded as oneOf with `required` on each and
	// `not` excluding the other, which forbids both `{}` and `{name, group}`.
	schema.OneOf = []*jsonschema.Schema{
		{Required: []string{"name"}, Not: &jsonschema.Schema{Required: []string{"group"}}},
		{Required: []string{"group"}, Not: &jsonschema.Schema{Required: []string{"name"}}},
	}

	// Prevent empty or leading-whitespace identifiers in both name and group fields.
	minLen := uint64(1)

	if schema.Properties != nil {
		if nameProp, ok := schema.Properties.Get("name"); ok && nameProp != nil {
			nameProp.MinLength = &minLen
			nameProp.Pattern = "^\\S"
		}

		if groupProp, ok := schema.Properties.Get("group"); ok && groupProp != nil {
			groupProp.MinLength = &minLen
			groupProp.Pattern = "^\\S"
		}
	}
}
