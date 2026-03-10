// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package snapshot

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/testhelpers"
	"github.com/stretchr/testify/require"
)

// Matches '##:##:##'. We don't require it to be strictly at the beginning of the line; with ANSI style codes,
// it may not be the first set of literal characters in the input line. We recognize that this runs the risk
// of us #-replacing non-timestamps that happen to look like dd:dd:dd.
var timestampRegex = regexp.MustCompile(`(\d{1,2}:\d{1,2}:\d{1,2})`)

// cachedVersionString stores the output of 'azldev --version' to avoid running the command multiple times.
//
//nolint:gochecknoglobals // This is intentionally global to take advantage of caching.
var cachedVersionString string

const replacementVersionString = "testing-version-1.2.3"

func getSnapDir(t *testing.T) string {
	t.Helper()

	// Use the scenario directory instead of the default; otherwise, snaps ends up
	// using the directory that *this* function is defined in, instead of the directory that
	// the calling test is defined in.
	return filepath.Join(mageutil.ScenarioDir(), "__snapshots__")
}

// TestSnapshottableCmd runs a configured test command and compares the output against a snapshot.
func TestSnapshottableCmd(t *testing.T, testParams testhelpers.ScenarioTest) {
	t.Helper()

	results, err := testParams.Run(t)
	require.NoError(t, err)

	parseSnapResults(t, results)
}

// filterTimestamps removes timestamps from the command output. We expect warning or error messages to contain
// timestamps, which would make the snapshots fail.
//
// i.e., "\x1b[1m23:45:06\x1b[0m \x1b[93mWRN\x1b[0m SOME ERROR" needs to have the '1m23:45:06' part removed. All ansi
// escape codes are also removed to simplify the regex. This function removes all occurrences of the any text
// matching the pattern.
func filterTimestamps(input string) string {
	// Need to filter line by line otherwise the regex becomes unmanageable
	lines := strings.Split(input, "\n")

	for i, line := range lines {
		lines[i] = timestampRegex.ReplaceAllString(line, "##:##:##")
	}

	return strings.Join(lines, "\n")
}

// getTestVersion returns the version string of the binary under test. It runs the command
// 'azldev --version' and parses the output to extract the version string. The version string is cached
// to avoid running the command multiple times.
func getTestVersion(t *testing.T) string {
	t.Helper()

	// Output should be of the form "azldev version 1.2.3". The version is the third part of the output.
	const partsCount = 3

	if cachedVersionString != "" {
		return cachedVersionString
	}

	versionCmd := cmdtest.NewScenarioTest("--version")
	results, err := versionCmd.Locally().Run(t)
	require.NoError(t, err)
	require.NotEmpty(t, results.Stdout)

	parts := strings.Split(results.Stdout, " ")
	require.Len(t, parts, partsCount)

	cachedVersionString = strings.TrimSpace(parts[partsCount-1])

	return cachedVersionString
}

// filterVersion replaces all occurrences of the version string in the input with [replacementVersionString].
func filterVersion(version, input string) string {
	return strings.ReplaceAll(input, version, replacementVersionString)
}

func parseSnapResults(t *testing.T, results testhelpers.TestResults) {
	t.Helper()

	snapConfig := NewConfig(t)

	t.Run("stdout", func(t *testing.T) {
		input := results.Stdout
		input = filterTimestamps(input)
		input = filterVersion(getTestVersion(t), input)
		snapConfig.MatchStandaloneSnapshot(t, input)
	})

	t.Run("stderr", func(t *testing.T) {
		input := results.Stderr
		input = filterTimestamps(input)
		input = filterVersion(getTestVersion(t), input)
		snapConfig.MatchStandaloneSnapshot(t, input)
	})

	snapConfig.MatchStandaloneJSON(t, results)
}

// NewConfig constructs a new [snaps.Config] configured appropriately for the tests using this package.
func NewConfig(t *testing.T) *snaps.Config {
	t.Helper()

	return snaps.WithConfig(snaps.Dir(getSnapDir(t)))
}
