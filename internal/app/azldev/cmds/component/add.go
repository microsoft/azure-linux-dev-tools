// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/creachadair/tomledit"
	"github.com/creachadair/tomledit/parser"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
)

// Options for adding components to the project configuration.
type AddComponentOptions struct{}

func addOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewComponentAddCommand())
}

// Constructs a [cobra.Command] for "component add" CLI subcommand.
func NewComponentAddCommand() *cobra.Command {
	options := &AddComponentOptions{}

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add component(s) to this project",
		Long: `Add one or more components to the project configuration.

Each component name is added as a bare entry in the root config file,
inheriting defaults from the distro configuration. If a component with
the same name already exists, the command returns an error.`,
		Example: `  # Add a single component
  azldev component add curl

  # Add multiple components at once
  azldev component add curl wget bash`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			return true, AddComponentsToConfig(env.FS(), env.Config(), options, args)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	return cmd
}

// Updates project config to include new software components.
func AddComponentsToConfig(
	fs opctx.FS, config *projectconfig.ProjectConfig, options *AddComponentOptions, names []string,
) error {
	// Bail early if there's nothing to be done.
	if len(names) == 0 {
		return errors.New("no component names provided")
	}

	doc, err := parseConfigFileForEditing(fs, config.RootConfigFilePath)
	if err != nil {
		return err
	}

	// Add sub-tables for each component name
	for _, name := range names {
		// Make sure the component doesn't already exist.
		if _, exists := config.Components[name]; exists {
			return fmt.Errorf("component %q already exists in project config", name)
		}

		// Compose the key name for this component's name.
		componentKey := parser.Key{"components", name}

		// Check if component sub-table already exists
		if doc.First(componentKey...) == nil {
			// Create a new section for this component
			doc.Sections = append(doc.Sections, &tomledit.Section{
				Heading: &parser.Heading{
					Name: componentKey,
				},
			})
		}
	}

	err = updateConfigFileFromDoc(fs, config.RootConfigFilePath, doc)
	if err != nil {
		return fmt.Errorf("failed to update project config file %q:\n%w", config.RootConfigFilePath, err)
	}

	return nil
}

// parseConfigFileForEditing parses the input TOML config file for structure-preserving editing.
// It will not be fully deserialized into golang structs, but will retain comments and other
// human-authored structure.
func parseConfigFileForEditing(fs opctx.FS, configFilePath string) (*tomledit.Document, error) {
	configFileBytes, err := fileutils.ReadFile(fs, configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read project config file %q:\n%w", configFilePath, err)
	}

	configFileReader := bytes.NewReader(configFileBytes)

	doc, err := tomledit.Parse(configFileReader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse project config file %q:\n%w", configFilePath, err)
	}

	return doc, nil
}

// updateConfigFileFromDoc writes the given TOML document to the given config file path, overwriting
// any existing file present at that path. It uses the [fileutils.FileUpdateWriter] facility to
// use atomic file updates when used on compatible filesystems; this decreases the likelihood of
// data loss or corruption of the original file in case of error.
func updateConfigFileFromDoc(fs opctx.FS, configFilePath string, doc *tomledit.Document) error {
	var formatter tomledit.Formatter

	updateWriter, err := fileutils.NewFileUpdateWriter(fs, configFilePath)
	if err != nil {
		return fmt.Errorf("failed to create file update writer for %q:\n%w", configFilePath, err)
	}

	err = formatter.Format(updateWriter, doc)
	if err != nil {
		return fmt.Errorf("failed to update project config file %q:\n%w", configFilePath, err)
	}

	// Commit!
	err = updateWriter.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit updates to project config file %q:\n%w", configFilePath, err)
	}

	return nil
}
