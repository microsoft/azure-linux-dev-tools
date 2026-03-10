// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders_test

import (
	"errors"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs/specs_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestSourceManager_NewSourceManager_NilEnv(t *testing.T) {
	t.Helper()

	sourceManager, err := sourceproviders.NewSourceManager(nil)
	require.Error(t, err)
	require.Nil(t, sourceManager)
	require.EqualError(t, err, "environment cannot be nil")
}

func TestSourceManager_NewSourceManager_Success(t *testing.T) {
	t.Helper()
	env := testutils.NewTestEnv(t)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env)
	require.NoError(t, err)
	require.NotNil(t, sourceManager)
}

func TestSourceManager_FetchComponent_EmptyComponentName(t *testing.T) {
	t.Helper()

	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	component.EXPECT().GetName().Return("")

	sourceManager, err := sourceproviders.NewSourceManager(env.Env)
	require.NoError(t, err)

	err = sourceManager.FetchComponent(t.Context(), component, "/output")
	require.Error(t, err)
	require.Contains(t, err.Error(), "component name is empty")
}

func TestSourceManager_FetchComponent_LocalComponent_SpecError(t *testing.T) {
	t.Helper()

	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	spec := specs_testutils.NewMockComponentSpec(ctrl)

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)
	component.EXPECT().GetSpec().AnyTimes().Return(spec)

	// Spec returns error when GetPath is called
	spec.EXPECT().GetPath().Return("", errors.New("failed to get spec path"))

	sourceManager, err := sourceproviders.NewSourceManager(env.Env)
	require.NoError(t, err)

	err = sourceManager.FetchComponent(t.Context(), component, "/output")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to fetch local component")
}

func TestSourceManager_FetchComponent_UpstreamComponent_NoProviders(t *testing.T) {
	t.Helper()

	// Create an environment that will fail to create upstream providers
	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeUpstream,
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env)
	require.NoError(t, err)

	err = sourceManager.FetchComponent(t.Context(), component, "/output")
	require.Error(t, err)

	require.Contains(t, err.Error(), "failed to fetch upstream component")
}

func TestSourceManager_FetchComponent_LocalComponent_ProviderError(t *testing.T) {
	t.Helper()

	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	spec := specs_testutils.NewMockComponentSpec(ctrl)

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/nonexistent/path/test.spec",
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)
	component.EXPECT().GetSpec().AnyTimes().Return(spec)

	// Spec file doesn't exist
	spec.EXPECT().GetPath().Return("/nonexistent/path/test.spec", nil)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env)
	require.NoError(t, err)

	err = sourceManager.FetchComponent(t.Context(), component, "/output")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to fetch local component")
}

func TestSourceManager_FetchComponent_UpstreamComponent_AllProvidersFail(t *testing.T) {
	t.Helper()

	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeUpstream,
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env)
	require.NoError(t, err)

	err = sourceManager.FetchComponent(t.Context(), component, "/output")
	require.Error(t, err)
	// With the test environment now properly configured with an upstream distro,
	// providers are created and the fetch is attempted but fails during Git operations
	require.Contains(t, err.Error(), "failed to fetch upstream component")
}

func TestSourceManager_FetchComponent_EmptyDestPath(t *testing.T) {
	t.Helper()

	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	spec := specs_testutils.NewMockComponentSpec(ctrl)

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)
	component.EXPECT().GetSpec().AnyTimes().Return(spec)

	// Spec exists - use AnyTimes() since it may or may not be called depending on when the error occurs
	spec.EXPECT().GetPath().AnyTimes().Return("/test/test.spec", nil)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env)
	require.NoError(t, err)

	// Empty destination path should be caught by the provider
	err = sourceManager.FetchComponent(t.Context(), component, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "destination path cannot be empty")
}
