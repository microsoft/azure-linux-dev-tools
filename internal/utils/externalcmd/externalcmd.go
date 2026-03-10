// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package externalcmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/acarl005/stripansi"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/nxadm/tail"
)

// ErrMissingExecutable is returned when a required executable cannot be found or acquired.
var ErrMissingExecutable = errors.New("executable missing")

type factory struct {
	// Dependencies
	dryRunInfo    opctx.DryRunnable
	eventListener opctx.EventListener
}

// Ensure that [factory] implements [opctx.CmdFactory].
var _ opctx.CmdFactory = &factory{}

// Command implements [opctx.CmdFactory].
func (f *factory) Command(cmd *exec.Cmd) (opctx.Cmd, error) {
	return newExternalCmd(f, f.dryRunInfo, f.eventListener, cmd)
}

// CommandInSearchPath implements [opctx.CmdFactory].
func (f *factory) CommandInSearchPath(name string) bool {
	_, err := exec.LookPath(name)

	return err == nil
}

// NewCmdFactory returns an implementation of [opctx.CmdFactory] that uses standard host OS facilities
// for executing commands.
func NewCmdFactory(dryRunInfo opctx.DryRunnable, eventListener opctx.EventListener) (*factory, error) {
	if dryRunInfo == nil {
		return nil, errors.New("dryRunInfo cannot be nil")
	}

	if eventListener == nil {
		return nil, errors.New("eventListener cannot be nil")
	}

	return &factory{
		dryRunInfo:    dryRunInfo,
		eventListener: eventListener,
	}, nil
}

// Implements [opctx.Cmd] using standard system interfaces.
type cmd struct {
	// Actual command object.
	inner *exec.Cmd

	// Dependencies
	cmdFactory    opctx.CmdFactory
	dryRunInfo    opctx.DryRunnable
	eventListener opctx.EventListener

	// Parameters.
	longRunning   bool
	description   string
	progressTitle string

	// Listener state.
	stdoutListener         opctx.LineListener
	stderrListener         opctx.LineListener
	fileListeners          []fileListenerRegistration
	fileListenerCancelFunc context.CancelFunc

	// Execution state.
	started         bool
	stdioListenerWg sync.WaitGroup
	fileListenerWg  sync.WaitGroup
}

type fileListenerRegistration struct {
	path     string
	listener opctx.LineListener
}

// Ensure that [cmd] implements [opctx.Cmd].
var _ opctx.Cmd = &cmd{}

// Wraps [exec.Cmd] in a [cmd] object for use with this package.
func newExternalCmd(
	cmdFactory opctx.CmdFactory,
	dryRunInfo opctx.DryRunnable,
	eventListener opctx.EventListener,
	execCmd *exec.Cmd,
) (*cmd, error) {
	if cmdFactory == nil {
		return nil, errors.New("cmdFactory cannot be nil")
	}

	if dryRunInfo == nil {
		return nil, errors.New("dryRunInfo cannot be nil")
	}

	if eventListener == nil {
		return nil, errors.New("eventListener cannot be nil")
	}

	if execCmd == nil {
		return nil, errors.New("cmd cannot be nil")
	}

	return &cmd{
		cmdFactory:    cmdFactory,
		dryRunInfo:    dryRunInfo,
		eventListener: eventListener,
		inner:         execCmd,
	}, nil
}

// GetArgs returns the command-line arguments for the command.
func (c *cmd) GetArgs() []string {
	return c.inner.Args
}

// SetStdin implements the [opctx.Cmd] interface.
func (c *cmd) SetStdin(stdin io.Reader) {
	c.inner.Stdin = stdin
}

// SetStdout implements the [opctx.Cmd] interface.
func (c *cmd) SetStdout(stdout io.Writer) {
	c.inner.Stdout = stdout
}

// SetStderr implements the [opctx.Cmd] interface.
func (c *cmd) SetStderr(stderr io.Writer) {
	c.inner.Stderr = stderr
}

