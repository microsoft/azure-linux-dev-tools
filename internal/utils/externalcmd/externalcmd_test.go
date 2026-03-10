// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test private functions
package externalcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

//
// The tests in this file aren't faithful unit tests because they require running
// external processes, outside of any test filesystem. In the future, we may
// choose to mark them with a 'scenario' build tag and filter them out of
// normal unit test execution.
//

func newEchoCmd(text string) *exec.Cmd {
	return exec.CommandContext(context.Background(), "sh", "-c", fmt.Sprintf("printf '%s'", text))
}

func linesToString(lines []string) string {
	return strings.Join(lines, "\n") + "\n"
}

func mockDependencies(t *testing.T) (*opctx_test.MockDryRunnable, *opctx_test.MockEventListener) {
	t.Helper()

	ctrl := gomock.NewController(t)

	return opctx_test.NewMockDryRunnable(ctrl), opctx_test.NewMockEventListener(ctrl)
}

func TestNewCmdFactory(t *testing.T) {
	mockDryRunnable, mockEventListener := mockDependencies(t)
	factory, err := NewCmdFactory(mockDryRunnable, mockEventListener)
	require.NoError(t, err)
	require.NotNil(t, factory)

	// We expect 'sh' is always present in *some* form.
	assert.True(t, factory.CommandInSearchPath("sh"))

	// Make sure we can construct a command.
	cmd, err := factory.Command(newEchoCmd("hello"))

	require.NoError(t, err)
	assert.NotNil(t, cmd)
}

func TestNewCmdFactoryFailsForNilDryRunnable(t *testing.T) {
	_, mockEventListener := mockDependencies(t)
	factory, err := NewCmdFactory(nil, mockEventListener)

	require.Error(t, err)
	assert.Nil(t, factory)
}

func TestNewCmdFactoryFailsForNilEventListener(t *testing.T) {
	ctrl := gomock.NewController(t)
	factory, err := NewCmdFactory(opctx_test.NewMockDryRunnable(ctrl), nil)

	require.Error(t, err)
	assert.Nil(t, factory)
}

func TestNewExternalCommand(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDryRunnable, mockEventListener := mockDependencies(t)
	mockDryRunnable.EXPECT().DryRun().AnyTimes()
	cmd, err := newExternalCmd(
		opctx_test.NewMockCmdFactory(ctrl),
		mockDryRunnable,
		mockEventListener,
		newEchoCmd("hello"))

	require.NoError(t, err)
	assert.NotNil(t, cmd)
}

func TestNewExternalCommandFailsForNilCmdFactory(t *testing.T) {
	mockDryRunnable, mockEventListener := mockDependencies(t)
	cmd, err := newExternalCmd(
		nil,
		mockDryRunnable,
		mockEventListener,
		newEchoCmd("hello"))

	require.Error(t, err)
	assert.Nil(t, cmd)
}

func TestNewExternalCommandFailsForNilDryRunnable(t *testing.T) {
	ctrl := gomock.NewController(t)
	cmd, err := newExternalCmd(
		opctx_test.NewMockCmdFactory(ctrl),
		nil,
		opctx_test.NewMockEventListener(ctrl),
		newEchoCmd("hello"))

	require.Error(t, err)
	assert.Nil(t, cmd)
}

func TestNewExternalCommandFailsForNilEventListener(t *testing.T) {
	ctrl := gomock.NewController(t)
	cmd, err := newExternalCmd(
		opctx_test.NewMockCmdFactory(ctrl),
		opctx_test.NewMockDryRunnable(ctrl),
		nil,
		newEchoCmd("hello"))

	require.Error(t, err)
	assert.Nil(t, cmd)
}

func TestNewExternalCommandFailsForNilCmd(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDryRunnable, mockEventListener := mockDependencies(t)
	cmd, err := newExternalCmd(
		opctx_test.NewMockCmdFactory(ctrl),
		mockDryRunnable,
		mockEventListener,
		nil)

	require.Error(t, err)
	assert.Nil(t, cmd)
}

