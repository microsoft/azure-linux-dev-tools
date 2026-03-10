// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/auribuo/stylishcobra"
	"github.com/charmbracelet/lipgloss"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/muesli/termenv"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"go.szostok.io/version"
	"golang.org/x/sys/unix"
)

// Type of a callback function that can be registered for invocation after the application
// has loaded configuration, but before it fully parses command-line arguments.
type PostInitCallbackFunc func(app *App, env *Env) error

// An instance of the azldev CLI application. This is typically used as a singleton.
type App struct {
	// Global options for the CLI.
	explicitProjectDir      string
	verbose                 bool
	quiet                   bool
	acceptAllPrompts        bool
	dryRun                  bool
	networkRetries          int
	reportFormat            ReportFormat
	disableDefaultConfig    bool
	permissiveConfigParsing bool
	configFiles             []string
	colorMode               ColorMode

	// Root command for the CLI.
	cmd cobra.Command

	// Dependencies
	fsFactory    opctx.FileSystemFactory
	osEnvFactory opctx.OSEnvFactory

	// Registered callbacks to be invoked after configuration has been loaded.
	postInitCallbacks []PostInitCallbackFunc
}

// appDryRunnable is an implementation of [opctx.DryRunnable] that is used to
// determine whether the application is running in dry run mode.
type appDryRunnable struct {
	dryRun bool
}

// NewAppDryRunnable constructs a new [appDryRunnable] instance with the specified dry run mode.
func NewAppDryRunnable(dryRun bool) *appDryRunnable {
	return &appDryRunnable{dryRun: dryRun}
}

// DryRun implements [opctx.DryRunnable].
func (d appDryRunnable) DryRun() bool {
	return d.dryRun
}

func getUsageInfo(version string) string {
	return "🐧 Azure Linux Dev Tool " + version
}

func getDisplayVersion() string {
	ver := version.Get().Version
	if ver == "(devel)" {
		ver = "0.0.0-devel"
	}

	return ver
}

// Constructs a new CLI application instance.
func NewApp(fsFactory opctx.FileSystemFactory, osEnvFactory opctx.OSEnvFactory) *App {
	app := &App{
		colorMode:    ColorModeAuto,     // Default to auto-colorization.
		reportFormat: ReportFormatTable, // Default to table format for reports.
		fsFactory:    fsFactory,
		osEnvFactory: osEnvFactory,
	}

	displayVersion := getDisplayVersion()
	usageInfo := getUsageInfo(displayVersion)

	// Define the top-level command for the CLI.
	app.cmd = cobra.Command{
		Use:   "azldev",
		Short: usageInfo,
		Long: `Azure Linux Dev Tool (azldev) manages Azure Linux projects, components,
images, and builds. It provides a unified CLI for the full development
workflow: creating projects, importing and building RPM packages,
customizing images, and more.

Run azldev from the root of an Azure Linux project (where azldev.toml
lives), or use -C to point to one.`,
		Version: displayVersion,
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			slog.Debug("Command annotations", "annotations", command.Annotations)

			if _, ok := command.Annotations[CommandAnnotationRootOK]; !ok && os.Geteuid() == 0 {
				return errors.New("this command may not be run as root")
			}

			env, err := GetEnvFromCommand(command)
			if err != nil {
				return err
			}

			// For any environmental flags that are parsed from global flags during the final
			// argument parse, we must apply them here. (They aren't available yet any earlier).
			env.SetDefaultReportFormat(app.reportFormat)
			env.SetAcceptAllPrompts(app.acceptAllPrompts)
			env.SetColorMode(app.colorMode)
			env.SetNetworkRetries(app.networkRetries)
			env.SetPermissiveConfigParsing(app.permissiveConfigParsing)

			return nil
		},
		// Silence errors, as we handle them ourselves; note that this will get
		// effectively inherited by all subcommands since it's set on the root.
		SilenceErrors: true,
	}

	// Define command groups.
	app.cmd.AddGroup(&cobra.Group{
		ID:    CommandGroupPrimary,
		Title: "Primary commands:",
	})
	app.cmd.AddGroup(&cobra.Group{
		ID:    CommandGroupMeta,
		Title: "Meta commands:",
	})

	app.cmd.SetHelpCommandGroupID(CommandGroupMeta)
	app.cmd.SetCompletionCommandGroupID(CommandGroupMeta)

	// Define global flags and configuration settings.
	app.cmd.PersistentFlags().BoolVarP(&app.verbose, "verbose", "v", false, "enable verbose output")
	app.cmd.PersistentFlags().BoolVarP(&app.quiet, "quiet", "q", false, "only enable minimal output")
	app.cmd.PersistentFlags().BoolVarP(&app.acceptAllPrompts, "accept-all", "y", false, "accept all prompts")
	app.cmd.PersistentFlags().BoolVar(&app.disableDefaultConfig, "no-default-config", false,
		"disable default configuration")
	app.cmd.PersistentFlags().StringVarP(&app.explicitProjectDir, "project", "C", "",
		"path to Azure Linux project")
	app.cmd.PersistentFlags().StringArrayVar(&app.configFiles, "config-file", nil,
		"additional TOML config file(s) to merge (may be repeated)")
	app.cmd.PersistentFlags().BoolVarP(&app.dryRun, "dry-run", "n", false, "dry run only (do not take action)")
	app.cmd.PersistentFlags().VarP(&app.reportFormat, "output-format", "O",
		"output format {csv, json, markdown, table}")
	app.cmd.PersistentFlags().Var(&app.colorMode, "color",
		"output colorization mode {always, auto, never}")
	app.cmd.PersistentFlags().BoolVar(&app.permissiveConfigParsing, "permissive-config",
		false, "do not fail on unknown fields in TOML config files")

	return app
}

