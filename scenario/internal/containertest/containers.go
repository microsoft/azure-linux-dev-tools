// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package containertest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/buildtestenv"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/testhelpers"
	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// DefaultTimeout is the default timeout for a command to run in a container. If no timeout is given, this
	// is used. This will only affect the container running time, not the 'go test' timeout. If the timeout is
	// too long, the test will immediately fail with an error.
	DefaultTimeout = 45 * time.Minute
	// Do not set any explicit timeout.
	NoTimeout = 0

	// Default path to work directory in the test container.
	ContainerWorkDir = "/workdir"

	// Modify the default reaper timeouts since some of our tests can be quite long-running.
	connectionTimeoutEnvVar   = "RYUK_CONNECTION_TIMEOUT"
	DefaultReaperTimeout      = 10 * time.Minute
	reconnectionTimeoutEnvVar = "RYUK_RECONNECTION_TIMEOUT"
	reconnectionTimeout       = 10 * time.Minute

	// Container image reference.
	containerURI = "github.com/azldev/azldev-test/scenario/container"
	containerTag = "latest"

	// Container refresh time, timestamp is rounded to this value. If it changes, the container will be rebuilt.
	containerRefreshTime = time.Hour * 24 // 24 hours
)

var (
	// ErrTimeout is returned when a scenario test exceeds the allowed time limit.
	ErrTimeout = errors.New("scenario test exceeded allowed time limit")
	// ErrUnsupported is returned when a scenario test is not supported on the current platform.
	ErrUnsupported = errors.New("not supported")
)

// Encapsulates the collateral for a containerized scenario test.
type ContainerCmdTestCollateral struct {
	workdir          string
	testBinaryPath   *string
	network          bool
	privileged       bool
	filesDstSrc      map[string]string
	filesDstContents map[string]io.Reader
	extraMounts      []ContainerMount
	env              map[string]string
}

// NewContainerTestCollateral constructs a new, default collateral object for a containerized test.
// Functions provided on the returned value allow updating its configuration.
func NewContainerTestCollateral(t *testing.T) *ContainerCmdTestCollateral {
	t.Helper()

	return &ContainerCmdTestCollateral{
		workdir:          t.TempDir(),
		testBinaryPath:   nil,
		filesDstSrc:      map[string]string{},
		filesDstContents: map[string]io.Reader{},
		network:          false,
		privileged:       false,
		env:              map[string]string{},
	}
}

// WithTestBinaryPath points to the test binary that should be automatically added to the container's $PATH.
func (c *ContainerCmdTestCollateral) WithTestBinaryPath(path string) *ContainerCmdTestCollateral {
	c.testBinaryPath = &path

	return c
}

// WithNetwork sets the container command to run with networking enabled.
func (c *ContainerCmdTestCollateral) WithNetwork() *ContainerCmdTestCollateral {
	c.network = true

	return c
}

// WithPrivilege sets the command to be run in a privileged container.
func (c *ContainerCmdTestCollateral) WithPrivilege() *ContainerCmdTestCollateral {
	c.privileged = true

	return c
}

// WithExtraFiles adds extra files to the container. The files are copied from the host to the container,
// and described with a map from the destination path in the container to the source path on the host.
func (c *ContainerCmdTestCollateral) WithExtraFiles(files map[string]string) *ContainerCmdTestCollateral {
	c.filesDstSrc = files

	return c
}

// WithExtraFileContents adds extra files to the container. The files are created in the container with the
// string contents provided. The files are described with a map from the destination path in the container to a
// reader providing the contents to write.
func (c *ContainerCmdTestCollateral) WithExtraFileContents(files map[string]io.Reader) *ContainerCmdTestCollateral {
	c.filesDstContents = files

	return c
}

// WithExtraMounts adds extra mounts to the container.
func (c *ContainerCmdTestCollateral) WithExtraMounts(mounts []ContainerMount) *ContainerCmdTestCollateral {
	c.extraMounts = mounts

	return c
}

// WithEnv sets the environment variables to be passed to the container.
func (c *ContainerCmdTestCollateral) WithEnv(env map[string]string) *ContainerCmdTestCollateral {
	c.env = env

	return c
}

// Workdir returns the work directory for the test.
func (c *ContainerCmdTestCollateral) Workdir() string {
	return c.workdir
}

type logger struct {
	stdout, stderr []string
	buildLogWriter *bytes.Buffer
}

func (l *logger) Accept(log testcontainers.Log) {
	switch log.LogType {
	case testcontainers.StdoutLog:
		l.stdout = append(l.stdout, string(log.Content))
	case testcontainers.StderrLog:
		l.stderr = append(l.stderr, string(log.Content))
	}
}

