// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/invopop/jsonschema"
	orderedmap "github.com/pb33f/ordered-map/v2"
)

// ResolvedTest is a concrete test definition resolved from a direct [tests.X]
// reference or from expansion of a [test-groups.X] reference.
type ResolvedTest struct {
	Name       string
	Definition TestDefinition
}

// TestKind indicates what kind of behavior a test exercises.
type TestKind string

const (
	TestKindFunctional  TestKind = "functional"
	TestKindPerformance TestKind = "performance"
)

func (k TestKind) IsValid() bool {
	switch k {
	case "":
		return true
	case TestKindFunctional,
		TestKindPerformance:
		return true
	default:
		return false
	}
}

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
	// closed enum in the schema tag at the schema layer. [TestDefinition.Validate]
	// rejects unknown values at load time; the schema enum and Validate should be
	// kept in sync.
	Type string `toml:"type" json:"type" jsonschema:"required,title=Type,description=Test framework type,enum=pytest,enum=lisa,enum=tmt"`

	// Human-readable description.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this test"`

	// Kind hints at what the test exercises.
	Kind TestKind `toml:"kind,omitempty" json:"kind,omitempty" jsonschema:"title=Kind,description=Kind hint for the test,enum=functional,enum=performance"`

	// LongRunning hints to schedulers/policy that this test may take a long time.
	LongRunning bool `toml:"long-running,omitempty" json:"longRunning,omitempty" jsonschema:"title=Long running,description=Hints that this test may run for hours"`

	// MetricsEnabled hints to the test execution environment (TEE) / validation
	// service whether metrics from this test run should be collected and stored
	// (e.g. in Kusto). This is metadata only; azldev does not act on it directly.
	MetricsEnabled bool `toml:"metrics-enabled,omitempty" json:"metricsEnabled,omitempty" jsonschema:"title=Metrics enabled,description=Hints to the test execution environment/validation service whether metrics from this test should be collected and stored"`

	// RequiredCapabilities lists capability tokens an image must declare to be a
	// valid target for this test. Tokens are matched against [ImageCapabilities].
	RequiredCapabilities []string `toml:"required-capabilities,omitempty" json:"requiredCapabilities,omitempty" jsonschema:"title=Required capabilities,description=Capability tokens the image must declare"`

	// Framework-specific subtables. Kept untyped so framework schema can evolve
	// independently of the dev-tools type definitions.
	Lisa   map[string]any `toml:"lisa,omitempty"   json:"lisa,omitempty"   jsonschema:"title=LISA config,description=LISA-specific configuration"`
	Tmt    map[string]any `toml:"tmt,omitempty"    json:"tmt,omitempty"    jsonschema:"title=TMT config,description=TMT-specific configuration"`
	Pytest map[string]any `toml:"pytest,omitempty" json:"pytest,omitempty" jsonschema:"title=Pytest config,description=pytest-specific configuration"`

	// dir is the directory of the config file that defined this test. It is not
	// serialized (so 'azldev config dump' is unaffected) and is populated during
	// config load to resolve relative pytest paths at test-execution time.
	dir string
}

// WithAbsolutePaths returns a deep copy of the test definition, recording
// referenceDir (the defining config file's directory) so relative pytest paths
// (e.g. 'working-dir') can be resolved at test-execution time. Path values in
// framework-specific subtables are preserved exactly as authored, so
// 'azldev config dump' shows the user-defined values; see
// [TestDefinition.PytestWorkingDir] for the execution-time resolution.
func (t TestDefinition) WithAbsolutePaths(referenceDir string) TestDefinition {
	result := t
	result.dir = referenceDir
	result.Lisa = cloneStringAnyMap(t.Lisa)
	result.Tmt = cloneStringAnyMap(t.Tmt)
	result.Pytest = cloneStringAnyMap(t.Pytest)

	return result
}

// PytestWorkingDir returns the pytest 'working-dir' resolved to an absolute path
// relative to the config file that defined the test. The stored config keeps the
// authored (possibly relative) value; this resolves it only for execution. An
// empty 'working-dir' returns "".
func (t TestDefinition) PytestWorkingDir() string {
	workingDir, _ := t.Pytest["working-dir"].(string)

	return makeAbsolute(t.dir, workingDir)
}

