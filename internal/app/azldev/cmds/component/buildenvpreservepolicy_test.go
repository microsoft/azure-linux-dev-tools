// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringification(t *testing.T) {
	policy := component.BuildEnvPreserveAlways
	assert.Equal(t, "always", policy.String())

	policy = component.BuildEnvPreserveNever
	assert.Equal(t, "never", policy.String())

	policy = component.BuildEnvPreserveOnFailure
	assert.Equal(t, "on-failure", policy.String())
}

func TestSet(t *testing.T) {
	var policy component.BuildEnvPreservePolicy

	err := policy.Set("always")
	require.NoError(t, err)
	assert.Equal(t, component.BuildEnvPreserveAlways, policy)

	err = policy.Set("never")
	require.NoError(t, err)
	assert.Equal(t, component.BuildEnvPreserveNever, policy)

	err = policy.Set("on-failure")
	require.NoError(t, err)
	assert.Equal(t, component.BuildEnvPreserveOnFailure, policy)

	err = policy.Set("")
	require.NoError(t, err)
	assert.Equal(t, component.BuildEnvPreserveOnFailure, policy)

	err = policy.Set("unsupported-value")
	require.Error(t, err)
	assert.Equal(t, component.BuildEnvPreserveOnFailure, policy)
}

func TestType(t *testing.T) {
	var policy component.BuildEnvPreservePolicy

	assert.Equal(t, "policy", policy.Type())
}

func TestShouldPreserve(t *testing.T) {
	policy := component.BuildEnvPreserveAlways
	assert.True(t, policy.ShouldPreserve(true))
	assert.True(t, policy.ShouldPreserve(false))

	policy = component.BuildEnvPreserveNever
	assert.False(t, policy.ShouldPreserve(true))
	assert.False(t, policy.ShouldPreserve(false))

	policy = component.BuildEnvPreserveOnFailure
	assert.False(t, policy.ShouldPreserve(true))
	assert.True(t, policy.ShouldPreserve(false))

	policy = "unsupported-value"
	assert.False(t, policy.ShouldPreserve(true))
	assert.False(t, policy.ShouldPreserve(false))
}
