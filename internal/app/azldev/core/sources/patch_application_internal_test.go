// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestAddedPatchWarningsAfterAllOverlays(t *testing.T) {
	tests := []struct {
		name        string
		prep        []string
		later       []projectconfig.ComponentOverlay
		wantWarning string
	}{
		{name: "missing application", prep: []string{"%setup -q"}, wantWarning: "no recognized command"},
		{name: "matching bulk application", prep: []string{"%autosetup -p1"}},
		{name: "matching forge wrapper", prep: []string{"%forgeautosetup -p1"}},
		{name: "matching go wrapper", prep: []string{"%goprep -p1"}},
		{name: "go setup without application", prep: []string{"%goprep"}, wantWarning: "no recognized command"},
		{name: "bare bulk nested patch", prep: []string{"%autosetup"}, wantWarning: "omits '-p'"},
		{
			name: "later overlay adds application", prep: []string{"%setup -q"},
			later: []projectconfig.ComponentOverlay{{
				Type: projectconfig.ComponentOverlayAppendSpecLines, SectionName: "%prep",
				Lines: []string{"%patch -P0 -p1"},
			}},
		},
		{
			name: "later overlay fixes strip", prep: []string{"%autosetup"},
			later: []projectconfig.ComponentOverlay{{
				Type: projectconfig.ComponentOverlaySearchAndReplaceInSpec, SectionName: "%prep",
				Regex: "^%autosetup$", Replacement: "%autosetup -p1",
			}},
		},
		{
			name: "later overlay removes patch", prep: []string{"%setup -q"},
			later: []projectconfig.ComponentOverlay{{
				Type: projectconfig.ComponentOverlayRemovePatch, Filename: "fix.patch",
			}},
		},
		{
			name: "later overlay removes file but leaves declaration", prep: []string{"%autosetup -p1"},
			later: []projectconfig.ComponentOverlay{{
				Type: projectconfig.ComponentOverlayRemoveFile, Filename: "fix.patch",
			}},
			wantWarning: "final file is missing",
		},
		{
			name: "later overlay changes final patch content", prep: []string{"%autosetup"},
			later: []projectconfig.ComponentOverlay{{
				Type: projectconfig.ComponentOverlaySearchAndReplaceInFile, Filename: "fix.patch",
				Regex: "[ab]/lib/", Replacement: "",
			}},
		},
		{
			name: "explicit strip conflict", prep: []string{"%autosetup -p2"},
			wantWarning: "uses '-p2'",
		},
		{
			name: "multiple directories", prep: []string{"%setup -q", "pushd lib", "%autopatch -p1", "popd"},
			wantWarning: "working directories",
		},
		{
			name: "conditional application", prep: []string{"%setup -q", "%ifarch x86_64", "%autopatch -p1", "%endif"},
			wantWarning: "conditional",
		},
	}
	for _, mode := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(mode), func(t *testing.T) {
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					ctx := testctx.NewCtx()
					require.NoError(t, ctx.FS().MkdirAll("/sources", fileperms.PublicDir))

					lines := append([]string{"Name: test", "%prep"}, test.prep...)
					lines = append(lines, "%build", "make")
					require.NoError(t, fileutils.WriteFile(ctx.FS(), "/sources/test.spec",
						[]byte(strings.Join(lines, "\n")+"\n"), fileperms.PublicFile))
					require.NoError(t, fileutils.WriteFile(ctx.FS(), "/fix.patch",
						[]byte("--- a/lib/file.c\n+++ b/lib/file.c\n"), fileperms.PublicFile))

					overlays := []projectconfig.ComponentOverlay{{
						Type: projectconfig.ComponentOverlayAddPatch, Source: "/fix.patch",
					}}
					overlays = append(overlays, test.later...)
					component := components_testutils.NewMockComponent(gomock.NewController(t))
					component.EXPECT().GetName().AnyTimes().Return("test")
					component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{Overlays: overlays})

					preparer := &sourcePreparerImpl{
						fs: ctx.FS(), dryRunnable: ctx, eventListener: ctx, specEditor: mode,
					}

					var (
						diagnostics []spec.PatchApplicationDiagnostic
						reports     int
					)

					WithPatchDiagnostics(func(final []spec.PatchApplicationDiagnostic) {
						diagnostics = final
						reports++
					})(preparer)

					var logs bytes.Buffer

					previous := slog.Default()
					slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
					t.Cleanup(func() { slog.SetDefault(previous) })

					_, err := preparer.applyOverlays(component, "/sources", "")
					require.NoError(t, err, "diagnostic warnings must not fail preparation")
					assert.Equal(t, 1, reports, "report only the final overlay state")

					if test.wantWarning == "" {
						assert.Empty(t, diagnostics)
						assert.NotContains(t, logs.String(), "Added patch application")
					} else {
						require.Len(t, diagnostics, 1)
						assert.Equal(t, "fix.patch", diagnostics[0].Filename)
						assert.Equal(t, 0, diagnostics[0].PatchNumber)
						assert.Contains(t, diagnostics[0].Reason, test.wantWarning)
					}

					assert.Empty(t, logs.String(), "the diagnostic sink owns reporting")
				})
			}
		})
	}
}

