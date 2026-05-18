// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolveComponents is a small helper to drive the component resolver in
// internal tests so we can call buildSpecQueryInputs with realistic inputs.
func resolveComponents(t *testing.T, testEnv *testutils.TestEnv, names ...string) []components.Component {
	t.Helper()

	resolver := components.NewResolver(testEnv.Env)

	comps, err := resolver.FindComponents(&components.ComponentFilter{
		ComponentNamePatterns: names,
	})
	require.NoError(t, err)

	return comps.Components()
}

func TestBuildSpecQueryInputs_Happy(t *testing.T) {
	const (
		componentName    = "curl"
		renderedSpecsDir = "/project/specs"
	)

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Project.RenderedSpecsDir = renderedSpecsDir
	testEnv.Config.Components[componentName] = projectconfig.ComponentConfig{
		Name: componentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/project/curl.spec",
		},
		Build: projectconfig.ComponentBuildConfig{
			With:    []string{"foo"},
			Without: []string{"bar"},
			Defines: map[string]string{"key": "value"},
		},
	}

	// Spec source (for resolver) and rendered spec (for our path).
	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), "/project/curl.spec", []byte(""), fileperms.PublicFile,
	))

	renderedDir := filepath.Join(renderedSpecsDir, "c", componentName)
	require.NoError(t, fileutils.MkdirAll(testEnv.FS(), renderedDir))
	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), filepath.Join(renderedDir, componentName+".spec"),
		[]byte(""), fileperms.PublicFile,
	))

	resolved := resolveComponents(t, testEnv, componentName)
	require.Len(t, resolved, 1)

	inputs, skipped, err := buildSpecQueryInputs(testEnv.Env, resolved, renderedSpecsDir)
	require.NoError(t, err)
	assert.Zero(t, skipped)
	require.Len(t, inputs, 1)

	assert.Equal(t, componentName, inputs[0].Name)
	assert.Equal(t, filepath.Join("c", componentName, componentName+".spec"), inputs[0].SpecRelPath)
	assert.Equal(t, []string{"foo"}, inputs[0].With)
	assert.Equal(t, []string{"bar"}, inputs[0].Without)
	assert.Equal(t, map[string]string{"key": "value"}, inputs[0].Defines)
}

func TestBuildSpecQueryInputs_SkipsRenderFailedMarker(t *testing.T) {
	const (
		componentName    = "curl"
		renderedSpecsDir = "/project/specs"
	)

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Project.RenderedSpecsDir = renderedSpecsDir
	testEnv.Config.Components[componentName] = projectconfig.ComponentConfig{
		Name: componentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/project/curl.spec",
		},
	}

	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), "/project/curl.spec", []byte(""), fileperms.PublicFile,
	))

	renderedDir := filepath.Join(renderedSpecsDir, "c", componentName)
	require.NoError(t, fileutils.MkdirAll(testEnv.FS(), renderedDir))
	// Spec file exists, but so does the marker — the marker wins.
	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), filepath.Join(renderedDir, componentName+".spec"),
		[]byte(""), fileperms.PublicFile,
	))
	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), filepath.Join(renderedDir, renderErrorMarkerFile),
		[]byte("RENDER FAILED"), fileperms.PublicFile,
	))

	resolved := resolveComponents(t, testEnv, componentName)

	inputs, skipped, err := buildSpecQueryInputs(testEnv.Env, resolved, renderedSpecsDir)
	require.NoError(t, err)
	assert.Empty(t, inputs)
	assert.Equal(t, 1, skipped)
}

func TestBuildSpecQueryInputs_SkipsMissingSpec(t *testing.T) {
	const (
		componentName    = "curl"
		renderedSpecsDir = "/project/specs"
	)

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Project.RenderedSpecsDir = renderedSpecsDir
	testEnv.Config.Components[componentName] = projectconfig.ComponentConfig{
		Name: componentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/project/curl.spec",
		},
	}

	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), "/project/curl.spec", []byte(""), fileperms.PublicFile,
	))

	// Rendered dir exists but the .spec inside it does not.
	require.NoError(t, fileutils.MkdirAll(
		testEnv.FS(), filepath.Join(renderedSpecsDir, "c", componentName),
	))

	resolved := resolveComponents(t, testEnv, componentName)

	inputs, skipped, err := buildSpecQueryInputs(testEnv.Env, resolved, renderedSpecsDir)
	require.NoError(t, err)
	assert.Empty(t, inputs)
	assert.Equal(t, 1, skipped)
}
