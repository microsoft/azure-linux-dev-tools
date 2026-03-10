// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package defers_test

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDefer is a test struct with a deferrable call that can be configured to fail.
type mockDefer struct {
	deferErr error
	called   bool
}

func (m *mockDefer) Call() error {
	m.called = true

	return m.deferErr
}

func setDummyLogger(logBuffer *bytes.Buffer) *slog.Logger {
	logger := slog.New(slog.NewTextHandler(logBuffer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	oldDefault := slog.Default()
	slog.SetDefault(logger)

	return oldDefault
}

// This function simulates an operation that returns more than just an error.
func funcWithOutputAndErr(funcErr error) (int, error) {
	return 42, funcErr
}

func funcWithDeferErr(t *testing.T, funcErr, deferErr error) (err error) {
	t.Helper()

	// This order of operations shows that ':=' doesn't shadow the named return 'err'.
	output, err := funcWithOutputAndErr(funcErr)

	defer defers.HandleDeferError(func() error {
		return deferErr
	}, &err)

	// Dummy check to force usage of the 'output' variable.
	require.IsType(t, 0, output)

	// Returning 'nil' to show that this doesn't prevent [defers.HandleDeferError] from
	// updating the actually returned error.
	return err
}

func TestHandleDeferErrorInvalidInput(t *testing.T) {
	t.Run("nil deferred function panics", func(t *testing.T) {
		var err error

		assert.Panics(t, func() {
			defers.HandleDeferError(nil, &err)
		})
	})

	t.Run("nil error pointer panics", func(t *testing.T) {
		assert.Panics(t, func() {
			defers.HandleDeferError(func() error { return nil }, nil)
		})
	})
}

func TestHandleDeferErrorValidInput(t *testing.T) {
	t.Run("successful defer does not log errors and keeps error nil", func(t *testing.T) {
		var (
			err       error
			logBuffer bytes.Buffer
		)

		deferObj := &mockDefer{}

		// Capture log output
		oldDefault := setDummyLogger(&logBuffer)
		defer slog.SetDefault(oldDefault)

		defers.HandleDeferError(deferObj.Call, &err)

		assert.True(t, deferObj.called)
		assert.Empty(t, logBuffer.String())
		assert.NoError(t, err, "expected no error after successful defer and when initial error is nil")
	})

	t.Run("successful defer with existing error does not overwrite it", func(t *testing.T) {
		var logBuffer bytes.Buffer

		deferObj := &mockDefer{}

		// Capture log output
		oldDefault := setDummyLogger(&logBuffer)
		defer slog.SetDefault(oldDefault)

		initialErr := errors.New("initial error")
		outputErr := initialErr

		defers.HandleDeferError(deferObj.Call, &outputErr)

		assert.True(t, deferObj.called)
		assert.Empty(t, logBuffer.String())
		assert.Equal(t, initialErr, outputErr, "expected existing error to remain unchanged after successful defer")
	})

	t.Run("failed defer updates error", func(t *testing.T) {
		var logBuffer bytes.Buffer

		oldDefault := setDummyLogger(&logBuffer)
		defer slog.SetDefault(oldDefault)

		deferErr := errors.New("mock close error")
		deferObj := &mockDefer{deferErr: deferErr}

		initialErr := errors.New("initial error")
		outputErr := initialErr

		defers.HandleDeferError(deferObj.Call, &outputErr)

		assert.True(t, deferObj.called)
		assert.Empty(t, logBuffer.String(), "expected no log output for valid input")
		require.ErrorIs(t, outputErr, initialErr, "expected output error to match initial error")
		require.ErrorIs(t, outputErr, deferErr, "expected error to be updated with defer error")
	})
}

func TestHandleDeferErrorFromFunction(t *testing.T) {
	t.Run("OK function, nil defer error returns nil", func(t *testing.T) {
		err := funcWithDeferErr(t, nil, nil)

		assert.NoError(t, err, "expected no error when defer function succeeds without error")
	})

	t.Run("OK function, non-nil defer error returns defer error", func(t *testing.T) {
		deferErr := errors.New("defer error from lower function")
		err := funcWithDeferErr(t, nil, deferErr)

		if assert.Error(t, err, "expected error from deferred function") {
			assert.ErrorIs(t, err, deferErr, "expected error to match defer error")
		}
	})

	t.Run("failing function, nil defer error returns function error", func(t *testing.T) {
		funcErr := errors.New("lower function error")
		err := funcWithDeferErr(t, funcErr, nil)

		if assert.Error(t, err, "expected error from function call") {
			assert.ErrorIs(t, err, funcErr, "expected error to match function error")
		}
	})

	t.Run("failing function, non-nil defer error returns both errors", func(t *testing.T) {
		funcErr := errors.New("lower function error")
		deferErr := errors.New("defer error from lower function")
		err := funcWithDeferErr(t, funcErr, deferErr)

		if assert.Error(t, err, "expected error from both function and deferred call") {
			assert.ErrorIs(t, err, funcErr, "expected error to match function error")
			assert.ErrorIs(t, err, deferErr, "expected error to match defer error")
		}
	})
}
