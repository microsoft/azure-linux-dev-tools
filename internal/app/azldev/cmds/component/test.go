// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
	"github.com/spf13/cobra"
)

// ComponentTestOptions holds options for running component TMT tests locally.
type ComponentTestOptions struct {
	ImagePath string
	RPMPaths  []string
	Tests     []string
	WorkDir   string
	Provision string
}

type tmtSource struct {
	GitURL string `toml:"git-url"`
	Ref    string `toml:"ref"`
}

type tmtConfig struct {
	Source tmtSource `toml:"source"`
	Plan   string    `toml:"plan"`
}

type tmtPlanExport struct {
	Name      string           `json:"name"`
	Provision []map[string]any `json:"provision"`
}

// tmtRunSettings holds the fully resolved inputs shared by every test in a run.
type tmtRunSettings struct {
	ImagePath      string
	RPMs           []string
	WorkDir        string
	TMTProgramPath string
	Provision      string
}

const (
	tmtPythonProgram    = "python3"
	tmtVenvDirName      = "venv"
	tmtProgram          = "tmt"
	tmtProvisionLocal   = "local"
	tmtProvisionVirtual = "virtual"
	// Keep the TMT CLI behavior stable for a given azldev revision. Update this
	// pin deliberately after validating component test execution with the newer
	// version.
	tmtVersion                     = "1.78.0"
	tmtPipRequirement              = "tmt[provision-virtual]==" + tmtVersion
	tmtCandidateRPMPrepareStepName = "azldev-candidate-rpms"
)

const testcloudPluginTemplate = `import tmt.steps.provision.testcloud as testcloud
from tmt.guest import GuestSsh
from tmt.utils import Command

testcloud.TESTCLOUD_WORKAROUNDS.extend(%s)

# TMT pulls each finish task's data over the unprivileged SSH connection
# immediately after that task finishes. An appended finish task is therefore
# too late for an upstream task which creates a root-owned artifact, such as
# a copied audit.log. Repair the disposable guest artifact directory directly
# before each pull so the source task's output is included in the transfer.
_azldev_original_pull = GuestSsh.pull

def _azldev_pull(self, source=None, destination=None, options=None):
	path = source or self.plan_workdir
	if self.user != "root":
		self.execute(Command("sudo", "chmod", "-R", "a+rX", path), silent=True)
	return _azldev_original_pull(self, source=source, destination=destination, options=options)

GuestSsh.pull = _azldev_pull
`

func testOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewComponentTestCmd())
}

