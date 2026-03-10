// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package version_test

import (
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/version"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionCmd(t *testing.T) {
	cmd := version.NewVersionCmd()
	assert.NotNil(t, cmd)
	assert.Equal(t, "version", cmd.Use)
	assert.Contains(t, cmd.Aliases, "ver")
	assert.Equal(t, "Print the CLI version", cmd.Short)
}

func TestVersionCmd_JSONOutput(t *testing.T) {
	env := testutils.NewTestEnv(t)
	env.Env.SetDefaultReportFormat(azldev.ReportFormatJSON)

	reportOutput := new(strings.Builder)
	env.Env.SetReportFile(reportOutput)

	cmd := version.NewVersionCmd()
	require.NotNil(t, cmd)

	// Set up context
	cmd.SetContext(env.Env)

	// Execute the command
	err := cmd.Execute()
	require.NoError(t, err)

	// Verify JSON output
	output := reportOutput.String()
	assert.Contains(t, output, `"version"`)
	assert.Contains(t, output, `"gitCommit"`)
	assert.Contains(t, output, `"goVersion"`)
	assert.Contains(t, output, `"platform"`)
	assert.Contains(t, output, `"compiler"`)
	assert.Contains(t, output, `"buildDate"`)
	assert.Contains(t, output, `"commitDate"`)
	assert.Contains(t, output, `"dirtyBuild"`)
}

func TestGetVersionInfo(t *testing.T) {
	info := version.GetVersionInfo()
	assert.NotNil(t, info)

	// These fields should always be populated
	assert.NotEmpty(t, info.GoVersion)
	assert.NotEmpty(t, info.Compiler)
	assert.NotEmpty(t, info.Platform)

	// These may be empty in development builds, but the fields should exist
	assert.NotNil(t, info.Version)
	assert.NotNil(t, info.GitCommit)
	assert.NotNil(t, info.BuildDate)
	assert.NotNil(t, info.CommitDate)
}
