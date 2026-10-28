// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package pytest

import (
	"errors"
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/pelletier/go-toml/v2"
)

// RunTestDefinition prepares a configured pytest test and executes it.
func RunTestDefinition(
	env *azldev.Env, resolvedTest projectconfig.ResolvedTest,
	imageConfig *projectconfig.ImageConfig, options *Options,
) error {
	suiteConfig, err := testDefinitionToSuiteConfig(resolvedTest)
	if err != nil {
		return err
	}

	return RunPytestSuite(env, suiteConfig, imageConfig, options)
}

func testDefinitionToSuiteConfig(resolvedTest projectconfig.ResolvedTest) (*projectconfig.TestSuiteConfig, error) {
	pytestConfig, err := decodePytestConfig(resolvedTest.Definition.Pytest)
	if err != nil {
		return nil, fmt.Errorf("decode pytest config for test %#q:\n%w", resolvedTest.Name, err)
	}

	// The stored config keeps 'working-dir' as authored; resolve it relative to
	// the defining config file's directory only here, for execution.
	pytestConfig.WorkingDir = resolvedTest.Definition.PytestWorkingDir()

	suiteConfig := &projectconfig.TestSuiteConfig{
		Name:        resolvedTest.Name,
		Description: resolvedTest.Definition.Description,
		Type:        projectconfig.TestTypePytest,
		Pytest:      pytestConfig,
	}

	if err := suiteConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid pytest test %#q:\n%w", resolvedTest.Name, err)
	}

	return suiteConfig, nil
}

func decodePytestConfig(raw map[string]any) (*projectconfig.PytestConfig, error) {
	if raw == nil {
		return nil, errors.New("missing [pytest] subtable")
	}

	bytes, err := toml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal pytest config:\n%w", err)
	}

	pytestConfig := &projectconfig.PytestConfig{}
	if err := toml.Unmarshal(bytes, pytestConfig); err != nil {
		return nil, fmt.Errorf("unmarshal pytest config:\n%w", err)
	}

	return pytestConfig, nil
}
