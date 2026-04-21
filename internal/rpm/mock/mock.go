// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/brunoga/deep"
	"github.com/fatih/color"
	"github.com/kballard/go-shellquote"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/hostinfo"
	"github.com/samber/lo"
)

const (
	// The name of the host user group that the calling user must be a member of to run "mock".
	MockGroup = "mock"
	// The name of the mock executable.
	MockBinary = "mock"
)

// Encapsulates options for invoking mock.
type Runner struct {
	//
	// NOTE: Any updates to the struct must be reflected in the implementation of [Clone].
	//

	// Injected dependencies
	fs            opctx.FS
	osEnv         opctx.OSEnv
	cmdFactory    opctx.CmdFactory
	eventListener opctx.EventListener

	// Verbosity
	verbose bool

	mockConfigPath string
	bindMounts     []bindMountRequest
	enableNetwork  bool

	// noPreClean requests that the root *not* be cleaned on command execution, even if mock deems
	// it ought to be.
	noPreClean bool

	// baseDir is the base directory where mock will create all of its root directories. If not set,
	// it defaults to the host system default (typically, /var/lib/mock).
	baseDir string // optional

	// rootDir is the directory where the mock root will be created. If not set, it defaults to
	// being created under the baseDir.
	rootDir string // optional

	// configOpts is an optional set of key-value pairs that will be passed through to mock as
	// --config-opts key=value arguments, allowing callers to override mock's configuration.
	configOpts map[string]string

	// unprivileged requests that chroot commands run as the unprivileged mockbuild
	// user instead of root. Corresponds to mock's --unpriv flag.
	unprivileged bool
}

// BuildLogDetails encapsulates details extracted from mock build logs that may be relevant to
// understanding the cause of a build failure.
type BuildLogDetails struct {
	// RPMBuildErrors is a list of error messages extracted from mock build logs.
	RPMBuildErrors []string

	// LastRPMBuildLogLines is a list of the last lines of the mock build log, which may contain
	// relevant context about why a build failure occurred.
	LastRPMBuildLogLines []string
}

type bindMountRequest struct {
	hostPath     string
	mockRootPath string
}

// Constructs a new [Runner] that can be used to invoke mock.
func NewRunner(ctx opctx.Ctx, mockConfigPath string) *Runner {
	return &Runner{
		fs:             ctx.FS(),
		osEnv:          ctx.OSEnv(),
		cmdFactory:     ctx,
		eventListener:  ctx,
		verbose:        ctx.Verbose(),
		mockConfigPath: mockConfigPath,
	}
}

// Clone creates a deep copy of the provided [Runner] instance.
func (r *Runner) Clone() *Runner {
	return &Runner{
		fs:             r.fs,
		osEnv:          r.osEnv,
		cmdFactory:     r.cmdFactory,
		eventListener:  r.eventListener,
		verbose:        r.verbose,
		mockConfigPath: r.mockConfigPath,
		bindMounts:     deep.MustCopy(r.bindMounts),
		enableNetwork:  r.enableNetwork,
		noPreClean:     r.noPreClean,
		unprivileged:   r.unprivileged,
		baseDir:        r.baseDir,
		rootDir:        r.rootDir,
		configOpts:     deep.MustCopy(r.configOpts),
	}
}

// Updates the [Runner]'s configuration to add a bind mount, exposing the host
// directory at path `hostPath` in the mock root at path `mockRootPath`.
func (r *Runner) AddBindMount(hostPath, mockRootPath string) *Runner {
	r.bindMounts = append(r.bindMounts, bindMountRequest{
		hostPath:     hostPath,
		mockRootPath: mockRootPath,
	})

	return r
}

// BindMounts retrieves the set of bind mounts configured for this [Runner], expressed as
// a map from host path to mock root path.
func (r *Runner) BindMounts() map[string]string {
	return lo.SliceToMap(r.bindMounts, func(item bindMountRequest) (string, string) {
		return item.hostPath, item.mockRootPath
	})
}

