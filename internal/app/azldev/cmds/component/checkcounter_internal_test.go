// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const testRenderedSpecsDir = "/specs"

const testNSSRenderedDir = "/specs/n/nss"

// TestCheckEVRCounterSelections covers which components a release-only rebuild would bump. The
// gate deliberately covers every counter-bumped component, not only those with an explicit
// 'release.counter'.
func TestCheckEVRCounterSelections(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()

	explicitCounter := projectconfig.ReleaseCounter{
		Source:    projectconfig.ReleaseCounterSourceSpecMacro,
		Directive: "global",
		Name:      "baserelease",
	}

	writeSelectionSpec(t, memFS, "auto-static-release", "Release: 7%{?dist}\n")
	writeSelectionSpec(t, memFS, "auto-autorelease", "Release: %autorelease\n")
	writeSelectionSpec(t, memFS, "auto-nonstandard", "Release: 0.7.gitabc%{?dist}\n")

	comps := []components.Component{
		newCounterMockComp(ctrl, memFS, "static-default", projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationStatic,
		}),
		newCounterMockComp(ctrl, memFS, "explicit-counter", projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
			Counter:     &explicitCounter,
		}),
		newCounterMockComp(ctrl, memFS, "manual", projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationManual,
		}),
		newCounterMockComp(ctrl, memFS, "rpmautospec", projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAutorelease,
		}),
		newCounterMockComp(ctrl, memFS, "auto-static-release", projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		}),
		newCounterMockComp(ctrl, memFS, "auto-autorelease", projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		}),
		newCounterMockComp(ctrl, memFS, "auto-nonstandard", projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		}),
		newCounterMockComp(ctrl, memFS, "auto-unrendered", projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		}),
	}

	selections := checkEVRCounterSelections(memFS, comps)

	byName := make(map[string]checkEVRCounterSelection, len(selections))
	for _, selection := range selections {
		byName[selection.Name] = selection
	}

	assert.NotContains(t, byName, "manual", "a manual Release is owned by the maintainer")
	assert.NotContains(t, byName, "rpmautospec", "an explicit autorelease Release is owned by rpmautospec")
	assert.NotContains(t, byName, "auto-autorelease", "an auto component using %autorelease is never bumped")

	require.Contains(t, byName, "static-default")
	assert.Equal(t, sources.DefaultReleaseCounter(), byName["static-default"].Counter)

	require.Contains(t, byName, "explicit-counter")
	assert.Equal(t, explicitCounter, byName["explicit-counter"].Counter)

	require.Contains(t, byName, "auto-static-release",
		"an auto component with a static Release falls back to the built-in counter at render time")
	assert.Equal(t, sources.DefaultReleaseCounter(), byName["auto-static-release"].Counter)

	require.Contains(t, byName, "auto-nonstandard",
		"an auto component whose Release cannot be bumped must be reported, not skipped")

	require.Contains(t, byName, "auto-unrendered")
	assert.Contains(t, byName["auto-unrendered"].SelectionError, "reading Release tag",
		"a component whose rendered spec cannot be read must be reported rather than assumed unmanaged")
}

func TestCheckEVRCounterSelections_SkipsComponentsWithoutConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	comp := components_testutils.NewMockComponent(ctrl)
	comp.EXPECT().GetName().AnyTimes().Return("ghost")
	comp.EXPECT().GetConfig().AnyTimes().Return(nil)

	assert.Empty(t, checkEVRCounterSelections(afero.NewMemMapFs(), []components.Component{comp}))
}

func TestAutoReleaseUsesBuiltInCounter(t *testing.T) {
	memFS := afero.NewMemMapFs()
	writeSelectionSpec(t, memFS, "nss", "Release: 3%{?dist}\n")
	writeSelectionSpec(t, memFS, "curl", "Release: %{autorelease}\n")

	bumped, err := autoReleaseUsesBuiltInCounter(memFS, "nss", renderedConfig("nss"))
	require.NoError(t, err)
	assert.True(t, bumped)

	bumped, err = autoReleaseUsesBuiltInCounter(memFS, "curl", renderedConfig("curl"))
	require.NoError(t, err)
	assert.False(t, bumped)

	_, err = autoReleaseUsesBuiltInCounter(memFS, "zlib", &projectconfig.ComponentConfig{Name: "zlib"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rendered spec directory")
}

func TestCounterForReleaseCheck(t *testing.T) {
	explicit := projectconfig.ReleaseCounter{
		Source:    projectconfig.ReleaseCounterSourceSpecMacro,
		Directive: "global",
		Name:      "baserelease",
	}

	assert.Equal(t, explicit, counterForReleaseCheck(projectconfig.ReleaseConfig{Counter: &explicit}))
	assert.Equal(t, sources.DefaultReleaseCounter(), counterForReleaseCheck(projectconfig.ReleaseConfig{}))
}

func renderedConfig(componentName string) *projectconfig.ComponentConfig {
	return &projectconfig.ComponentConfig{
		Name:            componentName,
		RenderedSpecDir: filepath.Join(testRenderedSpecsDir, componentName[:1], componentName),
	}
}

func newCounterMockComp(
	ctrl *gomock.Controller,
	_ opctx.FS,
	name string,
	release projectconfig.ReleaseConfig,
) *components_testutils.MockComponent {
	config := renderedConfig(name)
	config.Release = release

	comp := components_testutils.NewMockComponent(ctrl)
	comp.EXPECT().GetName().AnyTimes().Return(name)
	comp.EXPECT().GetConfig().AnyTimes().Return(config)

	return comp
}

func writeSelectionSpec(t *testing.T, fs opctx.FS, componentName string, releaseLines string) {
	t.Helper()

	renderedDir := filepath.Join(testRenderedSpecsDir, componentName[:1], componentName)
	require.NoError(t, fileutils.MkdirAll(fs, renderedDir))

	content := "Name: " + componentName + "\nVersion: 1\n" + releaseLines + "Summary: Test\nLicense: MIT\n"
	require.NoError(t, fileutils.WriteFile(
		fs, filepath.Join(renderedDir, componentName+".spec"), []byte(content), fileperms.PublicFile))
}
