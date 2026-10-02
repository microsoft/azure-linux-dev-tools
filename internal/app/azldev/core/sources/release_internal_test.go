// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const testSourcesDir = "/sources"

func rpmdevReleaseTestPreparer(t *testing.T) (*sourcePreparerImpl, *testctx.TestCtx) {
	t.Helper()

	ctx := newBumpspecCtx()

	return &sourcePreparerImpl{fs: ctx.FS(), bumpspecCtx: ctx, bumpspecScratchDir: "/work/missing"}, ctx
}

func newTestPreparer(memFS afero.Fs) *sourcePreparerImpl {
	return &sourcePreparerImpl{fs: memFS}
}

type failingCommandCtx struct {
	*testctx.TestCtx
	t *testing.T
}

var _ opctx.Ctx = (*failingCommandCtx)(nil)

func (c *failingCommandCtx) Command(cmd *exec.Cmd) (opctx.Cmd, error) {
	c.t.Fatalf("default legacy release path must not construct command %#v", cmd.Args)

	return nil, errors.New("unexpected command construction")
}

func (c *failingCommandCtx) CommandInSearchPath(name string) bool {
	c.t.Fatalf("default legacy release path must not discover command %#q", name)

	return false
}

func writeTestSpec(t *testing.T, memFS afero.Fs, name, content string) string {
	t.Helper()

	specPath := filepath.Join(testSourcesDir, name, name+".spec")
	require.NoError(t, fileutils.MkdirAll(memFS, filepath.Dir(specPath)))
	require.NoError(t, fileutils.WriteFile(memFS, specPath, []byte(content), fileperms.PublicFile))

	return specPath
}

func writeStaticReleaseTestSpec(t *testing.T, memFS afero.Fs, name, release string) string {
	t.Helper()

	return writeTestSpec(t, memFS, name,
		"Name: "+name+"\nVersion: 1.0.0\nRelease: "+release+"\nSummary: Test\nLicense: MIT\n")
}

func mockReleaseComponent(
	ctrl *gomock.Controller, name string, calculation projectconfig.ReleaseCalculation,
) *components_testutils.MockComponent {
	comp := components_testutils.NewMockComponent(ctrl)
	comp.EXPECT().GetName().AnyTimes().Return(name)
	comp.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{Calculation: calculation},
		Build:   projectconfig.ComponentBuildConfig{Defines: map[string]string{"fixture": "1"}},
	})

	return comp
}

func mockComponent(
	ctrl *gomock.Controller, name string, config *projectconfig.ComponentConfig,
) *components_testutils.MockComponent {
	comp := components_testutils.NewMockComponent(ctrl)
	comp.EXPECT().GetName().AnyTimes().Return(name)
	comp.EXPECT().GetConfig().AnyTimes().Return(config)

	return comp
}

func testChanges() []FingerprintChange {
	return []FingerprintChange{
		{CommitMetadata: CommitMetadata{Hash: "first", Timestamp: 1, Message: "different"}},
		{CommitMetadata: CommitMetadata{Hash: "second", Timestamp: 2, Message: "metadata"}},
	}
}