// SetDescription implements the [opctx.Cmd] interface.
func (c *cmd) SetDescription(description string) opctx.Cmd {
	c.description = description

	return c
}

// SetLongRunning implements the [opctx.Cmd] interface.
func (c *cmd) SetLongRunning(progressTitle string) opctx.Cmd {
	c.longRunning = true
	c.progressTitle = progressTitle

	return c
}

// AddRealTimeFileListener implements the [opctx.Cmd] interface.
func (c *cmd) AddRealTimeFileListener(path string, listener opctx.LineListener) error {
	if c.started {
		return errors.New("cannot add file listener after command has started")
	}

	c.fileListeners = append(c.fileListeners, fileListenerRegistration{path, listener})

	return nil
}

// SetRealTimeStdoutListener implements the [opctx.Cmd] interface.
func (c *cmd) SetRealTimeStdoutListener(listener opctx.LineListener) error {
	if c.started {
		return errors.New("cannot set listener after command has started")
	}

	c.stdoutListener = listener

	return nil
}

// SetRealTimeStderrListener implements the [opctx.Cmd] interface.
func (c *cmd) SetRealTimeStderrListener(listener opctx.LineListener) error {
	if c.started {
		return errors.New("cannot set listener after command has started")
	}

	c.stderrListener = listener

	return nil
}

func (c *cmd) preRun() error {
	progName := c.inner.Path
	if !strings.ContainsRune(progName, filepath.Separator) {
		if !c.cmdFactory.CommandInSearchPath(progName) {
			return fmt.Errorf("program %#q required:\n%w", progName, ErrMissingExecutable)
		}
	}

	slog.Debug("Invoking external cmd", "command", c.inner.Path, "args", c.inner.Args)

	c.started = true

	return nil
}

func (c *cmd) cleanup() {
	// On failure, the stdio listeners should end up closing because the other side of the
	// pipe will go away. We need to explicitly shutdown the file listeners, though.
	if c.fileListenerCancelFunc != nil {
		c.fileListenerCancelFunc()
		c.fileListenerCancelFunc = nil
	}

	// Wait for all pending listeners. We may have already waited for some of them; this
	// is safe to run again. We need to do this, though, to ensure we clean up in failure
	// cases too.
	c.stdioListenerWg.Wait()
	c.fileListenerWg.Wait()
}

// Start implements  the [opctx.Cmd] interface.
func (c *cmd) Start(ctx context.Context) error {
	err := c.preRun()
	if err != nil {
		return err
	}

	if c.dryRunInfo.DryRun() {
		slog.Info("Dry run; would exec", "command", c.inner.Path, "args", c.inner.Args)

		return err
	}

	if c.stdoutListener != nil {
		var reader io.ReadCloser

		reader, err = c.inner.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to connect to stdout of child process:\n%w", err)
		}

		c.stdioListenerWg.Add(1)

		go c.processStdio(ctx, &c.stdioListenerWg, reader, c.stdoutListener)
	}

	if c.stderrListener != nil {
		var reader io.ReadCloser

		reader, err = c.inner.StderrPipe()
		if err != nil {
			return fmt.Errorf("failed to connect to stderr of child process:\n%w", err)
		}

		c.stdioListenerWg.Add(1)

		go c.processStdio(ctx, &c.stdioListenerWg, reader, c.stderrListener)
	}

	// Set up a child context that we can use to cancel the file listeners.
	if len(c.fileListeners) > 0 {
		var fileListenerContext context.Context

		fileListenerContext, c.fileListenerCancelFunc = context.WithCancel(ctx)

		for _, reg := range c.fileListeners {
			tailConfig := tail.Config{
				Location:      &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd},
				Follow:        true,
				CompleteLines: true,
				Logger:        tail.DiscardingLogger,
			}

			var tailed *tail.Tail

			tailed, err = tail.TailFile(reg.path, tailConfig)
			if err != nil {
				return fmt.Errorf("failed to tail file '%s':\n%w", reg.path, err)
			}

			c.fileListenerWg.Add(1)

			// Pass through the child context we created specifically for file listeners.
			go c.processFileRealTime(ctx, &c.fileListenerWg, fileListenerContext, tailed, reg.listener)
		}
	}

	err = c.inner.Start()
	if err != nil {
		// We need to cleanup since we got partly initialized.
		c.cleanup()

		return fmt.Errorf("failed to start command:\n%w", err)
	}

	return nil
}