// Updates the [Runner]'s configuration to enable external network access from within the mock root.
func (r *Runner) EnableNetwork() *Runner {
	r.enableNetwork = true

	return r
}

// HasNetworkEnabled indicates whether the [Runner] is configured to enable network access.
func (r *Runner) HasNetworkEnabled() bool {
	return r.enableNetwork
}

// WithNoPreClean updates the [Runner]'s configuration to ensure that mock does *not* pre-clean
// the root when invoking an operation.
func (r *Runner) WithNoPreClean() *Runner {
	r.noPreClean = true

	return r
}

// HasNoPreClean indicates whether the [Runner] is configured to avoid pre-cleaning the root.
func (r *Runner) HasNoPreClean() bool {
	return r.noPreClean
}

// WithUnprivileged configures the [Runner] to drop privileges before running
// chroot commands, using mock's --unpriv flag. Commands will run as the
// mockbuild user instead of root.
func (r *Runner) WithUnprivileged() *Runner {
	r.unprivileged = true

	return r
}

// HasUnprivileged indicates whether the [Runner] is configured to drop privileges
// for chroot commands.
func (r *Runner) HasUnprivileged() bool {
	return r.unprivileged
}

// WithBaseDir updates the [Runner]'s configuration to set which directory mock roots are created
// under by default. If not set, mock will write under its default base (/var/lib/mock).
func (r *Runner) WithBaseDir(baseDir string) *Runner {
	r.baseDir = baseDir

	return r
}

// BaseDir retrieves the path to the base directory used by this [Runner] for mock roots.
func (r *Runner) BaseDir() string {
	return r.baseDir
}

// WithRootDir update's the [Runner]'s configuration to set which directory the root is created
// under.
func (r *Runner) WithRootDir(rootDir string) *Runner {
	r.rootDir = rootDir

	return r
}

// RootDir retrieves the path to the mock root used by this [Runner].
func (r *Runner) RootDir() string {
	return r.rootDir
}

// WithConfigOpts updates the [Runner]'s configuration to set arbitrary config options that will
// be passed through to mock as --config-opts key=value arguments.
func (r *Runner) WithConfigOpts(opts map[string]string) *Runner {
	r.configOpts = opts

	return r
}

// ConfigOpts retrieves the set of arbitrary config options configured for this [Runner].
func (r *Runner) ConfigOpts() map[string]string {
	return r.configOpts
}

// Retrieves the path to the mock .cfg file used by this [Runner].
func (r *Runner) ConfigPath() string {
	return r.mockConfigPath
}

