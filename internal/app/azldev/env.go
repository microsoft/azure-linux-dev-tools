// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/charmbracelet/gum/confirm"
	"github.com/mattn/go-isatty"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// Parameters used to construct an [Env].
type EnvOptions struct {
	// Path to the project's root directory.
	ProjectDir string

	// The loaded configuration for the project.
	Config *projectconfig.ProjectConfig

	// Injected dependencies.
	DryRunnable   opctx.DryRunnable
	EventListener opctx.EventListener
	Interfaces    SystemInterfaces
}

// Constructs default [EnvOptions].
func NewEnvOptions() EnvOptions {
	return EnvOptions{ProjectDir: "", Config: nil}
}

// Ensure that Env implements [opctx.Ctx].
var _ opctx.Ctx = &Env{}

// Core environment structure, available to CLI commands. Implements [opctx.Ctx]
// for use with lower-level packages.
type Env struct {
	// Root of the project directory.
	projectDir string
	// Temporary working directory (intermediate artifacts)
	workDir string
	// Log directory.
	logsDir string
	// Output directory.
	outputDir string

	// Path specific to classic AZL3 (or earlier) toolkit.
	// NOTE: This is hardcoded for now, but should be removed or factored out in the future.
	classicToolkitDir string

	// Tool behavior/preferences.
	defaultReportFormat     ReportFormat
	colorMode               ColorMode
	reportFile              io.Writer
	verbose                 bool
	quiet                   bool
	promptsAllowed          bool
	acceptAllPrompts        bool
	networkRetries          int
	permissiveConfigParsing bool

	// Injected dependencies.
	cmdFactory    opctx.CmdFactory
	dryRunnable   opctx.DryRunnable
	eventListener opctx.EventListener
	fsFactory     opctx.FileSystemFactory
	osEnvFactory  opctx.OSEnvFactory

	// Deserialized project-specific configuration.
	config *projectconfig.ProjectConfig

	// Top-level context.
	//nolint:containedctx // We embed a context so we don't have to pass it *and* the env around everywhere.
	ctx context.Context

	// Start time: used for consistent timestamping of artifacts.
	constructionTime time.Time
}

// Constructs a new [Env] using specified options.
func NewEnv(ctx context.Context, options EnvOptions) *Env {
	var workDir, logDir, outputDir string

	if options.Config != nil {
		workDir = options.Config.Project.WorkDir
		logDir = options.Config.Project.LogDir
		outputDir = options.Config.Project.OutputDir
	}

	var classicToolkitDir string
	if options.ProjectDir != "" {
		classicToolkitDir = filepath.Join(options.ProjectDir, "toolkit")
	}

	return &Env{
		// Context
		ctx: ctx,

		// Locations
		projectDir: options.ProjectDir,
		workDir:    workDir,
		logsDir:    logDir,
		outputDir:  outputDir,

		// NOTE: This is hardcoded for now, but should be removed or factored out in the future.
		classicToolkitDir: classicToolkitDir,

		// Loaded configuration
		config: options.Config,

		// Injected dependencies.
		cmdFactory:    options.Interfaces.CmdFactory,
		dryRunnable:   options.DryRunnable,
		eventListener: options.EventListener,
		fsFactory:     options.Interfaces.FileSystemFactory,
		osEnvFactory:  options.Interfaces.OSEnvFactory,

		// Tool behavior/preferences
		defaultReportFormat:     ReportFormatTable,
		colorMode:               ColorModeAuto,
		reportFile:              os.Stdout,
		verbose:                 false,
		quiet:                   false,
		promptsAllowed:          isatty.IsTerminal(os.Stdin.Fd()),
		permissiveConfigParsing: false,

		// Start time.
		constructionTime: time.Now(),
	}
}

// Returns whether all prompts should be auto-accepted without being displayed.
func (env *Env) AllPromptsAccepted() bool {
	return env.acceptAllPrompts
}

// Indicates whether the user has requested a "dry run" where no changes are made to
// the host system, and no non-trivial computation occurs.
func (env *Env) DryRun() bool {
	return env.dryRunnable.DryRun()
}

