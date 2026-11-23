// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os/exec"
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
		name            string
		prep            []string
		conditionalPrep bool
		patch           []string
		later           []projectconfig.ComponentOverlay
		lintLines       []string
		lintOutput      string
		wantWarning     string
	}{
		{name: "missing application", prep: []string{"%setup -q"}, wantWarning: "no recognized command"},
		{
			name: "matching bulk application", prep: []string{"%autosetup -p1 -n %{name}-%{version}"},
			lintLines:  []string{"%autosetup -p1 -n %{name}-%{version}"},
			lintOutput: `{"all":true,"numbers":[]}`,
		},
		{name: "matching forge wrapper", prep: []string{"%forgeautosetup -p1"}},
		{name: "matching go wrapper", prep: []string{"%goprep -p1"}},
		{name: "go setup without application", prep: []string{"%goprep"}, wantWarning: "no recognized command"},
		{
			name: "bare bulk selection does not predict strip", prep: []string{"%autosetup"},
			lintLines: []string{"%autosetup"}, lintOutput: `{"all":true,"numbers":[]}`,
		},
		{
			name: "go vendor contents are not inspected", prep: []string{"%goprep -p1"},
			patch: []string{"--- a/vendor/lib/file.c", "+++ b/vendor/lib/file.c"},
		},
		{
			name: "later overlay adds application", prep: []string{"%setup -q"},
			later: []projectconfig.ComponentOverlay{{
				Type: projectconfig.ComponentOverlayAppendSpecLines, SectionName: "%prep",
				Lines: []string{"%patch -P0 -p1"},
			}},
			lintLines: []string{"%patch -P0 -p1"}, lintOutput: `{"all":false,"numbers":[0]}`,
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
			name: "explicit strip level is not predicted", prep: []string{"%autosetup -p2"},
			lintLines: []string{"%autosetup -p2"}, lintOutput: `{"all":true,"numbers":[]}`,
		},
		{
			name: "multiple directories", prep: []string{"%setup -q", "pushd lib", "%autopatch -p1", "popd"},
			wantWarning: "working directories",
		},
		{
			name: "conditional application", prep: []string{"%setup -q", "%ifarch x86_64", "%autopatch -p1", "%endif"},
			wantWarning: "conditional",
		},
		{
			name: "conditional prep cannot trust commands after endif", conditionalPrep: true,
			prep: []string{"%patch -P0 -p1"}, wantWarning: "conditional, or nonstandard preparation",
		},
		{
			name: "both RPM branches are excluded", prep: []string{
				"%ifarch x86_64", "%autosetup -p1", "%else", "%patch -P0 -p1", "%endif",
			},
			wantWarning: "conditional branches",
		},
		{
			name: "supported commands after endif are batched without disabled setup", prep: []string{
				"%autosetup -N", "%ifarch x86_64", "%patch -P2 -p1", "%endif", "%patch 1 -p1", "%patch -P0 -p1",
			},
			lintLines: []string{"%patch 1 -p1", "%patch -P0 -p1"}, lintOutput: `{"all":false,"numbers":[0,1]}`,
		},
		{
			name: "a linter selection does not mask an excluding range", prep: []string{
				"%autosetup -N", "%autopatch -m1 -M5 -p1", "%patch -P2 -p1 # -P0", "%build", "%patch -P0 -p1",
			},
			lintLines: []string{"%patch -P2 -p1"}, lintOutput: `{"all":false,"numbers":[2]}`,
			wantWarning: "no recognized command",
		},
		{
			name: "a Go range can select a patch the linter did not select", prep: []string{
				"%autosetup -N", "%autopatch -m0 -M1 -p1", "%patch -P2 -p1",
			},
			lintLines: []string{"%patch -P2 -p1"}, lintOutput: `{"all":false,"numbers":[2]}`,
		},
		{
			name: "shell conditional is not linted", prep: []string{
				"%setup -q", "%patch -P1 -p1", "if test -f lib/file.c; then", "%patch -P0 -p1", "fi",
			},
			lintLines: []string{"%patch -P1 -p1"}, lintOutput: `{"all":false,"numbers":[1]}`,
			wantWarning: "shell control flow",
		},
		{
			name: "rpmlint would misread attached directory name", prep: []string{"%autosetup -nName -p1"},
		},
		{
			name: "missing rpmlint retains an actual Go selection", prep: []string{"%patch -P0 -p1"},
			lintLines: []string{"%patch -P0 -p1"}, lintOutput: `{"unavailable":true}`,
		},
		{
			name: "an available empty linter result does not fall back to Go", prep: []string{"%patch -P0 -p1"},
			lintLines: []string{"%patch -P0 -p1"}, lintOutput: `{"all":false,"numbers":[]}`,
			wantWarning: "no recognized command",
		},
	}
	for _, mode := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(mode), func(t *testing.T) {
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					ctx := testctx.NewCtx()
					require.NoError(t, ctx.FS().MkdirAll("/sources", fileperms.PublicDir))

					lines := []string{"Name: test"}
					if test.conditionalPrep {
						lines = append(lines, "%description", "fixture", "%ifarch x86_64", "%prep", "%endif")
					} else {
						lines = append(lines, "%prep")
					}

					lines = append(lines, test.prep...)
					lines = append(lines, "%build", "make")
					require.NoError(t, fileutils.WriteFile(ctx.FS(), "/sources/test.spec",
						[]byte(strings.Join(lines, "\n")+"\n"), fileperms.PublicFile))

					patch := test.patch
					if patch == nil {
						patch = []string{"--- a/lib/file.c", "+++ b/lib/file.c"}
					}

					require.NoError(t, fileutils.WriteFile(ctx.FS(), "/fix.patch",
						[]byte(strings.Join(patch, "\n")+"\n"), fileperms.PublicFile))

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
					WithRPMLint(ctx)(preparer)

					var lintCalls int

					ctx.CmdFactory.RegisterCommandInSearchPath(python3Binary)
					ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
						lintCalls++

						require.Equal(t, []string{python3Binary, "-I", "-B", "-c", rpmlintPatchSelectionScript}, cmd.Args)

						var inputs []string

						require.NoError(t, json.NewDecoder(cmd.Stdin).Decode(&inputs))
						require.Equal(t, test.lintLines, inputs)

						return test.lintOutput, nil
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

					_, err := preparer.applyOverlays(ctx, component, "/sources", "")
					require.NoError(t, err, "diagnostic warnings must not fail preparation")
					assert.Equal(t, 1, reports, "report only the final overlay state")

					if test.lintLines == nil {
						assert.Zero(t, lintCalls)
					} else {
						assert.Equal(t, 1, lintCalls)
					}

					if test.wantWarning == "" {
						assert.Empty(t, diagnostics)
						assert.NotContains(t, logs.String(), "Added patch application")
					} else {
						require.Len(t, diagnostics, 1)
						assert.Equal(t, "fix.patch", diagnostics[0].Filename)
						assert.Equal(t, 0, diagnostics[0].PatchNumber)
						assert.Contains(t, diagnostics[0].Reason, test.wantWarning)
					}

					if test.lintOutput == `{"unavailable":true}` {
						assert.Contains(t, logs.String(), "rpmlint is not installed")
					} else {
						assert.Empty(t, logs.String(), "the diagnostic sink owns reporting")
					}
				})
			}
		})
	}
}