// TestGroup is a [test-groups.X] declaration: a named bundle of test references that
// images or components can target via a single name.
type TestGroup struct {
	// Human-readable description.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this test group"`

	// Tests is the ordered list of test references that make up the group's
	// membership. Group refs are validated as invalid at load time.
	Tests []TestRef `toml:"tests,omitempty" json:"tests,omitempty" jsonschema:"title=Tests,description=Ordered test references for this group (name only)"`
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

// ResolveTestRefs expands a list of [TestRef] entries into concrete tests.
func (cfg *ProjectConfig) ResolveTestRefs(refs []TestRef) ([]ResolvedTest, error) {
	resolved := make([]ResolvedTest, 0, len(refs))

	for _, ref := range refs {
		hasName := ref.Name != ""
		hasGroup := ref.Group != ""

		switch {
		case hasName && hasGroup:
			return nil, fmt.Errorf(
				"%w: must set exactly one of 'name' or 'group', got both %#q and %#q",
				ErrInvalidTestRef, ref.Name, ref.Group,
			)

		case hasName:
			testDef, ok := cfg.Tests[ref.Name]
			if !ok {
				return nil, fmt.Errorf("%w: %#q", ErrUndefinedTest, ref.Name)
			}

			resolved = append(resolved, ResolvedTest{Name: ref.Name, Definition: testDef})

		case hasGroup:
			group, ok := cfg.TestGroups[ref.Group]
			if !ok {
				return nil, fmt.Errorf("%w: %#q", ErrUndefinedTestGroup, ref.Group)
			}

			for _, groupRef := range group.Tests {
				if groupRef.Group != "" {
					return nil, fmt.Errorf("%w: %#q", ErrNestedTestGroupReference, ref.Group)
				}

				testDef, ok := cfg.Tests[groupRef.Name]
				if !ok {
					return nil, fmt.Errorf("%w: %#q", ErrUndefinedTest, groupRef.Name)
				}

				resolved = append(resolved, ResolvedTest{Name: groupRef.Name, Definition: testDef})
			}

		default:
			return nil, fmt.Errorf("%w: must set exactly one of 'name' or 'group'", ErrInvalidTestRef)
		}
	}

	return resolved, nil
}

// ResolveTestSelectors resolves user-provided selectors, where each selector may
// reference either a concrete test or a test group.
func (cfg *ProjectConfig) ResolveTestSelectors(selectors []string) ([]ResolvedTest, error) {
	refs := make([]TestRef, 0, len(selectors))

	for _, selector := range selectors {
		_, isTest := cfg.Tests[selector]
		_, isGroup := cfg.TestGroups[selector]

		switch {
		case isTest && isGroup:
			return nil, fmt.Errorf("ambiguous test selector %#q matches both [tests] and [test-groups]", selector)
		case isTest:
			refs = append(refs, TestRef{Name: selector})
		case isGroup:
			refs = append(refs, TestRef{Group: selector})
		default:
			return nil, fmt.Errorf("unknown test selector %#q", selector)
		}
	}

	return cfg.ResolveTestRefs(refs)
}

// ResolveImageTests expands the new-style test refs associated with an image.
func (cfg *ProjectConfig) ResolveImageTests(image *ImageConfig) ([]ResolvedTest, error) {
	if image == nil || image.Tests == nil {
		return nil, nil
	}

	return cfg.ResolveTestRefs(image.Tests.Tests)
}

// ResolveComponentTests expands the new-style test refs associated with a component.
func (cfg *ProjectConfig) ResolveComponentTests(component *ComponentConfig) ([]ResolvedTest, error) {
	if component == nil || component.Tests == nil {
		return nil, nil
	}

	return cfg.ResolveTestRefs(component.Tests.Tests)
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}

	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}

	return result
}