func TestPatchDiagnosticsOnlyInspectPatchAddOverlays(t *testing.T) {
	ctx := testctx.NewCtx()
	preparer := &sourcePreparerImpl{fs: ctx.FS(), dryRunnable: ctx}
	require.NoError(t, preparer.inspectAddedPatchApplications("test", "/missing", "/missing/test.spec",
		[]projectconfig.ComponentOverlay{{Type: projectconfig.ComponentOverlayAddSpecTag, Tag: "Name", Value: "test"}}))
}

func TestPatchDiagnosticsGeneratedMacroFiles(t *testing.T) {
	tests := []struct {
		name    string
		prep    string
		defines map[string]string
		change  bool
		remove  bool
		reason  string
	}{
		{
			name: "patch-named component retains literal numbering", prep: "%setup -q",
			reason: "no recognized command",
		},
		{name: "forge wrapper still recognized", prep: "%forgeautosetup -p1"},
		{
			name: "configured wrapper redefinition is not inspected", prep: "%forgeautosetup -p1",
			defines: map[string]string{"forgeautosetup": "echo skipped"},
		},
		{
			name: "configured setup redefinition remains setup-only warning", prep: "%setup -q",
			defines: map[string]string{"setup": "%autopatch -p1"},
			reason:  "no recognized command",
		},
		{
			name: "configured backend is not inspected", prep: "%forgeautosetup",
			defines: map[string]string{"__scm": "git"}, reason: "omits '-p'",
		},
		{name: "changed generated file is not inspected", prep: "%forgeautosetup -p1", change: true},
		{name: "removed generated file is not inspected", prep: "%forgeautosetup -p1", remove: true},
	}
	for _, mode := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(mode), func(t *testing.T) {
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					ctx := testctx.NewCtx()
					lines := []string{"Name: test-patch", "%prep", test.prep, "%build", "make"}
					require.NoError(t, fileutils.WriteFile(ctx.FS(), "/sources/test-patch.spec",
						[]byte(strings.Join(lines, "\n")+"\n"), fileperms.PublicFile))
					require.NoError(t, fileutils.WriteFile(ctx.FS(), "/fix.patch",
						[]byte("--- a/lib/file.c\n+++ b/lib/file.c\n"), fileperms.PublicFile))

					config := &projectconfig.ComponentConfig{
						Build: projectconfig.ComponentBuildConfig{With: []string{"patches"}, Defines: test.defines},
						Overlays: []projectconfig.ComponentOverlay{{
							Type: projectconfig.ComponentOverlayAddPatch, Source: "/fix.patch",
						}},
					}
					if test.change {
						config.Overlays = append(config.Overlays, projectconfig.ComponentOverlay{
							Type:     projectconfig.ComponentOverlaySearchAndReplaceInFile,
							Filename: "test-patch.azl.macros", Regex: "_with_patches", Replacement: "_with_modified",
						})
					}

					if test.remove {
						config.Overlays = append(config.Overlays, projectconfig.ComponentOverlay{
							Type: projectconfig.ComponentOverlayRemoveFile, Filename: "test-patch.azl.macros",
						})
					}

					comp := components_testutils.NewMockComponent(gomock.NewController(t))
					comp.EXPECT().GetName().AnyTimes().Return("test-patch")
					comp.EXPECT().GetConfig().AnyTimes().Return(config)

					var diagnostics []spec.PatchApplicationDiagnostic

					preparer := &sourcePreparerImpl{
						fs: ctx.FS(), dryRunnable: ctx, eventListener: ctx, specEditor: mode,
					}
					WithPatchDiagnostics(func(final []spec.PatchApplicationDiagnostic) { diagnostics = final })(preparer)
					macroPath, err := preparer.writeMacrosFile(ctx, comp, "/sources")
					require.NoError(t, err)
					_, err = preparer.applyOverlays(comp, "/sources", filepath.Base(macroPath))
					require.NoError(t, err)

					if test.reason == "" {
						assert.Empty(t, diagnostics)
					} else {
						require.Len(t, diagnostics, 1)
						assert.Contains(t, diagnostics[0].Reason, test.reason)
						assert.Equal(t, 0, diagnostics[0].PatchNumber)
					}
				})
			}
		})
	}
}