// Returns the names of the app's commands. The optional provided list of ancestors
// instructs this function to traverse through the command hierarchy before retrieving
// child names. This function is largely intended for tests to introspect on the
// commands available in a given App instance.
func (a *App) CommandNames(ancestors ...string) ([]string, error) {
	// Start at the root.
	cursor := &a.cmd

	// Walk through the ancestors provided, in order.
	for _, ancestor := range ancestors {
		found := false

		// Look up this ancestor name in the current cursor's children.
		for _, child := range cursor.Commands() {
			if child.Use == ancestor {
				cursor = child
				found = true

				break
			}
		}

		if !found {
			return nil, fmt.Errorf("ancestor command not found: %s", ancestor)
		}
	}

	// We found the command; collect and return the names of its immediate children.
	names := lo.Map(cursor.Commands(), func(cmd *cobra.Command, _ int) string {
		return cmd.Use
	})

	return names, nil
}

// Adds the given command as a new top-level command to the CLI.
func (a *App) AddTopLevelCommand(child *cobra.Command) {
	// Only set the default group if no group is already set
	if child.GroupID == "" {
		child.GroupID = CommandGroupPrimary
	}

	a.cmd.AddCommand(child)
}

// Registers a callback that will be executed after configuration has been loaded.
func (a *App) RegisterPostInitCallback(callback PostInitCallbackFunc) {
	a.postInitCallbacks = append(a.postInitCallbacks, callback)
}