// Validate checks that exactly one framework subtable is set and it matches Type.
func (t TestDefinition) Validate(testName string) error {
	if t.Type == "" {
		return fmt.Errorf("%w: test %#q is missing required field 'type'", ErrMissingTestField, testName)
	}

	if !t.Kind.IsValid() {
		return fmt.Errorf("%w: test %#q has invalid kind %#q", ErrUnknownTestKind, testName, t.Kind)
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

	subtablePresence := map[string]bool{
		"pytest": t.Pytest != nil,
		"lisa":   t.Lisa != nil,
		"tmt":    t.Tmt != nil,
	}

	if !subtablePresence[rule.required] {
		return fmt.Errorf(
			"%w: test %#q of type %#q requires a [%s] subtable",
			ErrMissingTestField,
			testName,
			t.Type,
			rule.required,
		)
	}

	for _, subtable := range rule.disallowed {
		if subtablePresence[subtable] {
			return fmt.Errorf(
				"%w: test %#q of type %#q cannot include subtable '%s'",
				ErrMismatchedTestSubtable,
				testName,
				t.Type,
				subtable,
			)
		}
	}

	if t.Type == "lisa" {
		if err := validateLisaSelection(t.Lisa, testName); err != nil {
			return err
		}
	}

	if t.Type == "tmt" {
		if err := validateTmtConfig(t.Tmt, testName); err != nil {
			return err
		}
	}

	if t.Type == "pytest" {
		if err := validatePytestConfig(t.Pytest, testName); err != nil {
			return err
		}
	}

	return nil
}

// validatePytestConfig checks that a [tests.X.pytest] subtable sets both
// 'working-dir' and 'test-paths', which are mandatory for pytest tests.
func validatePytestConfig(pytest map[string]any, testName string) error {
	if !isNonEmptyString(pytest["working-dir"]) {
		return fmt.Errorf(
			"%w: test %#q pytest.working-dir is required and must be a non-empty string",
			ErrInvalidPytestConfig, testName,
		)
	}

	if err := validateStringList(pytest["test-paths"], "pytest.test-paths", testName, ErrInvalidPytestConfig); err != nil {
		return err
	}

	return nil
}

// validateTmtConfig checks that a [tests.X.tmt] subtable sets both 'plan' and
// 'source' (git-url, ref), which are mandatory for tmt tests.
func validateTmtConfig(tmt map[string]any, testName string) error {
	if !isNonEmptyString(tmt["plan"]) {
		return fmt.Errorf(
			"%w: test %#q tmt.plan is required and must be a non-empty string",
			ErrInvalidTmtConfig, testName,
		)
	}

	rawSource, hasSource := tmt["source"]
	if !hasSource {
		return fmt.Errorf(
			"%w: test %#q tmt.source (git-url, ref) is required",
			ErrInvalidTmtConfig, testName,
		)
	}

	sourceMap, ok := rawSource.(map[string]any)
	if !ok {
		return fmt.Errorf(
			"%w: test %#q tmt.source must be a table with 'git-url' and 'ref'",
			ErrInvalidTmtConfig, testName,
		)
	}

	gitURL, _ := sourceMap["git-url"].(string)
	ref, _ := sourceMap["ref"].(string)
	source := GitSourceConfig{GitURL: gitURL, Ref: ref}

	if err := source.Validate(fmt.Sprintf("test %#q tmt.source", testName)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidTmtConfig, err)
	}

	return nil
}

// JSONSchemaExtend narrows [TestGroup.Tests] to name-only refs so editor-time
// validation matches runtime behavior (group refs in [test-groups] are rejected).
func (TestGroup) JSONSchemaExtend(schema *jsonschema.Schema) {
	if schema == nil || schema.Properties == nil {
		return
	}

	testsProp, ok := schema.Properties.Get("tests")
	if !ok || testsProp == nil {
		return
	}

	minLen := uint64(1)
	itemProps := orderedmap.New[string, *jsonschema.Schema]()
	itemProps.Set("name", &jsonschema.Schema{
		Type:        "string",
		MinLength:   &minLen,
		Pattern:     "^\\S",
		Description: "Name of a test",
	})

	testsProp.Items = &jsonschema.Schema{
		Type:       "object",
		Properties: itemProps,
		Required:   []string{"name"},
		Not: &jsonschema.Schema{
			Required: []string{"group"},
		},
	}
}

func validateLisaSelection(lisa map[string]any, testName string) error {
	hasSelector := false

	if rawCriteria, ok := lisa["criteria"]; ok {
		hasSelector = true

		if err := validateLisaCriteria(rawCriteria, testName); err != nil {
			return err
		}
	}

	if rawName, ok := lisa["testcase-name"]; ok {
		hasSelector = true

		if !isNonEmptyString(rawName) {
			return fmt.Errorf(
				"%w: test %#q lisa.testcase-name must be a non-empty string",
				ErrInvalidLisaSelection,
				testName,
			)
		}
	}

	if rawName, ok := lisa["name"]; ok {
		hasSelector = true

		if !isNonEmptyString(rawName) {
			return fmt.Errorf(
				"%w: test %#q lisa.name must be a non-empty string",
				ErrInvalidLisaSelection,
				testName,
			)
		}
	}

	if rawNames, ok := lisa["testcase-names"]; ok {
		hasSelector = true

		if err := validateStringList(rawNames, "lisa.testcase-names", testName, ErrInvalidLisaSelection); err != nil {
			return err
		}
	}

	if !hasSelector {
		return fmt.Errorf(
			"%w: test %#q of type %#q must set at least one LISA selector: criteria, testcase-name, testcase-names, or name",
			ErrInvalidLisaSelection,
			testName,
			"lisa",
		)
	}

	return nil
}

