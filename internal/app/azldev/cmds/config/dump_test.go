// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config_test

import (
	"encoding/json"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/config"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDumpConfig(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Project.RenderedSpecsDir = "/project/specs"
	testEnv.Config.DefaultComponentConfig.Build.With = []string{"project-default"}
	testEnv.Config.Components["example"] = projectconfig.ComponentConfig{
		Name:         "example",
		OverlayFiles: []string{"/project/components/example.overlay.toml"},
		SourceFiles: []projectconfig.SourceFileReference{{
			Filename: "generated.tar.gz",
			Origin: projectconfig.Origin{
				Type:   projectconfig.OriginTypeCustom,
				Script: "/project/components/generate.sh",
			},
		}},
	}
	testEnv.Config.ComponentGroups["example-group"] = projectconfig.ComponentGroupConfig{
		Components: []string{"example"},
		DefaultComponentConfig: projectconfig.ComponentConfig{
			Build: projectconfig.ComponentBuildConfig{With: []string{"group-default"}},
		},
	}
	testEnv.Config.GroupsByComponent["example"] = []string{"example-group"}
	distro := testEnv.Config.Distros["test-distro"]
	distroVersion := distro.Versions["1.0"]
	distroVersion.DefaultComponentConfig.Build.With = []string{"distro-default"}
	distro.Versions["1.0"] = distroVersion
	testEnv.Config.Distros["test-distro"] = distro
	testEnv.WriteDefaultLock(t, "example")
	require.NoError(t, fileutils.WriteFile(
		testEnv.TestFS,
		"/project/components/example.overlay.toml",
		[]byte(`[metadata]
category = "azl-branding-policy"
upstream-status = "inapplicable"

[[overlays]]
type = "spec-set-tag"
tag = "Vendor"
value = "Microsoft"
`),
		fileperms.PrivateFile,
	))

	testCases := []struct {
		name      string
		dump      func() (string, error)
		unmarshal func([]byte, any) error
	}{
		{
			name: "TOML",
			dump: func() (string, error) {
				return config.DumpConfig(testEnv.Env, config.ConfigDumpFormatTOML)
			},
			unmarshal: toml.Unmarshal,
		},
		{
			name: "JSON",
			dump: func() (string, error) {
				return config.DumpConfig(testEnv.Env, config.ConfigDumpFormatJSON)
			},
			unmarshal: json.Unmarshal,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			configText, err := testCase.dump()
			require.NoError(t, err)

			var dumped projectconfig.ProjectConfig
			require.NoError(t, testCase.unmarshal([]byte(configText), &dumped))

			dumpedComponent := dumped.Components["example"]
			assert.Equal(t,
				[]string{"distro-default", "project-default", "group-default"},
				dumpedComponent.Build.With,
			)
			require.Len(t, dumpedComponent.Overlays, 1)
			assert.Equal(t, "Vendor", dumpedComponent.Overlays[0].Tag)
			assert.Empty(t, dumpedComponent.OverlayFiles)
			assert.Nil(t, dumpedComponent.Locked)
			assert.Empty(t, dumpedComponent.RenderedSpecDir)
			assert.Equal(t, []string{"project-default"}, dumped.DefaultComponentConfig.Build.With)
			assert.Equal(t, []string{"group-default"},
				dumped.ComponentGroups["example-group"].DefaultComponentConfig.Build.With)
			assert.Equal(t, "generate.sh", dumpedComponent.SourceFiles[0].Origin.Script)
		})
	}

	originalComponent := testEnv.Config.Components["example"]
	assert.Equal(t, []string{"/project/components/example.overlay.toml"}, originalComponent.OverlayFiles)
	assert.Empty(t, originalComponent.Overlays)
	assert.Nil(t, originalComponent.Locked)
	assert.Empty(t, originalComponent.RenderedSpecDir)
	assert.Equal(t, "/project/components/generate.sh", originalComponent.SourceFiles[0].Origin.Script)
}

func TestDumpConfigIncludesSpecDiscoveredComponents(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.ComponentGroups["local-specs"] = projectconfig.ComponentGroupConfig{
		SpecPathPatterns: []string{"/project/specs/**/*.spec"},
	}
	require.NoError(t, fileutils.WriteFile(
		testEnv.TestFS,
		"/project/specs/example/example.spec",
		[]byte("Name: example\n"),
		fileperms.PrivateFile,
	))

	configText, err := config.DumpConfig(testEnv.Env, config.ConfigDumpFormatJSON)
	require.NoError(t, err)

	var dumped projectconfig.ProjectConfig
	require.NoError(t, json.Unmarshal([]byte(configText), &dumped))

	dumpedComponent, found := dumped.Components["example"]
	require.True(t, found)
	assert.Equal(t, projectconfig.SpecSourceTypeLocal, dumpedComponent.Spec.SourceType)
	assert.Equal(t, "/project/specs/example/example.spec", dumpedComponent.Spec.Path)
}