// NewComponentTestCmd constructs the 'component test' command.
func NewComponentTestCmd() *cobra.Command {
	options := &ComponentTestOptions{}

	cmd := &cobra.Command{
		Use:   "test COMPONENT",
		Short: "Run a component's TMT tests in a local QEMU VM",
		Long: `Run TMT test definitions associated with a component against an Azure Linux
image using TMT's virtual provisioner (QEMU/testcloud).

The image must be a local qcow2 file. Every RPM supplied with --rpm is copied
into the guest and installed by TMT before the selected plan executes. This
ensures the test validates the locally built package rather than the version
already present in the image. The guest can be modified by test preparation and
execution; a disposable testcloud overlay is used for the virtual disk.

REQUIRED RPMs:
  After building a component with 'azldev component build COMPONENT', built RPMs
  are typically located in base/out/rpms/rpm-base/. You must pass at minimum:
  - The main package (e.g., buildah-1.0.0-1.x86_64.rpm)
  - The -tests package (e.g., buildah-tests-1.0.0-1.x86_64.rpm)

  To discover what was built:
    ls base/out/rpms/rpm-base/<component>*

  Then pass each relevant RPM with a separate --rpm flag.

PROVISIONER MODES:
  - virtual (default): Runs tests in a QEMU/testcloud VM using the Azure Linux
                       image. Most flexible; guest is isolated. Requires --image-path.
  - local: Runs tests directly on this machine. Host must be Azure Linux 4.
           Useful for quick testing; modifies host state.

AZURE LINUX 4 PREREQUISITES:
  To run with --provision local, install the host dependencies:
    sudo tdnf install -y python3 python3-pip git sudo

  azldev creates a per-work-directory Python environment and installs the
  pinned TMT version there. The local provisioner uses sudo to install the
  supplied candidate RPMs and execute the plan, so it modifies the host.

azldev creates or reuses an isolated Python environment under --work-dir and
installs TMT with virtual-provisioner support there. python3 and git must be
available on the host.`,
		Example: `  # Build the component first
  azldev component build buildah

  # Discover available RPMs
  ls base/out/rpms/rpm-base/buildah*

  # Run tests with discovered RPMs
  azldev component test buildah \
    --image-path ./base/out/images/vm-base/azl4-vm-base.x86_64.qcow2 \
    --rpm ./base/out/rpms/rpm-base/buildah-1.29.0-5.x86_64.rpm \
    --rpm ./base/out/rpms/rpm-base/buildah-tests-1.29.0-5.x86_64.rpm`,
		Args: cobra.ExactArgs(1),
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			return nil, runComponentTMTTests(env, args[0], options)
		}),
	}

	cmd.Flags().StringVarP(&options.ImagePath, "image-path", "i", "", "Path to the qcow2 image under test")
	_ = cmd.MarkFlagFilename("image-path")
	cmd.Flags().StringSliceVarP(
		&options.RPMPaths, "rpm", "r", nil, "Local RPM to install before testing (required; may be repeated). "+
			"Typically include the main package and -tests package.",
	)
	_ = cmd.MarkFlagFilename("rpm")
	cmd.Flags().StringSliceVarP(
		&options.Tests, "test", "t", nil, "Mapped TMT test name to run (may be repeated; defaults to all)",
	)
	cmd.Flags().StringVar(
		&options.WorkDir, "work-dir", "", "Directory for cloned metadata and TMT artifacts (default: current directory)",
	)
	_ = cmd.MarkFlagDirname("work-dir")
	cmd.Flags().StringVar(&options.Provision, "provision", tmtProvisionVirtual,
		"TMT provisioner mode: 'virtual' (default) runs tests in QEMU; 'local' runs on this machine (must be Azure Linux 4)")
	_ = cmd.MarkFlagRequired("rpm")

	return cmd
}

func runComponentTMTTests(env *azldev.Env, componentName string, options *ComponentTestOptions) error {
	if err := validateComponentTestOptions(env, options); err != nil {
		return err
	}

	imagePath, err := resolveTMTImagePath(env, options.Provision, options.ImagePath)
	if err != nil {
		return err
	}

	rpms, err := absoluteRegularFiles(env, options.RPMPaths, "rpm")
	if err != nil {
		return err
	}

	resolved, err := resolveComponentTMTTests(env, componentName, options.Tests)
	if err != nil {
		return err
	}

	workDir, tmtProgramPath, err := prepareTMTEnvironment(env, options.WorkDir, options.Provision)
	if err != nil {
		return err
	}

	settings := tmtRunSettings{
		ImagePath:      imagePath,
		RPMs:           rpms,
		WorkDir:        workDir,
		TMTProgramPath: tmtProgramPath,
		Provision:      options.Provision,
	}

	for _, test := range resolved {
		if err := runOneTMTTest(env, test, settings); err != nil {
			return fmt.Errorf("TMT test %#q:\n%w", test.Name, err)
		}
	}

	return nil
}

func validateComponentTestOptions(env *azldev.Env, options *ComponentTestOptions) error {
	if err := validateTMTProvisionOptions(options.Provision, options.ImagePath); err != nil {
		return err
	}

	// The local provisioner runs the plan's prepare and execute steps directly on
	// this machine, so restrict it to the distribution the candidate RPMs target.
	if options.Provision == tmtProvisionLocal {
		if err := prereqs.RequireAzureLinux4(env); err != nil {
			return fmt.Errorf("validate local TMT provisioner host:\n%w", err)
		}

		// The --become flag in TMT args requires sudo for privilege escalation.
		if err := prereqs.RequireExecutable(env, "sudo", nil); err != nil {
			return fmt.Errorf("sudo is required for '--provision local':\n%w", err)
		}
	}

	return nil
}

