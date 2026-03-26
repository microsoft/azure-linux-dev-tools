// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

type appEventListener struct {
	eventLevel  int
	eventLogger *slog.Logger
	quiet       bool
}

// Ensure [appEventListener] implements [opctx.EventListener].
var _ opctx.EventListener = &appEventListener{}

// NewEventListener creates a new event listener for the environment.
func NewEventListener(eventLogger *slog.Logger, quiet bool) (*appEventListener, error) {
	if eventLogger == nil {
		return nil, errors.New("event logger cannot be nil")
	}

	return &appEventListener{
		eventLevel:  0,
		eventLogger: eventLogger,
		quiet:       quiet,
	}, nil
}

// StartEvent implements the [opctx.EventListener] interface.
//
//nolint:ireturn,nolintlint // We need to return an interface because of the interface definition.
func (el *appEventListener) StartEvent(name string, args ...any) opctx.Event {
	if name != "" {
		const spacesPerLevel = 2

		prefix := strings.Repeat(" ", el.eventLevel*spacesPerLevel)

		if !el.quiet {
			fmt.Fprintf(os.Stderr, "\r")
		}

		el.eventLogger.Info(prefix+name, args...)
	}

	el.eventLevel++

	return &event{
		parentEventListener: el,
		name:                name,
		quiet:               el.quiet,
	}
}

// Event implements the [opctx.EventListener] interface.
//
// Records an event and immediately ends it.
func (el *appEventListener) Event(name string, args ...any) {
	el.StartEvent(name, args...).End()
}
