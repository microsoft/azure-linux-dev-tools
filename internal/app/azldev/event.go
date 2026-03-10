// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

type event struct {
	parentEventListener *appEventListener
	name                string
	spinner             *spinner.Spinner

	lastReportedCompletionRatio float64

	initializedProgressBar bool
	progressBar            progress.Model
}

// Ensure that [event] implements [opctx.Event].
var _ opctx.Event = &event{}

// Marks the event as complete.
func (e *event) End() {
	// If a spinner was started for indeterminate progress, then stop it now.
	if e.spinner != nil {
		e.spinner.Stop()
		e.spinner = nil
	}

	// If we had setup a progress bar, then clean it up now.
	if e.initializedProgressBar {
		e.stopProgress()
		e.initializedProgressBar = false
	}

	// Decrement our event nesting level.
	if e.parentEventListener.eventLevel > 0 {
		e.parentEventListener.eventLevel--
	}
}

func (e *event) SetLongRunning(longRunningText string) {
	const percent = 100

	// Start an indeterminate spinner to indicate to the user that *something* is happening.
	s := spinner.New(spinner.CharSets[11], percent*time.Millisecond, spinner.WithWriter(os.Stderr))
	s.Suffix = " " + longRunningText
	s.Start()

	e.spinner = s
}

func (e *event) SetProgress(unitsComplete int64, totalUnits int64) {
	// For now, only update progress visually when the completion ratio has increased by at least 1%
	// since progress was last rendered.
	const minRatioIncreaseForUpdate = 0.01

	// If we don't have the "denominator" for progress, then bail early to avoid division by zero.
	// There's not much we could really do with the progress bar anyhow.
	if totalUnits == 0 {
		return
	}

	// If an indeterminate progress indicator was started before we got here, then stop it now.
	if e.spinner != nil {
		e.spinner.Stop()
		e.spinner = nil
	}

	// Compute the completion ratio, and only update the progress bar if it has increased by at least
	// our default min increment.
	completionRatio := float64(unitsComplete) / float64(totalUnits)
	if completionRatio >= e.lastReportedCompletionRatio+minRatioIncreaseForUpdate {
		e.displayProgress(completionRatio)
		e.lastReportedCompletionRatio = completionRatio
	}
}

func (e *event) displayProgress(completionRatio float64) {
	// If we haven't yet set up the progress bar, do so now.
	if !e.initializedProgressBar {
		e.progressBar = progress.New(progress.WithDefaultGradient())
		e.initializedProgressBar = true
	}

	// Move the cursor back to the beginning of the current line and display the progress bar to stderr.
	fmt.Fprintf(os.Stderr, "\r%s", e.progressBar.ViewAs(completionRatio))
}

func (e *event) stopProgress() {
	// Clear the progress bar from the current line and reset the cursor back to the beginning of the line.
	fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", e.progressBar.Width))
}
