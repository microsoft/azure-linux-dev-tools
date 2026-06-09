// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"

	"dario.cat/mergo"
)

var (
	// ErrDuplicateTestGroups is returned when duplicate conflicting test-group definitions are found.
	ErrDuplicateTestGroups = errors.New("duplicate test-group")
	// ErrUndefinedTestGroup is returned when something references a test-group name that is not defined.
	ErrUndefinedTestGroup = errors.New("undefined test-group reference")
)

// TestGroupConfig is a named bundle of tests. Test-groups are referenced from
// images and components via [TestRef] entries with [TestRef.Group] set. A group
// is a stable, named handle for an underlying set of tests, allowing the
// member list to evolve without churning every image/component reference.
//
// Group membership is a single layer of indirection: groups list test names
// only, not other groups.
type TestGroupConfig struct {
	// The test-group's name; not present in serialized TOML files (populated from the map key).
	Name string `toml:"-" json:"name" table:",sortkey"`

	// Description of the test-group.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this test-group"`

	// Tests lists the names of [TestConfig]s that belong to this group.
	Tests []string `toml:"tests,omitempty" json:"tests,omitempty" jsonschema:"title=Tests,description=Names of tests (keys in [tests]) that belong to this group"`

	// Reference to the source config file that this definition came from; not present
	// in serialized files.
	SourceConfigFile *ConfigFile `toml:"-" json:"-" table:"-"`
}

// Validate checks that the test-group has a non-empty name and that the
// members slice does not contain duplicates. Whether the named tests actually
// exist is checked at project level by [validateTestGroupMembership].
func (g *TestGroupConfig) Validate() error {
	seen := make(map[string]bool, len(g.Tests))

	for _, name := range g.Tests {
		if name == "" {
			return fmt.Errorf("test-group %#q contains an empty test name", g.Name)
		}

		if seen[name] {
			return fmt.Errorf("test-group %#q contains duplicate test %#q", g.Name, name)
		}

		seen[name] = true
	}

	return nil
}

// MergeUpdatesFrom updates the test-group config with overrides present in other.
func (g *TestGroupConfig) MergeUpdatesFrom(other *TestGroupConfig) error {
	err := mergo.Merge(g, other, mergo.WithOverride, mergo.WithAppendSlice)
	if err != nil {
		return fmt.Errorf("failed to merge test-group config:\n%w", err)
	}

	return nil
}

// WithAbsolutePaths returns a copy of the test-group config. It exists for
// symmetry with the other config types; [TestGroupConfig] has no path fields,
// so the copy is byte-identical aside from defensive slice cloning.
func (g *TestGroupConfig) WithAbsolutePaths(_ string) *TestGroupConfig {
	result := &TestGroupConfig{
		Name:             g.Name,
		Description:      g.Description,
		Tests:            append([]string(nil), g.Tests...),
		SourceConfigFile: g.SourceConfigFile,
	}

	return result
}
