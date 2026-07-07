// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package advanced

import (
	"fmt"
	"os"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/buildenvfactory"
	"github.com/microsoft/azure-linux-dev-tools/internal/buildenv"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/spf13/cobra"
)

func mockOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewMockCmd())
}

// Constructs a [cobra.Command] for the "mock" subcommand hierarchy.
func NewMockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mock",
		Short: "Run RPM mock tool",
		Long: `Run RPM mock tool commands directly.

Provides low-level access to mock for building RPMs from SRPMs and
starting interactive shell sessions in mock chroot environments.`,
	}

	cmd.AddCommand(NewBuildRPMCmd())
	cmd.AddCommand(NewShellCmd())

	return cmd
}

// Options controlling how to run mock commands.
type MockCmdOptions struct {
	// Path to the .cfg file to use with mock.
	MockConfigPath string
}

// Options controlling how to build an RPM from a source RPM.
type BuildRPMOptions struct {
	// Common mock options.
	MockCmdOptions

	// Path to the SRPM to build.
	SRPMPath string
	// Path to the output directory for final RPM files.
	OutputDirPath string
	// Whether to skip the %check section of the RPM spec.
	NoCheck bool
}

// Constructs a [cobra.Command] for the "mock build-rpms" subcommand.
func NewBuildRPMCmd() *cobra.Command {
	options := &BuildRPMOptions{}

	// We don't *require* a valid project configuration, but may use one if it's available.
	cmd := &cobra.Command{
		Use:   "build-rpms",
		Short: "Use mock to build an RPM",
		Long: `Build binary RPMs from a source RPM using mock.

This is a low-level command that invokes mock directly with the given
SRPM and configuration. For most use cases, prefer 'azldev component build'
which handles source preparation and overlay application automatically.`,
		Example: `  # Build from an SRPM
  azldev advanced mock build-rpms --srpm ./my-package.src.rpm -o ./rpms/

  # Build with a custom mock config
  azldev advanced mock build-rpms -c my-mock.cfg --srpm ./my-package.src.rpm -o ./rpms/`,
		RunE: azldev.RunFuncWithoutRequiredConfig(func(env *azldev.Env) (results interface{}, err error) {
			return BuildRPMS(env, options)
		}),
	}

	cmd.Flags().StringVarP(&options.MockConfigPath, "config", "c", "", "Path to the mock .cfg file")
	cmd.Flags().StringVar(&options.SRPMPath, "srpm", "", "Path to the SRPM to build")
	cmd.Flags().StringVarP(&options.OutputDirPath, "output-dir", "o", "", "Path to output directory")
	cmd.Flags().BoolVar(&options.NoCheck, "no-check", false, "Skip package %check tests")

	_ = cmd.MarkFlagRequired("srpm")
	_ = cmd.MarkFlagRequired("output-dir")

	return cmd
}

// Options controlling how to run a shell command in a mock environment.
type ShellOptions struct {
	// Common mock options.
	MockCmdOptions

	// Whether or not to enable external network access from within the mock root the shell is executed in.
	EnableNetwork bool

	// Packages to add to the mock root before starting the shell.
	PackagesToAdd []string
}

// Constructs a [cobra.Command] for the 'mock shell' command.
func NewShellCmd() *cobra.Command {
	options := &ShellOptions{}

	// We don't *require* a valid project configuration, but may use one if it's available.
	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Enter mock shell",
		Long: `Start an interactive shell inside a mock chroot environment.

This is useful for inspecting built RPMs, debugging package issues, or
running smoke tests. Packages can be pre-installed into the chroot using
--add-package. Extra arguments after -- are passed to the shell command.`,
		Example: `  # Open a mock shell
  azldev advanced mock shell

  # Open a shell with packages pre-installed
  azldev advanced mock shell --add-package /path/to/my-package.rpm

  # Open a shell with network access
  azldev advanced mock shell --enable-network

  # Run a command inside the mock shell
  azldev advanced mock shell -- rpm -qa`,
		RunE: azldev.RunFuncWithoutRequiredConfigWithExtraArgs(
			func(env *azldev.Env, extraArgs []string) (results interface{}, err error) {
				return true, RunShell(env, options, extraArgs)
			},
		),
	}

	cmd.Flags().StringVarP(&options.MockConfigPath, "config", "c", "", "Path to the mock .cfg file")
	cmd.Flags().BoolVar(&options.EnableNetwork, "enable-network", false, "Enable network access in the mock root")
	cmd.Flags().StringArrayVarP(&options.PackagesToAdd, "add-package", "p", []string{},
		"Package to add to the mock root before starting the shell",
	)

	return cmd
}

// Builds RPMs from sources, using options.
func BuildRPMS(env *azldev.Env, options *BuildRPMOptions) (results interface{}, err error) {
	runner, err := makeMockRunner(env, &options.MockCmdOptions)
	if err != nil {
		return results, err
	}

	buildOptions := mock.RPMBuildOptions{
		NoCheck: options.NoCheck,
	}

	evt := env.StartEvent("Building RPM with mock",
		"srpmPath", options.SRPMPath, "outputDirPath", options.OutputDirPath)

	defer evt.End()

	// Build!
	err = runner.BuildRPM(env, options.SRPMPath, options.OutputDirPath, buildOptions)
	if err != nil {
		return results, fmt.Errorf("failed to build RPM: %w", err)
	}

	return true, nil
}

// Executes an interactive shell within a mock root. Uses the provided [ShellOptions] to configure the shell.
func RunShell(env *azldev.Env, options *ShellOptions, extraArgs []string) error {
	runner, err := makeMockRunner(env, &options.MockCmdOptions)
	if err != nil {
		return err
	}

	if options.EnableNetwork {
		runner.EnableNetwork()
	}

	if len(options.PackagesToAdd) > 0 {
		err = runner.InstallPackages(env, options.PackagesToAdd)
		if err != nil {
			return fmt.Errorf("failed to install packages in mock root (%v): %w", options.PackagesToAdd, err)
		}
	}

	cmd, err := runner.CmdInChroot(env, extraArgs, true /*interactive*/)
	if err != nil {
		return fmt.Errorf("failed to create shell command: %w", err)
	}

	cmd.SetStdin(os.Stdin)
	cmd.SetStdout(os.Stdout)
	cmd.SetStderr(os.Stderr)

	err = cmd.Run(env)
	if err != nil {
		return fmt.Errorf("failed to run shell: %w", err)
	}

	return nil
}

func makeMockRunner(env *azldev.Env, options *MockCmdOptions) (runner *mock.Runner, err error) {
	// If we have an explicit config path, then use it.
	if options.MockConfigPath != "" {
		return mock.NewRunner(env, options.MockConfigPath), nil
	}

	// Otherwise, try to find the right one for this environment.
	factory, err := buildenvfactory.NewMockRootFactoryForEnv(env)
	if err != nil {
		return nil, fmt.Errorf("failed to create mock root factory: %w", err)
	}

	root, err := factory.CreateMockRoot(buildenv.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create mock root: %w", err)
	}

	return root.GetRunner(), nil
}
