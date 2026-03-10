// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/stretchr/testify/assert"
)

func TestNewEnv(t *testing.T) {
	const (
		testProjectRoot = "/non/existent/dir"
		testLogDir      = "/non/existent/logs"
		testWorkDir     = "/non/existent/work"
		testOutputDir   = "/non/existent/output"
	)

	ctx, cancelFunc := context.WithCancel(t.Context())
	defer cancelFunc()

	testEnv := testutils.NewTestEnv(t)

	options := azldev.NewEnvOptions()
	options.DryRunnable = testEnv.DryRunnable
	options.EventListener = testEnv.EventListener
	options.Interfaces = testEnv.TestInterfaces
	options.ProjectDir = testProjectRoot

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
	assert.False(t, env.ClassicToolkitPresent())

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
