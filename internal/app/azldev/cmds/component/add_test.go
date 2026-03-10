// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testConfigPath = "/project/azldev.toml"

func TestAddComponentsToConfig(t *testing.T) {
	// Define a set of data-driven test cases for adding components to the project config.
	tests := []struct {
		name           string
		initialConfig  string
		existingNames  []string
		namesToAdd     []string
		expectedError  bool
		expectedConfig string
	}{
		{
			name:           "No names",
			initialConfig:  "",
			existingNames:  []string{},
			namesToAdd:     []string{},
			expectedError:  true,
			expectedConfig: "",
		},
		{
			name:          "Add single component to empty config",
			initialConfig: "",
			existingNames: []string{},
			namesToAdd:    []string{"test-component"},
			expectedError: false,
			expectedConfig: `[components.test-component]
`,
		},
		{
			name: "Add single component to existing config without components section",
			initialConfig: `[project]
description = "test project"
`,
			existingNames: []string{},
			namesToAdd:    []string{"test-component"},
			expectedError: false,
			expectedConfig: `[project]
description = "test project"

[components.test-component]
`,
		},
		{
			name:          "Add multiple components to empty config",
			initialConfig: "",
			existingNames: []string{},
			namesToAdd:    []string{"component-a", "component-b", "component-c"},
			expectedError: false,
			expectedConfig: `[components.component-a]

[components.component-b]

[components.component-c]
`,
		},
		{
			name: "Add component to config with existing components section",
			initialConfig: `[components]

[components.existing-component]
`,
			existingNames: []string{},
			namesToAdd:    []string{"new-component"},
			expectedError: false,
			expectedConfig: `[components]

[components.existing-component]

[components.new-component]
`,
		},
		{
			name: "Add component that already exists (should not duplicate)",
			initialConfig: `[components.test-component]
`,
			existingNames: []string{"test-component"},
			namesToAdd:    []string{"test-component"},
			expectedError: true,
			expectedConfig: `[components.test-component]
`,
		},
		{
			name: "Add multiple components with some existing",
			initialConfig: `[components.existing-component]
`,
			existingNames: []string{"existing-component"},
			namesToAdd:    []string{"existing-component", "new-component-1", "new-component-2"},
			expectedError: true,
			expectedConfig: `[components.existing-component]
`,
		},
		{
			name: "Add components to config with comments and other sections",
			initialConfig: `# This is a test config
[project]
description = "test project"

# Component definitions
[components]

# This component already exists
[components.existing-component]

[distros.test]
description = "test distro"
`,
			existingNames: []string{},
			namesToAdd:    []string{"new-component"},
			expectedError: false,
			expectedConfig: `# This is a test config
[project]
description = "test project"

# Component definitions
[components]

# This component already exists
[components.existing-component]

[distros.test]
description = "test distro"

[components.new-component]
`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			// Create test context with in-memory filesystem
			ctx := testctx.NewCtx()

			// Write initial config file
			require.NoError(t, fileutils.WriteFile(ctx.FS(),
				testConfigPath, []byte(testCase.initialConfig), fileperms.PrivateFile))

			// Create test project config
			config := &projectconfig.ProjectConfig{
				RootConfigFilePath: testConfigPath,
				Components:         make(map[string]projectconfig.ComponentConfig),
			}

			// Simulate existing names.
			for _, name := range testCase.existingNames {
				config.Components[name] = projectconfig.ComponentConfig{
					Name: name,
				}
			}

			// Create options
			options := &component.AddComponentOptions{}

			// Call function under test
			err := component.AddComponentsToConfig(ctx.FS(), config, options, testCase.namesToAdd)
			if testCase.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// In both error and non-error cases, verify the resulting config file contents.
			actualConfigBytes, err := fileutils.ReadFile(ctx.FS(), testConfigPath)
			require.NoError(t, err)

			actualConfig := string(actualConfigBytes)
			assert.Equal(t, testCase.expectedConfig, actualConfig)
		})
	}
}
