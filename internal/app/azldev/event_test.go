// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // testing unexported internal types
package azldev

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureStderr(t *testing.T, action func()) string {
	t.Helper()

	originalStderr := os.Stderr
	reader, writer, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		os.Stderr = originalStderr
		_ = writer.Close()
		_ = reader.Close()
	})

	os.Stderr = writer

	action()

	require.NoError(t, writer.Close())

	os.Stderr = originalStderr

	output, err := io.ReadAll(reader)
	require.NoError(t, err)

	return string(output)
}

func TestEvent_QuietModeSkipsLongRunningAndProgressRendering(t *testing.T) {
	testEvent := &event{
		quiet: true,
	}

	stderrOutput := captureStderr(t, func() {
		testEvent.SetLongRunning("working")
		testEvent.SetProgress(1, 10)
	})

	assert.Empty(t, stderrOutput)
	assert.Nil(t, testEvent.spinner)
	assert.False(t, testEvent.initializedProgressBar)
	assert.Zero(t, testEvent.lastReportedCompletionRatio)
}