func resolveTMTImagePath(env *azldev.Env, provision string, configuredImagePath string) (string, error) {
	if provision != tmtProvisionVirtual {
		return "", nil
	}

	if configuredImagePath == "" {
		return "", errors.New("'--image-path' is required with '--provision virtual'")
	}

	imagePath, err := absoluteProjectPath(env.ProjectDir(), configuredImagePath)
	if err != nil {
		return "", fmt.Errorf("resolve image path:\n%w", err)
	}

	info, err := env.FS().Stat(imagePath)
	if err != nil {
		return "", fmt.Errorf("image path %#q:\n%w", imagePath, err)
	}

	// The virtual provisioner copies the image into a testcloud overlay, so a
	// directory or device node would only fail once TMT is already running.
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("image path %#q must be a regular file, found %s", imagePath, info.Mode().Type())
	}

	return imagePath, nil
}

func resolveComponentTMTTests(
	env *azldev.Env, componentName string, selectors []string,
) ([]projectconfig.ResolvedTest, error) {
	resolver := components.NewResolver(env)

	set, err := resolver.FindComponents(&components.ComponentFilter{ComponentNamePatterns: []string{componentName}})
	if err != nil {
		return nil, fmt.Errorf("resolve component %#q:\n%w", componentName, err)
	}

	if set.Len() != 1 {
		return nil, fmt.Errorf("expected exactly one component named %#q, found %d", componentName, set.Len())
	}

	resolved, err := env.Config().ResolveComponentTests(set.Components()[0].GetConfig())
	if err != nil {
		return nil, fmt.Errorf("resolve tests for component %#q:\n%w", componentName, err)
	}

	resolved = selectTMTTests(resolved, selectors)
	if len(resolved) == 0 {
		return nil, fmt.Errorf("component %#q has no selected TMT tests", componentName)
	}

	return resolved, nil
}

func prepareTMTEnvironment(env *azldev.Env, configuredWorkDir string, provision string) (string, string, error) {
	workDir, err := componentTMTWorkDir(env, configuredWorkDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve work directory:\n%w", err)
	}

	tmtProgramPath := filepath.Join(workDir, "tmt", tmtVenvDirName, "bin", tmtProgram)
	if env.DryRun() {
		return workDir, tmtProgramPath, nil
	}

	if err := env.FS().MkdirAll(workDir, fileperms.PublicDir); err != nil {
		return "", "", fmt.Errorf("create work directory:\n%w", err)
	}

	tmtProgramPath, err = ensureTMTVenv(env, workDir, provision)
	if err != nil {
		return "", "", err
	}

	return workDir, tmtProgramPath, nil
}

// ensureTMTVenv creates or reuses an isolated TMT installation. This follows
// the local LISA runner pattern: Python and git are explicit host
// prerequisites, while the test framework itself is installed in a venv under
// the selected work directory rather than assumed to be packaged by the host
// distribution. For virtual provisioning, the testcloud plugin supplies
// provisioner support. For local provisioning, only base TMT is required.
func ensureTMTVenv(env *azldev.Env, workDir string, provision string) (string, error) {
	if err := prereqs.RequireExecutable(env, tmtPythonProgram, nil); err != nil {
		return "", fmt.Errorf("python3 is required to run TMT tests:\n%w", err)
	}

	if err := prereqs.RequireExecutable(env, "git", nil); err != nil {
		return "", fmt.Errorf("git is required to clone TMT test metadata:\n%w", err)
	}

	venvDir := filepath.Join(workDir, "tmt", tmtVenvDirName)
	venvPython := filepath.Join(venvDir, "bin", tmtPythonProgram)
	tmtProgramPath := filepath.Join(venvDir, "bin", tmtProgram)

	venvExists, err := fileutils.Exists(env.FS(), venvPython)
	if err != nil {
		return "", fmt.Errorf("check TMT venv at %#q:\n%w", venvDir, err)
	}

	if !venvExists {
		slog.Info("Creating TMT Python venv", slog.String("path", venvDir))

		if err := runHostCommand(env, "", tmtPythonProgram, "-m", "venv", venvDir); err != nil {
			return "", fmt.Errorf("create TMT Python venv at %#q:\n%w", venvDir, err)
		}
	} else {
		slog.Info("Reusing TMT Python venv", slog.String("path", venvDir))
	}

	requirement := "tmt==" + tmtVersion
	if provision == tmtProvisionVirtual {
		requirement = tmtPipRequirement
	}

	slog.Info("Installing TMT", slog.String("venv", venvDir), slog.String("requirement", requirement))

	if err := runHostCommand(
		env, "", venvPython, "-m", "pip", "install", "--quiet", requirement,
	); err != nil {
		return "", fmt.Errorf("install TMT:\n%w", err)
	}

	return tmtProgramPath, nil
}