func TestCommandRun(t *testing.T) {
	const testText = "hello"

	ctx := testctx.NewCtx()
	mockDryRunnable, _ := mockDependencies(t)

	// Construct a command that will write to a file (so we can see if it ran).
	filePath := filepath.Join(t.TempDir(), "test-file")
	cmd, err := newExternalCmd(
		ctx,
		mockDryRunnable,
		ctx,
		exec.CommandContext(t.Context(), "sh", "-c", fmt.Sprintf("echo '%s' > '%s'", testText, filePath)))
	require.NoError(t, err)
	require.NotNil(t, cmd)

	cmd.SetDescription("Some command")

	// Run the command in non-dry run mode.
	mockDryRunnable.EXPECT().DryRun().Return(false).AnyTimes()
	require.NoError(t, cmd.Run(t.Context()))

	// Command was not "long running" so no event should have fired.
	require.Empty(t, ctx.Events)

	// Make sure the command created its file.
	require.FileExists(t, filePath)

	// Make sure the command did what we expected (i.e., wrote the test string).
	readBytes, err := os.ReadFile(filePath)
	require.NoError(t, err)

	assert.Equal(t, testText+"\n", string(readBytes))
}

func TestCommandDryRuns(t *testing.T) {
	tests := []struct {
		name        string
		dryRun      bool
		cmdSucceeds bool
	}{
		{
			name:   "real run of 'false' returns error",
			dryRun: false,
		},
		{
			name:   "dry run of 'false' does not return error",
			dryRun: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockCmdFactory := opctx_test.NewMockCmdFactory(gomock.NewController(t))
			mockDryRunnable, mockEventListener := mockDependencies(t)
			mockDryRunnable.EXPECT().DryRun().Return(test.dryRun).MinTimes(1)
			cmd, err := newExternalCmd(
				mockCmdFactory,
				mockDryRunnable,
				mockEventListener,
				exec.CommandContext(t.Context(), "sh", "-c", "false"))
			require.NoError(t, err)

			assert.Equal(t, test.dryRun, cmd.Run(t.Context()) == nil)
		})
	}
}

func TestRunNonExistingCmd(t *testing.T) {
	ctx := testctx.NewCtx()

	tests := []struct {
		name                       string
		executablePath             string
		expectedDryRunChecks       int
		expectErrMissingExecutable bool
	}{
		{
			name:                       "non-existent full path executable",
			executablePath:             "/non-existent-path/non-existent-executable",
			expectedDryRunChecks:       1,
			expectErrMissingExecutable: false,
		},
		{
			name:                       "non-existent filename-only executable",
			executablePath:             "non-existent-executable",
			expectedDryRunChecks:       0,
			expectErrMissingExecutable: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockDryRunnable, mockEventListener := mockDependencies(t)
			mockDryRunnable.EXPECT().DryRun().Return(false).Times(test.expectedDryRunChecks)

			cmd, err := newExternalCmd(
				ctx.CmdFactory,
				mockDryRunnable,
				mockEventListener,
				exec.CommandContext(t.Context(), test.executablePath))
			require.NoError(t, err)

			err = cmd.Run(t.Context())
			require.Error(t, err)
			assert.Equal(t, test.expectErrMissingExecutable, errors.Is(err, ErrMissingExecutable))
		})
	}
}

func TestCommandRun_Failure(t *testing.T) {
	ctx := testctx.NewCtx()
	mockDryRunnable, mockEventListener := mockDependencies(t)
	mockDryRunnable.EXPECT().DryRun().Return(false).MinTimes(1)
	cmd, err := newExternalCmd(
		ctx.CmdFactory,
		mockDryRunnable,
		mockEventListener,
		exec.CommandContext(t.Context(), "sh", "-c", "false"))
	require.NoError(t, err)

	err = cmd.Run(t.Context())
	require.Error(t, err)

	var exitErr *exec.ExitError

	// Make sure there's a native exit error in there.
	require.ErrorAs(t, err, &exitErr)
	require.NotZero(t, exitErr.ExitCode())
}