// Returns whether user blocking prompts are allowed.
func (env *Env) PromptsAllowed() bool {
	return env.promptsAllowed
}

// Returns whether the user has requested a "quiet" run where only minimal output is displayed.
func (env *Env) Quiet() bool {
	return env.quiet
}

// SetQuiet enables or disables "quiet" mode.
func (env *Env) SetQuiet(quiet bool) {
	env.quiet = quiet
}

// Returns whether the user has requested a "verbose" run where debug output is displayed to stdio.
func (env *Env) Verbose() bool {
	return env.verbose
}

// SetVerbose enables or disables "verbose" mode.
func (env *Env) SetVerbose(verbose bool) {
	env.verbose = verbose
}

// NetworkRetries returns the maximum number of attempts for network operations.
func (env *Env) NetworkRetries() int {
	return env.networkRetries
}

// SetNetworkRetries sets the maximum number of attempts for network operations.
// Values less than 1 are clamped to 1.
func (env *Env) SetNetworkRetries(retries int) {
	if retries < 1 {
		retries = 1
	}

	env.networkRetries = retries
}

// PermissiveConfigParsing returns whether permissive parsing of configuration files
// is enabled, where unknown fields are ignored instead of causing an error.
func (env *Env) PermissiveConfigParsing() bool {
	return env.permissiveConfigParsing
}

// SetPermissiveConfigParsing enables or disables permissive parsing of
// configuration files, where unknown fields are ignored instead of causing an error.
func (env *Env) SetPermissiveConfigParsing(permissive bool) {
	env.permissiveConfigParsing = permissive
}

// SetEventListener registers the event listener to be used in this environment.
func (env *Env) SetEventListener(eventListener opctx.EventListener) {
	env.eventListener = eventListener
}

// Retrieves the ambient [context.Context] associated with this environment.
func (env *Env) Context() context.Context {
	return env.ctx
}

// ConfirmAutoResolution prompts the user to confirm auto-resolution of a problem. The provided
// text is displayed to the user as explanation.
func (env *Env) ConfirmAutoResolution(text string) bool {
	if env.AllPromptsAccepted() {
		return true
	}

	if env.PromptsAllowed() {
		options := confirm.Options{
			Prompt:  text + " (Y/n)",
			Default: true,
		}

		err := options.Run()

		return err == nil
	}

	return false
}

// Event implements the [opctx.EventListener] interface.
//
// Records an event and immediately ends it.
func (env *Env) Event(name string, args ...any) {
	env.eventListener.StartEvent(name, args...).End()
}

// StartEvent implements the [opctx.EventListener] interface.
func (env *Env) StartEvent(name string, args ...any) opctx.Event {
	return env.eventListener.StartEvent(name, args...)
}

// Returns the file path to the loaded project's root directory.
func (env *Env) ProjectDir() string {
	return env.projectDir
}

// Returns the file path to the loaded project's internal working directory.
func (env *Env) WorkDir() string {
	return env.workDir
}

// Returns the file path to the loaded project's log directory.
func (env *Env) LogsDir() string {
	return env.logsDir
}

// Returns the file path to the loaded project's output directory.
func (env *Env) OutputDir() string {
	return env.outputDir
}

// Enables or disables "accept all prompts" mode.
func (env *Env) SetAcceptAllPrompts(acceptAllPrompts bool) {
	env.acceptAllPrompts = acceptAllPrompts
}

// Returns the default report format for this environment.
func (env *Env) DefaultReportFormat() ReportFormat {
	return env.defaultReportFormat
}

// Sets the default report format for this environment.
func (env *Env) SetDefaultReportFormat(format ReportFormat) {
	env.defaultReportFormat = format
}

// Returns the color mode for output from this environment.
func (env *Env) ColorMode() ColorMode {
	return env.colorMode
}

// Sets the color mode for output from this environment.
func (env *Env) SetColorMode(colorMode ColorMode) {
	env.colorMode = colorMode
}

