// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGenerateRunbookYAML(t *testing.T) {
	data, err := generateRunbookYAML(
		"lisa-qemu",
		[]string{"verify_cpu_count", "verify_grub"},
		"/abs/image.qcow2",
		"/home/user/.ssh/id_rsa",
	)
	require.NoError(t, err)

	var runbook lisaRunbook
	require.NoError(t, yaml.Unmarshal(data, &runbook))

	assert.Equal(t, "lisa-qemu", runbook.Name)
	require.Len(t, runbook.Include, 1)
	assert.Equal(t, "lisa/microsoft/runbook/tiers/tier.yml", runbook.Include[0].Path)

	require.Len(t, runbook.Testcase, 1)
	assert.Equal(t, "verify_cpu_count|verify_grub", runbook.Testcase[0].Criteria.Name)

	require.Len(t, runbook.Notifier, 1)
	assert.Equal(t, "html", runbook.Notifier[0].Type)

	require.Len(t, runbook.Platform, 1)
	platform := runbook.Platform[0]
	assert.Equal(t, "qemu", platform.Type)
	assert.Equal(t, "/home/user/.ssh/id_rsa", platform.AdminPrivateKeyFile)
	assert.Equal(t, "no", platform.KeepEnvironment)
	assert.Equal(t, runbookBootTimeoutSeconds, platform.Qemu.NetworkBootTimeout)
	assert.Equal(t, "/abs/image.qcow2", platform.Requirement.Qemu.Qcow2)
}

func TestGenerateRunbookYAML_SingleTestCase(t *testing.T) {
	data, err := generateRunbookYAML("solo", []string{"verify_grub"}, "img", "key")
	require.NoError(t, err)

	var runbook lisaRunbook
	require.NoError(t, yaml.Unmarshal(data, &runbook))

	require.Len(t, runbook.Testcase, 1)
	assert.Equal(t, "verify_grub", runbook.Testcase[0].Criteria.Name)
}
