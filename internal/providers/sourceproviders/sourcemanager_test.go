// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs/specs_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const (
	testDestDir       = "/output"
	testSourceTarball = "source.tar.gz"
)

// testDefaultDistro returns a [sourceproviders.ResolvedDistro] matching the test
// environment's default distro configuration.
func testDefaultDistro() sourceproviders.ResolvedDistro {
	return sourceproviders.ResolvedDistro{
		Ref: projectconfig.DistroReference{Name: "test-distro", Version: "1.0"},
		Definition: projectconfig.DistroDefinition{
			LookasideBaseURI: "https://example.com/lookaside/$pkg/$filename/$hashtype/$hash/$filename",
			DistGitBaseURI:   "https://example.com/upstream/$pkg.git",
			Versions: map[string]projectconfig.DistroVersionDefinition{
				"1.0": {DistGitBranch: "main"},
			},
		},
		Version: projectconfig.DistroVersionDefinition{
			DistGitBranch: "main",
		},
	}
}

func TestSourceManager_NewSourceManager_NilEnv(t *testing.T) {
	t.Helper()

	sourceManager, err := sourceproviders.NewSourceManager(nil, sourceproviders.ResolvedDistro{})
	require.Error(t, err)
	require.Nil(t, sourceManager)
	require.EqualError(t, err, "environment cannot be nil")
}

func TestSourceManager_NewSourceManager_Success(t *testing.T) {
	t.Helper()
	env := testutils.NewTestEnv(t)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)
	require.NotNil(t, sourceManager)
}

func TestSourceManager_FetchComponent_EmptyComponentName(t *testing.T) {
	t.Helper()

	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	component.EXPECT().GetName().Return("")

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
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

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)

	err = sourceManager.FetchComponent(t.Context(), component, "/output")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to fetch local component")
}

func TestSourceManager_FetchComponent_UpstreamComponent_NoProviders(t *testing.T) {
	t.Helper()

	// In the new architecture, distro is resolved before creating the source manager.
	// A nonexistent distro is caught at ResolveDistro time, not at FetchComponent time.
	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType:     projectconfig.SpecSourceTypeUpstream,
			UpstreamDistro: projectconfig.DistroReference{Name: "nonexistent-distro", Version: "1.0"},
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

	_, err := sourceproviders.ResolveDistro(env.Env, component)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to resolve distro")
}

func TestSourceManager_ResolveDistro_NoDistroConfigured(t *testing.T) {
	t.Helper()

	// When no distro is configured (neither on the component nor on the project),
	// ResolveDistro should return an actionable error.
	env := testutils.NewTestEnv(t)

	// Clear the project's default distro so there is no fallback.
	env.Config.Project.DefaultDistro = projectconfig.DistroReference{}

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

	_, err := sourceproviders.ResolveDistro(env.Env, component)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no distro configured")
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

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
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

	// Make git commands fail so all providers return errors.
	env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		return errors.New("simulated git failure")
	}

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)

	err = sourceManager.FetchComponent(t.Context(), component, "/output")
	require.Error(t, err)
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

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)

	// Empty destination path should be caught by the provider
	err = sourceManager.FetchComponent(t.Context(), component, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "destination path cannot be empty")
}

func TestSourceManager_FetchFiles_NoSourceFiles(t *testing.T) {
	t.Helper()

	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	componentConfig := &projectconfig.ComponentConfig{}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)

	err = sourceManager.FetchFiles(t.Context(), component, testDestDir)
	require.NoError(t, err)
}

func TestSourceManager_FetchFiles_ExistingFile(t *testing.T) {
	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	require.NoError(t, env.TestFS.MkdirAll(testDestDir, fileperms.PrivateDir))

	destPath := filepath.Join(testDestDir, testSourceTarball)
	require.NoError(t, fileutils.WriteFile(env.TestFS, destPath, []byte("existing content"), fileperms.PrivateFile))

	componentConfig := &projectconfig.ComponentConfig{
		SourceFiles: []projectconfig.SourceFileReference{{
			Filename: testSourceTarball,
			Hash:     "abc123",
			HashType: fileutils.HashTypeSHA256,
		}},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)

	err = sourceManager.FetchFiles(t.Context(), component, testDestDir)
	require.NoError(t, err)
}