// Returns the writer to be used for writing result reports.
func (env *Env) ReportFile() io.Writer {
	return env.reportFile
}

// Sets the writer to be used for writing result reports.
func (env *Env) SetReportFile(reportFile io.Writer) {
	env.reportFile = reportFile
}

// Resolves the environment's default "distro" -- i.e., the distro that is being built in and against.
// On success, returns back the definition of the distro as well as the definition of the specific
// version of the distro.
func (env *Env) Distro() (
	distroDef projectconfig.DistroDefinition, distroVersionDef projectconfig.DistroVersionDefinition, err error,
) {
	if env.config == nil {
		return distroDef, distroVersionDef, errors.New("can't resolve distro: no project config loaded")
	}

	if env.config.Project.DefaultDistro.Name == "" {
		return distroDef, distroVersionDef, errors.New("no default distro selected in project config")
	}

	return env.ResolveDistroRef(env.config.Project.DefaultDistro)
}

// ResolveDistroRef resolves a distro reference to the actual distro definition and version.
func (env *Env) ResolveDistroRef(distroRef projectconfig.DistroReference) (
	distroDef projectconfig.DistroDefinition, distroVersionDef projectconfig.DistroVersionDefinition, err error,
) {
	var distroFound bool

	//
	// Look up the distro in the project config.
	//

	if env.config == nil {
		return distroDef, distroVersionDef, errors.New("can't resolve distro: no project config loaded")
	}

	if distroDef, distroFound = env.config.Distros[distroRef.Name]; !distroFound {
		return distroDef, distroVersionDef, fmt.Errorf("distro '%s' not found in project config", distroRef.Name)
	}

	//
	// We have the distro; figure out which version we want to look up. If one was not specified in the
	// reference, then inherit the default version from the distro definition.
	//

	version := distroRef.Version
	if version == "" {
		version = distroDef.DefaultVersion
	}

	if version == "" {
		return distroDef, distroVersionDef, errors.New("no distro version selected in project config")
	}

	var distroVersionFound bool

	if distroVersionDef, distroVersionFound = distroDef.Versions[version]; !distroVersionFound {
		return distroDef, distroVersionDef,
			fmt.Errorf("distro version '%s' not found in distro '%s'", version, distroRef.Name)
	}

	return distroDef, distroVersionDef, nil
}

// Returns the loaded project configuration. Note that the configuration may include raw
// data that hasn't yet been resolved. For querying resolved component configuration, the
// [components] package should be used instead.
func (env *Env) Config() *projectconfig.ProjectConfig {
	return env.config
}

// Deadline implements the [context.Context] interface.
func (env *Env) Deadline() (deadline time.Time, ok bool) {
	return env.ctx.Deadline()
}

// Done implements the [context.Context] interface.
func (env *Env) Done() <-chan struct{} {
	return env.ctx.Done()
}

// Err implements the [context.Context] interface.
//
//nolint:wrapcheck // We are intentionally just forwarding the call.
func (env *Env) Err() error {
	return env.ctx.Err()
}

// Value implements the [context.Context] interface.
func (env *Env) Value(key any) any {
	return env.ctx.Value(key)
}

// Command implements the [opctx.CmdFactory] interface.
func (env *Env) Command(cmd *exec.Cmd) (opctx.Cmd, error) {
	//nolint:wrapcheck // We are intentionally just forwarding the call.
	return env.cmdFactory.Command(cmd)
}

// CommandInSearchPath implements the [opctx.CmdFactory] interface.
func (env *Env) CommandInSearchPath(name string) bool {
	return env.cmdFactory.CommandInSearchPath(name)
}

// FS implements the [opctx.FileSystemFactory] interface.
func (env *Env) FS() opctx.FS {
	return env.fsFactory.FS()
}

// OSEnv implements the [opctx.OSEnvFactory] interface.
func (env *Env) OSEnv() opctx.OSEnv {
	return env.osEnvFactory.OSEnv()
}

// Reports the time that this environment was constructed.
func (env *Env) ConstructionTime() time.Time {
	return env.constructionTime
}
