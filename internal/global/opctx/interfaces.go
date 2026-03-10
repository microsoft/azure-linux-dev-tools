// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// This package defines basic types used across the containing project.

//go:generate go tool -modfile=../../../tools/mockgen/go.mod mockgen -source=interfaces.go -destination=opctx_test/opctx_mocks.go -package=opctx_test --copyright_file=../../../.license-preamble

package opctx

import (
	"context"
	"io"
	"os/exec"

	"github.com/spf13/afero"
)

// Basic project-wide interface for operations. Primary means for
// injecting basic functionality (e.g., filesystem access, command
// execution) across the code base. Also provides means for the
// implementation to implement a richer user experience with
// progress reporting, etc.
type Ctx interface {
	// Standard golang context
	context.Context

	// User experience interfaces
	Prompter
	EventListener
	Verbosity

	// External interaction
	CmdFactory
	FileSystemFactory
	OSEnvFactory

	// Other basic behaviors
	DryRunnable
}

// DryRunnable interface can be consulted by effectful operations to determine if we are running in
// dry-run mode. In dry-run mode, no actual changes are intended to be made to the host system.
type DryRunnable interface {
	// DryRun returns true if the operation is running in dry-run mode, meaning that no actual
	// changes are intended to be made to the host system.
	DryRun() bool
}

// Tracks an ongoing event and its progress.
type Event interface {
	// Marks the event as a long-running operation, as a hint to the user experience. Provides a title
	// string that could be used for a textual progress indicator.
	SetLongRunning(title string)
	// Updates the last-known quantitative progress made toward completing the event. Provides a discrete
	// number of units completed toward a total number of units.
	SetProgress(unitsComplete int64, totalUnits int64)
	// Marks the event as completed.
	End()
}

// Interface implemented by an event listener. Receives reports of new events.
type EventListener interface {
	// Reports an event without duration or span. Once called, the event is considered complete.
	// Provides a name for the event and arbitrary matched pairs of string/any values, exactly
	// like slog.
	Event(name string, args ...any)
	// Reports the start of an event with duration, taking the same arguments as Event().
	// Returns an [Event] object that the caller is responsible for calling .End() on
	// when the event is complete. This is often done through a defer statement.
	StartEvent(name string, args ...any) Event
}

// Encapsulates verbosity configuration.
type Verbosity interface {
	// Queries whether the verbosity level is set to "verbose".
	Verbose() bool
}

// Encapsulates prompt configuration and interaction.
type Prompter interface {
	// Are blocking prompts allowed?
	PromptsAllowed() bool
	// Should all prompts be auto-accepted without being displayed?
	AllPromptsAccepted() bool
	// Prompt the user to confirm auto-resolution of a problem.
	ConfirmAutoResolution(text string) bool
}

// Factory for creating [Cmd] instances.
type CmdFactory interface {
	// Constructs an executable [Cmd] from a standard [exec.Cmd] object.
	Command(cmd *exec.Cmd) (Cmd, error)
	// Looks for the given command filename in the current environment's default search path.
	CommandInSearchPath(name string) bool
}

// Factory for accessing the filesystem.
type FileSystemFactory interface {
	// Retrieves the base filesystem interface.
	FS() FS
}

// Factory for accessing the OS environment.
type OSEnvFactory interface {
	// Retrieves the base OS environment interface.
	OSEnv() OSEnv
}

// Wrapper for afero.Fs, primarily to avoid importing afero everywhere.
type FS interface {
	afero.Fs
}

// Wrapper for afero.File, primarily to avoid importing afero everywhere.
type File interface {
	afero.File
}

// A line-based listener for a stdio or file stream.
type LineListener = func(ctx context.Context, line string)

// Encapsulates a command to be executed.
//
//nolint:interfacebloat
type Cmd interface {
	// Starts the command.
	Start(ctx context.Context) error
	// Waits for the running command to exit.
	Wait(ctx context.Context) error
	// Starts and then synchronously waits for the command.
	Run(ctx context.Context) error
	// Runs the command, retrieving the command's stdout output in string form.
	// Trims the string for whitespace and removes ANSI/VT100-style escape
	// sequences.
	RunAndGetOutput(ctx context.Context) (string, error)

	// Associates a human-readable description with the command.
	SetDescription(description string) Cmd
	// Provides a hint that the command will run for a human-perceivable length
	// of time. Associates the command's long-running execution with a specific
	// caption that may be used by a UI with an indeterminate progress indicator.
	SetLongRunning(progressTitle string) Cmd

	// Registers a function that will be invoked with each line written to the given
	// file during the execution of the command.
	AddRealTimeFileListener(path string, listener LineListener) error
	// Registers a function that will be invoked with each line of text written
	// to stderr by the command.
	SetRealTimeStderrListener(listener LineListener) error
	// Registers a function that will be invoked with each line of text written
	// to stdout by the command.
	SetRealTimeStdoutListener(listener LineListener) error

	// Sets the command's standard input stream.
	SetStdin(stdin io.Reader)
	// Sets the command's standard output stream.
	SetStdout(stdout io.Writer)
	// Sets the command's standard error stream.
	SetStderr(stderr io.Writer)

	// GetArgs returns the command-line arguments for the command.
	GetArgs() []string
}

// Encapsulates access to the host OS environment.
type OSEnv interface {
	// Retrieve the process's current working directory.
	Getwd() (string, error)
	// Change the process's current working directory.
	Chdir(path string) error
	// Retrieve the value of the environment variable named by the key.
	Getenv(key string) string
	// Checks if the current user is a member of the given group.
	IsCurrentUserMemberOf(groupName string) (bool, error)
	// Looks up the group ID for the given group name.
	LookupGroupID(groupName string) (gid int, err error)
}