func TestPatchDiagnosticsReadErrors(t *testing.T) {
	ctx := testctx.NewCtx()
	preparer := &sourcePreparerImpl{fs: ctx.FS(), dryRunnable: ctx, specEditor: spec.EditorLegacy}
	err := preparer.inspectAddedPatchApplications("test", "/sources", "/sources/test.spec",
		[]projectconfig.ComponentOverlay{{Type: projectconfig.ComponentOverlayAddPatch, Source: "/fix.patch"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading final spec")
}

func TestPatchDiagnosticsDryRunUsesOriginalPatch(t *testing.T) {
	ctx := testctx.NewCtx()
	ctx.DryRunValue = true
	require.NoError(t, ctx.FS().MkdirAll("/sources", fileperms.PublicDir))

	lines := []string{"Name: test", "Patch7: renamed.patch", "%prep", "%autosetup", ""}
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/sources/test.spec",
		[]byte(strings.Join(lines, "\n")), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/fix.patch",
		[]byte("--- a/lib/file.c\n+++ b/lib/file.c\n"), fileperms.PublicFile))

	preparer := &sourcePreparerImpl{fs: ctx.FS(), dryRunnable: ctx, specEditor: spec.EditorLegacy}

	var logs bytes.Buffer

	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	overlay := projectconfig.ComponentOverlay{
		Type: projectconfig.ComponentOverlayAddPatch, Source: "/fix.patch", Filename: "renamed.patch",
	}
	require.NoError(t, preparer.inspectAddedPatchApplications("test", "/sources", "/sources/test.spec",
		[]projectconfig.ComponentOverlay{overlay, overlay}))
	assert.Equal(t, 1, strings.Count(logs.String(), "Added patch application"))
	assert.Contains(t, logs.String(), "patch=renamed.patch")
	assert.Contains(t, logs.String(), "patchNumber=7")
	assert.NotContains(t, logs.String(), "line=")
	assert.Contains(t, logs.String(), "command=%autosetup")
	assert.Contains(t, logs.String(), "omits '-p'")
	assert.NotContains(t, logs.String(), "cannot be read")
}

func TestPatchDiagnosticsWithoutSinkRetainWarningBehavior(t *testing.T) {
	ctx := testctx.NewCtx()
	lines := []string{"Name: test", "Patch0: fix.patch", "%prep", "%setup -q"}
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/sources/test.spec",
		[]byte(strings.Join(lines, "\n")+"\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/sources/fix.patch", nil, fileperms.PublicFile))
	preparer := &sourcePreparerImpl{fs: ctx.FS(), dryRunnable: ctx}

	var logs bytes.Buffer

	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	require.NoError(t, preparer.inspectAddedPatchApplications("test", "/sources", "/sources/test.spec",
		[]projectconfig.ComponentOverlay{{Type: projectconfig.ComponentOverlayAddPatch, Source: "/fix.patch"}}))
	assert.Equal(t, 1, strings.Count(logs.String(), "Added patch application"))
	assert.Contains(t, logs.String(), "level=WARN")
	assert.NotContains(t, logs.String(), "level=ERROR")
}
