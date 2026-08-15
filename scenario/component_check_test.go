// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checkEVRReport struct {
	Selector   string                    `json:"selector"`
	Components []checkEVRComponentResult `json:"components"`
}

type checkEVRComponentResult struct {
	Component     string   `json:"component"`
	CounterSource string   `json:"counterSource"`
	Notes         []string `json:"notes"`
	Verdict       string   `json:"verdict"`
}

// TestComponentCheckEVRCounterDrift covers the counter-validation half of 'component check
// --evr', which is the only half that reaches a verdict without a mock chroot: a counter that
// no longer resolves against the rendered spec is reported before anything is staged, so the
// run never starts mock. The bump-effectiveness half needs rpmspec in the target chroot and is
// covered by unit tests that inject evaluated EVR pairs directly.
func TestComponentCheckEVRCounterDrift(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	azldevBin, err := testhelpers.FindTestBinary()
	require.NoError(t, err)

	const componentName = "ansible-packaging"
	const renderedSpecPath = "SPECS/a/ansible-packaging/ansible-packaging.spec"
	projectDir := t.TempDir()
	writeFileInDir(t, projectDir, "azldev.toml", `includes = ["distro.toml"]

[project]
default-distro = { name = "testdistro", version = "1.0" }
rendered-specs-dir = "SPECS"

[components.ansible-packaging]
spec = { type = "local", path = "SPECS/a/ansible-packaging/ansible-packaging.spec" }

[components.ansible-packaging.release]
calculation = "static"

[components.ansible-packaging.release.counter]
source = "release-tag"
regex = '^[0-9]+\.([0-9]+)(?:%\{\??dist\})?$'
`)
	writeFileInDir(t, projectDir, "distro.toml", minimalDistroTOML)
	// The rendered Release lost the '.N' field the counter regex captures, which is exactly the
	// upstream drift the gate exists to catch.
	writeFileInDir(t, projectDir, renderedSpecPath,
		"Name: ansible-packaging\nVersion: 1\nRelease: 18%{?dist}\nSummary: Test\nLicense: MIT\n")

	command := exec.CommandContext(t.Context(),
		azldevBin, "-C", projectDir, "--no-default-config", "component", "check",
		"--evr", "-a", "-q", "-O", "json",
	)
	command.Env = append(os.Environ(), "AZLDEV_ALLOW_ROOT=1")

	var stdoutBuffer, stderrBuffer bytes.Buffer

	command.Stdout = &stdoutBuffer
	command.Stderr = &stderrBuffer

	err = command.Run()
	require.Error(t, err, "drifted release tag must fail the gate: %s", stderrBuffer.String())

	var report checkEVRReport
	require.NoError(t, json.Unmarshal(stdoutBuffer.Bytes(), &report))
	assert.Equal(t, "identity", report.Selector)
	require.Len(t, report.Components, 1)
	assert.Equal(t, componentName, report.Components[0].Component)
	assert.Equal(t, "release-tag", report.Components[0].CounterSource)
	assert.Equal(t, "counter-invalid", report.Components[0].Verdict)
	assert.NotEmpty(t, report.Components[0].Notes)
}