func TestTryBumpRPMDevBumpspec_UsesEVRTransactionsAndFixedMetadata(t *testing.T) {
	ctrl := gomock.NewController(t)
	preparer, ctx := rpmdevReleaseTestPreparer(t)
	original := "Name: test-pkg\nVersion: 1.0\nRelease: 4%{?dist}\n%changelog\n"
	specPath := writeTestSpec(t, ctx.FS(), "test-pkg", original)
	comp := mockReleaseComponent(ctrl, "test-pkg", projectconfig.ReleaseCalculationAuto)
	queries := 0
	bumps := 0
	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		switch {
		case isEVRQuery(cmd):
			queries++
			_, _ = fmt.Fprint(cmd.Stdout, evrOutput("test-pkg", "0", strconv.Itoa(3+(queries+1)/2)))
		case cmd.Path == RPMDevBumpspecBinary:
			bumps++

			assert.Equal(t, "- rebuilt", cmd.Args[2])
			assert.Equal(t, "Mon Jan 06 2025", cmd.Args[6])
			content := stringMustRead(t, ctx, specPath)
			content = strings.Replace(content, fmt.Sprintf("Release: %d%%{?dist}", 3+bumps),
				fmt.Sprintf("Release: %d%%{?dist}", 4+bumps), 1)
			require.NoError(t, fileutils.WriteFile(ctx.FS(), specPath, []byte(content), fileperms.PublicFile))
		case isReleaseComparison(cmd):
			_, _ = fmt.Fprintln(cmd.Stdout, "-1")
		default:
			return fmt.Errorf("unexpected command: %#v", cmd.Args)
		}

		return nil
	}

	require.NoError(t, preparer.tryBumpRPMDevBumpspec(context.Background(), comp,
		filepath.Join(testSourcesDir, "test-pkg"), testChanges()))
	assert.Equal(t, 2, bumps)
	assert.Equal(t, 4, queries)
	assert.Contains(t, stringMustRead(t, ctx, specPath), "Release: 6%{?dist}")
	entries, err := afero.ReadDir(ctx.FS(), "/work/missing")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestTryBumpRPMDevBumpspec_RollsBackWholeSequence(t *testing.T) {
	ctrl := gomock.NewController(t)
	preparer, ctx := rpmdevReleaseTestPreparer(t)
	original := "Name: osbs-client\nVersion: 1.0\n%if 0\n%global release 23\n%else\n" +
		"%global release 6\n%endif\nRelease: %{release}\n"
	specPath := writeTestSpec(t, ctx.FS(), "osbs-client", original)
	comp := mockReleaseComponent(ctrl, "osbs-client", projectconfig.ReleaseCalculationAuto)
	queries := 0
	bumps := 0
	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		switch {
		case isEVRQuery(cmd):
			queries++
			_, _ = fmt.Fprint(cmd.Stdout, evrOutput("osbs-client", "0", "6"))
		case cmd.Path == RPMDevBumpspecBinary:
			bumps++

			content := stringMustRead(t, ctx, specPath)
			if bumps == 1 {
				return fileutils.WriteFile(ctx.FS(), specPath, []byte(strings.Replace(content, "release 23", "release 24", 1)),
					fileperms.PublicFile)
			}

			return fileutils.WriteFile(ctx.FS(), specPath, []byte(strings.Replace(content, "release 6", "release 7", 1)),
				fileperms.PublicFile)
		case isReleaseComparison(cmd):
			_, _ = fmt.Fprintln(cmd.Stdout, "0")
		}

		return nil
	}

	err := preparer.tryBumpRPMDevBumpspec(
		context.Background(), comp, filepath.Join(testSourcesDir, "osbs-client"), testChanges())
	require.ErrorIs(t, err, ErrRPMDevBumpspecNoMutation)
	assert.Equal(t, original, stringMustRead(t, ctx, specPath), "inactive release mutation must be rolled back")
	assert.Equal(t, 1, bumps)
}

func TestTryBumpStaticRelease_ManualSkips(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	comp := mockComponent(ctrl, "kernel", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{Calculation: projectconfig.ReleaseCalculationManual},
	})

	require.NoError(t, preparer.tryBumpStaticRelease(comp, testSourcesDir, 3))
}

func TestTryBumpStaticRelease_AutoreleaseSkips(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)
	writeStaticReleaseTestSpec(t, memFS, "test-pkg", "%autorelease")

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{Calculation: projectconfig.ReleaseCalculationAuto},
	})

	require.NoError(t, preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3))
}

func TestTryBumpStaticRelease_StaticBumps(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)
	specPath := writeStaticReleaseTestSpec(t, memFS, "test-pkg", "1%{?dist}")

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{Calculation: projectconfig.ReleaseCalculationAuto},
	})

	require.NoError(t, preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3))

	content, err := fileutils.ReadFile(memFS, specPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Release: 4%{?dist}")
}

func TestTryBumpStaticRelease_NonStandardErrorsWithoutManual(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)
	writeStaticReleaseTestSpec(t, memFS, "kernel", "%{pkg_release}")

	comp := mockComponent(ctrl, "kernel", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{Calculation: projectconfig.ReleaseCalculationAuto},
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
	writeStaticReleaseTestSpec(t, memFS, "kernel", "%{pkg_release}")

	comp := mockComponent(ctrl, "kernel", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{Calculation: projectconfig.ReleaseCalculationManual},
	})

	require.NoError(t, preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "kernel"), 3))
}

func TestTryBumpStaticRelease_ExplicitAutoreleaseSkips(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)
	comp := mockComponent(ctrl, "gvisor", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{Calculation: projectconfig.ReleaseCalculationAutorelease},
	})

	require.NoError(t, preparer.tryBumpStaticRelease(comp, testSourcesDir, 3))
}

func TestTryBumpStaticRelease_ExplicitStaticBumps(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)
	writeStaticReleaseTestSpec(t, memFS, "test-pkg", "1%{?dist}")

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{Calculation: projectconfig.ReleaseCalculationStatic},
	})

	require.NoError(t, preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3))
	content, err := fileutils.ReadFile(memFS, filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "Release: 4%{?dist}")
}

func TestTryBumpStaticRelease_ExplicitStaticErrorsOnAutorelease(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)
	writeStaticReleaseTestSpec(t, memFS, "test-pkg", "%autorelease")

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{Calculation: projectconfig.ReleaseCalculationStatic},
	})

	err := preparer.tryBumpStaticRelease(comp, filepath.Join(testSourcesDir, "test-pkg"), 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `release.calculation = "autorelease"`)
}

