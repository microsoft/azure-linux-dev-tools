// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projecttest

import (
	"path"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

// TemplatedTestProjectOption is a function that can be used to configure a [templatedTestProject].
type TemplatedTestProjectOption func(*templatedTestProject)

type templatedTestProject struct {
	templateDir           string
	useTestDefaultConfigs bool
}

// TemplatedUseTestDefaultConfigs configures the templated project to include the test default configs.
// This modifies the project's azldev.toml after copying to add an include directive that
// references the test default configs (which must be copied into the container separately
// using [WithTestDefaultConfigs] on the [ProjectTest]).
func TemplatedUseTestDefaultConfigs() TemplatedTestProjectOption {
	return func(p *templatedTestProject) {
		p.useTestDefaultConfigs = true
	}
}

func NewTemplatedTestProject(
	t *testing.T,
	templateDir string,
	options ...TemplatedTestProjectOption,
) *templatedTestProject {
	t.Helper()

	// Resolve any relative paths.
	if !path.IsAbs(templateDir) {
		// Resolve paths relative to the directory that our caller's source file is located in.
		_, callerPath, _, _ := runtime.Caller(1)

		callerPath, err := filepath.Abs(callerPath)
		require.NoError(t, err)

		dirPath := filepath.Dir(callerPath)

		templateDir = filepath.Join(dirPath, templateDir)
	}

	project := &templatedTestProject{
		templateDir: templateDir,
	}

	for _, option := range options {
		option(project)
	}

	return project
}

func (p *templatedTestProject) Serialize(t *testing.T, projectDir string) {
	t.Helper()

	osFS := afero.NewOsFs()

	// Copy the template dir to the destination.
	err := fileutils.CopyDirRecursive(notDryRun{}, osFS, p.templateDir, projectDir, fileutils.CopyDirOptions{})
	require.NoError(t, err)

	// If test default configs are requested, modify the azldev.toml to include them.
	if p.useTestDefaultConfigs {
		configFilePath := filepath.Join(projectDir, projectconfig.DefaultConfigFileName)

		// Read and parse the existing config file.
		configBytes, err := afero.ReadFile(osFS, configFilePath)
		require.NoError(t, err, "failed to read config file for test default configs injection")

		var config projectconfig.ConfigFile
		require.NoError(t, toml.Unmarshal(configBytes, &config),
			"failed to parse config file for test default configs injection")

		// Prepend the test default configs include path.
		config.Includes = append([]string{TestDefaultConfigsIncludePath}, config.Includes...)

		// Write back the modified config file.
		modifiedBytes, err := toml.Marshal(config)
		require.NoError(t, err, "failed to marshal config file with test default configs")

		require.NoError(t, afero.WriteFile(osFS, configFilePath, modifiedBytes, fileperms.PublicFile),
			"failed to write config file with test default configs")
	}
}

type notDryRun struct{}

func (notDryRun) DryRun() bool {
	return false
}
