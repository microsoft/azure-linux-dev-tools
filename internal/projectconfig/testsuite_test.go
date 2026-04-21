// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageCapabilities_EnabledNames(t *testing.T) {
	t.Run("all enabled", func(t *testing.T) {
		caps := projectconfig.ImageCapabilities{
			MachineBootable:          lo.ToPtr(true),
			Container:                lo.ToPtr(true),
			Systemd:                  lo.ToPtr(true),
			RuntimePackageManagement: lo.ToPtr(true),
		}
		assert.Equal(t, []string{
			"machine-bootable", "container", "systemd", "runtime-package-management",
		}, caps.EnabledNames())
	})

	t.Run("partial enabled", func(t *testing.T) {
		caps := projectconfig.ImageCapabilities{
			Container: lo.ToPtr(true),
			Systemd:   lo.ToPtr(true),
		}
		assert.Equal(t, []string{"container", "systemd"}, caps.EnabledNames())
	})

	t.Run("explicitly false excluded", func(t *testing.T) {
		caps := projectconfig.ImageCapabilities{
			MachineBootable: lo.ToPtr(false),
			Container:       lo.ToPtr(true),
		}
		assert.Equal(t, []string{"container"}, caps.EnabledNames())
	})

	t.Run("all nil returns nil", func(t *testing.T) {
		caps := projectconfig.ImageCapabilities{}
		assert.Nil(t, caps.EnabledNames())
	})
}

func TestImageConfig_TestNames(t *testing.T) {
	t.Run("with tests", func(t *testing.T) {
		img := projectconfig.ImageConfig{
			Tests: projectconfig.ImageTestsConfig{
				TestSuites: []projectconfig.TestSuiteRef{
					{Name: "smoke"},
					{Name: "integration"},
				},
			},
		}
		assert.Equal(t, []string{"smoke", "integration"}, img.TestNames())
	})

	t.Run("no tests returns empty", func(t *testing.T) {
		img := projectconfig.ImageConfig{}
		assert.Empty(t, img.TestNames())
	})
}

func TestValidateTestSuiteReferences(t *testing.T) {
	t.Run("valid references", func(t *testing.T) {
		cfg := projectconfig.ProjectConfig{
			Images: map[string]projectconfig.ImageConfig{
				"myimage": {
					Name:  "myimage",
					Tests: projectconfig.ImageTestsConfig{TestSuites: []projectconfig.TestSuiteRef{{Name: "smoke"}}},
				},
			},
			TestSuites: map[string]projectconfig.TestSuiteConfig{
				"smoke": {
					Name: "smoke",
				},
			},
			Components:        make(map[string]projectconfig.ComponentConfig),
			ComponentGroups:   make(map[string]projectconfig.ComponentGroupConfig),
			Distros:           make(map[string]projectconfig.DistroDefinition),
			GroupsByComponent: make(map[string][]string),
			PackageGroups:     make(map[string]projectconfig.PackageGroupConfig),
		}
		assert.NoError(t, cfg.Validate())
	})

	t.Run("undefined test reference", func(t *testing.T) {
		cfg := projectconfig.ProjectConfig{
			Images: map[string]projectconfig.ImageConfig{
				"myimage": {
					Name:  "myimage",
					Tests: projectconfig.ImageTestsConfig{TestSuites: []projectconfig.TestSuiteRef{{Name: "nonexistent"}}},
				},
			},
			TestSuites:        make(map[string]projectconfig.TestSuiteConfig),
			Components:        make(map[string]projectconfig.ComponentConfig),
			ComponentGroups:   make(map[string]projectconfig.ComponentGroupConfig),
			Distros:           make(map[string]projectconfig.DistroDefinition),
			GroupsByComponent: make(map[string][]string),
			PackageGroups:     make(map[string]projectconfig.PackageGroupConfig),
		}
		err := cfg.Validate()
		require.Error(t, err)
		require.ErrorIs(t, err, projectconfig.ErrUndefinedTestSuite)
		assert.Contains(t, err.Error(), "nonexistent")
	})

	t.Run("image with no tests is valid", func(t *testing.T) {
		cfg := projectconfig.ProjectConfig{
			Images: map[string]projectconfig.ImageConfig{
				"myimage": {Name: "myimage"},
			},
			TestSuites:        make(map[string]projectconfig.TestSuiteConfig),
			Components:        make(map[string]projectconfig.ComponentConfig),
			ComponentGroups:   make(map[string]projectconfig.ComponentGroupConfig),
			Distros:           make(map[string]projectconfig.DistroDefinition),
			GroupsByComponent: make(map[string][]string),
			PackageGroups:     make(map[string]projectconfig.PackageGroupConfig),
		}
		assert.NoError(t, cfg.Validate())
	})
}