// Main entry point for the azldev CLI. Responsible for parsing command-line arguments,
// initializing logging, loading configuration, and executing the requested command.
func (a *App) Execute(args []string) int {
	//
	// Minimal hand-parse of flags that may influence logging or configuration.
	// We may dynamically register more subcommands based on the configuration.
	// We tried to do this with cobra first, but it was too difficult to get
	// the "right thing" to happen.
	//
	a.handParseConfigFlags(args)

	envOptions := a.initializeEnvOptions()

	//
	// Init logging as early as possible, but without a file log; messages will only
	// be delivered to standard I/O.
	//
	stdioLogger := a.initStdioLogging()

	if err := setEventListener(stdioLogger, envOptions); err != nil {
		slog.Error("Error setting event listener.", "err", err)

		return 1
	}

	//
	// Configuration loading may require a temporary directory to write out default
	// config files. Since we don't have a proper work directory until after we load
	// configuration, we need an "early" temp directory that we'll auto-clean.
	//
	earlyTempDirPath, err := fileutils.MkdirTempInTempDir(a.fsFactory.FS(), "azldev-tmp-")
	if err != nil {
		slog.Error("Error creating early temp dir for configuration loading.", "err", err)

		return 1
	}

	defer os.RemoveAll(earlyTempDirPath)

	//
	// Set up root context and allocate a cancellation channel.
	//
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	//
	// Set up an environment object for the CLI. This will be what all commands
	// will primarily interact with to query configuration. Note that the
	// project dir and config may not be present. Downstream consumers that depend
	// on them will need to check them appropriately.
	//
	env := NewEnv(ctx, *envOptions)
	env.SetVerbose(a.verbose)
	env.SetQuiet(a.quiet)
	env.SetColorMode(a.colorMode)
	//
	// If we managed to find a project + configuration, then we can let anyone who was
	// interested have an opportunity to add subcommands (or do whatever they need to
	// do for their initialization). This allows for initialization dependent on the
	// configuration and the project we've been configured for.
	//
	if err = a.handlePostInitCallbacks(env); err != nil {
		slog.Error("Error handling post-init callbacks.", "err", err)

		return 1
	}

	//
	// Remove any intermediate commands that didn't get children.
	//
	a.removeEmptyCommands()

	//
	// Make sure graceful cancellation happens.
	//
	a.setupSignalHandling(cancel)

	//
	// Finally, we dispatch to the command. On failure, make sure to let the user know
	// where the verbose log is stored.
	//
	return a.dispatchToCommand(env, args)
}

func (a *App) initializeEnvOptions() *EnvOptions {
	envOptions := NewEnvOptions()
	envOptions.Interfaces.FileSystemFactory = a.fsFactory
	envOptions.Interfaces.OSEnvFactory = a.osEnvFactory
	envOptions.DryRunnable = NewAppDryRunnable(a.dryRun)

	return &envOptions
}

// We cancel on normal exit from this function, as well as when a SIGINT or SIGTERM is received.
func (*App) setupSignalHandling(cancel context.CancelFunc) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, unix.SIGTERM)

	go func() {
		for sig := range sigs {
			slog.Warn("Interrupt detected", "kind", sig)
			cancel()
		}
	}()
}

func (a *App) handlePostInitCallbacks(env *Env) error {
	err := a.callPostInitCallbacks(env)
	if err != nil {
		return fmt.Errorf("error during post-config initialization:\n%w", err)
	}

	return nil
}

func setEventListener(stdioLogger *slog.Logger, envOptions *EnvOptions) error {
	eventListener, err := NewEventListener(stdioLogger)
	if err != nil {
		return fmt.Errorf("error initializing event listener:\n%w", err)
	}

	envOptions.EventListener = eventListener

	return nil
}

// Hand-parses a few critical configuration flags from the command line -- just enough to
// find the project and load configuration so we can properly use cobra facilities to
// parse the full command line.
func (a *App) handParseConfigFlags(args []string) {
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; args[index] {
		// We parse verbosity flags since we need to know them to correctly initialize logging.
		case "-v", "--verbose":
			a.verbose = true
		case "-q", "--quiet":
			a.quiet = true
		// We parse the project directory and config file flags here, since we need to know
		// them in order to find the root and load configuration. We need to do that before
		// we do a proper argument parse due to configuration-sensitive command registrations.
		case "-C", "--project":
			index++
			if index < len(args) {
				a.explicitProjectDir = args[index]
			}
		case "--no-default-config":
			a.disableDefaultConfig = true
		case "--permissive-config":
			a.permissiveConfigParsing = true
		case "--config-file":
			index++
			if index < len(args) {
				a.configFiles = append(a.configFiles, args[index])
			}
		case "-n", "--dry-run":
			a.dryRun = true
		case "--color":
			index++
			if index < len(args) {
				_ = a.colorMode.Set(args[index])
			}
		default:
			a.handParsePrefixedFlags(arg)
		}
	}
}

