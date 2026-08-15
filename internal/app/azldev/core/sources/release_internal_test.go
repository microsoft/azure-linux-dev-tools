// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const testSourcesDir = "/sources"

func newTestPreparer(memFS afero.Fs) *sourcePreparerImpl {
	return &sourcePreparerImpl{
		fs: memFS,
	}
}

func writeTestSpec(t *testing.T, memFS afero.Fs, name, release string) {
	t.Helper()

	writeTestSpecContent(t, memFS, name,
		"Name: "+name+"\nVersion: 1.0.0\nRelease: "+release+"\nSummary: Test\nLicense: MIT\n")
}

func writeTestSpecContent(t *testing.T, memFS afero.Fs, name, content string) {
	t.Helper()

	specDir := filepath.Join(testSourcesDir, name)
	require.NoError(t, fileutils.MkdirAll(memFS, specDir))

	specPath := filepath.Join(specDir, name+".spec")

	require.NoError(t, fileutils.WriteFile(memFS, specPath, []byte(content), fileperms.PublicFile))
}

func mockComponent(
	ctrl *gomock.Controller, name string, config *projectconfig.ComponentConfig,
) *components_testutils.MockComponent {
	comp := components_testutils.NewMockComponent(ctrl)
	comp.EXPECT().GetName().AnyTimes().Return(name)
	comp.EXPECT().GetConfig().AnyTimes().Return(config)

	return comp
}

func TestTryBumpStaticRelease_ManualSkips(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	comp := mockComponent(ctrl, "kernel", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationManual,
		},
	})

	// No spec file needed — should skip before reading anything.
	err := preparer.tryBumpStaticRelease(comp, testSourcesDir, 3)
	require.NoError(t, err)
}

func TestTryBumpStaticRelease_AutoreleaseSkips(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeTestSpec(t, memFS, "test-pkg", "%autorelease")

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3)
	require.NoError(t, err)
}

func TestTryBumpStaticRelease_StaticBumps(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeTestSpec(t, memFS, "test-pkg", "1%{?dist}")

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3)
	require.NoError(t, err)

	// Verify the spec was updated.
	specPath := filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec")
	content, err := fileutils.ReadFile(memFS, specPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Release: 4%{?dist}")
}

func TestTryBumpStaticRelease_StaticBumpsNonConditionalDist(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeTestSpec(t, memFS, "test-pkg", "1%{dist}")

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3)
	require.NoError(t, err)

	specPath := filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec")
	content, err := fileutils.ReadFile(memFS, specPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Release: 4%{dist}")
}

func TestTryBumpStaticRelease_NonStandardErrorsWithoutManual(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeTestSpec(t, memFS, "kernel", "%{pkg_release}")

	comp := mockComponent(ctrl, "kernel", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "kernel"), 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be auto-bumped")
	assert.Contains(t, err.Error(), "release.calculation")
}

func TestTryBumpStaticRelease_NonStandardSucceedsWithManual(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeTestSpec(t, memFS, "kernel", "%{pkg_release}")

	comp := mockComponent(ctrl, "kernel", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationManual,
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "kernel"), 3)
	require.NoError(t, err)
}

func TestTryBumpStaticRelease_ExplicitAutoreleaseSkips(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	// Spec has a static release, but config says autorelease — should skip.
	comp := mockComponent(ctrl, "gvisor", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAutorelease,
		},
	})

	// No spec file needed — should skip before reading anything.
	err := preparer.tryBumpStaticRelease(comp, testSourcesDir, 3)
	require.NoError(t, err)
}

func TestTryBumpStaticRelease_ExplicitStaticBumps(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeTestSpec(t, memFS, "test-pkg", "1%{?dist}")

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationStatic,
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3)
	require.NoError(t, err)

	// Verify the spec was updated.
	specPath := filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec")
	content, err := fileutils.ReadFile(memFS, specPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Release: 4%{?dist}")
}

func TestTryBumpStaticRelease_ExplicitStaticErrorsOnAutorelease(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	// Spec uses %autorelease, but config says static — should error.
	writeTestSpec(t, memFS, "test-pkg", "%autorelease")

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationStatic,
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `release.calculation = "autorelease"`)
}