func (c *cmd) processStdio(
	ctx context.Context,
	wg *sync.WaitGroup,
	reader io.ReadCloser,
	listener opctx.LineListener,
) {
	defer reader.Close()
	defer wg.Done()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()

		cleanedLine := strings.TrimSpace(stripansi.Strip(line))
		if cleanedLine != "" {
			listener(ctx, cleanedLine)
		}
	}
}

func (c *cmd) processFileRealTime(
	ctx context.Context,
	waitGroup *sync.WaitGroup,
	context context.Context,
	tailed *tail.Tail,
	listener opctx.LineListener,
) {
	defer func() {
		_ = tailed.Stop()
	}()

	defer waitGroup.Done()

	for {
		select {
		case <-context.Done():
			return
		case line, ok := <-tailed.Lines:
			if !ok {
				return
			}

			cleanedLine := strings.TrimSpace(line.Text)
			if cleanedLine != "" {
				listener(ctx, cleanedLine)
			}
		}
	}
}

// Wait implements  the [opctx.Cmd] interface.
func (c *cmd) Wait(ctx context.Context) error {
	if !c.dryRunInfo.DryRun() {
		if c.longRunning {
			evt := c.eventListener.StartEvent(c.description)
			defer evt.End()

			evt.SetLongRunning(c.progressTitle)
		}

		// Per documentation, the .Wait() call below will close any pipes
		// for redirected stdout/stderr. Once that happens, any goroutines
		// we spawned as stdout/stderr listeners won't be able to read
		// the remainder of output that the process has already emitted.
		// Let's wait for (just) them to finish before we call .Wait().
		// Note that a listener that gets stuck will cause us to get
		// stuck too.
		c.stdioListenerWg.Wait()

		// Perform the wait and capture its error.
		err := c.inner.Wait()

		// Before returning, make sure we perform any clean up.
		c.cleanup()

		if err != nil {
			return fmt.Errorf("failed to wait for command %#q:\n%w", c.inner.Args, err)
		}
	}

	return nil
}

// Run implements the [opctx.Cmd] interface.
func (c *cmd) Run(ctx context.Context) error {
	err := c.Start(ctx)
	if err != nil {
		return fmt.Errorf("failed to start command %#q:\n%w", c.inner.Args, err)
	}

	err = c.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for command %#q:\n%w", c.inner.Args, err)
	}

	return nil
}

// RunAndGetOutput implements the [opctx.Cmd] interface.
func (c *cmd) RunAndGetOutput(ctx context.Context) (output string, err error) {
	// Check for incompatible configurations.
	if len(c.fileListeners) > 0 || c.stdoutListener != nil || c.stderrListener != nil {
		return "", errors.New("cannot use stdio or real-time listeners with RunAndGetOutput")
	}

	if c.dryRunInfo.DryRun() {
		// Let [Run] handle the dry run; leave `output` empty.
		return "", c.Run(ctx)
	}

	err = c.preRun()
	if err != nil {
		return "", err
	}

	var bytes []byte

	bytes, err = c.inner.Output()
	if err == nil {
		return string(bytes), nil
	}

	//
	// If we got down here, we failed. Let's try to gather what info we can
	// and send any captured stderr contents to the log.
	//

	var exitErr *exec.ExitError

	if errors.As(err, &exitErr) {
		stderr := string(exitErr.Stderr)
		if stderr != "" {
			slog.Warn("external cmd wrote to stderr", "stderr", stderr)
		}
	}

	return string(bytes), fmt.Errorf("failed to run command '%s':\n%w", c.inner.Args, err)
}