func componentTMTWorkDir(env *azldev.Env, configuredWorkDir string) (string, error) {
	if configuredWorkDir == "" {
		workDir, err := env.OSEnv().Getwd()
		if err != nil {
			return "", fmt.Errorf("get current working directory:\n%w", err)
		}

		return workDir, nil
	}

	return absoluteProjectPath(env.ProjectDir(), configuredWorkDir)
}

func absoluteProjectPath(projectDir string, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectDir, path)
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make path %#q absolute:\n%w", path, err)
	}

	return absolutePath, nil
}

func absoluteRegularFiles(env *azldev.Env, paths []string, kind string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		pattern, err := absoluteProjectPath(env.ProjectDir(), path)
		if err != nil {
			return nil, fmt.Errorf("resolve %s path %#q:\n%w", kind, path, err)
		}

		matches, err := fileutils.Glob(env.FS(), pattern,
			doublestar.WithFilesOnly(),
			doublestar.WithFailOnIOErrors(),
		)
		if err != nil {
			return nil, fmt.Errorf("expand %s path pattern %#q:\n%w", kind, path, err)
		}

		if len(matches) == 0 {
			return nil, fmt.Errorf("%s path pattern %#q matched no files", kind, path)
		}

		sort.Strings(matches)
		result = append(result, matches...)
	}

	return result, nil
}

func removePreviousTMTRepository(env *azldev.Env, repoDir string) error {
	if err := env.FS().RemoveAll(repoDir); err != nil {
		return fmt.Errorf("remove test metadata repository %#q:\n%w", repoDir, err)
	}

	return nil
}

func selectTMTTests(tests []projectconfig.ResolvedTest, selectors []string) []projectconfig.ResolvedTest {
	result := make([]projectconfig.ResolvedTest, 0, len(tests))
	for _, test := range tests {
		if test.Definition.Type != "tmt" {
			continue
		}

		if len(selectors) == 0 || slices.Contains(selectors, test.Name) {
			result = append(result, test)
		}
	}

	return result
}

func runOneTMTTest(env *azldev.Env, test projectconfig.ResolvedTest, settings tmtRunSettings) error {
	config, err := decodeTMTConfig(test.Definition.Tmt)
	if err != nil {
		return err
	}

	if err := validateTMTTestName(test.Name); err != nil {
		return err
	}

	testDir := filepath.Join(settings.WorkDir, test.Name)
	repoDir := filepath.Join(testDir, "repo")
	tmtWorkDir := filepath.Join(testDir, "tmt")

	pluginDir, err := prepareTMTTestDir(env, testDir, repoDir, settings)
	if err != nil {
		return err
	}

	if err := runHostCommand(env, testDir, "git", "clone", "--no-checkout", config.Source.GitURL, repoDir); err != nil {
		return fmt.Errorf("clone test metadata:\n%w", err)
	}

	if err := runHostCommand(env, repoDir, "git", "checkout", "--detach", config.Source.Ref); err != nil {
		return fmt.Errorf("checkout test metadata:\n%w", err)
	}

	var hardwareArgs []string
	if settings.Provision == tmtProvisionVirtual {
		hardwareArgs, err = resolvedPlanHardwareArgs(
			env, repoDir, settings.TMTProgramPath, config.Plan,
		)
		if err != nil {
			return fmt.Errorf("resolve hardware for TMT plan %#q:\n%w", config.Plan, err)
		}
	}

	args := componentTMTArgs(config, tmtWorkDir, settings.Provision, settings.ImagePath, hardwareArgs, settings.RPMs)

	if err := runTMTCommand(env, repoDir, pluginDir, settings.TMTProgramPath, settings.Provision, args...); err != nil {
		return fmt.Errorf("run TMT plan %#q (artifacts: %#q):\n%w", config.Plan, tmtWorkDir, err)
	}

	return nil
}