// handParsePrefixedFlags handles flag variants with = assignment syntax (e.g., --project=value).
func (a *App) handParsePrefixedFlags(arg string) {
	switch {
	case strings.HasPrefix(arg, "-C"):
		a.explicitProjectDir = strings.TrimPrefix(arg, "-C")
	case strings.HasPrefix(arg, "--project="):
		a.explicitProjectDir = strings.TrimPrefix(arg, "--project=")
	case strings.HasPrefix(arg, "--color="):
		_ = a.colorMode.Set(strings.TrimPrefix(arg, "--color="))
	case strings.HasPrefix(arg, "--config-file="):
		a.configFiles = append(a.configFiles, strings.TrimPrefix(arg, "--config-file="))
	}
}

// Initializes stdio-only logging.
func (a *App) initStdioLogging() *slog.Logger {
	logger := slog.New(a.createStdioLogHandler())
	slog.SetDefault(logger)

	return logger
}

// shouldDisableColor determines whether color output should be disabled based on the
// application's configuration and the environment.
func (a *App) shouldDisableColor() bool {
	switch a.colorMode {
	case ColorModeAlways:
		return false
	case ColorModeNever:
		return true
	case ColorModeAuto:
		fallthrough
	default:
		return !isatty.IsTerminal(os.Stdout.Fd())
	}
}

func (a *App) createStdioLogHandler() slog.Handler {
	stdioHandler := tint.NewHandler(os.Stderr, &tint.Options{
		Level:      a.getLogLevel(),
		TimeFormat: time.TimeOnly,
		NoColor:    a.shouldDisableColor(),
	})

	return stdioHandler
}

func (a *App) getLogLevel() slog.Level {
	switch {
	case a.verbose:
		return slog.LevelDebug
	case a.quiet:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// Invokes all registered post-init callbacks. Fails early if any callback returns an error.
func (a *App) callPostInitCallbacks(env *Env) error {
	for _, callback := range a.postInitCallbacks {
		err := callback(a, env)
		if err != nil {
			return err
		}
	}

	return nil
}

// Removes any intermediate commands that don't have children.
func (a *App) removeEmptyCommands() {
	for _, cmd := range a.cmd.Commands() {
		if cmd.Run == nil && cmd.RunE == nil && !cmd.HasSubCommands() {
			a.cmd.RemoveCommand(cmd)
		}
	}
}

// Actually dispatches control to the command, passing along the provided arguments. Returns the
// final exit code that should be percolated up.
func (a *App) dispatchToCommand(env *Env, args []string) int {
	// Perform any final updates to the command before executing it: fill in arguments,
	// apply any styles, etc.
	a.cmd.SetArgs(args)
	a.applyStylesToCommand()

	err := a.cmd.ExecuteContext(env)
	if err != nil {
		slog.Error("Error: " + err.Error())

		return 1
	}

	return 0
}

func (a *App) applyStylesToCommand() {
	// Apply styles to the command using the stylishcobra package. We unconditionally disable extra newlines
	// to produce a more compact output.
	config := stylishcobra.Setup(&a.cmd).DisableExtraNewlines()

	// If colorization is enabled, apply colors and other styles.
	if !a.shouldDisableColor() {
		// If we're force-enabling color, then make sure that lipgloss won't disable colors on us.
		if a.colorMode == ColorModeAlways && lipgloss.ColorProfile() == termenv.Ascii {
			lipgloss.SetColorProfile(termenv.TrueColor)
		}

		// Apply our preferred styles.
		config.
			StyleHeadings(
				lipgloss.NewStyle().Underline(true).Bold(true).Foreground(lipgloss.ANSIColor(termenv.ANSIBrightGreen)),
			).
			StyleCommands(lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(termenv.ANSICyan)).Bold(true)).
			StyleAliases(lipgloss.NewStyle().Bold(true).Italic(true)).
			StyleExample(lipgloss.NewStyle().Italic(true)).
			StyleExecName(lipgloss.NewStyle().Bold(true).Italic(true)).
			StyleFlags(lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(termenv.ANSICyan)).Bold(true)).
			StyleFlagsDataType(lipgloss.NewStyle().Foreground(lipgloss.Color("#444444")).Italic(true))
	}

	// Call Init() to apply the built-up styles.
	config.Init()
}
