// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestRenderPatchWarningsJSONAndPolicy(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		prep        string
		resolved    bool
		mockError   string
		wantStatus  string
		wantError   bool
		wantFinding bool
	}{
		{
			name: "default warns for setup-only flow", prep: "%setup -q", wantStatus: renderStatusOK,
			wantFinding: true,
		},
		{
			name: "setup-only warning does not fail with fail-on-error", args: []string{"--fail-on-error"},
			prep: "%setup -q", wantStatus: renderStatusOK, wantFinding: true,
		},
		{
			name: "default warns for excluded patch", prep: "%setup -q\n%patch -P1 -p1",
			wantStatus: renderStatusOK, wantFinding: true,
		},
		{
			name: "excluded patch warning does not fail with fail-on-error", args: []string{"--fail-on-error"},
			prep: "%setup -q\n%patch -P1 -p1", wantStatus: renderStatusOK, wantFinding: true,
		},
		{
			name: "default warns for opaque macro", prep: "%setup -q\n%apply_fixes",
			wantStatus: renderStatusOK, wantFinding: true,
		},
		{
			name: "explicit warn with fail-on-error still publishes", args: []string{"--patch-warnings=warn", "--fail-on-error"},
			prep: "%setup -q\n./prepare.sh", wantStatus: renderStatusOK, wantFinding: true,
		},
		{
			name: "strip mismatch warns by default", prep: "%autosetup -p0",
			wantStatus: renderStatusOK, wantFinding: true,
		},
		{
			name: "strict rejects without command failure", args: []string{"--patch-warnings=error"},
			prep: "%setup -q", wantStatus: renderStatusError, wantFinding: true,
		},
		{
			name: "strict with fail-on-error", args: []string{"--patch-warnings=error", "--fail-on-error"},
			prep: "%setup -q", wantStatus: renderStatusError, wantError: true, wantFinding: true,
		},
		{
			name: "strict rejects uncertain application", args: []string{"--patch-warnings=error"},
			prep: "%autosetup -p%{strip}", wantStatus: renderStatusError,
			wantFinding: true,
		},
		{
			name: "strict uncertainty fails with fail-on-error", args: []string{"--patch-warnings=error", "--fail-on-error"},
			prep: "%autosetup -p%{strip}", wantStatus: renderStatusError, wantError: true,
			wantFinding: true,
		},
		{
			name: "strict rejects strip mismatch", args: []string{"--patch-warnings=error"},
			prep: "%autosetup -p0", wantStatus: renderStatusError,
			wantFinding: true,
		},
		{
			name: "strict mismatch fails with fail-on-error", args: []string{"--patch-warnings=error", "--fail-on-error"},
			prep: "%autosetup -p0", wantStatus: renderStatusError, wantError: true,
			wantFinding: true,
		},
		{
			name: "mismatch alone does not fail with fail-on-error", args: []string{"--fail-on-error"},
			prep: "%autosetup -p0", wantStatus: renderStatusOK, wantFinding: true,
		},
		{
			name: "strict allows resolved final overlay", args: []string{"--patch-warnings=error"},
			prep: "%setup -q", resolved: true, wantStatus: renderStatusOK,
		},
		{
			name: "strict recognizes forge", args: []string{"--patch-warnings=error"},
			prep: "%forgeautosetup -p1", wantStatus: renderStatusOK,
		},
		{
			name: "strict accepts explicit match despite opaque flow",
			args: []string{"--patch-warnings=error", "--fail-on-error"},
			prep: strings.Join([]string{
				"%if %{with compat}", "%setup -q -n %{compat_source}",
				"%autopatch -m%{compat}00 -M%{compat}99 -p1", "%endif",
				"%autosetup -N -n %{main_source}", "%autopatch -M%{limit} -p1", "%patch -P0 -p1",
			}, "\n"),
			wantStatus: renderStatusOK,
		},
		{
			name:       "strict accepts bulk match despite opaque flow",
			args:       []string{"--patch-warnings=error", "--fail-on-error"},
			prep:       "%{gpgverify} --data=%{SOURCE0}\n%autosetup -n %{name}-%{version} -p1\nautoreconf -i",
			wantStatus: renderStatusOK,
		},
		{
			name: "strict rejects excluded patch", args: []string{"--patch-warnings=error", "--fail-on-error"},
			prep: "%setup -q\n%patch -P1 -p1", wantStatus: renderStatusError, wantError: true, wantFinding: true,
		},
		{
			name: "strict recognizes go", args: []string{"--patch-warnings=error"},
			prep: "%goprep -p1", wantStatus: renderStatusOK,
		},
		{
			name: "mock failure retains warnings", prep: "%setup -q\n%apply_fixes", mockError: "mock failed",
			wantStatus: renderStatusError, wantFinding: true,
		},
	}

	for _, editor := range []string{specEditorLegacy, specEditorExperimental} {
		t.Run(editor, func(t *testing.T) {
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					testEnv := testutils.NewTestEnv(t)
					addPatchWarningRenderComponent(t, testEnv, "test-patch", test.prep, test.resolved)
					stubPatchWarningRenderMock(t, testEnv, test.mockError)

					var logs bytes.Buffer

					previous := slog.Default()
					slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
					t.Cleanup(func() { slog.SetDefault(previous) })

					var report strings.Builder

					testEnv.Env.SetDefaultReportFormat(azldev.ReportFormatJSON)
					testEnv.Env.SetReportFile(&report)

					cmd := newComponentCmd(&azldev.App{})
					cmd.SetArgs(append([]string{
						"render", "test-patch", "-o", "/rendered", "--spec-editor", editor,
					}, test.args...))

					err := cmd.ExecuteContext(testEnv.Env)
					if test.wantError {
						require.ErrorContains(t, err, "component(s) failed to render")
					} else {
						require.NoError(t, err)
					}

					var results []RenderResult
					require.NoError(t, json.Unmarshal([]byte(report.String()), &results),
						"JSON results must still be emitted on strict failure")
					require.Len(t, results, 1)
					result := results[0]
					assert.Equal(t, test.wantStatus, result.Status)
					assert.Equal(t, "test-patch", result.Component)

					if !test.wantFinding {
						assert.Empty(t, result.PatchWarnings)
						assert.NotContains(t, report.String(), `"patchWarnings"`)
					} else {
						require.Len(t, result.PatchWarnings, 1)
						diagnostic := result.PatchWarnings[0]
						assert.Equal(t, "fix.patch", diagnostic.Filename)
						assert.Equal(t, 0, diagnostic.PatchNumber)

						if strings.HasPrefix(test.prep, "%setup -q") {
							assert.Empty(t, diagnostic.Command, "there is no recognized application command")
						} else {
							assert.Equal(t, test.prep, diagnostic.Command)
						}

						assert.NotEmpty(t, diagnostic.Reason)
						assert.Contains(t, report.String(), `"patchNumber": 0`)
						assert.NotContains(t, report.String(), `"line":`)
						assert.NotContains(t, report.String(), `"spec":`)
						assert.NotContains(t, report.String(), `"code":`)
						assert.Equal(t, 1, strings.Count(logs.String(), "Added patch application diagnostic"))

						level := "level=WARN"
						if test.wantStatus == renderStatusError && test.mockError == "" {
							level = "level=ERROR"
						}

						assert.Contains(t, logs.String(), level+` msg="Added patch application diagnostic"`)
					}

					specPath := filepath.Join(result.OutputDir, "test-patch.spec")
					if test.wantStatus == renderStatusOK {
						content, readErr := fileutils.ReadFile(testEnv.TestFS, specPath)
						require.NoError(t, readErr)
						assert.Contains(t, string(content), "\n"+test.prep+"\n")
						assert.Contains(t, string(content), "fix.patch")

						if !test.resolved {
							assert.Equal(t, strings.Count(test.prep, "%patch -P0"),
								strings.Count(string(content), "%patch -P0"),
								"analysis must not insert an application command")
						}
					} else {
						exists, existsErr := fileutils.Exists(testEnv.TestFS, specPath)
						require.NoError(t, existsErr)
						assert.False(t, exists, "strict mode must not publish an unverified spec")
						exists, existsErr = fileutils.Exists(testEnv.TestFS,
							filepath.Join(result.OutputDir, renderErrorMarkerFile))
						require.NoError(t, existsErr)
						assert.True(t, exists, "rejected components use the existing failure marker")
					}
				})
			}
		})
	}
}