// prepareTMTTestDir creates the per-test directory and, for virtual runs, the
// testcloud plugin. It returns the plugin directory, which is empty when no
// plugin is needed.
func prepareTMTTestDir(env *azldev.Env, testDir string, repoDir string, settings tmtRunSettings) (string, error) {
	if env.DryRun() {
		return "", nil
	}

	if err := env.FS().MkdirAll(testDir, fileperms.PublicDir); err != nil {
		return "", fmt.Errorf("create test directory:\n%w", err)
	}

	// A caller may intentionally reuse --work-dir after a failed or completed
	// run. Metadata is always cloned at the pinned ref, so remove only the
	// previous checkout while retaining TMT artifacts for diagnosis.
	if err := removePreviousTMTRepository(env, repoDir); err != nil {
		return "", fmt.Errorf("remove previous test metadata checkout:\n%w", err)
	}

	if settings.Provision != tmtProvisionVirtual {
		return "", nil
	}

	pluginDir, err := writeTestcloudPlugin(env, testDir)
	if err != nil {
		return "", fmt.Errorf("write testcloud cloud-init plugin:\n%w", err)
	}

	return pluginDir, nil
}

func validateTMTTestName(name string) error {
	if name == "" || name == "." || name == ".." ||
		filepath.IsAbs(name) || filepath.Clean(name) != name || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid test name %#q: must be a simple name (no path separators)", name)
	}

	return nil
}

// componentTMTArgs builds a run which changes only the provisioner and adds the
// candidate RPM installation. In particular, it deliberately leaves discover,
// execute, and report to the selected plan: upstream Fedora plans commonly use
// non-default plugins or scripts for those steps.
func componentTMTArgs(
	config tmtConfig,
	tmtWorkDir string,
	provision string,
	imagePath string,
	hardwareArgs []string,
	rpms []string,
) []string {
	args := make([]string, 0, 32+len(hardwareArgs)+len(rpms))

	// Both provisioners need --become: candidate RPM installation and most plans'
	// execute steps require root, and neither the testcloud guest user nor the
	// invoking local user is root. TMT escalates with passwordless sudo.
	args = append(args,
		"run", "--all", "--keep", "--workdir-root", tmtWorkDir,
		"plan", "--name", config.Plan,
		"provision", "--how", provision, "--become",
	)

	// Only the virtual provisioner boots an image, so the disk and hardware
	// constraints are meaningless for a local run.
	if provision == tmtProvisionVirtual {
		args = append(args, "--image", imagePath)

		// Emit hardware constraints from the plan, plus boot.method for UEFI.
		for _, hardwareArg := range hardwareArgs {
			args = append(args, "--hardware", hardwareArg)
		}
	}

	// Insert a distinct step rather than updating the plan's preparation. An
	// update can inherit a plan entry's `when` conditions and skip candidate
	// RPM installation entirely.
	args = append(args, "prepare", "--insert", "--how", "install", "--name", tmtCandidateRPMPrepareStepName)
	for _, rpm := range rpms {
		args = append(args, "--package", rpm)
	}

	return args
}

// resolvedPlanHardwareArgs exports the plan's declared hardware constraints and
// appends boot.method=uefi. TMT treats command-line --hardware options as a
// replacement for a plan's hardware block, not an addition, so re-emitting the
// resolved constraints retains requirements such as memory, CPU topology, and
// disk size while adding UEFI firmware.
func resolvedPlanHardwareArgs(
	env *azldev.Env, repoDir string, tmtProgramPath string, plan string,
) ([]string, error) {
	// The plan export command produces no output in dry-run mode. In that case,
	// emit only boot.method so the printed invocation represents what would run.
	if env.DryRun() {
		return []string{"boot.method = uefi"}, nil
	}

	hardware, err := exportedPlanHardware(env, repoDir, tmtProgramPath, plan)
	if err != nil {
		return nil, err
	}

	return mergePlanHardwareArgs(hardware)
}

func exportedPlanHardware(env *azldev.Env, repoDir string, tmtProgramPath string, plan string) (map[string]any, error) {
	output, err := runHostCommandOutput(env, repoDir, tmtProgramPath, "plan", "export", plan, "--how", "json")
	if err != nil {
		return nil, err
	}

	var plans []tmtPlanExport
	if err := json.Unmarshal(output, &plans); err != nil {
		return nil, fmt.Errorf("decode tmt plan export output:\n%w", err)
	}

	var selected *tmtPlanExport

	for index := range plans {
		if plans[index].Name != plan {
			continue
		}

		if selected != nil {
			return nil, fmt.Errorf("multiple exported plans are named %#q", plan)
		}

		selected = &plans[index]
	}

	if selected == nil {
		return nil, fmt.Errorf("plan %#q was not returned by tmt plan export", plan)
	}

	if len(selected.Provision) > 0 {
		if value, ok := selected.Provision[0]["hardware"].(map[string]any); ok {
			return value, nil
		}
	}

	return map[string]any{}, nil
}