// RunCmdInContainer runs a command in a container. The command is run as the current user, and the working
// directory in the collateral object is mounted into the container at /workdir. The command is run from /workdir.
// The azldev command will be pre-provisioned in the container on available in the $PATH.
// If timeout is 0, the command will run until it exits or is killed by the go test timeout. If timeout is greater
// than 0, the command will be terminated at that time and the error 'ErrTimeout' will be returned.
func RunCmdInContainer(
	t *testing.T,
	collateral *ContainerCmdTestCollateral,
	cmd []string,
	timeout time.Duration,
) (results testhelpers.TestResults, err error) {
	t.Helper()

	// If network tests are disabled, and the test is requesting a network, skip the test.
	if os.Getenv(buildtestenv.TestingDisableNetworkTestsEnvVar) != "" && collateral.network {
		t.Skipf("skipping test %s because network tests are disabled", t.Name())
	}

	// Try to pre-provision the container image. This will run only once, and is best-effort. Errors
	// will be logged but not returned.
	cacheContainerImage(t)

	return runCmdInContainerImpl(t, collateral, cmd, timeout)
}

// runCmdInContainerImpl is the actual implementation of running a command in a container. It makes no attempt to
// cache the container image or check environment variables. It is split out from RunCmdInContainer so that it can be
// called from the cacheContainerImage function without causing a circular dependency.
func runCmdInContainerImpl(
	t *testing.T,
	collateral *ContainerCmdTestCollateral,
	cmd []string,
	timeout time.Duration,
) (results testhelpers.TestResults, err error) {
	t.Helper()

	// Compose the `ContainerFile` list to specify the files to copy into / write into the container.
	files := composeContainerFileList(collateral)

	// The logger captures the build and runtime output of the container. It has an
	// Accept method that is called for each log line generated by the container
	// which updates the stdout and stderr lists. The buildLogWriter is used to
	// capture the build output of the container.
	logger := &logger{
		stdout:         make([]string, 0),
		stderr:         make([]string, 0),
		buildLogWriter: &bytes.Buffer{},
	}

	dockerPath, err := testhelpers.FindTestDockerDirectory()
	require.NoError(t, err)

	// Need to duplicate the workdir here because the testcontainers library
	// expects a string pointer for the build args and consts can't be used.
	containerWorkDir := ContainerWorkDir

	uidStr := strconv.Itoa(os.Getuid())
	gidStr := strconv.Itoa(os.Getgid())

	binds := []string{collateral.workdir + ":" + ContainerWorkDir}
	if collateral.extraMounts != nil {
		for _, mount := range collateral.extraMounts {
			binds = append(binds, mount.ContainerMountString())
		}
	}

	// Build the container request. It is based on ./docker/Dockerfile. It will
	// mount the collateral.workdir directory into the container at /workdir, place
	// the azldev command at /azldev, and run the command in the container.
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			// Build the container from the Dockerfile in the "docker" directory.
			FromDockerfile: testcontainers.FromDockerfile{
				// This path is relative to this file.
				Context:        dockerPath,
				Dockerfile:     "Dockerfile",
				Repo:           containerURI,
				Tag:            containerTag,
				KeepImage:      true,
				BuildLogWriter: logger.buildLogWriter,
				BuildArgs: map[string]*string{
					"UID":       &uidStr,
					"GID":       &gidStr,
					"WORK_DIR":  &containerWorkDir,
					"TIMESTAMP": getRefreshTimestamp(t),
				},
			},

			// Mount the workdir for the test into the container.
			HostConfigModifier: func(h *container.HostConfig) {
				h.Binds = binds
				h.Privileged = collateral.privileged
			},

			// Run from the workdir.
			WorkingDir: "/workdir",

			// Test collateral.
			Files: files,

			Networks: getNetworkConfig(t, collateral.network),

			// Configure a logger.
			LogConsumerCfg: &testcontainers.LogConsumerConfig{
				Opts: []testcontainers.LogProductionOption{
					testcontainers.WithLogProductionTimeout(time.Minute),
				},
				Consumers: []testcontainers.LogConsumer{
					logger,
				},
			},

			// Configure the command to run, and setup a timeout for it.
			Cmd:        cmd,
			Env:        collateral.env,
			WaitingFor: getTimeoutConfig(t, timeout),
		},
		Started: true,
	}

	return generateResults(t, req, logger, collateral.workdir)
}

func composeContainerFileList(collateral *ContainerCmdTestCollateral,
) []testcontainers.ContainerFile {
	files := []testcontainers.ContainerFile{}
	if collateral.testBinaryPath != nil {
		files = append(files,
			testcontainers.ContainerFile{
				HostFilePath:      *collateral.testBinaryPath,
				ContainerFilePath: "/usr/local/bin/azldev",
				FileMode:          int64(fileperms.PublicExecutable),
			})
	}

	// Register files to be copied.
	for dst, src := range collateral.filesDstSrc {
		containerFilePath := dst
		if !path.IsAbs(containerFilePath) {
			containerFilePath = path.Join(ContainerWorkDir, containerFilePath)
		}

		files = append(files, testcontainers.ContainerFile{
			HostFilePath:      src,
			ContainerFilePath: containerFilePath,
			FileMode:          int64(fileperms.PublicFile),
		})
	}

	// Register files to map to be written from a reader.
	for dst, contents := range collateral.filesDstContents {
		containerFilePath := dst
		if !path.IsAbs(containerFilePath) {
			containerFilePath = path.Join(ContainerWorkDir, containerFilePath)
		}

		files = append(files, testcontainers.ContainerFile{
			ContainerFilePath: containerFilePath,
			Reader:            contents,
			FileMode:          int64(fileperms.PublicFile),
		})
	}

	return files
}