// InitRoot initializes a mock root.
func (r *Runner) InitRoot(ctx context.Context) (err error) {
	slog.Debug("Initializing mock root", "dir", r.rootDir)

	err = r.ensureMockPresentAndConfigured()
	if err != nil {
		return err
	}

	args := r.getBaseArgs()
	args = append(args, "--init")

	cmd := exec.CommandContext(ctx, MockBinary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	extcmd, err := r.cmdFactory.Command(cmd)
	if err != nil {
		return fmt.Errorf("failed to create external command for mock:\n%w", err)
	}

	if !r.verbose {
		extcmd.SetLongRunning("Waiting for mock (initializing build root)...")
	}

	err = extcmd.Run(ctx)
	if err != nil {
		return fmt.Errorf("mock failed to build SRPM:\n%w", err)
	}

	return nil
}

// Retrieves the path to the mock root used by this [Runner].
func (r *Runner) GetRootPath(ctx context.Context) (string, error) {
	slog.Debug("Finding mock root path")

	// We're going to need to run mock, so make sure we can. Note that even
	// basic operations like --print-root-path won't succeed if the user
	// isn't a member of the mock group, for example.
	err := r.ensureMockPresentAndConfigured()
	if err != nil {
		return "", err
	}

	// Invoke mock and ask it what the root path is. This is the most accurate
	// way to find the root, since we feed it the same config we'll use to build.
	args := r.getBaseArgs()
	args = append(args, "--print-root-path")

	cmd, err := r.cmdFactory.Command(exec.CommandContext(ctx, MockBinary, args...))
	if err != nil {
		return "", fmt.Errorf("failed to create command to get mock root path:\n%w", err)
	}

	rootPath, err := cmd.RunAndGetOutput(ctx)
	if err != nil {
		return "", fmt.Errorf("command to get mock root path failed:\n%w", err)
	}

	return strings.TrimSpace(rootPath), nil
}

// CommonBuildOptions encapsulates options that are shared between SRPM and RPM builds.
type CommonBuildOptions struct {
	With    []string
	Without []string
	Defines map[string]string

	LocalRepoPaths []string
}

// SRPMBuildOptions encapsulates options for building source RPMs using mock.
type SRPMBuildOptions struct {
	CommonBuildOptions
	// Add any SRPM-specific options here in the future if needed
}

// RPMBuildOptions encapsulates options for building binary RPMs using mock.
type RPMBuildOptions struct {
	CommonBuildOptions

	// Binary RPM specific options
	NoCheck      bool
	ForceRebuild bool
}

// Builds a Source RPM (SRPM) using mock.
func (r *Runner) BuildSRPM(
	ctx context.Context, specPath, sourceDirPath, outputDirPath string, options SRPMBuildOptions,
) error {
	slog.Debug("Building SRPM", "sourceDir", sourceDirPath)

	err := r.ensureMockPresentAndConfigured()
	if err != nil {
		return err
	}

	args := append(r.getBaseArgs(),
		"--buildsrpm",
		"--spec", specPath,
		"--sources", sourceDirPath,
		"--resultdir", outputDirPath,
	)

	// Add 'with'-style enabled conditionals.
	for _, with := range options.With {
		args = append(args, "--with", with)
	}

	// Add 'without'-style disabled conditionals.
	for _, without := range options.Without {
		args = append(args, "--without", without)
	}

	// Add override macros.
	for macro, value := range options.Defines {
		args = append(args, "--define", macro+" "+value)
	}

	// If provided, pass along extra local repo dirs.
	for _, localRepoPath := range options.LocalRepoPaths {
		absLocalRepoPath, err := filepath.Abs(localRepoPath)
		if err != nil {
			return fmt.Errorf("failed to get absolute path for local repo %#q:\n%w", localRepoPath, err)
		}

		args = append(args, "--addrepo", absLocalRepoPath)
	}

	cmd := exec.CommandContext(ctx, MockBinary, args...)
	cmd.Stdout = os.Stdout

	extcmd, err := r.cmdFactory.Command(cmd)
	if err != nil {
		return fmt.Errorf("failed to create external command for mock:\n%w", err)
	}

	if !r.verbose {
		extcmd.SetLongRunning("Waiting for mock (building SRPM)...")
	}

	// Watch output logs in real-time so we can asynchronously synthesize progress updates.
	err = addMockCmdListeners(r.eventListener, extcmd, outputDirPath)
	if err != nil {
		return err
	}

	err = extcmd.Run(ctx)
	if err != nil {
		return fmt.Errorf("mock failed to build SRPM:\n%w", err)
	}

	return nil
}

// Given a path to a Source RPM (SRPM), invokes mock to build a binary RPM.
func (r *Runner) BuildRPM(ctx context.Context, srpmPath, outputDirPath string, options RPMBuildOptions) error {
	slog.Debug("Building RPM", "SRPM", srpmPath)

	err := r.ensureMockPresentAndConfigured()
	if err != nil {
		return err
	}

	args := append(r.getBaseArgs(),
		"--resultdir",
		outputDirPath,
	)

	// If provided, pass along extra local repo dirs.
	for _, localRepoPath := range options.LocalRepoPaths {
		absLocalRepoPath, err := filepath.Abs(localRepoPath)
		if err != nil {
			return fmt.Errorf("failed to get absolute path for local repo %#q:\n%w", localRepoPath, err)
		}

		args = append(args, "--addrepo", absLocalRepoPath)
	}

	// If the output dir already contains a "relevant" binary RPM, mock
	// won't rebuild. If we need to force it for some reason, we can do so.
	if options.ForceRebuild {
		args = append(args, "--rebuild")
	}

	// Unless requested to skip checks, we run them.
	if options.NoCheck {
		args = append(args, "--nocheck")
	}

	// Add 'with'-style enabled conditionals.
	for _, with := range options.With {
		args = append(args, "--with", with)
	}

	// Add 'without'-style disabled conditionals.
	for _, without := range options.Without {
		args = append(args, "--without", without)
	}

	// Add override macros.
	for macro, value := range options.Defines {
		args = append(args, "--define", macro+" "+value)
	}

	args = append(args, srpmPath)

	// Arrange for stdout/stderr to be directly passed through. In typical
	// non-verbose usage, we added --quiet above and this won't lead to much
	// being passed through.
	cmd := exec.CommandContext(ctx, MockBinary, args...)
	cmd.Stdout = os.Stdout

	extcmd, err := r.cmdFactory.Command(cmd)
	if err != nil {
		return fmt.Errorf("failed to create external command for mock:\n%w", err)
	}

	extcmd = extcmd.SetLongRunning("Waiting for mock (building RPM)...")

	// Watch output logs in real-time so we can asynchronously synthesize progress updates.
	err = addMockCmdListeners(r.eventListener, extcmd, outputDirPath)
	if err != nil {
		return err
	}

	err = extcmd.Run(ctx)
	if err != nil {
		err = fmt.Errorf("mock failed to build RPM from SRPM at '%s':\n%w", srpmPath, err)

		return err
	}

	return err
}

// Adds to the given [opctx.Cmd] real-time file listeners that watch for interesting,
// well-known log files written by mock to the primary output directory. These listeners
// will only run for the duration of mock's invocation, allowing us to get insights
// into what's happening *inside* mock while we're otherwise opaquely blocking on its
// execution.
func addMockCmdListeners(eventListener opctx.EventListener, extcmd opctx.Cmd, outputDirPath string) error {
	// Color-code stderr lines.
	err := extcmd.SetRealTimeStderrListener(func(ctx context.Context, line string) {
		if strings.HasPrefix(line, "No matching package to install:") {
			color.Set(color.FgHiYellow)
		} else {
			color.Set(color.FgHiBlack, color.Italic)
		}

		defer color.Unset()

		fmt.Fprintf(os.Stderr, "%s\n", line)
	})
	if err != nil {
		return fmt.Errorf("failed to setup mock stderr listener: %w", err)
	}

	// Messages we care about in 'state.log' will look something like the following (without the indent):
	//     2021-05-19 16:20:11,859 - Start: rpmbuild test-1.0.0.src.rpm
	stateLogRe := regexp.MustCompile(`^[^ ]+ [^ ]+ - (.*)$`)

	// Watch well-known log 'state.log', which gives us very high-level information
	// about where we are in the mock build process.
	err = extcmd.AddRealTimeFileListener(
		filepath.Join(outputDirPath, "state.log"),
		func(_ context.Context, line string) {
			if matches := stateLogRe.FindStringSubmatch(line); len(matches) > 1 {
				body := matches[1]

				switch {
				case strings.HasPrefix(body, "Start: rpmbuild "):
					eventListener.Event("Invoking rpmbuild inside build root")
				case strings.HasPrefix(body, "Finish: rpmbuild "):
					eventListener.Event("Finished running rpmbuild inside build root")
				case strings.HasPrefix(body, "Start: chroot init"):
					eventListener.Event("Initializing mock build root")
				}
			}
		})
	if err != nil {
		return fmt.Errorf("failed to watch mock state.log:\n%w", err)
	}

	// Messages we care about in 'build.log' will look something like the following (without the indent):
	//     Executing(%prep): more details here
	buildLogRe := regexp.MustCompile(`^Executing\(([^\)]+)\):.*$`)

	// Watch well-known log file 'build.log', which has insights into what's happening
	// *inside* the mock build root (e.g., running %prep vs. running %build).
	err = extcmd.AddRealTimeFileListener(filepath.Join(outputDirPath, "build.log"), func(_ context.Context, line string) {
		if matches := buildLogRe.FindStringSubmatch(line); len(matches) > 1 {
			section := matches[1]
			eventListener.Event("Running: " + section)
		}
	})
	if err != nil {
		return fmt.Errorf("failed to watch mock build.log:\n%w", err)
	}

	return nil
}

func (r *Runner) ensureMockPresentAndConfigured() error {
	if !r.cmdFactory.CommandInSearchPath(MockBinary) {
		return errors.New("mock tool required but could not be found in path")
	}

	// Make sure the current user is a member of the 'mock' group. This is a
	// strict requirement of running 'mock', even for basic commands.
	isMember, err := r.osEnv.IsCurrentUserMemberOf(MockGroup)
	if err != nil {
		return fmt.Errorf("unable to confirm if current user is member of 'mock' group:\n%w", err)
	}

	if !isMember {
		return errors.New("current user is not a member of 'mock' group; " +
			"please add the user to the group by running: sudo usermod -aG mock $USER")
	}

	return nil
}

// Builds a wrapper command that will run the specified inside a mock chroot.
func (r *Runner) CmdInChroot(ctx context.Context, args []string, interactive bool) (cmd opctx.Cmd, err error) {
	// We're going to need to run mock, so make sure we can.
	err = r.ensureMockPresentAndConfigured()
	if err != nil {
		return nil, err
	}

	mockArgs := r.getBaseArgs()

	if interactive {
		mockArgs = append(mockArgs, "--shell")
	} else {
		mockArgs = append(mockArgs, "--chroot")
	}

	if r.unprivileged {
		mockArgs = append(mockArgs, "--unpriv")
	}

	if len(args) > 0 {
		mockArgs = append(mockArgs, shellquote.Join(args...))
	}

	cmd, err = r.cmdFactory.Command(exec.CommandContext(ctx, MockBinary, mockArgs...))
	if err != nil {
		return nil, fmt.Errorf("failed to create command to run in mock root:\n%w", err)
	}

	return cmd, nil
}

// InstallPackages installs the specified packages into the mock root.
func (r *Runner) InstallPackages(ctx context.Context, packages []string) error {
	err := r.ensureMockPresentAndConfigured()
	if err != nil {
		return err
	}

	mockArgs := r.getBaseArgs()
	mockArgs = append(mockArgs, "--install")
	mockArgs = append(mockArgs, packages...)

	cmd := exec.CommandContext(ctx, MockBinary, mockArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	extcmd, err := r.cmdFactory.Command(cmd)
	if err != nil {
		return fmt.Errorf("failed to create external command for mock:\n%w", err)
	}

	extcmd = extcmd.SetLongRunning("Waiting for mock (installing packages)...")

	err = extcmd.Run(ctx)
	if err != nil {
		return fmt.Errorf("mock failed to install packages (%v):\n%w", packages, err)
	}

	return nil
}

// ScrubRoot removes the mock root.
func (r *Runner) ScrubRoot(ctx context.Context) error {
	err := r.ensureMockPresentAndConfigured()
	if err != nil {
		return err
	}

	mockArgs := r.getBaseArgs()

	if r.baseDir != "" {
		// Lie about the cache topdir so that mock doesn't delete our *actual* shared cache.
		mockArgs = append(mockArgs, "--config-opts", "cache_topdir="+r.baseDir)
		mockArgs = append(mockArgs, "--scrub", "all")
	} else {
		mockArgs = append(mockArgs, "--scrub", "chroot")
	}

	cmd := exec.CommandContext(ctx, MockBinary, mockArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	extcmd, err := r.cmdFactory.Command(cmd)
	if err != nil {
		return fmt.Errorf("failed to create external command for mock:\n%w", err)
	}

	extcmd = extcmd.SetLongRunning("Waiting for mock (cleaning build root)...")

	err = extcmd.Run(ctx)
	if err != nil {
		return fmt.Errorf("mock failed to scrub root:\n%w", err)
	}

	return nil
}

func (r *Runner) getBaseArgs() (args []string) {
	if !r.verbose {
		args = append(args, "--quiet")
	}

	// WORKAROUND: On WSL systems, mock will incorrectly assume that the system was
	// *not* booted by systemd and disable use of systemd-nspawn. Until we can get
	// this addressed upstream, we apply a workaround to force use of systemd-nspawn
	// for isolation on WSL hosts.
	if hostinfo.IsWSL() {
		args = append(args, "--isolation", "nspawn")
	}

	args = append(args, "-r", r.mockConfigPath)
	args = append(args, "--configdir", filepath.Dir(r.mockConfigPath))

	if r.baseDir != "" {
		args = append(args, "--config-opts", "basedir="+r.baseDir)
	}

	if r.rootDir != "" {
		args = append(args, "--rootdir", r.rootDir)
	}

	// Emit any caller-specified config overrides.
	if len(r.configOpts) > 0 {
		// Sort keys for deterministic output.
		keys := make([]string, 0, len(r.configOpts))
		for k := range r.configOpts {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		for _, k := range keys {
			args = append(args, "--config-opts", k+"="+r.configOpts[k])
		}
	}

	if r.enableNetwork {
		args = append(args, "--enable-network")
	}

	if r.noPreClean {
		args = append(args, "--no-clean")
	}

	if len(r.bindMounts) > 0 {
		pairs := []string{}

		for _, bindMount := range r.bindMounts {
			pairs = append(pairs, fmt.Sprintf(`("%s", "%s")`, bindMount.hostPath, bindMount.mockRootPath))
		}

		optionArg := fmt.Sprintf("--plugin-option=bind_mount:dirs=[%s]", strings.Join(pairs, ", "))

		args = append(args, optionArg)
	}

	return args
}

// TryGetFailureDetails makes a best-effort attempt to extract details from mock build logs that may be
// relevant to understanding the cause of a build failure. This is intended to be called after a build
// failure to glean any insights we can from mock's logs about why the failure might have occurred.
func (r *Runner) TryGetFailureDetails(fs opctx.FS, outputDirPath string) (details *BuildLogDetails) {
	const maxContextLinesToCapture = 10

	details = &BuildLogDetails{}

	// Go through build.log.
	buildLogPath := filepath.Join(outputDirPath, "build.log")
	buildLogBytes, _ := fileutils.ReadFile(fs, buildLogPath)
	buildLogLines := strings.Split(string(buildLogBytes), "\n")

	rpmBuildErrorsStartIndex := -1
	rpmBuildErrorsEndIndex := -1

	for lineIndex, line := range buildLogLines {
		if strings.HasPrefix(line, "RPM build errors:") {
			rpmBuildErrorsStartIndex = lineIndex + 1
		} else if strings.HasPrefix(line, "EXCEPTION:") {
			rpmBuildErrorsEndIndex = lineIndex
		}
	}

	// If we see evidence of "RPM build errors", then try to capture relevant lines.
	if rpmBuildErrorsStartIndex > 0 {
		contextEndIndex := rpmBuildErrorsStartIndex - 1

		if rpmBuildErrorsEndIndex < 0 {
			rpmBuildErrorsEndIndex = len(buildLogLines)
		}

		details.RPMBuildErrors = buildLogLines[rpmBuildErrorsStartIndex:rpmBuildErrorsEndIndex]

		contextLinesToCapture := min(maxContextLinesToCapture, contextEndIndex)
		contextStartIndex := contextEndIndex - contextLinesToCapture

		details.LastRPMBuildLogLines = buildLogLines[contextStartIndex:contextEndIndex]
	}

	return details
}
