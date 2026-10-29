// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build e2e && linux

package scenario_tests

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/containertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tmtArtifactRow struct {
	Test        string   `json:"test"`
	ExitCode    *int     `json:"exitCode"`
	ArtifactDir string   `json:"artifactDir"`
	JUnit       []string `json:"junit"`
}

func TestTMTLocalReporting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test")
	}
	base, err := filepath.Abs(filepath.Join("..", "out", "tmt-local-e2e"))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(base, fileperms.PrivateDir))
	// Keep retained private payloads out of Go's recursive package discovery.
	artifacts, err := os.MkdirTemp(base, "_run-")
	require.NoError(t, err)
	t.Logf("Retained local TMT artifacts: %s", artifacts)

	test := cmdtest.NewScenarioTest("fixture/run.sh").
		WithCustomCmd("bash").
		WithTimeout(20 * time.Minute).
		InContainer().
		WithDockerfile("Dockerfile.tmt").
		WithInit().
		WithNetwork().
		WithExtraMounts([]containertest.ContainerMount{
			containertest.NewContainerMount(artifacts, "/artifacts", nil),
		})
	test.AddDirRecursive(t, "fixture", filepath.Join("testdata", "tmt-local"))
	test.AddDirRecursive(t, "root-fixture", filepath.Join("testdata", "tmt-root"))
	test.AddSingleFile("project/azldev.toml", filepath.Join("testdata", "tmt-virtual", "azldev.toml"))
	test.AddSingleFile("project/azldev-tmt-probe.spec", filepath.Join("testdata", "tmt-virtual", "azldev-tmt-probe.spec"))
	results, err := test.Run(t)
	require.NoError(t, os.WriteFile(filepath.Join(artifacts, "container.stdout"), []byte(results.Stdout), fileperms.PrivateFile))
	require.NoError(t, os.WriteFile(filepath.Join(artifacts, "container.stderr"), []byte(results.Stderr), fileperms.PrivateFile))
	require.NoError(t, err)
	results.AssertZeroExitCode(t)

	roots := map[string]bool{}
	for _, tc := range []struct {
		name                         string
		azldevExit, tmtExit, reports int
		pass, fail, skip, errors     int
	}{
		{"pass", 0, 0, 1, 1, 0, 0, 0},
		{"private-data", 0, 0, 1, 1, 0, 0, 0},
		{"mixed", 1, 2, 1, 1, 1, 2, 2},
		{"skip", 1, 4, 1, 0, 0, 1, 0},
		{"fail", 1, 1, 1, 0, 1, 0, 0},
		{"warn", 1, 1, 1, 0, 0, 0, 1},
		{"with.dots", 0, 0, 1, 1, 0, 0, 0},
		{"root", 0, 0, 1, 1, 0, 0, 0},
		{"empty", 1, 3, 0, 0, 0, 0, 0},
		{"prepare-failure", 1, 2, 1, 0, 0, 0, 1},
		{"finish-failure", 1, 2, 1, 1, 0, 0, 0},
		{"report-failure", 1, 2, 0, 0, 0, 0, 0},
		{"multi", 0, 0, 2, 1, 0, 1, 0},
		{"pass-empty", 0, 0, 1, 1, 0, 0, 0},
		{"sigint", 1, -1, 0, 0, 0, 0, 0},
		{"sigterm", 1, -1, 0, 0, 0, 0, 0},
		{"pipe-timeout", 1, 0, 0, 0, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(artifacts, tc.name+".json"))
			require.NoError(t, err)
			var rows []tmtArtifactRow
			decoder := json.NewDecoder(bytes.NewReader(data))
			require.NoError(t, decoder.Decode(&rows), "stdout: %s", data)
			require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
			require.Len(t, rows, 1)
			row := rows[0]
			require.Equal(t, "azldev-tmt-probe", row.Test)
			exit, err := os.ReadFile(filepath.Join(artifacts, tc.name+".exit"))
			require.NoError(t, err)
			assert.Equal(t, strconv.Itoa(tc.azldevExit)+"\n", string(exit))
			if tc.tmtExit < 0 {
				assert.Nil(t, row.ExitCode)
			} else {
				require.NotNil(t, row.ExitCode)
				assert.Equal(t, tc.tmtExit, *row.ExitCode)
			}
			assert.Len(t, row.JUnit, tc.reports, "%s", data)
			assert.False(t, roots[row.ArtifactDir], "work-directory reuse must not reuse a run")
			roots[row.ArtifactDir] = true
			rel, err := filepath.Rel("/artifacts", row.ArtifactDir)
			require.NoError(t, err)
			require.True(t, filepath.IsLocal(rel))
			root := filepath.Join(artifacts, rel)
			counts := [4]int{}
			for _, report := range row.JUnit {
				require.True(t, filepath.IsLocal(report))
				assert.Contains(t, report, "/report/azldev-junit/junit.xml")
				testCounts := localTMTJUnitCounts(t, filepath.Join(root, report))
				for index, count := range testCounts {
					counts[index] += count
				}
			}
			assert.Equal(t, [4]int{tc.pass, tc.fail, tc.skip, tc.errors}, counts)
			runnerLog := filepath.Join(root, "001-azldev-tmt-probe", "runner.log")
			log, err := os.ReadFile(runnerLog)
			require.NoError(t, err)
			if tc.name == "private-data" {
				privateDirs, globErr := filepath.Glob(filepath.Join(root,
					"*/native/plans/private-data/execute/data/guest/*/tests/private-data-*/data/private"))
				require.NoError(t, globErr)
				require.Len(t, privateDirs, 1)
				_, readErr := os.ReadDir(privateDirs[0])
				require.ErrorIs(t, readErr, os.ErrPermission, "private payload must remain unreadable")
			}
			if tc.name == "pass" {
				assert.Contains(t, string(log), "azldev-junit")
				testLogs, globErr := filepath.Glob(filepath.Join(root,
					"*/native/plans/pass/execute/data/guest/*/tests/probe-*/output.txt"))
				require.NoError(t, globErr)
				require.Len(t, testLogs, 1)
				testOutput, readErr := os.ReadFile(testLogs[0])
				require.NoError(t, readErr)
				assert.Contains(t, string(testOutput), "noisy-probe-stdout")
				assert.Contains(t, string(testOutput), "noisy-probe-stderr")
				copied := t.TempDir()
				require.NoError(t, os.CopyFS(copied, os.DirFS(root)))
				assert.Equal(t, counts, localTMTJUnitCounts(t, filepath.Join(copied, row.JUnit[0])))
				supplied, globErr := filepath.Glob(filepath.Join(root, "*/native/plans/pass/report/supplied-junit/junit.xml"))
				require.NoError(t, globErr)
				require.Len(t, supplied, 1, "existing reporter must remain")
			}
			if strings.HasPrefix(tc.name, "sig") || tc.name == "pipe-timeout" {
				assert.Contains(t, string(log), "runner stderr")
				require.FileExists(t, filepath.Join(root, "001-azldev-tmt-probe/native/diagnostic.txt"))
			}
		})
	}
}

func localTMTJUnitCounts(t *testing.T, path string) [4]int {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var document struct {
		XMLName xml.Name `xml:"testsuites"`
		Cases   []struct {
			Failure *struct{} `xml:"failure"`
			Error   *struct{} `xml:"error"`
			Skipped *struct{} `xml:"skipped"`
		} `xml:"testsuite>testcase"`
	}
	require.NoError(t, xml.Unmarshal(data, &document))
	counts := [4]int{}
	for _, result := range document.Cases {
		switch {
		case result.Failure != nil:
			counts[1]++
		case result.Error != nil:
			counts[3]++
		case result.Skipped != nil:
			counts[2]++
		default:
			counts[0]++
		}
	}

	return counts
}