func TestBumpStaticRelease(t *testing.T) {
	for _, testCase := range []struct {
		name, value string
		commits     int
		expected    string
		wantErr     bool
	}{
		{"simple integer", "1", 3, "4", false},
		{"with conditional dist tag", "1%{?dist}", 2, "3%{?dist}", false},
		{"non-conditional dist tag", "1%{dist}", 2, "3%{dist}", false},
		{"larger base", "10%{?dist}", 5, "15%{?dist}", false},
		{"single commit", "1%{?dist}", 1, "2%{?dist}", false},
		{"preserves leading zero width", "007%{?dist}", 3, "010%{?dist}", false},
		{"no leading int", "%{?dist}", 1, "", true},
		{"empty string", "", 1, "", true},
		{"other macros", "17%{someothermacro}%{?dist}", 3, "", true},
		{"macro before dist", "0%{rc_subver}%{?dist}", 1, "", true},
		{"dotted with beta suffix", "1.39.b1%{?dist}", 3, "", true},
		{"dotted simple", "1.2%{?dist}", 2, "", true},
		{"dotted no suffix", "1.10", 5, "", true},
		{"dotted zero prefix", "0.1", 1, "", true},
		{"trailing dot before dist", "1.%{?dist}", 1, "", true},
		{"trailing dot no suffix", "1.", 1, "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := bumpStaticRelease(testCase.value, testCase.commits)
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, result)
			}
		})
	}
}

func TestReadAndBumpRelease_PreservesOperationalError(t *testing.T) {
	ctrl := gomock.NewController(t)
	preparer := newTestPreparer(afero.NewMemMapFs())
	comp := mockComponent(ctrl, "missing", &projectconfig.ComponentConfig{})

	err := preparer.readAndBumpRelease(comp, testSourcesDir, 1, false)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "cannot be auto-bumped")
}

func TestTryBumpStaticRelease_ConfiguredReleaseTagCounter(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeTestSpecContent(t, memFS, "test-pkg", `Name: test-pkg
Version: 1.0.0
Release: 1%{?dist}
%if 0
Release: 0.007.gitdead%{?dist}
%endif
Summary: Test
License: MIT
`)

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationStatic,
			Counter: &projectconfig.ReleaseCounter{
				Source: projectconfig.ReleaseCounterSourceReleaseTag,
				Regex:  `^0\.([0-9]+)(?:\.git.*)$`,
			},
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3)
	require.NoError(t, err)

	specPath := filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec")
	content, err := fileutils.ReadFile(memFS, specPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Release: 1%{?dist}")
	assert.Contains(t, string(content), "Release: 0.010.gitdead%{?dist}")
}

func TestTryBumpStaticRelease_ConfiguredReleaseTagCounterRequiresOneMatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeTestSpecContent(t, memFS, "test-pkg", `Name: test-pkg
Version: 1.0.0
Release: 0.001.gitfirst%{?dist}
%if 0
Release: 0.002.gitsecond%{?dist}
%endif
Summary: Test
License: MIT
`)

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
			Counter: &projectconfig.ReleaseCounter{
				Source: projectconfig.ReleaseCounterSourceReleaseTag,
				Regex:  `^0\.([0-9]+)(?:\.git.*)$`,
			},
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matched 2 main-package Release tags; expected exactly one")
}

func TestTryBumpStaticRelease_ConfiguredSpecMacroCounter(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeTestSpecContent(t, memFS, "test-pkg", `%global baserelease 007
Name: test-pkg
Version: 1.0.0
Release: %{baserelease}%{?dist}
Summary: Test
License: MIT
`)

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationStatic,
			Counter: &projectconfig.ReleaseCounter{
				Source:    projectconfig.ReleaseCounterSourceSpecMacro,
				Directive: "global",
				Name:      "baserelease",
			},
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3)
	require.NoError(t, err)

	specPath := filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec")
	content, err := fileutils.ReadFile(memFS, specPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "%global baserelease 010")
}

func TestTryBumpStaticRelease_ConfiguredSpecMacroCounterRejectsExpression(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeTestSpecContent(t, memFS, "test-pkg", `%global baserelease %{otherrelease}
Name: test-pkg
Version: 1.0.0
Release: %{baserelease}%{?dist}
Summary: Test
License: MIT
`)

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
			Counter: &projectconfig.ReleaseCounter{
				Source:    projectconfig.ReleaseCounterSourceSpecMacro,
				Directive: "global",
				Name:      "baserelease",
			},
		},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must define only a bare integer")
}