func TestPatchDiagnosticsOnlyInspectPatchAddOverlays(t *testing.T) {
	ctx := testctx.NewCtx()
	preparer := &sourcePreparerImpl{fs: ctx.FS(), dryRunnable: ctx}
	require.NoError(t, preparer.inspectAddedPatchApplications(ctx, "test", "/missing", "/missing/test.spec",
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
			defines: map[string]string{"__scm": "git"},
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
					_, err = preparer.applyOverlays(ctx, comp, "/sources", filepath.Base(macroPath))
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
	err := preparer.inspectAddedPatchApplications(ctx, "test", "/sources", "/sources/test.spec",
		[]projectconfig.ComponentOverlay{{Type: projectconfig.ComponentOverlayAddPatch, Source: "/fix.patch"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading final spec")
}

func TestPatchDiagnosticsDryRunChecksOriginalPatchPresence(t *testing.T) {
	ctx := testctx.NewCtx()
	ctx.DryRunValue = true
	require.NoError(t, ctx.FS().MkdirAll("/sources", fileperms.PublicDir))

	lines := []string{"Name: test", "Patch7: renamed.patch", "%prep", "%setup -q", "%patch -P0 -p1", ""}
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/sources/test.spec",
		[]byte(strings.Join(lines, "\n")), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/fix.patch",
		[]byte("--- a/lib/file.c\n+++ b/lib/file.c\n"), fileperms.PublicFile))

	preparer := &sourcePreparerImpl{fs: ctx.FS(), dryRunnable: ctx, specEditor: spec.EditorLegacy}
	WithRPMLint(ctx)(preparer)

	ctx.CmdFactory.RegisterCommandInSearchPath(python3Binary)
	ctx.CmdFactory.RunAndGetOutputHandler = func(*exec.Cmd) (string, error) {
		t.Fatal("patch diagnostics must not spawn Python in a dry run")

		return "", nil
	}

	var logs bytes.Buffer

	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	overlay := projectconfig.ComponentOverlay{
		Type: projectconfig.ComponentOverlayAddPatch, Source: "/fix.patch", Filename: "renamed.patch",
	}
	require.NoError(t, preparer.inspectAddedPatchApplications(ctx, "test", "/sources", "/sources/test.spec",
		[]projectconfig.ComponentOverlay{overlay, overlay}))
	assert.Equal(t, 1, strings.Count(logs.String(), "Added patch application"))
	assert.Contains(t, logs.String(), "patch=renamed.patch")
	assert.Contains(t, logs.String(), "patchNumber=7")
	assert.NotContains(t, logs.String(), "line=")
	assert.Contains(t, logs.String(), `command=""`)
	assert.Contains(t, logs.String(), "no recognized command")
	assert.NotContains(t, logs.String(), "final file is missing")
}

func TestPatchDiagnosticsRejectDirectoryInsteadOfPatch(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()
	require.NoError(t, ctx.FS().MkdirAll("/sources/fix.patch", fileperms.PublicDir))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/sources/test.spec",
		[]byte("Name: test\nPatch0: fix.patch\n%prep\n%autosetup\n"), fileperms.PublicFile))
	preparer := &sourcePreparerImpl{fs: ctx.FS(), dryRunnable: ctx}

	err := preparer.inspectAddedPatchApplications(ctx, "test", "/sources", "/sources/test.spec",
		[]projectconfig.ComponentOverlay{{Type: projectconfig.ComponentOverlayAddPatch, Source: "/fix.patch"}})
	require.ErrorContains(t, err, "checking final patch")
	assert.Contains(t, err.Error(), "is a directory, expected a file")
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
	require.NoError(t, preparer.inspectAddedPatchApplications(ctx, "test", "/sources", "/sources/test.spec",
		[]projectconfig.ComponentOverlay{{Type: projectconfig.ComponentOverlayAddPatch, Source: "/fix.patch"}}))
	assert.Equal(t, 1, strings.Count(logs.String(), "Added patch application"))
	assert.Contains(t, logs.String(), "level=WARN")
	assert.NotContains(t, logs.String(), "level=ERROR")
}