func validateLisaCriteria(rawCriteria any, testName string) error {
	criteriaList, err := normalizeCriteriaList(rawCriteria)
	if err != nil {
		return fmt.Errorf("%w: test %#q lisa.criteria %w", ErrInvalidLisaSelection, testName, err)
	}

	for idx, criteria := range criteriaList {
		if err := validateSingleLisaCriteria(criteria, testName, idx); err != nil {
			return err
		}
	}

	return nil
}

func normalizeCriteriaList(rawCriteria any) ([]map[string]any, error) {
	switch criteriaValue := rawCriteria.(type) {
	case map[string]any:
		if len(criteriaValue) == 0 {
			return nil, errors.New("must not be empty")
		}

		return []map[string]any{criteriaValue}, nil
	case []any:
		if len(criteriaValue) == 0 {
			return nil, errors.New("must not be an empty list")
		}

		result := make([]map[string]any, 0, len(criteriaValue))

		for entryIndex, item := range criteriaValue {
			criteriaMap, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("entry %d must be a table/object", entryIndex)
			}

			if len(criteriaMap) == 0 {
				return nil, fmt.Errorf("entry %d must not be empty", entryIndex)
			}

			result = append(result, criteriaMap)
		}

		return result, nil
	default:
		return nil, errors.New("must be a table or list of tables")
	}
}

func validateSingleLisaCriteria(criteria map[string]any, testName string, idx int) error {
	allowedKeys := map[string]bool{
		"name":           true,
		"area":           true,
		"category":       true,
		"priority":       true,
		"tags":           true,
		"testcase-name":  true,
		"testcase-names": true,
	}

	hasSelector := false

	for key, value := range criteria {
		if !allowedKeys[key] {
			return fmt.Errorf(
				"%w: test %#q lisa.criteria[%d] contains unsupported selector %#q",
				ErrInvalidLisaSelection,
				testName,
				idx,
				key,
			)
		}

		switch key {
		case "name", "area", "category", "testcase-name":
			if !isNonEmptyString(value) {
				return fmt.Errorf(
					"%w: test %#q lisa.criteria[%d].%s must be a non-empty string",
					ErrInvalidLisaSelection,
					testName,
					idx,
					key,
				)
			}

			hasSelector = true
		case "priority":
			if err := validateLisaPriority(value, testName, idx); err != nil {
				return err
			}

			hasSelector = true
		case "tags", "testcase-names":
			fieldName := "lisa.criteria[" + strconv.Itoa(idx) + "]." + key

			if err := validateStringList(value, fieldName, testName, ErrInvalidLisaSelection); err != nil {
				return err
			}

			hasSelector = true
		}
	}

	if !hasSelector {
		return fmt.Errorf(
			"%w: test %#q lisa.criteria[%d] must include at least one selector",
			ErrInvalidLisaSelection,
			testName,
			idx,
		)
	}

	return nil
}

func validateLisaPriority(value any, testName string, idx int) error {
	if isLisaPriorityValue(value) {
		return nil
	}

	return fmt.Errorf(
		"%w: test %#q lisa.criteria[%d].priority must be an integer 0..4 or a non-empty list of integers 0..4",
		ErrInvalidLisaSelection,
		testName,
		idx,
	)
}

func isLisaPriorityValue(value any) bool {
	if parsed, ok := parseLisaPriority(value); ok {
		return parsed >= 0 && parsed <= 4
	}

	priorityList, ok := value.([]any)
	if !ok || len(priorityList) == 0 {
		return false
	}

	for _, item := range priorityList {
		parsed, ok := parseLisaPriority(item)
		if !ok || parsed < 0 || parsed > 4 {
			return false
		}
	}

	return true
}

func parseLisaPriority(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}

		return int(typed), true
	default:
		return 0, false
	}
}

// validateStringList checks that value is a non-empty list of non-empty strings,
// wrapping any failure with the caller-supplied sentinel error so the error chain
// reflects the actual validation context (e.g. lisa vs. pytest) rather than always
// being attributed to LISA validation.
func validateStringList(value any, fieldName string, testName string, sentinel error) error {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return fmt.Errorf(
			"%w: test %#q %s must be a non-empty list of non-empty strings",
			sentinel,
			testName,
			fieldName,
		)
	}

	for _, item := range items {
		if !isNonEmptyString(item) {
			return fmt.Errorf(
				"%w: test %#q %s must be a non-empty list of non-empty strings",
				sentinel,
				testName,
				fieldName,
			)
		}
	}

	return nil
}

func isNonEmptyString(value any) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}

	return strings.TrimSpace(s) != ""
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