func TestTryBumpStaticRelease_BumpsLastConditionalReleaseAndRereadsIt(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)
	specDir := filepath.Join(testSourcesDir, "test-pkg")
	require.NoError(t, fileutils.MkdirAll(memFS, specDir))
	specPath := filepath.Join(specDir, "test-pkg.spec")
	require.NoError(t, fileutils.WriteFile(memFS, specPath, []byte(`Name: test-pkg
Version: 1.0.0
%if 0
Release: 1%{?dist}
%else
Release: 2%{?dist}
%endif
`), fileperms.PublicFile))

	comp := mockComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{
		Release: projectconfig.ReleaseConfig{Calculation: projectconfig.ReleaseCalculationAuto},
	})

	require.NoError(t, preparer.tryBumpStaticRelease(comp, specDir, 3))

	release, err := GetReleaseTagValue(memFS, specPath)
	require.NoError(t, err)
	assert.Equal(t, "5%{?dist}", release)

	content, err := fileutils.ReadFile(memFS, specPath)
	require.NoError(t, err)
	assert.Equal(t, `Name: test-pkg
Version: 1.0.0
%if 0
Release: 5%{?dist}
%else
Release: 5%{?dist}
%endif
`, string(content))
}

func TestTryBumpStaticRelease_StaticBumpsNonConditionalDist(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	preparer := newTestPreparer(memFS)

	writeStaticReleaseTestSpec(t, memFS, "test-pkg", "1%{dist}")

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

func TestTryBumpRPMDevBumpspec_RollsBackAfterKthFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	preparer, ctx := rpmdevReleaseTestPreparer(t)
	original := "Name: test-pkg\nVersion: 1.0\nRelease: 4\n"
	specPath := writeTestSpec(t, ctx.FS(), "test-pkg", original)
	comp := mockReleaseComponent(ctrl, "test-pkg", projectconfig.ReleaseCalculationAuto)
	queries := 0
	bumps := 0
	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		switch {
		case isEVRQuery(cmd):
			queries++

			release := "4"
			if queries > 1 {
				release = "5"
			}

			_, _ = fmt.Fprint(cmd.Stdout, evrOutput("test-pkg", "0", release))
		case cmd.Path == RPMDevBumpspecBinary:
			bumps++
			if bumps == 2 {
				return errors.New("second operation failed")
			}

			return fileutils.WriteFile(ctx.FS(), specPath,
				[]byte("Name: test-pkg\nVersion: 1.0\nRelease: 5\n"), fileperms.PublicFile)
		case isReleaseComparison(cmd):
			_, _ = fmt.Fprintln(cmd.Stdout, "-1")
		}

		return nil
	}

	err := preparer.tryBumpRPMDevBumpspec(
		context.Background(), comp, filepath.Join(testSourcesDir, "test-pkg"), testChanges())
	require.Error(t, err)
	assert.Equal(t, 2, bumps)
	assert.Equal(t, original, stringMustRead(t, ctx, specPath))
}

func releaseMatrixSpec(release string) string {
	return strings.Join([]string{
		"Name: test-pkg",
		"Version: 1.0",
		"Release: " + release,
		"Summary: Test package",
		"License: MIT",
		"",
		"%description",
		"Test package.",
		"",
		"%changelog",
		"* Mon Jan 01 2024 Existing Maintainer <existing@example.com> - 1.0-4",
		"- Existing entry",
		"",
	}, "\n")
}

