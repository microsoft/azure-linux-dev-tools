// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package advanced

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/cttools"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func ctToolsOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewCTToolsCmd())
}

// Constructs a [cobra.Command] for the "ct-tools" subcommand hierarchy.
func NewCTToolsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ct-tools",
		Short: "Control Tower tools",
		Long: `Control Tower tools for working with distro configuration.

Provides utilities for parsing, resolving, and dumping the fully merged
distro configuration used by Control Tower environments.`,
	}

	cmd.AddCommand(NewConfigDumpCmd())

	return cmd
}

// Options controlling the config-dump command.
type ConfigDumpOptions struct {
	// Path to the top-level TOML configuration file.
	ConfigPath string
	// The Control Tower environment to filter for (e.g. "ct-dev").
	Environment string
	// Output format: "json" or "yaml".
	Format string
}

// Constructs a [cobra.Command] for the "ct-tools config-dump" subcommand.
func NewConfigDumpCmd() *cobra.Command {
	options := &ConfigDumpOptions{}

	cmd := &cobra.Command{
		Use:   "config-dump",
		Short: "Dump fully resolved distro config",
		Long: `Parse and resolve all distro configuration TOML files starting from a
top-level file, merge includes, expand all templates (koji-targets,
build-roots, mock-options), and output the fully resolved configuration
filtered to a specific Control Tower environment.`,
		Example: `  # Dump config for ct-dev as JSON
  azldev advanced ct-tools config-dump --config /path/to/azurelinux.toml --environment ct-dev

  # Dump config for ct-prod as YAML
  azldev advanced ct-tools config-dump --config /path/to/azurelinux.toml --environment ct-prod --format yaml`,
		RunE: azldev.RunFuncWithoutRequiredConfig(func(env *azldev.Env) (results interface{}, err error) {
			return nil, RunConfigDump(options)
		}),
	}

	cmd.Flags().StringVar(&options.ConfigPath, "config", "", "Path to the top-level TOML configuration file")

	envHelp := "Control Tower environment name " +
		"(e.g. ct-dev, ct-staging, ct-prod)"
	cmd.Flags().StringVar(&options.Environment, "environment", "", envHelp)
	cmd.Flags().StringVar(&options.Format, "format", "json", "Output format: json or yaml")

	_ = cmd.MarkFlagRequired("config")
	_ = cmd.MarkFlagRequired("environment")

	return cmd
}

// RunConfigDump loads, resolves, filters, and outputs the distro configuration.
func RunConfigDump(options *ConfigDumpOptions) error {
	config, err := cttools.LoadConfig(options.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load config from %#q:\n%w", options.ConfigPath, err)
	}

	if err := cttools.ResolveTemplates(config); err != nil {
		return fmt.Errorf("failed to resolve templates:\n%w", err)
	}

	if err := cttools.FilterEnvironment(config, options.Environment); err != nil {
		return fmt.Errorf("failed to filter environment:\n%w", err)
	}

	var output []byte

	switch options.Format {
	case "json":
		output, err = json.MarshalIndent(config, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal config to JSON:\n%w", err)
		}
	case "yaml":
		output, err = yaml.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal config to YAML:\n%w", err)
		}
	default:
		return fmt.Errorf("unsupported output format %#q; use 'json' or 'yaml'", options.Format)
	}

	_, err = fmt.Fprintln(os.Stdout, string(output))
	if err != nil {
		return fmt.Errorf("failed to write output:\n%w", err)
	}

	return nil
}