func generateResults(t *testing.T, req testcontainers.GenericContainerRequest, logs *logger, workdir string,
) (results testhelpers.TestResults, err error) {
	t.Helper()

	exitCode, err := runContainer(t, req, logs)
	if errors.Is(err, context.DeadlineExceeded) {
		return testhelpers.TestResults{}, fmt.Errorf("%w: %w", ErrTimeout, err)
	}

	require.NoError(t, err)

	results = testhelpers.TestResults{
		ExitCode: exitCode,
		Stdout:   strings.Join(logs.stdout, ""),
		Stderr:   strings.Join(logs.stderr, ""),
		Workdir:  workdir,
	}

	return results, nil
}

func runContainer(t *testing.T, req testcontainers.GenericContainerRequest, logs *logger) (returnCode int, err error) {
	t.Helper()

	ctx := t.Context()

	// Set the reaper timeouts to the default values if they are not set.
	setReaperTimeouts()

	container, err := testcontainers.GenericContainer(ctx, req)
	defer testcontainers.CleanupContainer(t, container)

	if errors.Is(err, context.DeadlineExceeded) {
		return -1, fmt.Errorf("%w: %w", ErrTimeout, err)
	}

	// If we saw an image building error (there's not an obviously better way to detect it), then
	// dump the image build log to the test output.
	if err != nil && strings.Contains(err.Error(), "build image") {
		t.Logf("Container image build log: %s", logs.buildLogWriter.String())
	}

	// Any error other than timeout is a failure.
	require.NoError(t, err)

	// The container will run until the wait condition is met, or the timeout is reached.
	state, err := container.State(ctx)
	require.NoError(t, err)

	retCode := state.ExitCode

	return retCode, nil
}

// The loggers may still be running after the container exits, so we need to wait for them to finish or some of the
// logs may be lost. Just give it a bit of extra time to finish even after the container is done. This can be done by
// creating a custom wait strategy that waits for the nested strategy to finish, then waits for an extra time increment.

var _ wait.Strategy = &delayStrategy{}

// delayStrategy wraps a normal wait strategy, plus an extra delay.
type delayStrategy struct {
	nestedWaitStrategy wait.Strategy
}

// WaitUntilReady waits a small amount of time for the container logs to finish writing.
func (ws *delayStrategy) WaitUntilReady(ctx context.Context, target wait.StrategyTarget) error {
	const extraDelay = 500 * time.Millisecond

	// First wait for the nested strategy to finish. Capture the "real" error.
	err := ws.nestedWaitStrategy.WaitUntilReady(ctx, target)

	// Now wait for the extra time. Event if the context is done, we still need to wait.
	time.Sleep(extraDelay)

	//nolint:wrapcheck // We transparently return the original error.
	return err
}

func getTimeoutConfig(t *testing.T, timeout time.Duration) *delayStrategy {
	t.Helper()

	strategy := &delayStrategy{
		nestedWaitStrategy: wait.ForExit(),
	}

	if timeout <= 0 {
		// Don't set a timeout, always wait for the command to exit.
		return strategy
	}

	// Ensure we won't exceed the test timeout. We should immediately fail if the timeout might be exceeded.
	deadline, ok := t.Deadline()
	if ok {
		delta := time.Until(deadline)
		// Round to the nearest second (it usually takes a few milliseconds to get here)
		delta = delta.Round(time.Second)
		// If the requested timeout will exceed the test timeout, fail immediately.
		if delta < timeout {
			t.Errorf("%s's timeout of %v exceeds 'go test' timeout of %v.", t.Name(), timeout, delta)
			t.Errorf("Set a longer timeout for this test using 'go test -timeout %v'.", timeout)
			t.FailNow()
		}
	}

	// Set the timeout.
	strategy.nestedWaitStrategy = wait.ForExit().WithExitTimeout(timeout)

	return strategy
}

func getNetworkConfig(t *testing.T, network bool) []string {
	t.Helper()

	// If the network is enabled, return nil. This will use the default network.
	if network {
		return nil
	}

	// Disable the network for the container.
	return []string{"none"}
}

// getRefreshTimestamp returns a timestamp which is rounded to the nearest container refresh time. This is used to
// force the container to be refreshed on a regular basis, e.g. once a day.
func getRefreshTimestamp(t *testing.T) *string {
	t.Helper()

	now := time.Now()
	rounded := now.Truncate(containerRefreshTime)
	result := rounded.Format(time.RFC3339)

	return &result
}

func setReaperTimeouts() {
	// Check if the env vars for the reaper timeouts are set, if not set them to the default values.
	if os.Getenv(connectionTimeoutEnvVar) == "" {
		os.Setenv(connectionTimeoutEnvVar, DefaultReaperTimeout.String())
	}

	if os.Getenv(reconnectionTimeoutEnvVar) == "" {
		os.Setenv(reconnectionTimeoutEnvVar, reconnectionTimeout.String())
	}
}