func TestTryBumpRelease_DefaultLegacyCompatibilityMatrix(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		release     string
		calculation projectconfig.ReleaseCalculation
		changes     []FingerprintChange
		wantSpec    string
		wantErr     string
	}{
		{
			name: "zero changes", release: "4%{?dist}", calculation: projectconfig.ReleaseCalculationAuto,
			wantSpec: releaseMatrixSpec("4%{?dist}"),
		},
		{
			name: "manual mode", release: "%{pkg_release}", calculation: projectconfig.ReleaseCalculationManual,
			changes: testChanges(), wantSpec: releaseMatrixSpec("%{pkg_release}"),
		},
		{
			name: "explicit autorelease mode", release: "4", calculation: projectconfig.ReleaseCalculationAutorelease,
			changes: testChanges(), wantSpec: releaseMatrixSpec("4"),
		},
		{
			name: "auto autorelease skips", release: "%{autorelease -e asan}", calculation: projectconfig.ReleaseCalculationAuto,
			changes: testChanges(), wantSpec: releaseMatrixSpec("%{autorelease -e asan}"),
		},
		{
			name:        "explicit static autorelease mismatch",
			release:     "%autorelease",
			calculation: projectconfig.ReleaseCalculationStatic,
			changes:     testChanges(),
			wantSpec:    releaseMatrixSpec("%autorelease"),
			wantErr:     `release.calculation = "autorelease"`,
		},
		{
			name: "bare integer succeeds", release: "4", calculation: projectconfig.ReleaseCalculationAuto,
			changes: testChanges(), wantSpec: releaseMatrixSpec("6"),
		},
		{
			name: "conditional dist succeeds", release: "4%{?dist}", calculation: projectconfig.ReleaseCalculationAuto,
			changes: testChanges(), wantSpec: releaseMatrixSpec("6%{?dist}"),
		},
		{
			name: "dist succeeds", release: "4%{dist}", calculation: projectconfig.ReleaseCalculationAuto,
			changes: testChanges(), wantSpec: releaseMatrixSpec("6%{dist}"),
		},
		{
			name: "dotted release errors", release: "4.1", calculation: projectconfig.ReleaseCalculationAuto,
			changes: testChanges(), wantSpec: releaseMatrixSpec("4.1"), wantErr: "cannot be auto-bumped",
		},
		{
			name: "macro release errors", release: "%{pkg_release}", calculation: projectconfig.ReleaseCalculationAuto,
			changes: testChanges(), wantSpec: releaseMatrixSpec("%{pkg_release}"), wantErr: "cannot be auto-bumped",
		},
		{
			name: "manual nonstandard release", release: "%{pkg_release}", calculation: projectconfig.ReleaseCalculationManual,
			changes: testChanges(), wantSpec: releaseMatrixSpec("%{pkg_release}"),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ctx := testctx.NewCtx()
			commandCtx := &failingCommandCtx{TestCtx: ctx, t: t}
			preparer := &sourcePreparerImpl{
				fs: ctx.FS(), bumpspecCtx: commandCtx, bumpspecScratchDir: "/work/missing",
			}
			comp := mockReleaseComponent(ctrl, "test-pkg", testCase.calculation)
			specPath := writeTestSpec(t, ctx.FS(), "test-pkg", releaseMatrixSpec(testCase.release))

			err := preparer.tryBumpRelease(
				context.Background(), comp, filepath.Join(testSourcesDir, "test-pkg"), testCase.changes)
			if testCase.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, testCase.wantSpec, stringMustRead(t, ctx, specPath))
		})
	}
}

func TestTryBumpRelease_UsesRPMDevBumpspecOnlyWhenConfigured(t *testing.T) {
	ctrl := gomock.NewController(t)
	preparer, ctx := rpmdevReleaseTestPreparer(t)
	preparer.releaseStrategy = releaseStrategyRPMDevBumpspec
	specPath := writeTestSpec(t, ctx.FS(), "test-pkg", "Name: test-pkg\nVersion: 1.0\nRelease: 4\n")
	comp := mockReleaseComponent(ctrl, "test-pkg", projectconfig.ReleaseCalculationAuto)
	queries := 0
	bumps := 0
	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		switch {
		case isEVRQuery(cmd):
			queries++

			release := "4"
			if queries > 1 {
				release = "5"
			}

			_, _ = fmt.Fprint(cmd.Stdout, evrOutput("test-pkg", "0", release))
		case cmd.Path == RPMDevBumpspecBinary:
			bumps++

			return fileutils.WriteFile(ctx.FS(), specPath,
				[]byte("Name: test-pkg\nVersion: 1.0\nRelease: 5\n"), fileperms.PublicFile)
		case isReleaseComparison(cmd):
			_, _ = fmt.Fprintln(cmd.Stdout, "-1")
		default:
			return fmt.Errorf("unexpected command: %#v", cmd.Args)
		}

		return nil
	}

	require.NoError(t, preparer.tryBumpRelease(context.Background(), comp,
		filepath.Join(testSourcesDir, "test-pkg"), testChanges()[:1]))
	assert.Equal(t, 1, bumps)
}

func TestTryBumpRPMDevBumpspec_UsesConfiguredSpecEditor(t *testing.T) {
	ctrl := gomock.NewController(t)
	preparer, ctx := rpmdevReleaseTestPreparer(t)
	preparer.specEditor = spec.EditorMode("unsupported")

	writeTestSpec(t, ctx.FS(), "test-pkg", "Name: test-pkg\nVersion: 1.0\nRelease: 4\n")

	comp := mockReleaseComponent(ctrl, "test-pkg", projectconfig.ReleaseCalculationAuto)

	err := preparer.tryBumpRPMDevBumpspec(
		context.Background(), comp, filepath.Join(testSourcesDir, "test-pkg"), testChanges()[:1])
	require.ErrorContains(t, err, "unknown spec editor")
	assert.ErrorContains(t, err, "unsupported")
}
