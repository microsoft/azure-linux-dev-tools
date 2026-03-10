// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package testhelpers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ScenarioTest is an interface describing a scenario test.
type ScenarioTest interface {
	// Runs the scenario test, returning results.
	Run(t *testing.T) (results TestResults, err error)
}

// TestResults is a struct describing the results of a scenario test.
type TestResults struct {
	// Basics
	ExitCode int

	// We exclude stdout and stderr from JSON serialization because they are best separately saved.
	Stdout string `json:"-"`
	Stderr string `json:"-"`

	// We exclude the workdir path from JSON serialization as well; it's not stable.
	Workdir string `json:"-"`
}

// AssertNonZeroExitCode asserts that the exit code of the test is non-zero. If the assertion fails,
// it dumps the test summary to the test log.
func (r *TestResults) AssertNonZeroExitCode(t *testing.T) {
	t.Helper()

	if !assert.NotEqual(t, 0, r.ExitCode, "exit code expected to be non-zero") {
		r.DumpSummary(t)
	}
}

// AssertZeroExitCode asserts that the exit code of the test is 0. If the assertion fails,
// it dumps the test summary to the test log.
func (r *TestResults) AssertZeroExitCode(t *testing.T) {
	t.Helper()

	if !assert.Equal(t, 0, r.ExitCode, "exit code expected to be 0") {
		r.DumpSummary(t)
	}
}

// AssertStdoutContains asserts that the stdout output of the test contains the given string.
// If the assertion fails, it dumps the test summary to the test log.
func (r *TestResults) AssertStdoutContains(t *testing.T, expected string) {
	t.Helper()

	if !assert.Contains(t, r.Stdout, expected, "stdout expected to contain %q", expected) {
		r.DumpSummary(t)
	}
}

// AssertStderrContains asserts that the stderr output of the test contains the given string.
// If the assertion fails, it dumps the test summary to the test log.
func (r *TestResults) AssertStderrContains(t *testing.T, expected string) {
	t.Helper()

	if !assert.Contains(t, r.Stderr, expected, "stderr expected to contain %q", expected) {
		r.DumpSummary(t)
	}
}

// DumpSummary writes a brief summary of the test execution results to the test log. This is typically only
// invoked for failure cases, or in verbose modes.
func (r *TestResults) DumpSummary(t *testing.T) {
	t.Helper()

	t.Log("-------------------------------------")
	t.Logf("[EXIT CODE] %d", r.ExitCode)

	if r.Stdout != "" {
		for _, line := range strings.Split(r.Stdout, "\n") {
			t.Logf("[STDOUT] %s", line)
		}
	}

	if r.Stderr != "" {
		for _, line := range strings.Split(r.Stderr, "\n") {
			t.Logf("[STDERR] %s", line)
		}
	}

	t.Log("-------------------------------------")
}
