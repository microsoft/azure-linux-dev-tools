// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package testctx

import (
	"context"
	"io"
	"os/exec"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

// TestCmdFactory is a test implementation of the [opctx.CmdFactory] interface,
// intended for use in unit tests. It allows for simulating command execution
// and capturing output without actually running any commands.
type TestCmdFactory struct {
	RunHandler             func(*exec.Cmd) error
	RunAndGetOutputHandler func(*exec.Cmd) (string, error)

	Stdout          string
	Stderr          string
	ListenableFiles map[string]string

	// Map of command names we are supposed to pretend are in the search path.
	commandsInSearchPath map[string]bool
}

// Creates a new instance of [TestCmdFactory], with no commands present in the default search path.
func NewTestCmdFactory() *TestCmdFactory {
	return &TestCmdFactory{
		commandsInSearchPath: make(map[string]bool),
	}
}

// Command implements the [opctx.CmdFactory] interface.
func (f *TestCmdFactory) Command(cmd *exec.Cmd) (opctx.Cmd, error) {
	return &TestCmd{
		Cmd:     cmd,
		Factory: f,

		FileListeners: make(map[string]opctx.LineListener),
	}, nil
}

// CommandInSearchPath implements the [opctx.CmdFactory] interface.
func (f *TestCmdFactory) CommandInSearchPath(name string) bool {
	return f.commandsInSearchPath[name]
}

// Registers a command as being in the search path; used by tests to simulate preconditions
// for code-under-test that checks for the presence of commands, or executes commands.
func (f *TestCmdFactory) RegisterCommandInSearchPath(name string) {
	f.commandsInSearchPath[name] = true
}

// TestCmd is a test implementation of the [opctx.Cmd] interface, used by [TestCmdFactory].
type TestCmd struct {
	Cmd     *exec.Cmd
	Factory *TestCmdFactory

	StdoutListener opctx.LineListener
	StderrListener opctx.LineListener
	FileListeners  map[string]opctx.LineListener
}

// Start implements the [opctx.Cmd] interface.
func (c *TestCmd) Start(ctx context.Context) error {
	return nil
}

// Wait implements the [opctx.Cmd] interface.
func (c *TestCmd) Wait(ctx context.Context) error {
	if c.StdoutListener != nil {
		for _, line := range strings.Split(c.Factory.Stdout, "\n") {
			c.StdoutListener(ctx, line)
		}
	}

	if c.StderrListener != nil {
		for _, line := range strings.Split(c.Factory.Stderr, "\n") {
			c.StderrListener(ctx, line)
		}
	}

	if c.Factory.ListenableFiles != nil {
		for path, fileListener := range c.FileListeners {
			if fileContents, ok := c.Factory.ListenableFiles[path]; ok {
				for _, line := range strings.Split(fileContents, "\n") {
					fileListener(ctx, line)
				}
			}
		}
	}

	if c.Factory.RunHandler != nil {
		return c.Factory.RunHandler(c.Cmd)
	}

	return nil
}

// Run implements the [opctx.Cmd] interface.
func (c *TestCmd) Run(ctx context.Context) error {
	err := c.Start(ctx)
	if err != nil {
		return err
	}

	return c.Wait(ctx)
}

// RunAndGetOutput implements the [opctx.Cmd] interface.
func (c *TestCmd) RunAndGetOutput(ctx context.Context) (string, error) {
	if c.Factory.RunAndGetOutputHandler != nil {
		return c.Factory.RunAndGetOutputHandler(c.Cmd)
	}

	return "", nil
}

// SetDescription implements the [opctx.Cmd] interface.
func (c *TestCmd) SetDescription(description string) opctx.Cmd {
	return c
}

// SetLongRunning implements the [opctx.Cmd] interface.
func (c *TestCmd) SetLongRunning(progressTitle string) opctx.Cmd {
	return c
}

// AddRealTimeFileListener implements the [opctx.Cmd] interface.
func (c *TestCmd) AddRealTimeFileListener(path string, listener opctx.LineListener) error {
	c.FileListeners[path] = listener

	return nil
}

// SetRealTimeStderrListener implements the [opctx.Cmd] interface.
func (c *TestCmd) SetRealTimeStderrListener(listener opctx.LineListener) error {
	c.StderrListener = listener

	return nil
}

// SetRealTimeStdoutListener implements the [opctx.Cmd] interface.
func (c *TestCmd) SetRealTimeStdoutListener(listener opctx.LineListener) error {
	c.StdoutListener = listener

	return nil
}

// GetArgs implements the [opctx.Cmd] interface.
func (c *TestCmd) GetArgs() []string {
	return c.Cmd.Args
}

// SetStdin implements the [opctx.Cmd] interface.
func (c *TestCmd) SetStdin(_ io.Reader) {}

// SetStdout implements the [opctx.Cmd] interface.
func (c *TestCmd) SetStdout(_ io.Writer) {}

// SetStderr implements the [opctx.Cmd] interface.
func (c *TestCmd) SetStderr(_ io.Writer) {}