func mergePlanHardwareArgs(hardware map[string]any) ([]string, error) {
	constraints, err := flattenHardwareConstraints("", hardware)
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, len(constraints)+1)
	for _, constraint := range constraints {
		// Skip any boot.method from the plan; we hardcode UEFI below.
		if strings.HasPrefix(constraint, "boot.method ") {
			continue
		}

		args = append(args, constraint)
	}

	args = append(args, "boot.method = uefi")

	return args, nil
}

// flattenHardwareConstraints converts the JSON-shaped TMT hardware block into
// the constraint syntax accepted by --hardware. Lists use TMT's indexed syntax,
// e.g. disk[0].size >= 512 GB. Boolean expressions cannot be represented by
// the CLI's flat syntax without changing their meaning, so reject them rather
// than silently weakening a test plan.
//
//nolint:cyclop // Each supported JSON type has distinct constraint serialization.
func flattenHardwareConstraints(prefix string, value any) ([]string, error) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			if key == "and" || key == "or" {
				return nil, fmt.Errorf("hardware constraint group %#q cannot be safely overridden", key)
			}

			keys = append(keys, key)
		}

		sort.Strings(keys)

		var constraints []string

		for _, key := range keys {
			nextPrefix := key
			if prefix != "" {
				nextPrefix = prefix + "." + key
			}

			child, err := flattenHardwareConstraints(nextPrefix, typed[key])
			if err != nil {
				return nil, err
			}

			constraints = append(constraints, child...)
		}

		return constraints, nil
	case []any:
		var constraints []string

		for index, item := range typed {
			child, err := flattenHardwareConstraints(fmt.Sprintf("%s[%d]", prefix, index), item)
			if err != nil {
				return nil, err
			}

			constraints = append(constraints, child...)
		}

		return constraints, nil
	case string:
		if prefix == "" {
			return nil, errors.New("hardware constraint has no key")
		}

		return []string{prefix + " " + typed}, nil
	case float64, bool:
		if prefix == "" {
			return nil, errors.New("hardware constraint has no key")
		}

		return []string{fmt.Sprintf("%s = %v", prefix, typed)}, nil
	default:
		return nil, fmt.Errorf("unsupported hardware constraint %#q of type %T", prefix, value)
	}
}

func decodeTMTConfig(raw map[string]any) (tmtConfig, error) {
	source, ok := raw["source"].(map[string]any)
	if !ok {
		return tmtConfig{}, errors.New("missing 'tmt.source'")
	}

	gitURL, _ := source["git-url"].(string)
	ref, _ := source["ref"].(string)

	sourceConfig := projectconfig.GitSourceConfig{GitURL: gitURL, Ref: ref}
	if err := sourceConfig.Validate("'tmt.source'"); err != nil {
		return tmtConfig{}, fmt.Errorf("validate 'tmt.source':\n%w", err)
	}

	plan, _ := raw["plan"].(string)
	if !strings.HasPrefix(plan, "/") {
		return tmtConfig{}, errors.New("'tmt.plan' must be an absolute plan name")
	}

	return tmtConfig{Source: tmtSource{GitURL: gitURL, Ref: ref}, Plan: plan}, nil
}

func validateTMTProvision(provision string) error {
	if provision != tmtProvisionLocal && provision != tmtProvisionVirtual {
		return fmt.Errorf("'--provision' must be either %q or %q", tmtProvisionLocal, tmtProvisionVirtual)
	}

	return nil
}

func validateTMTProvisionOptions(provision string, imagePath string) error {
	if err := validateTMTProvision(provision); err != nil {
		return err
	}

	if provision == tmtProvisionLocal && imagePath != "" {
		return errors.New("'--image-path' cannot be used with '--provision local'")
	}

	return nil
}

