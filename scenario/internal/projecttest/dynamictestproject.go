// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projecttest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoga/deep"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectgen"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

// TestProjectOption is a function that can be used to modify a TestProject in-place.
type DynamicTestProjectOption func(*dynamicTestProject)

// TestProject represents a test project that can be serialized to files and used in tests.
type dynamicTestProject struct {
	configFile projectconfig.ConfigFile

	// Maps relative file path to file contents (as bytes).
	otherFiles map[string][]byte

	// initGitRepo causes [Serialize] to initialize a git repo in the project directory
	// and commit all files. Required for commands that use synthetic history (e.g., render).
	initGitRepo bool
}

// NewDynamicTestProject dynamically constructs a new test project that can later be
// rendered to files and used in a test.
func NewDynamicTestProject(options ...DynamicTestProjectOption) *dynamicTestProject {
	// Start the project off with a basic configuration and an empty set of additional files.
	project := &dynamicTestProject{
		configFile: projectgen.GenerateBasicConfig("test-project"),
		otherFiles: make(map[string][]byte),
	}

	// Make sure we have an empty component map so we can easily add to it later.
	project.configFile.Components = make(map[string]projectconfig.ComponentConfig)

	// Apply any options, which may mutate the project in-place.
	for _, option := range options {
		option(project)
	}

	return project
}

// Serialize writes the project to the specified directory, creating it if it doesn't exist.
func (p *dynamicTestProject) Serialize(t *testing.T, projectDir string) {
	t.Helper()

	// Create the project directory if it doesn't exist.
	require.NoError(t, os.MkdirAll(projectDir, fileperms.PublicDir))

	// Serialize the config file to the default config filename under the project dir.
	require.NoError(t,
		p.configFile.Serialize(afero.NewOsFs(), filepath.Join(projectDir, projectconfig.DefaultConfigFileName)),
	)

	// Write out all files.
	for relativeFilePath, fileContent := range p.otherFiles {
		destFilePath := filepath.Join(projectDir, relativeFilePath)

		// Make sure the containing dir exists.
		destDirPath := filepath.Dir(destFilePath)
		require.NoError(t, os.MkdirAll(destDirPath, fileperms.PublicDir))

		// Write out the file.
		require.NoError(t, os.WriteFile(destFilePath, fileContent, fileperms.PublicFile))
	}

	// Initialize a git repo if requested.
	if p.initGitRepo {
		initProjectGitRepo(t, projectDir)
	}
}

// AddSpec adds (or updates) the contents of a spec file to the project.
func AddSpec(spec *TestSpec) DynamicTestProjectOption {
	return func(p *dynamicTestProject) {
		p.addSpecContents(spec.GetName(), spec.Render())
	}
}

// AddComponents adds (or updates) the configuration for a component to the project. The provided
// configuration value will remain under the ownership of the caller.
func AddComponent(componentConfig *projectconfig.ComponentConfig) DynamicTestProjectOption {
	return func(p *dynamicTestProject) {
		p.addComponent(componentConfig)
	}
}

// UseTestDefaultConfigs configures the project to include the test default configs.
// This adds an include directive to the project's azldev.toml that references the test
// default configs (which must be copied into the container separately using
// [WithTestDefaultConfigs] on the [ProjectTest]).
func UseTestDefaultConfigs() DynamicTestProjectOption {
	return func(p *dynamicTestProject) {
		// Prepend the test default configs include path so it's loaded first.
		// Project-specific settings will override it.
		p.configFile.Includes = append([]string{TestDefaultConfigsIncludePath}, p.configFile.Includes...)
	}
}

func (p *dynamicTestProject) addSpecContents(name, specContent string) {
	// Place specs in their own dir.
	relativeFilePath := filepath.Join("specs", name, name+".spec")

	p.otherFiles[relativeFilePath] = []byte(specContent)
}

func (p *dynamicTestProject) addComponent(componentConfig *projectconfig.ComponentConfig) {
	// Deep-clone the input configuration so we don't accidentally alias any internal pointers.
	p.configFile.Components[componentConfig.Name] = deep.MustCopy(*componentConfig)
}

// AddFile adds an arbitrary file to the project at the specified relative path.
// The path must be relative and must not escape the project directory.
func AddFile(relativePath, content string) DynamicTestProjectOption {
	cleaned := filepath.Clean(relativePath)
	if filepath.IsAbs(cleaned) || cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		panic(fmt.Sprintf("AddFile: path %#q is invalid or escapes the project directory", relativePath))
	}

	return func(p *dynamicTestProject) {
		p.otherFiles[cleaned] = []byte(content)
	}
}

// WithRenderedSpecsDir sets the rendered-specs-dir project config field.
func WithRenderedSpecsDir(dir string) DynamicTestProjectOption {
	return func(p *dynamicTestProject) {
		if p.configFile.Project == nil {
			p.configFile.Project = &projectconfig.ProjectInfo{}
		}

		p.configFile.Project.RenderedSpecsDir = dir
	}
}

// WithGitRepo initializes the project directory as a git repository with an initial
// commit containing all project files. Required for commands that use synthetic history
// (e.g., [component render]).
func WithGitRepo() DynamicTestProjectOption {
	return func(p *dynamicTestProject) {
		p.initGitRepo = true
	}
}

// initProjectGitRepo initializes a git repository in the specified directory and
// commits all files with a default commit message.
func initProjectGitRepo(t *testing.T, dir string) {
	t.Helper()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test User"},
		{"git", "add", "."},
		{"git", "-c", "commit.gpgsign=false", "commit", "-m", "Initial commit"},
	}

	for _, args := range cmds {
		cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "git command %v failed: %s", args, string(output))
	}
}