func TestRenderPatchWarningsStrictCheckOnlyWithoutDrift(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	addPatchWarningRenderComponent(t, testEnv, "test-patch", "%setup -q", false)

	marker := "/rendered/t/test-patch/" + renderErrorMarkerFile
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, marker,
		[]byte(renderErrorMarkerContent), fileperms.PublicFile))

	results, err := RenderComponents(testEnv.Env, &RenderOptions{
		OutputDir: "/rendered", OutputDirExplicit: true, CheckOnly: true, PatchWarnings: patchWarningsError,
		ComponentFilter: components.ComponentFilter{ComponentNamePatterns: []string{"test-patch"}},
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Changed, "a current failure marker satisfies check-only")
	assert.Equal(t, renderStatusError, results[0].Status)
	require.Len(t, results[0].PatchWarnings, 1)

	content, readErr := fileutils.ReadFile(testEnv.TestFS, marker)
	require.NoError(t, readErr)
	assert.Equal(t, renderErrorMarkerContent, string(content), "check-only must not write output")
	assert.Empty(t, testEnv.CommandsExecuted, "strict rejection must happen before batch mock processing")
}

func TestRenderPatchWarningsStrictWithoutAdditions(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	addPatchWarningRenderComponent(t, testEnv, "test-patch", "%setup -q", false)
	config := testEnv.Config.Components["test-patch"]
	config.Overlays = nil
	testEnv.Config.Components["test-patch"] = config
	stubPatchWarningRenderMock(t, testEnv, "")

	results, err := RenderComponents(testEnv.Env, &RenderOptions{
		OutputDir: "/rendered", OutputDirExplicit: true, PatchWarnings: patchWarningsError,
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, renderStatusOK, results[0].Status)
	assert.Empty(t, results[0].PatchWarnings)
}

func TestRenderPatchWarningsCheckOnlyDrift(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	addPatchWarningRenderComponent(t, testEnv, "test-patch", "%autosetup -p%{strip}", false)

	results, err := RenderComponents(testEnv.Env, &RenderOptions{
		OutputDir: "/rendered", OutputDirExplicit: true, CheckOnly: true, PatchWarnings: patchWarningsError,
		ComponentFilter: components.ComponentFilter{ComponentNamePatterns: []string{"test-patch"}},
	})
	require.ErrorContains(t, err, "rendered output is stale")
	require.Len(t, results, 1)
	assert.Equal(t, renderStatusError, results[0].Status)
	assert.True(t, results[0].Changed)

	exists, err := fileutils.Exists(testEnv.TestFS, "/rendered")
	require.NoError(t, err)
	assert.False(t, exists, "check-only must not create output or failure markers")
	assert.Empty(t, testEnv.CommandsExecuted, "reject before batch RPM processing")
}

func TestRenderPatchWarningsMixedComponents(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	addPatchWarningRenderComponent(t, testEnv, "warning", "%setup -q", false)
	addPatchWarningRenderComponent(t, testEnv, "applied", "%forgeautosetup -p1", false)
	stubPatchWarningRenderMock(t, testEnv, "")

	results, err := RenderComponents(testEnv.Env, &RenderOptions{
		OutputDir: "/rendered", OutputDirExplicit: true, PatchWarnings: patchWarningsError,
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "applied", results[0].Component)
	assert.Equal(t, renderStatusOK, results[0].Status)
	assert.Empty(t, results[0].PatchWarnings)
	assert.Equal(t, "warning", results[1].Component)
	assert.Equal(t, renderStatusError, results[1].Status)
	require.Len(t, results[1].PatchWarnings, 1)
	assert.Contains(t, results[1].PatchWarnings[0].Reason, "no recognized command")
}

func TestRenderPatchWarningsSurvivePreparationError(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	addPatchWarningRenderComponent(t, testEnv, "test-patch", "%setup -q", false)

	original := newSourcePreparer
	newSourcePreparer = func(
		manager sourceproviders.SourceManager, fileSystem opctx.FS, events opctx.EventListener,
		dryRunnable opctx.DryRunnable, options ...sources.PreparerOption,
	) (sources.SourcePreparer, error) {
		preparer, err := original(manager, fileSystem, events, dryRunnable, options...)
		if err != nil {
			return nil, err
		}

		return &patchWarningFailingPreparer{SourcePreparer: preparer}, nil
	}

	t.Cleanup(func() { newSourcePreparer = original })

	results, err := RenderComponents(testEnv.Env, &RenderOptions{
		OutputDir: "/rendered", OutputDirExplicit: true, PatchWarnings: patchWarningsError,
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, renderStatusError, results[0].Status)
	assert.Contains(t, results[0].Error, "failure after overlay analysis")
	require.Len(t, results[0].PatchWarnings, 1)
	assert.Contains(t, results[0].PatchWarnings[0].Reason, "no recognized command")
}

type patchWarningFailingPreparer struct {
	sources.SourcePreparer
}

func (p *patchWarningFailingPreparer) PrepareSources(
	ctx context.Context, comp components.Component, outputDir string, applyOverlays bool,
) error {
	if err := p.SourcePreparer.PrepareSources(ctx, comp, outputDir, applyOverlays); err != nil {
		return err
	}

	return errors.New("failure after overlay analysis")
}

func TestRenderPatchWarningsCancelledFinish(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	comp := components_testutils.NewMockComponent(gomock.NewController(t))
	comp.EXPECT().GetName().AnyTimes().Return("cancelled")

	warnings := []spec.PatchApplicationDiagnostic{{Filename: "fix.patch", Reason: "application cannot be verified"}}
	prepared := &preparedComponent{comp: comp, compOutputDir: "/rendered/c/cancelled", patchWarnings: warnings}
	cancelledEnv, cancel := testEnv.Env.WithCancel()
	cancel()

	result := finishOneComponent(cancelledEnv, testEnv.Env, prepared, nil, "/staging", false, false)
	assert.Equal(t, renderStatusCancelled, result.Status)
	assert.Equal(t, warnings, result.PatchWarnings)

	results := make([]*RenderResult, 1)
	parallelFinish(cancelledEnv, []*preparedComponent{prepared}, nil, results, "/staging", false, false)
	require.NotNil(t, results[0])
	assert.Equal(t, renderStatusCancelled, results[0].Status)
	assert.Equal(t, warnings, results[0].PatchWarnings)
}

func addPatchWarningRenderComponent(t *testing.T, env *testutils.TestEnv, name, prep string, resolved bool) {
	t.Helper()

	specPath := filepath.Join("/project", "specs", name, name+".spec")
	lines := []string{
		"Name: " + name, "Version: 1", "Release: 1", "Summary: patch fixture",
		"%description", "fixture", "%prep", prep, "%build", "make",
	}
	require.NoError(t, fileutils.WriteFile(env.TestFS, specPath,
		[]byte(strings.Join(lines, "\n")+"\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(env.TestFS, "/project/fix.patch",
		[]byte("--- a/lib/file.c\n+++ b/lib/file.c\n"), fileperms.PublicFile))

	overlays := []projectconfig.ComponentOverlay{{
		Type: projectconfig.ComponentOverlayAddPatch, Source: "/project/fix.patch",
	}}
	if resolved {
		overlays = append(overlays, projectconfig.ComponentOverlay{
			Type: projectconfig.ComponentOverlayAppendSpecLines, SectionName: "%prep",
			Lines: []string{"%patch -P0 -p1"},
		})
	}

	env.Config.Components[name] = projectconfig.ComponentConfig{
		Name: name, Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal, Path: specPath},
		Build:    projectconfig.ComponentBuildConfig{With: []string{"tests"}},
		Overlays: overlays,
	}
}

func stubPatchWarningRenderMock(t *testing.T, env *testutils.TestEnv, mockError string) {
	t.Helper()

	env.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)
	env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		if !strings.Contains(strings.Join(cmd.Args, " "), "render_process.py") {
			return nil
		}

		return afero.Walk(env.TestFS, env.Env.WorkDir(), func(path string, info fs.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if info.IsDir() || info.Name() != "inputs.json" {
				return nil
			}

			content, err := fileutils.ReadFile(env.TestFS, path)
			if err != nil {
				return err
			}

			var inputs []struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(content, &inputs); err != nil {
				return err
			}

			var results []map[string]any
			for _, input := range inputs {
				result := map[string]any{"name": input.Name, "specFiles": "Patch0: fix.patch", "error": nil}
				if mockError != "" {
					result["error"] = mockError
				}

				results = append(results, result)
			}

			output, err := json.Marshal(results)
			if err != nil {
				return err
			}

			return fileutils.WriteFile(env.TestFS, filepath.Join(filepath.Dir(path), "results.json"),
				output, fileperms.PublicFile)
		})
	}
}
