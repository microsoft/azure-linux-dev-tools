// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectgen

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

const (
	defaultIntermediateOutputDir = "build"
	DefaultLogDir                = "build/logs"
	DefaultWorkDir               = "build/work"
	defaultOutputDir             = "out"

	defaultFilePerms = 0o644
)

// ErrProjectRootAlreadyExists is returned when a project root directory already exists but an attempt
// is made to create it.
var ErrProjectRootAlreadyExists = errors.New("project root already exists")

// Options for creating a new project.
type NewProjectOptions struct{}

// CreateNewProject creates a new project at the specified directory path, using the provided options.
func CreateNewProject(fs opctx.FS, projectPath string, options *NewProjectOptions) error {
	slog.Debug("Creating new project", "path", projectPath)

	absProjectPath, err := filepath.Abs(projectPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for new project:\n%w", err)
	}

	// Make sure the path doesn't already exist.
	_, statErr := fs.Stat(absProjectPath)
	if statErr == nil {
		return fmt.Errorf("%w: %s", ErrProjectRootAlreadyExists, absProjectPath)
	}

	if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("failed to check project path '%s':\n%w", absProjectPath, statErr)
	}

	// Create the dir.
	err = fileutils.MkdirAll(fs, absProjectPath)
	if err != nil {
		return fmt.Errorf("failed to create project dir '%s':\n%w", absProjectPath, err)
	}

	// Initialize the project.
	err = InitializeProject(fs, projectPath, options)
	if err != nil {
		return fmt.Errorf("failed to init project dir '%s':\n%w", absProjectPath, err)
	}

	// Write out a basic .gitignore.
	err = writeGitIgnoreFile(fs, projectPath)
	if err != nil {
		return err
	}

	return nil
}

// InitializeProject initializes project configuration within a given existing directory, using the provided options.
func InitializeProject(fs opctx.FS, projectPath string, options *NewProjectOptions) error {
	var pathStat os.FileInfo

	// Make sure the dir exists.
	pathStat, err := fs.Stat(projectPath)
	if err != nil {
		return fmt.Errorf("failed to check project path '%s':\n%w", projectPath, err)
	}

	if !pathStat.IsDir() {
		return fmt.Errorf("path '%s' is not a directory", projectPath)
	}

	// Derive some basics.
	projectName := filepath.Base(filepath.Clean(projectPath))

	// Create a basic config.
	err = writeBasicConfigFile(fs, projectName, projectPath)
	if err != nil {
		return err
	}

	// Success!
	slog.Info("Initialized project", "path", projectPath)

	return nil
}

// GenerateBasicConfig generates a basic project configuration file with default paths and settings.
func GenerateBasicConfig(projectName string) projectconfig.ConfigFile {
	// Set basic directory paths as starting points for the user to customize.
	return projectconfig.ConfigFile{
		SchemaURI: projectconfig.DefaultSchemaURI,
		Project: &projectconfig.ProjectInfo{
			Description: projectName,
			LogDir:      DefaultLogDir,
			WorkDir:     DefaultWorkDir,
			OutputDir:   defaultOutputDir,
		},

		// Add a default component group that builds all *.spec files as components.
		ComponentGroups: map[string]projectconfig.ComponentGroupConfig{
			"default": {
				SpecPathPatterns: []string{
					"**/*.spec",
				},

				ExcludedPathPatterns: []string{
					filepath.Join(defaultIntermediateOutputDir, "**"),
					filepath.Join(defaultOutputDir, "**"),
				},
			},
		},
	}
}

func writeBasicConfigFile(fs opctx.FS, projectName, projectDirPath string) error {
	configFilePath := filepath.Join(projectDirPath, projectconfig.DefaultConfigFileName)

	// Make sure the config file doesn't already exist; we don't want to overwrite the user's file.
	if _, statErr := fs.Stat(configFilePath); statErr == nil {
		return fmt.Errorf("config file '%s' already exists", configFilePath)
	}

	// Write out basic config.
	err := GenerateBasicConfig(projectName).Serialize(fs, configFilePath)
	if err != nil {
		return fmt.Errorf("failed to save new config file to '%s':\n%w", configFilePath, err)
	}

	return nil
}

func writeGitIgnoreFile(fs opctx.FS, projectDirPath string) error {
	gitignoreFilePath := filepath.Join(projectDirPath, ".gitignore")

	gitIgnoreLines := []string{
		"# Ignore build log and work dirs",
		defaultIntermediateOutputDir + "/",
		"",
		"# Ignore build output",
		defaultOutputDir + "/",
	}

	gitIgnoreContents := strings.Join(gitIgnoreLines, "\n") + "\n"

	err := fileutils.WriteFile(fs, gitignoreFilePath, []byte(gitIgnoreContents), defaultFilePerms)
	if err != nil {
		return fmt.Errorf("failed to save new .gitignore file to '%s':\n%w", gitignoreFilePath, err)
	}

	return nil
}
