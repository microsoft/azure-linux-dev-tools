// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/tmt"
	"github.com/spf13/cobra"
)

func testOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewComponentTestCmd())
}

const componentTestLongDescription = `Run TMT test definitions associated with a component using TMT's virtual
(QEMU/testcloud), local, or connect (SSH) provisioner.

For virtual, the image must be a local qcow2 file. Every RPM supplied with --rpm is copied
into the guest and installed by TMT before the selected plan executes. This
ensures the test validates the locally built package rather than the version
already present in the image. The guest can be modified by test preparation and
execution; a disposable testcloud overlay is used for the virtual disk.

RPMs (required for virtual/local, optional for connect):
  After building a component with 'azldev component build COMPONENT', built RPMs
  are typically located in base/out/rpms/rpm-base/. For virtual/local, pass at minimum:
  - The main package (e.g., buildah-1.0.0-1.x86_64.rpm)
  - The -tests package (e.g., buildah-tests-1.0.0-1.x86_64.rpm)

  To discover what was built:
    ls base/out/rpms/rpm-base/<component>*

  Then pass each relevant RPM with a separate --rpm flag.

PROVISIONER MODES:
  - virtual (default): Runs tests in a QEMU/testcloud VM using the Azure Linux
                       image. Most flexible; guest is isolated. Requires --image-path.
                       Requires an explicit plan (tmt.plan or --plan): hardware
                       constraints are resolved per-plan.
  - local: Runs tests directly on this machine. Host must be Azure Linux 4.
           Useful for quick testing; modifies host state.
  - connect: Runs tests on an existing SSH guest; no VM lifecycle or image.
             Requires --guest, --user, and --key; --port defaults to 22.
             Use a non-root account with passwordless sudo. Preparation and
             tests modify the guest. Candidate RPMs are optional.
             Caller TMT_SSH_* environment settings are inherited unchanged.
             TMT copies the absolute work path to the same path on the guest
             as the SSH user. That user must be able to create/write it;
             normally use --work-dir /var/tmp/azldev-connect.

PLAN SELECTION:
  By default tmt discovers and runs every enabled plan it finds in the test
  source (mirrors Fedora Testing Farm's own default of no pinned plan name).
  Set 'tmt.plan' in the catalog entry, or pass --plan, to run one specific
  plan instead. --plan always takes precedence over a catalog entry's
  'tmt.plan' when both are given.

AZURE LINUX 4 PREREQUISITES:
  To run with --provision local, install the host dependencies:
    sudo tdnf install -y python3 python3-pip git sudo

  ('git' is only needed for azldev's own clone path; omit it when using
  '--source-dir' to skip that clone. The selected plan's own 'discover' step may
  still invoke git itself, e.g. via an fmf 'discover.url' pointing at another
  repo, independent of this prerequisite.)

  azldev creates a per-work-directory Python environment and installs the
  pinned TMT version there. The local provisioner uses sudo to install the
  supplied candidate RPMs and execute the plan, so it modifies the host.

azldev creates or reuses an isolated Python environment under --work-dir and
installs TMT with virtual-provisioner support there. python3 must be available
on the host; git is required for azldev's own catalog clone unless
'--source-dir' is used, though the selected plan's 'discover' step may still
invoke git itself regardless of '--source-dir'.

Reports and logs stay under --work-dir. Use -O json for artifact paths and TMT exit codes, including on failure.`

