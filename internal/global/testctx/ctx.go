// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package testctx

import (
	"context"
	"os/exec"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/spf13/afero"
)

// Test implementation of [opctx.Ctx]; allows for injecting simulated conditions.
type TestCtx struct {
	//nolint:containedctx // We are intentionally embedding a context.Context.
	Ctx context.Context

	testFS    opctx.FS
	testOSEnv opctx.OSEnv

	CmdFactory *TestCmdFactory

	AllPromptsAcceptedValue bool
	DryRunValue             bool
	PromptsAllowedValue     bool
	VerboseValue            bool

	Events []*TestEvent
}

// TestCtxOption is a function that modifies the [TestCtx] instance.
type TestCtxOption func(*TestCtx)

// Validate that TestCtx implements the [opctx.Ctx] interface.
var _ opctx.Ctx = &TestCtx{}

// Creates a new test implementation of [opctx.Ctx] with an in-memory filesystem.
func NewCtx(options ...TestCtxOption) *TestCtx {
	ctx := newDefaultCtx()

	for _, option := range options {
		if option != nil {
			option(ctx)
		}
	}

	return ctx
}

func newDefaultCtx() *TestCtx {
	ctx := &TestCtx{
		testFS:    afero.NewMemMapFs(),
		testOSEnv: NewTestOSEnv(),

		Ctx:        context.Background(),
		CmdFactory: NewTestCmdFactory(),

		AllPromptsAcceptedValue: false,
		DryRunValue:             false,
		PromptsAllowedValue:     false,
		VerboseValue:            false,

		Events: []*TestEvent{},
	}

	return ctx
}

// WithFS sets the filesystem to the provided [opctx.FS] implementation.
func WithFS(fs opctx.FS) TestCtxOption {
	return func(ctx *TestCtx) {
		ctx.testFS = fs
	}
}

// WithHostFS sets the filesystem to the host OS filesystem.
func WithHostFS() TestCtxOption {
	return WithFS(afero.NewOsFs())
}

// WithOSEnv sets the OSEnv to the provided [opctx.OSEnv] implementation.
func WithOSEnv(osEnv opctx.OSEnv) TestCtxOption {
	return func(ctx *TestCtx) {
		ctx.testOSEnv = osEnv
	}
}

// AllPromptsAccepted implements the [opctx.Prompter] interface.
func (t *TestCtx) AllPromptsAccepted() bool {
	return t.AllPromptsAcceptedValue
}

// Command implements the [opctx.CmdFactory] interface.
func (t *TestCtx) Command(cmd *exec.Cmd) (opctx.Cmd, error) {
	return t.CmdFactory.Command(cmd)
}

// CommandInSearchPath implements the [opctx.CmdFactory] interface.
func (t *TestCtx) CommandInSearchPath(name string) bool {
	return t.CmdFactory.CommandInSearchPath(name)
}

// ConfirmAutoResolution implements the [opctx.Prompter] interface.
func (t *TestCtx) ConfirmAutoResolution(text string) bool {
	return t.AllPromptsAccepted()
}

// Deadline implements the [context.Context] interface.
func (t *TestCtx) Deadline() (deadline time.Time, ok bool) {
	return t.Ctx.Deadline()
}

// Done implements the [context.Context] interface.
func (t *TestCtx) Done() <-chan struct{} {
	return t.Ctx.Done()
}

// DryRun implements the [opctx.Ctx] interface.
func (t *TestCtx) DryRun() bool {
	return t.DryRunValue
}

// Err implements the [context.Context] interface.
func (t *TestCtx) Err() error {
	//nolint:wrapcheck // We are intentionally just forwarding the call.
	return t.Ctx.Err()
}

// FS implements the [opctx.FileSystemFactory] interface.
func (t *TestCtx) FS() opctx.FS {
	return t.testFS
}

// OSEnv implements the [opctx.OSEnvFactory] interface.
func (t *TestCtx) OSEnv() opctx.OSEnv {
	return t.testOSEnv
}

// PromptsAllowed implements the [opctx.Prompter] interface.
func (t *TestCtx) PromptsAllowed() bool {
	return t.PromptsAllowedValue
}

// StartEvent implements the [opctx.EventListener] interface.
func (t *TestCtx) StartEvent(name string, args ...any) opctx.Event {
	event := &TestEvent{
		Name: name,
	}

	t.Events = append(t.Events, event)

	return event
}

// Event implements the [opctx.EventListener] interface.
func (t *TestCtx) Event(name string, args ...any) {
	t.StartEvent(name, args...).End()
}

// Value implements the [context.Context] interface.
func (t *TestCtx) Value(key any) any {
	return t.Ctx.Value(key)
}

// Verbose implements the [opctx.Verbosity] interface.
func (t *TestCtx) Verbose() bool {
	return t.VerboseValue
}
