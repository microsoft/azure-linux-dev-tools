// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
)

// CheckConfig encapsulates configuration for the %check section of a spec file.
type CheckConfig struct {
	// Skip indicates whether the %check section should be disabled for this component.
	Skip bool `toml:"skip,omitempty" json:"skip,omitempty" jsonschema:"title=Skip check,description=Disables the %check section by prepending 'exit 0' when set to true"`
	// SkipReason provides a required justification when Skip is true.
	SkipReason string `toml:"skip_reason,omitempty" json:"skipReason,omitempty" jsonschema:"title=Skip reason,description=Required justification for skipping the %check section"`
}

// Validate checks that required fields are set when Skip is true.
func (c *CheckConfig) Validate() error {
	if !c.Skip {
		return nil
	}

	if c.SkipReason == "" {
		return errors.New("check.skip_reason is required when check.skip is true")
	}

	return nil
}

// Encapsulates configuration for building a component. Configuration for how to acquire
// or prepare the sources for a component are out of scope.
type ComponentBuildConfig struct {
	// Which features should be enabled via `with` options to the builder.
	With []string `toml:"with,omitempty" json:"with,omitempty" jsonschema:"title=With options,description='with' options to pass to the builder."`
	// Which features should be disabled via `without` options to the builder.
	Without []string `toml:"without,omitempty" json:"without,omitempty" jsonschema:"title=Without options,description='without' options to pass to the builder."`
	// Macro definitions.
	Defines map[string]string `toml:"defines,omitempty" json:"defines,omitempty" jsonschema:"title=Macro definitions,description=Macro definitions to pass to the builder."`
	// Undefine macros that would otherwise be defined by the component configuration.
	Undefines []string `toml:"undefines,omitempty" json:"undefines,omitempty" jsonschema:"title=Undefined macros,description=Macro names to undefine when passing to the builder."`
	// Check section configuration.
	Check CheckConfig `toml:"check,omitempty" json:"check,omitempty" jsonschema:"title=Check configuration,description=Configuration for the %check section"`
	// Failure configuration and policy for this component's build.
	Failure ComponentBuildFailureConfig `toml:"failure,omitempty" json:"failure,omitempty" jsonschema:"title=Build failure configuration,description=Configuration and policy regarding build failures for this component."`
	// Hints for how or when to build the component; must not be required for correctness of builds.
	Hints ComponentBuildHints `toml:"hints,omitempty" json:"hints,omitempty" jsonschema:"title=Build hints,description=Non-essential hints for how or when to build the component."`
}

// ComponentBuildFailureConfig encapsulates configuration and policy regarding a component's
// build failure.
type ComponentBuildFailureConfig struct {
	// Expected indicates that this component is expected to fail building. This is intended to be used as a temporary
	// marker for components that are expected to fail until they can be fixed.
	Expected bool `toml:"expected,omitempty" json:"expected,omitempty" jsonschema:"title=Expected failure,description=Indicates that this component is expected to fail building."`
	// ExpectedReason provides a required justification when Expected is true.
	ExpectedReason string `toml:"expected-reason,omitempty" json:"expectedReason,omitempty" jsonschema:"title=Expected failure reason,description=Required justification for why this component is expected to fail building."`
}

// ComponentBuildHints encapsulates non-essential hints for how or when to build a component.
// These are not required for correctness of builds, but may be used by tools to provide guidance
// or optimizations.
type ComponentBuildHints struct {
	// Expensive indicates that building this component is relatively expensive compared to the rest of the distro.
	Expensive bool `toml:"expensive,omitempty" json:"expensive,omitempty" jsonschema:"title=Expensive to build,description=Indicates that building this component is expensive and should be carefully considered when scheduling."`
}

// Validate checks that the build configuration is valid.
func (c *ComponentBuildConfig) Validate() error {
	if err := c.Check.Validate(); err != nil {
		return fmt.Errorf("invalid build configuration:\n%w", err)
	}

	return nil
}