func TestCommandRun_LongRunning(t *testing.T) {
	const (
		testText          = "hello"
		testDesc          = "Some command"
		testProgressTitle = "Some progress"
	)

	ctx := testctx.NewCtx()
	mockDryRunnable, _ := mockDependencies(t)
	mockDryRunnable.EXPECT().DryRun().Return(false).MinTimes(1)
	cmd, err := newExternalCmd(
		ctx.CmdFactory,
		mockDryRunnable,
		ctx,
		newEchoCmd("hello"))
	require.NoError(t, err)

	cmd.SetDescription(testDesc)
	cmd.SetLongRunning(testProgressTitle)

	// Run the command.
	require.NoError(t, cmd.Run(t.Context()))

	// Event should have fired.
	require.NotEmpty(t, ctx.Events)
	require.Len(t, ctx.Events, 1)

	evt := ctx.Events[0]

	assert.Equal(t, testDesc, evt.Name)
	assert.Equal(t, testProgressTitle, evt.LongRunningDescription)
	assert.True(t, evt.Ended)
}

func TestCommandRun_Cancellation(t *testing.T) {
	ctx := testctx.NewCtx()
	mockDryRunnable, mockEventListener := mockDependencies(t)
	mockDryRunnable.EXPECT().DryRun().Return(false).MinTimes(1)

	// Set up a cancellable context.
	var cancelFunc context.CancelFunc

	ctx.Ctx, cancelFunc = context.WithCancel(ctx.Ctx)

	// Sleep for an unreasonable amount of time.
	cmd, err := newExternalCmd(
		ctx.CmdFactory,
		mockDryRunnable,
		mockEventListener,
		exec.CommandContext(ctx, "sleep", "54321"))
	require.NoError(t, err)

	// Start...
	require.NoError(t, cmd.Start(ctx))

	// ..and fast-follow with cancellation.
	cancelFunc()

	// ...and wait for the inevitable error.
	err = cmd.Wait(ctx)
	assert.Error(t, err)
}

func TestCommandRun_StdioListeners(t *testing.T) {
	testOutputLines := []string{"line1", "line2"}
	ctx := testctx.NewCtx()
	mockDryRunnable, mockEventListener := mockDependencies(t)
	mockDryRunnable.EXPECT().DryRun().Return(false).MinTimes(1)

	t.Run("stdout", func(t *testing.T) {
		linesReceived := []string{}

		cmd, err := newExternalCmd(
			ctx.CmdFactory,
			mockDryRunnable,
			mockEventListener,
			newEchoCmd(linesToString(testOutputLines)))
		require.NoError(t, err)

		require.NoError(t, cmd.SetRealTimeStdoutListener(func(ctx context.Context, line string) {
			linesReceived = append(linesReceived, line)
		}))

		// Make sure we saw the right lines.
		require.NoError(t, cmd.Run(t.Context()))
		assert.Equal(t, testOutputLines, linesReceived)
	})

	t.Run("stderr", func(t *testing.T) {
		linesReceived := []string{}

		innerCmd := exec.CommandContext(
			t.Context(),
			"sh", "-c",
			fmt.Sprintf("printf '%s' >&2", linesToString(testOutputLines)))

		cmd, err := newExternalCmd(
			ctx.CmdFactory,
			mockDryRunnable,
			mockEventListener,
			innerCmd)
		require.NoError(t, err)

		require.NoError(t, cmd.SetRealTimeStderrListener(func(ctx context.Context, line string) {
			linesReceived = append(linesReceived, line)
		}))

		// Make sure we saw the right lines.
		require.NoError(t, cmd.Run(t.Context()))
		assert.Equal(t, testOutputLines, linesReceived)
	})

	t.Run("both", func(t *testing.T) {
		testOutputLinesExtra := []string{"line3", "line4"}

		innerCmd := exec.CommandContext(
			t.Context(),
			"sh", "-c",
			fmt.Sprintf("printf '%s' >&2; printf '%s'", linesToString(testOutputLines),
				linesToString(testOutputLinesExtra)))

		cmd, err := newExternalCmd(
			ctx.CmdFactory,
			mockDryRunnable,
			mockEventListener,
			innerCmd)
		require.NoError(t, err)

		linesReceivedStdout := []string{}

		require.NoError(t, cmd.SetRealTimeStdoutListener(func(ctx context.Context, line string) {
			linesReceivedStdout = append(linesReceivedStdout, line)
		}))

		linesReceivedStderr := []string{}

		require.NoError(t, cmd.SetRealTimeStderrListener(func(ctx context.Context, line string) {
			linesReceivedStderr = append(linesReceivedStderr, line)
		}))

		// Make sure we saw the right lines.
		require.NoError(t, cmd.Run(t.Context()))
		assert.Equal(t, testOutputLinesExtra, linesReceivedStdout)
		assert.Equal(t, testOutputLines, linesReceivedStderr)
	})
}