func runHostCommand(env *azldev.Env, dir string, program string, args ...string) error {
	command := exec.CommandContext(env, program, args...)
	command.Dir = dir
	command.Stdout = os.Stdout

	var stderr bytes.Buffer

	command.Stderr = io.MultiWriter(os.Stderr, &stderr)

	wrapped, err := env.Command(command)
	if err != nil {
		return fmt.Errorf("wrap host command %#q:\n%w", program, err)
	}

	if err := wrapped.Run(env); err != nil {
		if trimmedStderr := strings.TrimSpace(stderr.String()); trimmedStderr != "" {
			return fmt.Errorf("run host command %#q:\n%s\n%w", program, trimmedStderr, err)
		}

		return fmt.Errorf("run host command %#q:\n%w", program, err)
	}

	return nil
}

func runHostCommandOutput(env *azldev.Env, dir string, program string, args ...string) ([]byte, error) {
	command := exec.CommandContext(env, program, args...)
	command.Dir = dir

	var stderr bytes.Buffer

	command.Stderr = &stderr

	wrapped, err := env.Command(command)
	if err != nil {
		return nil, fmt.Errorf("wrap host command %#q:\n%w", program, err)
	}

	output, err := wrapped.RunAndGetOutput(env)
	if err != nil {
		if trimmedStderr := strings.TrimSpace(stderr.String()); trimmedStderr != "" {
			return nil, fmt.Errorf("run host command %#q:\n%s\n%w", program, trimmedStderr, err)
		}

		return nil, fmt.Errorf("run host command %#q:\n%w", program, err)
	}

	return []byte(output), nil
}

// runTMTCommand runs the TMT CLI with the environment produced by
// [tmtCommandEnv] layered on top of the inherited process environment.
func runTMTCommand(
	env *azldev.Env, dir string, pluginDir string, tmtProgramPath string, provision string, args ...string,
) error {
	command := exec.CommandContext(env, tmtProgramPath, args...)
	command.Dir = dir
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	command.Env = append(os.Environ(), tmtCommandEnv(env, pluginDir, provision)...)

	wrapped, err := env.Command(command)
	if err != nil {
		return fmt.Errorf("wrap tmt command:\n%w", err)
	}

	if err := wrapped.Run(env); err != nil {
		return fmt.Errorf("run tmt command:\n%w", err)
	}

	return nil
}

// tmtCommandEnv returns the environment variables azldev adds on top of the
// inherited process environment when invoking TMT.
func tmtCommandEnv(env *azldev.Env, pluginDir string, provision string) []string {
	var vars []string

	if pluginDir != "" {
		vars = append(vars, "TMT_PLUGINS="+pluginDir)
	}

	// Give cloud-init a longer, but bounded, time to make the guest reachable.
	// Respect an explicit user setting for unusually fast or slow hosts.
	if env.OSEnv().Getenv("TMT_BOOT_TIMEOUT") == "" {
		vars = append(vars, "TMT_BOOT_TIMEOUT=300")
	}

	// The local provisioner executes the plan on this machine, which TMT
	// refuses to do without an explicit acknowledgement.
	if provision == tmtProvisionLocal {
		vars = append(vars, "TMT_FEELING_SAFE=1")
	}

	return vars
}

// writeTestcloudPlugin installs a tiny TMT plugin which adds a cloud-init
// workaround before testcloud's readiness check. Azure Linux VM images can run
// firewalld with no default allowance for testcloud's SSH endpoint.
func writeTestcloudPlugin(env *azldev.Env, testDir string) (string, error) {
	workarounds := []string{
		// Fedora Podman plans use this standard test account for linger and
		// rootless-container setup. Azure Linux cloud images do not ship it.
		"id -u fedora >/dev/null 2>&1 || useradd --create-home --groups wheel fedora",
		"firewall-cmd --add-service=ssh || :",
	}

	encodedWorkarounds, err := json.Marshal(workarounds)
	if err != nil {
		return "", fmt.Errorf("encode testcloud workarounds:\n%w", err)
	}

	pluginDir := filepath.Join(testDir, "tmt-plugins")
	if err := env.FS().MkdirAll(pluginDir, fileperms.PublicDir); err != nil {
		return "", fmt.Errorf("create testcloud plugin directory:\n%w", err)
	}

	plugin := fmt.Sprintf(testcloudPluginTemplate, encodedWorkarounds)

	pluginPath := filepath.Join(pluginDir, "azldev_testcloud.py")
	if err := fileutils.WriteFile(env.FS(), pluginPath, []byte(plugin), fileperms.PrivateFile); err != nil {
		return "", fmt.Errorf("write testcloud plugin:\n%w", err)
	}

	return pluginDir, nil
}
