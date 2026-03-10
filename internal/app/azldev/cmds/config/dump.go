// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type configDumpFormat string

const (
	ConfigDumpFormatTOML configDumpFormat = "toml"
	ConfigDumpFormatJSON configDumpFormat = "json"
)

// Assert that ConfigDumpFormat implements the [pflag.Value] interface.
var _ pflag.Value = (*configDumpFormat)(nil)

func (f *configDumpFormat) String() string {
	return string(*f)
}

// Parses the format from a string; used by command-line parser.
func (f *configDumpFormat) Set(value string) error {
	switch value {
	case "toml":
		*f = ConfigDumpFormatTOML
	case "json":
		*f = ConfigDumpFormatJSON
	default:
		return fmt.Errorf("unsupported format: %#q", value)
	}

	return nil
}

// Returns a descriptive string used in command-line help.
func (f *configDumpFormat) Type() string {
	return "fmt"
}

// Called once when the app is initialized; registers any commands or callbacks with the app.
func dumpConfigOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(newDumpCmd())
}

func newDumpCmd() *cobra.Command {
	configDumpFormat := ConfigDumpFormatTOML

	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Dump the current configuration",
		Long: `Dump the fully resolved project configuration.

Shows the merged result of all config files (embedded defaults, project
config, includes, and any extra --config-file arguments) after inheritance
and merge rules have been applied. Useful for debugging configuration
issues or inspecting effective values.`,
		Example: `  # Dump config as TOML (default)
  azldev config dump

  # Dump config as JSON
  azldev config dump -f json

  # Dump config quietly for scripting
  azldev config dump -q -f json`,
		RunE: azldev.RunFunc(func(env *azldev.Env) (interface{}, error) {
			configText, err := DumpConfig(env, configDumpFormat)
			if err != nil {
				return nil, err
			}

			fmt.Println(configText)

			return "", nil
		}),
		// Allowing 'config dump' to be run as root for two reasons:
		// 1. It doesn't modify anything -- even as root it's safe.
		// 2. There are container-based scenarios where the default user is root.
		Annotations: map[string]string{
			azldev.CommandAnnotationRootOK: "true",
		},
	}

	cmd.Flags().VarP(&configDumpFormat, "format", "f", "Output format {json, toml}")

	azldev.ExportAsMCPTool(cmd)

	return cmd
}

func DumpConfig(env *azldev.Env, format configDumpFormat) (string, error) {
	switch format {
	case ConfigDumpFormatTOML:
		tomlBytes, err := toml.Marshal(env.Config())
		if err != nil {
			return "", fmt.Errorf("failed to serialize config to TOML:\n%w", err)
		}

		return string(tomlBytes), nil
	case ConfigDumpFormatJSON:
		jsonBytes, err := json.MarshalIndent(env.Config(), "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to serialize config to JSON:\n%w", err)
		}

		return string(jsonBytes), nil
	default:
		return "", fmt.Errorf("unsupported format: %#q", format)
	}
}