// NewComponentTestCmd constructs the 'component test' command.
func NewComponentTestCmd() *cobra.Command {
	options := &tmt.Options{}

	cmd := &cobra.Command{
		Use:   "test COMPONENT",
		Short: "Run a component's TMT tests in a VM, on this host, or over SSH",
		Long:  componentTestLongDescription,
		Example: `  # Build the component first
  azldev component build buildah

  # Discover available RPMs
  ls base/out/rpms/rpm-base/buildah*

  # Run tests with discovered RPMs
  azldev component test buildah \
    --image-path ./base/out/images/vm-base/azl4-vm-base.x86_64.qcow2 \
    --rpm ./base/out/rpms/rpm-base/buildah-1.29.0-5.x86_64.rpm \
    --rpm ./base/out/rpms/rpm-base/buildah-tests-1.29.0-5.x86_64.rpm

  # Run on an existing disposable SSH guest
  azldev component test buildah --provision connect \
    --guest 127.0.0.1 --port 22429 --user tester --key ./test-key \
    --source-dir ./test-tree --work-dir /var/tmp/azldev-connect -O json`,
		Args: cobra.ExactArgs(1),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if options.Provision != tmt.ProvisionConnect {
				for _, flag := range []string{"guest", "port", "user", "key"} {
					if cmd.Flags().Changed(flag) {
						return fmt.Errorf("'--%s' requires '--provision connect'", flag)
					}
				}
			} else if cmd.Flags().Changed("image-path") {
				return errors.New("'--image-path' cannot be used with '--provision connect'")
			}

			return nil
		},
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			return tmt.RunComponentTests(env, args[0], options)
		}),
	}

	registerComponentTestFlags(cmd, options)

	return cmd
}

func registerComponentTestFlags(cmd *cobra.Command, options *tmt.Options) {
	cmd.Flags().StringVarP(&options.ImagePath, "image-path", "i", "", "Path to the qcow2 image under test")
	_ = cmd.MarkFlagFilename("image-path")
	cmd.Flags().StringSliceVarP(
		&options.RPMPaths, "rpm", "r", nil,
		"Local RPM to install before testing (required for virtual/local; may be repeated). "+
			"Typically include the main package and -tests package.",
	)
	_ = cmd.MarkFlagFilename("rpm")
	cmd.Flags().StringSliceVarP(
		&options.Tests, "test", "t", nil, "Mapped TMT test name to run (may be repeated; defaults to all)",
	)
	cmd.Flags().StringVar(
		&options.WorkDir, "work-dir", "", "Parent for the reusable TMT venv and fresh retained test artifacts "+
			"(default: current directory)",
	)
	_ = cmd.MarkFlagDirname("work-dir")
	cmd.Flags().StringVar(&options.Provision, "provision", tmt.ProvisionVirtual,
		"TMT provisioner: 'virtual' (default) uses QEMU; 'local' uses this Azure Linux 4 host; 'connect' uses SSH")
	cmd.Flags().StringVar(&options.Connection.Guest, "guest", "", "SSH guest hostname or address (required for connect)")
	cmd.Flags().IntVar(&options.Connection.Port, "port", tmt.DefaultSSHPort, "SSH port (connect only; 1..65535)")
	cmd.Flags().StringVar(&options.Connection.User, "user", "", "SSH user with passwordless sudo (required for connect)")
	cmd.Flags().StringVar(&options.Connection.Key, "key", "",
		"SSH private key file, relative to the project (required for connect)")
	_ = cmd.MarkFlagFilename("key")
	cmd.Flags().StringVar(&options.SourceDir, "source-dir", "",
		"Run tmt straight from this local fmf tree instead of cloning the catalog 'source'. Accepts the "+
			"output of either 'azldev component render' (with 'render.skip-file-filter = true') or "+
			"'azldev component prepare-sources'. Local inner-loop convenience; not used by cloud (TEE) runs.")
	_ = cmd.MarkFlagDirname("source-dir")
	cmd.Flags().StringVar(&options.Plan, "plan", "",
		"Absolute fmf plan name to run, overriding the catalog entry's 'tmt.plan'. Defaults to running "+
			"every enabled plan tmt discovers in the test source. Required with '--provision virtual', since "+
			"hardware export is resolved per-plan.")
	cmd.Flags().BoolVar(&options.FromSpec, "from-spec", false,
		"Deprecated: use '--source-dir' instead, pointed at the component's rendered spec directory "+
			"(e.g. 'SPECS/c/curl' under 'project.rendered-specs-dir'). Kept only for compatibility with "+
			"existing invocations; cannot be combined with '--source-dir'.")
	_ = cmd.Flags().MarkDeprecated("from-spec", "use '--source-dir' instead, pointed at the component's "+
		"rendered spec directory")
}
