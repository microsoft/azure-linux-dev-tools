// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateMockProcessor_UsesProjectDistroMockConfig(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	processor := createMockProcessor(testEnv.Env)

	require.NotNil(t, processor,
		"the project distro's mock config should make a processor available independently of any upstream distro")
	destroyMockProcessor(testEnv.Env, processor)
}

func TestCreateMockProcessor_ProjectDistroWithoutMockConfig(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	clearProjectMockConfig(testEnv.Env.Config())

	assert.Nil(t, createMockProcessor(testEnv.Env))
}

func TestCreateBuildMockProcessor_ReturnsProcessor(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	processor := createBuildMockProcessor(testEnv.Env)

	require.NotNil(t, processor,
		"the build command isolates the provenance root but must still resolve it from the project mock config")
	destroyMockProcessor(testEnv.Env, processor)
}

func TestCreateBuildMockProcessor_ProjectDistroWithoutMockConfig(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	clearProjectMockConfig(testEnv.Env.Config())

	assert.Nil(t, createBuildMockProcessor(testEnv.Env))
}

func TestDestroyMockProcessor_NilIsNoOp(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	assert.NotPanics(t, func() { destroyMockProcessor(testEnv.Env, nil) })
}

// clearProjectMockConfig removes the mock config path from the project's default
// distro version so the mock processor helpers report it as unavailable.
func clearProjectMockConfig(config *projectconfig.ProjectConfig) {
	distro := config.Distros[config.Project.DefaultDistro.Name]
	version := distro.Versions[config.Project.DefaultDistro.Version]
	version.MockConfigPath = ""
	distro.Versions[config.Project.DefaultDistro.Version] = version
	config.Distros[config.Project.DefaultDistro.Name] = distro
}