func TestSourceManager_FetchFiles_Errors(t *testing.T) {
	tests := []struct {
		name           string
		disableOrigins bool
		sourceFiles    []projectconfig.SourceFileReference
		expectedError  string
	}{
		{
			name:           "disable-origins rejects lookaside failure",
			disableOrigins: true,
			sourceFiles: []projectconfig.SourceFileReference{{
				Filename: testSourceTarball,
				Hash:     "abc123",
				HashType: "SHA256",
			}},
			expectedError: "disable-origins is enabled in the distro config",
		},
		{
			name: "no origin configured",
			sourceFiles: []projectconfig.SourceFileReference{{
				Filename: testSourceTarball,
			}},
			expectedError: "no origin configured",
		},
		{
			name: "invalid filename rejected",
			sourceFiles: []projectconfig.SourceFileReference{{
				Filename: "../escape.tar.gz",
				Origin: projectconfig.Origin{
					Type: projectconfig.OriginTypeURI,
					Uri:  "https://example.com/escape.tar.gz",
				},
			}},
			expectedError: "invalid source file reference",
		},
		{
			name: "origin fallback fails without URI",
			sourceFiles: []projectconfig.SourceFileReference{{
				Filename: testSourceTarball,
				Hash:     "abc123",
				HashType: "SHA256",
				Origin: projectconfig.Origin{
					Type: projectconfig.OriginTypeURI,
				},
			}},
			expectedError: "no URI configured",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)

			ctrl := gomock.NewController(t)
			component := components_testutils.NewMockComponent(ctrl)

			componentConfig := &projectconfig.ComponentConfig{
				SourceFiles: testCase.sourceFiles,
			}

			component.EXPECT().GetName().AnyTimes().Return("test-component")
			component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

			distro := testDefaultDistro()
			distro.Definition.DisableOrigins = testCase.disableOrigins

			sourceManager, err := sourceproviders.NewSourceManager(env.Env, distro)
			require.NoError(t, err)

			err = sourceManager.FetchFiles(t.Context(), component, testDestDir)
			require.Error(t, err)
			require.Contains(t, err.Error(), testCase.expectedError)
		})
	}
}

func TestSourceManager_ResolveSourceIdentity_EmptyComponentName(t *testing.T) {
	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	component.EXPECT().GetName().Return("")

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)

	_, err = sourceManager.ResolveSourceIdentity(t.Context(), component)
	require.Error(t, err)
	require.Contains(t, err.Error(), "component name is empty")
}

func TestSourceManager_ResolveSourceIdentity_LocalNoSpecPath(t *testing.T) {
	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)

	_, err = sourceManager.ResolveSourceIdentity(t.Context(), component)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no spec path configured")
}

func TestSourceManager_ResolveSourceIdentity_LocalSuccess(t *testing.T) {
	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	specContent := []byte("Name: test\nVersion: 1.0\n")
	require.NoError(t, fileutils.WriteFile(env.TestFS, "/specs/test.spec", specContent, fileperms.PrivateFile))

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/specs/test.spec",
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)

	identity, err := sourceManager.ResolveSourceIdentity(t.Context(), component)
	require.NoError(t, err)
	assert.Contains(t, identity, "sha256:")
}

func TestSourceManager_ResolveSourceIdentity_UpstreamNoProviders(t *testing.T) {
	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	// Clear the distro so no upstream providers are registered.
	emptyDistro := sourceproviders.ResolvedDistro{}

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeUpstream,
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, emptyDistro)
	require.NoError(t, err)

	_, err = sourceManager.ResolveSourceIdentity(t.Context(), component)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no upstream providers configured")
}

func TestSourceManager_ResolveSourceIdentity_UpstreamAllProvidersFail(t *testing.T) {
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

	// Make git commands fail so all providers return errors.
	env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		return errors.New("simulated git failure")
	}

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)

	_, err = sourceManager.ResolveSourceIdentity(t.Context(), component)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to resolve source identity")
}

func TestSourceManager_ResolveSourceIdentity_UnknownSourceType(t *testing.T) {
	env := testutils.NewTestEnv(t)
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)

	componentConfig := &projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: "unknown-type",
		},
	}

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)

	sourceManager, err := sourceproviders.NewSourceManager(env.Env, testDefaultDistro())
	require.NoError(t, err)

	_, err = sourceManager.ResolveSourceIdentity(t.Context(), component)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no identity provider for source type")
}