func TestCommandRun_FileListeners(t *testing.T) {
	testOutputLines := []string{
		"line1",
		"line2",
	}

	testPath := filepath.Join(t.TempDir(), "test-file")

	ctx := testctx.NewCtx()
	mockDryRunnable, mockEventListener := mockDependencies(t)
	mockDryRunnable.EXPECT().DryRun().Return(false).MinTimes(1)

	innerCmd := exec.CommandContext(
		t.Context(),
		"sh", "-c",
		fmt.Sprintf("printf '%s' > '%s'", linesToString(testOutputLines), testPath))

	cmd, err := newExternalCmd(
		ctx.CmdFactory,
		mockDryRunnable,
		mockEventListener,
		innerCmd)
	require.NoError(t, err)

	linesReceived := []string{}

	require.NoError(t, cmd.AddRealTimeFileListener(testPath, func(_ context.Context, line string) {
		linesReceived = append(linesReceived, line)
	}))

	require.NoError(t, cmd.Run(t.Context()))

	// Make sure we saw the right lines, or a strict prefix of them. The listener is a best-effort
	// thing, and is auto-cancelled when the command exits for cleanup. This means we may only
	// have seen 0 or 1 lines.
	assert.Equal(t, testOutputLines[:len(linesReceived)], linesReceived)
}

func TestCommandWait_NotRun(t *testing.T) {
	ctx := testctx.NewCtx()
	mockDryRunnable, mockEventListener := mockDependencies(t)
	mockDryRunnable.EXPECT().DryRun().Return(false).MinTimes(1)

	cmd, err := newExternalCmd(
		ctx.CmdFactory,
		mockDryRunnable,
		mockEventListener,
		newEchoCmd("hello"))
	require.NoError(t, err)

	assert.Error(t, cmd.Wait(ctx))
}

func TestCommandRunAndGetOutput_Success(t *testing.T) {
	const testStr = "\nhello, output reader!\n"

	ctx := testctx.NewCtx()
	mockDryRunnable, mockEventListener := mockDependencies(t)
	mockDryRunnable.EXPECT().DryRun().Return(false).MinTimes(1)

	// Confirm that leading and trailing space is not trimmed.
	cmd, err := newExternalCmd(
		ctx.CmdFactory,
		mockDryRunnable,
		mockEventListener,
		newEchoCmd(testStr))
	require.NoError(t, err)

	output, err := cmd.RunAndGetOutput(t.Context())
	require.NoError(t, err)
	assert.Equal(t, testStr, output)
}

func TestCommandRunAndGetOutput_DryRun(t *testing.T) {
	ctx := testctx.NewCtx()
	ctx.DryRunValue = true
	_, mockEventListener := mockDependencies(t)

	cmd, err := newExternalCmd(
		ctx.CmdFactory,
		ctx,
		mockEventListener,
		exec.CommandContext(t.Context(), "sh", "-c", "false"))
	require.NoError(t, err)

	// Shouldn't fail, because it's a dry run.
	_, err = cmd.RunAndGetOutput(t.Context())
	require.NoError(t, err)
}

func TestCommandRunAndGetOutput_Failure(t *testing.T) {
	const testStr = "some output"

	ctx := testctx.NewCtx()
	mockDryRunnable, mockEventListener := mockDependencies(t)
	mockDryRunnable.EXPECT().DryRun().Return(false).MinTimes(1)

	cmd, err := newExternalCmd(
		ctx.CmdFactory,
		mockDryRunnable,
		mockEventListener,
		exec.CommandContext(t.Context(), "sh", "-c", fmt.Sprintf("echo -n '%s' && false", testStr)),
	)
	require.NoError(t, err)

	output, err := cmd.RunAndGetOutput(t.Context())
	require.Error(t, err)

	var exitErr *exec.ExitError

	// Make sure there's a native exit error in there.
	require.ErrorAs(t, err, &exitErr)
	assert.NotZero(t, exitErr.ExitCode())

	// Make sure that any stdout output was returned, even on error.
	assert.Equal(t, testStr, output)
}
