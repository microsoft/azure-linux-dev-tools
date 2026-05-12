// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setDummyLogger(logBuffer *bytes.Buffer) *slog.Logger {
	logger := slog.New(slog.NewTextHandler(logBuffer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	oldDefault := slog.Default()
	slog.SetDefault(logger)

	return oldDefault
}

func TestNewEnv(t *testing.T) {
	const (
		testProjectRoot = "/non/existent/dir"
		testLogDir      = "/non/existent/logs"
		testWorkDir     = "/non/existent/work"
		testOutputDir   = "/non/existent/output"
	)

	config := &projectconfig.ProjectConfig{
		Project: projectconfig.ProjectInfo{
			LogDir:    testLogDir,
			WorkDir:   testWorkDir,
			OutputDir: testOutputDir,
		},
	}

	ctx, cancelFunc := context.WithCancel(t.Context())
	defer cancelFunc()

	testEnv := testutils.NewTestEnv(t)

	options := azldev.NewEnvOptions()
	options.DryRunnable = testEnv.DryRunnable
	options.EventListener = testEnv.EventListener
	options.Interfaces = testEnv.TestInterfaces
	options.ProjectDir = testProjectRoot
	options.Config = config

	env := azldev.NewEnv(ctx, options)

	// Pin defaults.
	assert.False(t, env.AllPromptsAccepted())
	assert.False(t, env.PromptsAllowed())
	assert.Equal(t, azldev.ReportFormatTable, env.DefaultReportFormat())
	assert.False(t, env.DryRun())
	assert.Equal(t, env.ReportFile(), os.Stdout)
	assert.False(t, env.Quiet())
	assert.False(t, env.Verbose())
	assert.False(t, env.PermissiveConfigParsing())

	// Confirm that our parameters were appropriately wrapped.
	assert.Equal(t, testProjectRoot, env.ProjectDir())
	assert.Equal(t, testWorkDir, env.WorkDir())
	assert.Equal(t, testLogDir, env.LogsDir())
	assert.Equal(t, testOutputDir, env.OutputDir())
	assert.Equal(t, config, env.Config())

	// Note that we can't find the distro.
	_, _, err := env.Distro()
	require.Error(t, err)

	// Confirm that the context is the right one.
	cancelFunc()
	assert.Equal(t, env.Err(), ctx.Err())
}

func TestSetNetworkRetries(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"positive value is preserved", 5, 5},
		{"one is preserved", 1, 1},
		{"zero is clamped to 1", 0, 1},
		{"negative value is clamped to 1", -1, 1},
		{"large negative is clamped to 1", -100, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testEnv := testutils.NewTestEnv(t)
			testEnv.Env.SetNetworkRetries(tt.input)
			assert.Equal(t, tt.expected, testEnv.Env.NetworkRetries())
		})
	}
}

func TestSetPermissiveConfigParsing(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	// Default should be false.
	assert.False(t, testEnv.Env.PermissiveConfigParsing())

	// Setting to true should stick.
	testEnv.Env.SetPermissiveConfigParsing(true)
	assert.True(t, testEnv.Env.PermissiveConfigParsing())

	// Setting back to false should work.
	testEnv.Env.SetPermissiveConfigParsing(false)
	assert.False(t, testEnv.Env.PermissiveConfigParsing())
}

func TestEnvConstructionTime(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	// Make sure it appears to be a valid time that's before or at *now*.
	assert.LessOrEqual(t, testEnv.Env.ConstructionTime(), time.Now())
}

func TestEnvWithCancel(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	original := testEnv.Env

	child, cancel := original.WithCancel()
	defer cancel()

	// Child should share config and project dir with original.
	assert.Equal(t, original.ProjectDir(), child.ProjectDir())
	assert.Equal(t, original.Config(), child.Config())

	// Cancelling the child should not cancel the original.
	cancel()
	require.Error(t, child.Err())
	assert.NoError(t, original.Err())
}

func TestFixSuggestions(t *testing.T) {
	t.Run("no suggestions does not panic", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)
		// Should not panic when no suggestions are present.
		assert.NotPanics(t, func() {
			testEnv.Env.PrintFixSuggestions()
		})
	})

	t.Run("single suggestion does not panic", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)
		testEnv.Env.AddFixSuggestion("run 'azldev component update -a'")
		assert.NotPanics(t, func() {
			testEnv.Env.PrintFixSuggestions()
		})
	})

	t.Run("multiple suggestions does not panic", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)
		testEnv.Env.AddFixSuggestion("first suggestion")
		testEnv.Env.AddFixSuggestion("second suggestion")
		testEnv.Env.AddFixSuggestion("third suggestion")
		// Should not panic with multiple suggestions.
		assert.NotPanics(t, func() {
			testEnv.Env.PrintFixSuggestions()
		})
	})

	t.Run("empty string suggestion does not panic", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)
		testEnv.Env.AddFixSuggestion("")
		assert.NotPanics(t, func() {
			testEnv.Env.PrintFixSuggestions()
		})
	})

	t.Run("suggestions added from child envs are visible on the parent env", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)

		const suggestionCount = 32

		var waitGroup sync.WaitGroup
		for suggestionIndex := range suggestionCount {
			waitGroup.Add(1)

			go func(index int) {
				defer waitGroup.Done()

				childEnv, cancel := testEnv.Env.WithCancel()
				defer cancel()

				childEnv.AddFixSuggestion(fmt.Sprintf("child suggestion %d", index))
			}(suggestionIndex)
		}

		waitGroup.Wait()

		var logBuffer bytes.Buffer

		oldDefault := setDummyLogger(&logBuffer)
		defer slog.SetDefault(oldDefault)

		testEnv.Env.PrintFixSuggestions()

		output := logBuffer.String()
		for suggestionIndex := range suggestionCount {
			assert.Contains(t, output, fmt.Sprintf("child suggestion %d", suggestionIndex))
		}
	})

	t.Run("concurrent suggestions on the same env are all preserved", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)

		const suggestionCount = 128

		var waitGroup sync.WaitGroup
		for suggestionIndex := range suggestionCount {
			waitGroup.Add(1)

			go func(index int) {
				defer waitGroup.Done()

				testEnv.Env.AddFixSuggestion(fmt.Sprintf("shared suggestion %d", index))
			}(suggestionIndex)
		}

		waitGroup.Wait()

		var logBuffer bytes.Buffer

		oldDefault := setDummyLogger(&logBuffer)
		defer slog.SetDefault(oldDefault)

		testEnv.Env.PrintFixSuggestions()

		output := logBuffer.String()
		assert.Equal(t, suggestionCount, strings.Count(output, "shared suggestion "))

		for suggestionIndex := range suggestionCount {
			assert.Contains(t, output, fmt.Sprintf("shared suggestion %d", suggestionIndex))
		}
	})
}
