// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package testctx

import "github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"

// TestEvent is a test implementation of the Event interface. It tracks updates
// to its state from function calls, allowing for test verification.
type TestEvent struct {
	Name  string
	Ended bool

	LongRunningDescription string

	LastUnitsCompleted int64
	LastTotalUnits     int64
}

// Validate that TestEvent implements the [opctx.Event] interface.
var _ opctx.Event = &TestEvent{}

// End implements the [opctx.Event] interface.
func (t *TestEvent) End() {
	t.Ended = true
}

// SetLongRunning implements the [opctx.Event] interface.
func (t *TestEvent) SetLongRunning(title string) {
	t.LongRunningDescription = title
}

// SetProgress implements the [opctx.Event] interface.
func (t *TestEvent) SetProgress(unitsComplete int64, totalUnits int64) {
	t.LastUnitsCompleted = unitsComplete
	t.LastTotalUnits = totalUnits
}
