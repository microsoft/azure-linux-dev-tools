// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package advanced

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/cttools"
	"github.com/spf13/cobra"
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
  azldev advanced ct-tools config-dump \
    --ct-config /path/to/azurelinux.toml \
    --environment ct-dev -O json`,
		RunE: azldev.RunFuncWithoutRequiredConfig(func(env *azldev.Env) (results interface{}, err error) {
			return RunConfigDump(env, options)
		}),
	}

	cmd.Flags().StringVar(
		&options.ConfigPath, "ct-config", "",
		"Path to the top-level CT distro TOML configuration file",
	)

	envHelp := "Control Tower environment name " +
		"(e.g. ct-dev, ct-staging, ct-prod)"
	cmd.Flags().StringVar(&options.Environment, "environment", "", envHelp)

	_ = cmd.MarkFlagRequired("ct-config")
	_ = cmd.MarkFlagRequired("environment")
	_ = cmd.MarkFlagFilename("ct-config", "toml")

	return cmd
}

// RunConfigDump loads, resolves, filters, and returns the distro configuration.
func RunConfigDump(env *azldev.Env, options *ConfigDumpOptions) (*cttools.DistroConfig, error) {
	config, err := cttools.LoadConfig(env.FS(), options.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from %#q:\n%w", options.ConfigPath, err)
	}

	if err := cttools.ResolveTemplates(config); err != nil {
		return nil, fmt.Errorf("failed to resolve templates:\n%w", err)
	}

	if err := cttools.FilterEnvironment(config, options.Environment); err != nil {
		return nil, fmt.Errorf("failed to filter environment:\n%w", err)
	}

	return config, nil
}
