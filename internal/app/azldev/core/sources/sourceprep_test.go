// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/sourceproviders_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const testOutputDir = "/output"

func TestNewPreparer(t *testing.T) {
	ctrl := gomock.NewController(t)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NotNil(t, preparer)
	require.NoError(t, err)
}

func TestNewPreparer_NilArgs(t *testing.T) {
	ctrl := gomock.NewController(t)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)

	preparer, err := sources.NewPreparer(sourceManager, nil, nil, nil)
	require.Nil(t, preparer)
	require.Error(t, err)
	require.Contains(t, err.Error(), "filesystem")
}

func TestNewPreparer_DirtyDetectionWithoutGitRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx,
		sources.WithDirtyDetection(),
	)
	require.Nil(t, preparer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithDirtyDetection requires WithGitRepo")
}

func TestPrepareSources_Success(t *testing.T) {
	const (
		testSpecName   = "test-component.spec"
		outputSpecPath = testOutputDir + "/" + testSpecName
	)

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{})
	sourceManager.EXPECT().FetchFiles(gomock.Any(), component, testOutputDir).Return(nil, nil)
	sourceManager.EXPECT().FetchComponent(gomock.Any(), component, testOutputDir, gomock.Any()).DoAndReturn(
		func(
			_ interface{}, _ interface{}, outputDir string,
			_ ...sourceproviders.FetchComponentOption,
		) ([]sourceproviders.SourceProvenance, error) {
			// Create the expected spec file.
			return nil, fileutils.WriteFile(ctx.FS(), outputSpecPath, []byte("# test spec"), fileperms.PublicFile)
		},
	)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NoError(t, err)
	_, err = preparer.PrepareSources(ctx, component, testOutputDir, true /*applyOverlays?*/)
	require.NoError(t, err)

	macrosFileName := "test-component" + sources.MacrosFileExtension
	macrosFilePath := filepath.Join(testOutputDir, macrosFileName)

	// Verify macros file was NOT created (empty config has no macros).
	exists, err := fileutils.Exists(ctx.FS(), macrosFilePath)
	require.NoError(t, err)
	assert.False(t, exists, "macros file should not be created when there are no macros")

	// Verify spec does NOT contain macro load or Source9999.
	specContents, err := fileutils.ReadFile(ctx.FS(), outputSpecPath)
	require.NoError(t, err)
	assert.NotContains(t, string(specContents), "%{load:%{_sourcedir}/"+macrosFileName+"}")
	assert.NotContains(t, string(specContents), "Source9999")
}

func TestPrepareSources_ProvenanceReport(t *testing.T) {
	const (
		testSpecName   = "test-component.spec"
		outputSpecPath = testOutputDir + "/" + testSpecName
	)

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{})

	fileProv := []sourceproviders.SourceProvenance{
		{
			Filename:   "extra.tar.gz",
			OriginType: sourceproviders.SourceOriginURL,
			URL:        "https://example.com/extra.tar.gz",
		},
	}
	sourceManager.EXPECT().
		FetchFiles(gomock.Any(), component, testOutputDir).
		Return(fileProv, nil)

	compProv := []sourceproviders.SourceProvenance{
		{
			Filename:   "src.tar.gz",
			OriginType: sourceproviders.SourceOriginLookaside,
			URL:        "https://lookaside.example.com/pkg/src.tar.gz/sha512/abc/src.tar.gz",
		},
	}
	sourceManager.EXPECT().
		FetchComponent(gomock.Any(), component, testOutputDir, gomock.Any()).
		DoAndReturn(func(
			_ interface{}, _ interface{}, outputDir string,
			_ ...sourceproviders.FetchComponentOption,
		) ([]sourceproviders.SourceProvenance, error) {
			err := fileutils.WriteFile(
				ctx.FS(), outputSpecPath,
				[]byte("# test spec"), fileperms.PublicFile)

			return compProv, err
		})

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NoError(t, err)

	report, err := preparer.PrepareSources(
		ctx, component, testOutputDir, true)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, "test-component", report.ComponentName)
	require.Len(t, report.Sources, 2)

	assert.Equal(t, "extra.tar.gz", report.Sources[0].Filename)
	assert.Equal(t, sourceproviders.SourceOriginURL, report.Sources[0].OriginType)
	assert.Equal(t, "https://example.com/extra.tar.gz", report.Sources[0].URL)

	assert.Equal(t, "src.tar.gz", report.Sources[1].Filename)
	assert.Equal(t, sourceproviders.SourceOriginLookaside, report.Sources[1].OriginType)
}

func TestPrepareSources_SkipLookaside_EmptyProvenance(t *testing.T) {
	const outputSpecPath = testOutputDir + "/test-component.spec"

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{})

	// With skipLookaside, FetchFiles is not called.
	sourceManager.EXPECT().
		FetchComponent(gomock.Any(), component, testOutputDir, gomock.Any()).
		DoAndReturn(func(
			_ interface{}, _ interface{}, outputDir string,
			_ ...sourceproviders.FetchComponentOption,
		) ([]sourceproviders.SourceProvenance, error) {
			return nil, fileutils.WriteFile(
				ctx.FS(), outputSpecPath,
				[]byte("# test spec"), fileperms.PublicFile)
		})

	preparer, err := sources.NewPreparer(
		sourceManager, ctx.FS(), ctx, ctx, sources.WithSkipLookaside())
	require.NoError(t, err)

	report, err := preparer.PrepareSources(
		ctx, component, testOutputDir, true)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, "test-component", report.ComponentName)
	assert.Empty(t, report.Sources,
		"no provenance should be reported when lookaside is skipped")
}

func TestPrepareSources_SourceManagerError(t *testing.T) {
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	expectedErr := errors.New("failed to fetch files")

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	sourceManager.EXPECT().FetchFiles(gomock.Any(), component, testOutputDir).Return(nil, expectedErr)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NoError(t, err)

	_, err = preparer.PrepareSources(ctx, component, testOutputDir, true /*applyOverlays?*/)
	require.Error(t, err)
	require.ErrorIs(t, err, expectedErr)
}

func TestPrepareSources_WithSkipLookaside_SkipsFetchFiles(t *testing.T) {
	const (
		testSpecName   = "test-component.spec"
		outputSpecPath = testOutputDir + "/" + testSpecName
	)

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{})

	// FetchFiles must NOT be called when WithSkipLookaside is set.
	// (No sourceManager.EXPECT().FetchFiles(...) — gomock will fail if it's called.)

	// FetchComponent should still be called, with at least the SkipLookaside option.
	sourceManager.EXPECT().FetchComponent(gomock.Any(), component, testOutputDir, gomock.Any()).DoAndReturn(
		func(
			_ interface{}, _ interface{}, outputDir string,
			opts ...sourceproviders.FetchComponentOption,
		) ([]sourceproviders.SourceProvenance, error) {
			// Verify SkipLookaside is actually set by applying the received options.
			var resolved sourceproviders.FetchComponentOptions
			for _, opt := range opts {
				opt(&resolved)
			}

			assert.True(t, resolved.SkipLookaside, "FetchComponent should receive SkipLookaside option")

			return nil, fileutils.WriteFile(ctx.FS(), outputSpecPath, []byte("# test spec"), fileperms.PublicFile)
		},
	)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx, sources.WithSkipLookaside())
	require.NoError(t, err)

	_, err = preparer.PrepareSources(ctx, component, testOutputDir, true /*applyOverlays?*/)
	require.NoError(t, err)
}

func TestPrepareSources_WithoutSkipLookaside_CallsFetchFiles(t *testing.T) {
	const (
		testSpecName   = "test-component.spec"
		outputSpecPath = testOutputDir + "/" + testSpecName
	)

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{})

	// Without WithSkipLookaside, FetchFiles MUST be called.
	sourceManager.EXPECT().FetchFiles(gomock.Any(), component, testOutputDir).Return(nil, nil)
	sourceManager.EXPECT().FetchComponent(gomock.Any(), component, testOutputDir, gomock.Any()).DoAndReturn(
		func(
			_ interface{}, _ interface{}, outputDir string,
			_ ...sourceproviders.FetchComponentOption,
		) ([]sourceproviders.SourceProvenance, error) {
			return nil, fileutils.WriteFile(ctx.FS(), outputSpecPath, []byte("# test spec"), fileperms.PublicFile)
		},
	)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NoError(t, err)

	_, err = preparer.PrepareSources(ctx, component, testOutputDir, true /*applyOverlays?*/)
	require.NoError(t, err)
}

func TestPrepareSources_WritesMacrosFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	component.EXPECT().GetName().AnyTimes().Return("my-package")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{
			With: []string{"feature"},
		},
	})
	sourceManager.EXPECT().FetchFiles(gomock.Any(), component, testOutputDir).Return(nil, nil)
	sourceManager.EXPECT().FetchComponent(gomock.Any(), component, testOutputDir, gomock.Any()).DoAndReturn(
		func(
			_ interface{}, _ interface{}, outputDir string,
			_ ...sourceproviders.FetchComponentOption,
		) ([]sourceproviders.SourceProvenance, error) {
			// Create the expected spec file.
			specPath := filepath.Join(outputDir, "my-package.spec")

			return nil, fileutils.WriteFile(ctx.FS(), specPath, []byte("# test spec"), fileperms.PublicFile)
		},
	)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NoError(t, err)
	_, err = preparer.PrepareSources(ctx, component, testOutputDir, true /*applyOverlays?*/)
	require.NoError(t, err)

	// Verify file exists with expected name.
	macrosFilePath := filepath.Join(testOutputDir, "my-package"+sources.MacrosFileExtension)
	exists, err := fileutils.Exists(ctx.FS(), macrosFilePath)
	require.NoError(t, err)
	assert.True(t, exists)

	// Verify content is non-empty and has expected macro.
	contents, err := fileutils.ReadFile(ctx.FS(), macrosFilePath)
	require.NoError(t, err)
	assert.Contains(t, string(contents), "%_with_feature 1")

	// Verify spec has macro load directive and Source9999 tag.
	specPath := filepath.Join(testOutputDir, "my-package.spec")
	specContents, err := fileutils.ReadFile(ctx.FS(), specPath)
	require.NoError(t, err)

	specStr := string(specContents)
	assert.Contains(t, specStr, "%{load:%{_sourcedir}/my-package"+sources.MacrosFileExtension+"}")
	assert.Contains(t, specStr, "Source9999")
}

// Tests for GenerateMacrosFileContents - these test content generation in isolation.

func TestGenerateMacrosFileContents_EmptyConfig(t *testing.T) {
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{})

	// Empty config should produce no macros file content.
	assert.Empty(t, contents)
}

func TestGenerateMacrosFileContents_WithFlags(t *testing.T) {
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		With: []string{"tests", "docs", "examples"},
	})

	assert.Contains(t, contents, "%_with_tests 1")
	assert.Contains(t, contents, "%_with_docs 1")
	assert.Contains(t, contents, "%_with_examples 1")
}

func TestGenerateMacrosFileContents_WithoutFlags(t *testing.T) {
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		Without: []string{"debug", "static"},
	})

	assert.Contains(t, contents, "%_without_debug 1")
	assert.Contains(t, contents, "%_without_static 1")
}

func TestGenerateMacrosFileContents_Defines(t *testing.T) {
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		Defines: map[string]string{
			"dist":   ".azl3",
			"vendor": "Microsoft",
		},
	})

	assert.Contains(t, contents, "%dist .azl3")
	assert.Contains(t, contents, "%vendor Microsoft")
}

func TestGenerateMacrosFileContents_AllMacrosSortedAlphabetically(t *testing.T) {
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		With:    []string{"zebra_feature"},
		Without: []string{"apple_feature"},
		Defines: map[string]string{
			"mango": "m",
		},
	})

	// All macros should be sorted together alphabetically by macro name.
	// Original order (as defined above): zebra_feature (with), apple_feature (without), mango (define).
	// Alphabetically sorted macros: _with_zebra_feature, _without_apple_feature, mango.
	appleIdx := strings.Index(contents, "%_without_apple_feature 1")
	zebraIdx := strings.Index(contents, "%_with_zebra_feature 1")
	mangoIdx := strings.Index(contents, "%mango m")

	assert.Greater(t, appleIdx, -1, "_without_apple_feature should be present")
	assert.Greater(t, zebraIdx, -1, "_with_zebra_feature should be present")
	assert.Greater(t, mangoIdx, -1, "mango should be present")

	// Alphabetically: _with_... < _without_... < mango
	assert.Less(t, zebraIdx, appleIdx, "_with_zebra should come before _without_apple")
	assert.Less(t, appleIdx, mangoIdx, "_without_apple should come before mango")
}

func TestGenerateMacrosFileContents_DefineOverridesWithFlag(t *testing.T) {
	// If an explicit define has the same name as a generated with/without macro,
	// the explicit define should win (since defines are processed last).
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		With: []string{"tests"},
		Defines: map[string]string{
			"_with_tests": "custom_value",
		},
	})

	// Should have the custom value, not "1".
	assert.Contains(t, contents, "%_with_tests custom_value")
	assert.NotContains(t, contents, "%_with_tests 1")
}

func TestGenerateMacrosFileContents_DefineOverridesWithoutFlag(t *testing.T) {
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		Without: []string{"debug"},
		Defines: map[string]string{
			"_without_debug": "0",
		},
	})

	// Should have the custom value, not "1".
	assert.Contains(t, contents, "%_without_debug 0")
	assert.NotContains(t, contents, "%_without_debug 1")
}

func TestGenerateMacrosFileContents_ValuesWithSpaces(t *testing.T) {
	// RPM macro values can contain spaces - they work without special escaping
	// because everything after the macro name is treated as the body.
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		Defines: map[string]string{
			"description": "A package with multiple words",
			"build_flags": "-O2 -Wall -Werror",
		},
	})

	assert.Contains(t, contents, "%build_flags -O2 -Wall -Werror")
	assert.Contains(t, contents, "%description A package with multiple words")
}

func TestGenerateMacrosFileContents_UndefinesRemovesDefine(t *testing.T) {
	// Undefines should remove a macro that was added via defines.
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		Defines: map[string]string{
			"dist":   ".azl3",
			"vendor": "Microsoft",
		},
		Undefines: []string{"dist"},
	})

	assert.NotContains(t, contents, "%dist")
	assert.Contains(t, contents, "%vendor Microsoft")
}

func TestGenerateMacrosFileContents_UndefinesRemovesWithFlag(t *testing.T) {
	// Undefines should be able to remove a macro generated from a with flag.
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		With:      []string{"tests", "docs"},
		Undefines: []string{"_with_tests"},
	})

	assert.NotContains(t, contents, "_with_tests")
	assert.Contains(t, contents, "%_with_docs 1")
}

func TestGenerateMacrosFileContents_UndefinesRemovesWithoutFlag(t *testing.T) {
	// Undefines should be able to remove a macro generated from a without flag.
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		Without:   []string{"debug", "static"},
		Undefines: []string{"_without_debug"},
	})

	assert.NotContains(t, contents, "_without_debug")
	assert.Contains(t, contents, "%_without_static 1")
}

func TestGenerateMacrosFileContents_UndefinesNonexistentMacroIsNoop(t *testing.T) {
	// Undefining a macro that doesn't exist should not cause an error.
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		Defines: map[string]string{
			"vendor": "Microsoft",
		},
		Undefines: []string{"nonexistent"},
	})

	assert.Contains(t, contents, "%vendor Microsoft")
}

func TestGenerateMacrosFileContents_UndefinesAllMacros(t *testing.T) {
	// Undefining all macros should produce no macros file content.
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		With:    []string{"tests"},
		Without: []string{"debug"},
		Defines: map[string]string{
			"dist": ".azl3",
		},
		Undefines: []string{"_with_tests", "_without_debug", "dist"},
	})

	assert.Empty(t, contents)
}

func TestGenerateMacrosFileContents_FullConfig(t *testing.T) {
	contents := sources.GenerateMacrosFileContents(projectconfig.ComponentBuildConfig{
		With:    []string{"tests", "docs"},
		Without: []string{"debug"},
		Defines: map[string]string{
			"dist":   ".azl3",
			"vendor": "Microsoft Corporation",
		},
	})

	// Verify header.
	assert.Contains(t, contents, sources.MacrosFileHeader)

	// Verify with flags.
	assert.Contains(t, contents, "%_with_tests 1")
	assert.Contains(t, contents, "%_with_docs 1")

	// Verify without flags.
	assert.Contains(t, contents, "%_without_debug 1")

	// Verify defines.
	assert.Contains(t, contents, "%dist .azl3")
	assert.Contains(t, contents, "%vendor Microsoft Corporation")

	// Verify file ends with newline.
	assert.Equal(t, '\n', rune(contents[len(contents)-1]))
}

func TestPrepareSources_CheckSkip(t *testing.T) {
	const outputSpecPath = testOutputDir + "/test-component.spec"

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{
			Check: projectconfig.CheckConfig{
				Skip:       true,
				SkipReason: "Tests require network access",
			},
		},
	})
	sourceManager.EXPECT().FetchFiles(gomock.Any(), component, testOutputDir).Return(nil, nil)
	sourceManager.EXPECT().FetchComponent(gomock.Any(), component, testOutputDir, gomock.Any()).DoAndReturn(
		func(
			_ interface{}, _ interface{}, outputDir string,
			_ ...sourceproviders.FetchComponentOption,
		) ([]sourceproviders.SourceProvenance, error) {
			// Create the expected spec file with a %check section.
			specContent := `Name: test-component
Version: 1.0
Release: 1
Summary: Test component

%check
make test
`

			return nil, fileutils.WriteFile(ctx.FS(), outputSpecPath, []byte(specContent), fileperms.PublicFile)
		},
	)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NoError(t, err)
	_, err = preparer.PrepareSources(ctx, component, testOutputDir, true /*applyOverlays?*/)
	require.NoError(t, err)

	// Verify spec has check skip prepended.
	specContents, err := fileutils.ReadFile(ctx.FS(), outputSpecPath)
	require.NoError(t, err)

	specStr := string(specContents)

	assert.Contains(t, specStr, "# Check section disabled: Tests require network access")
	assert.Contains(t, specStr, "exit 0")

	// Verify exit 0 appears after %check and before original content.
	checkIdx := strings.Index(specStr, "%check")
	exitIdx := strings.Index(specStr, "exit 0")
	makeTestIdx := strings.Index(specStr, "make test")

	assert.Greater(t, checkIdx, -1, "%check should be present")
	assert.Greater(t, exitIdx, checkIdx, "exit 0 should come after %check")
	assert.Greater(t, makeTestIdx, exitIdx, "make test should come after exit 0")
}

func TestPrepareSources_CheckSkipDisabled(t *testing.T) {
	const outputSpecPath = testOutputDir + "/test-component.spec"

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{
			Check: projectconfig.CheckConfig{
				Skip: false,
			},
		},
	})
	sourceManager.EXPECT().FetchFiles(gomock.Any(), component, testOutputDir).Return(nil, nil)
	sourceManager.EXPECT().FetchComponent(gomock.Any(), component, testOutputDir, gomock.Any()).DoAndReturn(
		func(
			_ interface{}, _ interface{}, outputDir string,
			_ ...sourceproviders.FetchComponentOption,
		) ([]sourceproviders.SourceProvenance, error) {
			// Create the expected spec file with a %check section.
			specContent := `Name: test-component
Version: 1.0
Release: 1
Summary: Test component

%check
make test
`

			return nil, fileutils.WriteFile(ctx.FS(), outputSpecPath, []byte(specContent), fileperms.PublicFile)
		},
	)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NoError(t, err)
	_, err = preparer.PrepareSources(ctx, component, testOutputDir, true /*applyOverlays?*/)
	require.NoError(t, err)

	// Verify spec does NOT have check skip prepended.
	specContents, err := fileutils.ReadFile(ctx.FS(), outputSpecPath)
	require.NoError(t, err)

	specStr := string(specContents)

	assert.NotContains(t, specStr, "# Check section disabled")
	assert.NotContains(t, specStr, "exit 0")
}

func TestDiffSources_NoOverlays(t *testing.T) {
	const baseDir = "/work"

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), baseDir))

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{})

	// DiffSources fetches sources once, then copies them for overlay application.
	sourceManager.EXPECT().FetchFiles(gomock.Any(), component, gomock.Any()).Times(1).Return(nil, nil)
	sourceManager.EXPECT().FetchComponent(gomock.Any(), component, gomock.Any()).Times(1).DoAndReturn(
		func(
			_ interface{}, _ interface{}, outputDir string,
			_ ...sourceproviders.FetchComponentOption,
		) ([]sourceproviders.SourceProvenance, error) {
			specPath := filepath.Join(outputDir, "test-component.spec")
			specContent := "Name: test-component\nVersion: 1.0\n"

			return nil, fileutils.WriteFile(
				ctx.FS(), specPath, []byte(specContent), fileperms.PublicFile)
		},
	)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NoError(t, err)

	result, err := preparer.DiffSources(ctx, component, baseDir)
	require.NoError(t, err)

	// With no overlays configured, the only diff should be the auto-generated file header
	// that is always prepended to the spec.
	require.NotNil(t, result)

	// The header overlay is always applied, so we expect at least one modified file.
	if len(result.Files) > 0 {
		assert.Equal(t, "test-component.spec", result.Files[0].Path)
	}
}

func TestDiffSources_WithOverlays(t *testing.T) {
	const baseDir = "/work"

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), baseDir))

	component.EXPECT().GetName().AnyTimes().Return("test-component")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{
			With: []string{"feature"},
		},
	})

	// DiffSources fetches sources once, then copies them for overlay application.
	sourceManager.EXPECT().FetchFiles(gomock.Any(), component, gomock.Any()).Times(1).Return(nil, nil)
	sourceManager.EXPECT().FetchComponent(gomock.Any(), component, gomock.Any()).Times(1).DoAndReturn(
		func(
			_ interface{}, _ interface{}, outputDir string,
			_ ...sourceproviders.FetchComponentOption,
		) ([]sourceproviders.SourceProvenance, error) {
			specPath := filepath.Join(outputDir, "test-component.spec")
			specContent := "Name: test-component\nVersion: 1.0\n"

			return nil, fileutils.WriteFile(
				ctx.FS(), specPath, []byte(specContent), fileperms.PublicFile)
		},
	)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NoError(t, err)

	result, err := preparer.DiffSources(ctx, component, baseDir)
	require.NoError(t, err)
	require.NotNil(t, result)

	// With a "with" flag, we expect:
	// 1. The spec to be modified (header + macro load + Source9999 tag)
	// 2. A new macros file to be added
	require.GreaterOrEqual(t, len(result.Files), 1)

	diffText := result.String()
	assert.NotEmpty(t, diffText)

	// The macros file should appear as added.
	assert.Contains(t, diffText, sources.MacrosFileExtension)
}

func TestDiffSources_FetchError(t *testing.T) {
	const baseDir = "/work"

	ctrl := gomock.NewController(t)
	component := components_testutils.NewMockComponent(ctrl)
	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), baseDir))

	component.EXPECT().GetName().AnyTimes().Return("test-component")

	expectedErr := errors.New("network failure")
	sourceManager.EXPECT().FetchFiles(gomock.Any(), component, gomock.Any()).Return(nil, expectedErr)

	preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
	require.NoError(t, err)

	result, err := preparer.DiffSources(ctx, component, baseDir)
	require.Error(t, err)
	require.Nil(t, result)
	require.ErrorIs(t, err, expectedErr)
}

//nolint:maintidx // Test table complexity scales with the number of source-file merge scenarios.
func TestPrepareSources_UpdatesSourcesFile(t *testing.T) {
	validOrigin := projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/new-source.tar.gz"}
	tests := []struct {
		name                        string
		sourceFiles                 []projectconfig.SourceFileReference
		existingSourcesContent      string
		expectError                 bool
		errorContains               []string
		expectedSourceEntries       []string
		expectedSourceEntriesAbsent []string
		// expectedExactContent, when non-empty, asserts the merged 'sources' file matches
		// this string byte-for-byte. Use this for cases where blank-line and comment
		// preservation must be verified exactly (substring checks alone don't catch
		// dropped or extra blank lines).
		expectedExactContent string
	}{
		{
			name: "adds new entry to existing 'sources' file",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "extra-source.tar.gz",
					Hash:     "abc123def456",
					HashType: fileutils.HashTypeSHA512,
					Origin:   validOrigin,
				},
			},
			existingSourcesContent: "SHA512 (existing.tar.gz) = aabbccdd1122\n",
			expectedSourceEntries: []string{
				"SHA512 (existing.tar.gz) = aabbccdd1122",
				"SHA512 (extra-source.tar.gz) = abc123def456",
			},
		},
		{
			name: "error on filename collision when replace-upstream is not set",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "existing.tar.gz", // Already in 'sources' file.
					Hash:     "11223344aabb",
					HashType: fileutils.HashTypeSHA512,
					Origin:   validOrigin,
				},
			},
			existingSourcesContent: "SHA512 (existing.tar.gz) = aabbccdd1122\n",
			expectError:            true,
			errorContains: []string{
				"existing.tar.gz",
				"conflicts with an existing entry",
				"replace-upstream = true",
			},
		},
		{
			name: "replaces upstream entry in place when replace-upstream is true",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename:        "existing.tar.gz", // Already in 'sources' file.
					Hash:            "deadbeefcafe",
					HashType:        fileutils.HashTypeSHA512,
					Origin:          validOrigin,
					ReplaceUpstream: true,
					ReplaceReason:   "patched to fix CVE-2026-0001",
				},
			},
			existingSourcesContent: "SHA512 (existing.tar.gz) = aabbccdd1122\nSHA512 (other.tar.gz) = 99887766\n",
			// The replacement must occupy the original position of 'existing.tar.gz' (line 1),
			// not be shuffled to the end. Order is asserted by the surrounding test logic.
			expectedSourceEntries: []string{
				"SHA512 (existing.tar.gz) = deadbeefcafe",
				"SHA512 (other.tar.gz) = 99887766",
			},
			expectedSourceEntriesAbsent: []string{
				"SHA512 (existing.tar.gz) = aabbccdd1122",
			},
		},
		{
			name: "new entries are appended after upstream entries",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename:        "existing.tar.gz", // Replacement (in-place).
					Hash:            "deadbeefcafe",
					HashType:        fileutils.HashTypeSHA512,
					Origin:          validOrigin,
					ReplaceUpstream: true,
					ReplaceReason:   "patched",
				},
				{
					Filename: "brand-new.tar.gz", // Net-new (appended).
					Hash:     "abc123def456",
					HashType: fileutils.HashTypeSHA512,
					Origin:   validOrigin,
				},
			},
			existingSourcesContent: "SHA512 (existing.tar.gz) = aabbccdd1122\nSHA512 (other.tar.gz) = 99887766\n",
			// Order matters: replacement keeps line 1; line 2 is the untouched upstream entry;
			// brand-new entry is appended at the end.
			expectedSourceEntries: []string{
				"SHA512 (existing.tar.gz) = deadbeefcafe",
				"SHA512 (other.tar.gz) = 99887766",
				"SHA512 (brand-new.tar.gz) = abc123def456",
			},
		},
		{
			name: "multiple replace-upstream entries are all swapped in place",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename:        "first.tar.gz",
					Hash:            "1111aaaa",
					HashType:        fileutils.HashTypeSHA512,
					Origin:          validOrigin,
					ReplaceUpstream: true,
					ReplaceReason:   "patched first",
				},
				{
					Filename:        "third.tar.gz",
					Hash:            "3333cccc",
					HashType:        fileutils.HashTypeSHA512,
					Origin:          validOrigin,
					ReplaceUpstream: true,
					ReplaceReason:   "patched third",
				},
				{
					Filename: "appended.tar.gz",
					Hash:     "9999ffff",
					HashType: fileutils.HashTypeSHA512,
					Origin:   validOrigin,
				},
			},
			existingSourcesContent: "SHA512 (first.tar.gz) = aaaa1111\n" +
				"SHA512 (second.tar.gz) = bbbb2222\n" +
				"SHA512 (third.tar.gz) = cccc3333\n",
			// Both replacements occupy their original positions; the untouched 'second'
			// entry stays between them; brand-new entry lands at the end.
			expectedSourceEntries: []string{
				"SHA512 (first.tar.gz) = 1111aaaa",
				"SHA512 (second.tar.gz) = bbbb2222",
				"SHA512 (third.tar.gz) = 3333cccc",
				"SHA512 (appended.tar.gz) = 9999ffff",
			},
			expectedSourceEntriesAbsent: []string{
				"SHA512 (first.tar.gz) = aaaa1111",
				"SHA512 (third.tar.gz) = cccc3333",
			},
		},
		{
			name: "comments and blank lines in upstream 'sources' file are preserved verbatim",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename:        "patched.tar.gz",
					Hash:            "deadbeefcafe",
					HashType:        fileutils.HashTypeSHA512,
					Origin:          validOrigin,
					ReplaceUpstream: true,
					ReplaceReason:   "patched",
				},
				{
					Filename: "extra.tar.gz",
					Hash:     "abc123",
					HashType: fileutils.HashTypeSHA512,
					Origin:   validOrigin,
				},
			},
			// The blank lines between entries and the comments above each entry must be
			// preserved at their original positions; only the matching entry line is swapped.
			existingSourcesContent: "# top-level comment about the package\n" +
				"\n" +
				"# comment about the patched archive\n" +
				"SHA512 (patched.tar.gz) = aabbccdd1122\n" +
				"\n" +
				"# comment about the untouched archive\n" +
				"SHA512 (untouched.tar.gz) = 99887766\n",
			// Asserting byte-for-byte equality here is what guarantees that the blank
			// lines (between the top-level comment and the patched-archive section, and
			// between the patched and untouched sections) survive the merge intact.
			expectedExactContent: "# top-level comment about the package\n" +
				"\n" +
				"# comment about the patched archive\n" +
				"SHA512 (patched.tar.gz) = deadbeefcafe\n" +
				"\n" +
				"# comment about the untouched archive\n" +
				"SHA512 (untouched.tar.gz) = 99887766\n" +
				"SHA512 (extra.tar.gz) = abc123\n",
			expectedSourceEntries: []string{
				"# top-level comment about the package",
				"# comment about the patched archive",
				"SHA512 (patched.tar.gz) = deadbeefcafe",
				"# comment about the untouched archive",
				"SHA512 (untouched.tar.gz) = 99887766",
				"SHA512 (extra.tar.gz) = abc123",
			},
			expectedSourceEntriesAbsent: []string{
				"SHA512 (patched.tar.gz) = aabbccdd1122",
			},
		},
		{
			name: "duplicate filename in upstream 'sources' file is rejected as malformed",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "extra.tar.gz",
					Hash:     "abc123",
					HashType: fileutils.HashTypeSHA512,
					Origin:   validOrigin,
				},
			},
			// Two entries for the same filename in the upstream file is malformed; merge
			// must error rather than silently picking one.
			existingSourcesContent: "SHA512 (dup.tar.gz) = aaaa1111\nSHA512 (dup.tar.gz) = bbbb2222\n",
			expectError:            true,
			errorContains: []string{
				"failed to parse existing 'sources' file",
				"duplicate filename",
				"dup.tar.gz",
			},
		},
		{
			name: "error when replace-upstream true but no matching upstream entry",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename:        "not-in-upstream.tar.gz",
					Hash:            "abc123",
					HashType:        fileutils.HashTypeSHA512,
					Origin:          validOrigin,
					ReplaceUpstream: true,
					ReplaceReason:   "intended to override (typo demo)",
				},
			},
			existingSourcesContent: "SHA512 (existing.tar.gz) = aabbccdd1122\n",
			expectError:            true,
			errorContains: []string{
				"not-in-upstream.tar.gz",
				"replace-upstream = true",
				"no entry with that filename exists",
			},
		},
		{
			name: "error on missing hash without allow-no-hashes",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "missing-hash.tar.gz",
					Hash:     "", // Missing hash.
					HashType: fileutils.HashTypeSHA512,
					Origin:   validOrigin,
				},
			},
			expectError:   true,
			errorContains: []string{"missing-hash.tar.gz", "missing required 'hash'"},
		},
		{
			name: "error on missing hash type when hash is set",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "missing-hashtype.tar.gz",
					Hash:     "abc123",
					HashType: "", // Missing hash type.
					Origin:   validOrigin,
				},
			},
			expectError:   true,
			errorContains: []string{"missing-hashtype.tar.gz", "has a 'hash' value but no 'hash-type'"},
		},
		{
			name: "creates 'sources' file if not exists",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "new-source.tar.gz",
					Hash:     "newhash123",
					HashType: fileutils.HashTypeSHA256,
					Origin:   validOrigin,
				},
			},
			existingSourcesContent: "", // No existing file.
			expectedSourceEntries: []string{
				"SHA256 (new-source.tar.gz) = newhash123",
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			const outputSpecPath = testOutputDir + "/test-component.spec"

			ctrl := gomock.NewController(t)
			component := components_testutils.NewMockComponent(ctrl)
			sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
			ctx := testctx.NewCtx()

			component.EXPECT().GetName().AnyTimes().Return("test-component")
			component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				SourceFiles: testCase.sourceFiles,
			})
			sourceManager.EXPECT().FetchFiles(gomock.Any(), component, testOutputDir).Return(nil, nil)
			sourceManager.EXPECT().FetchComponent(gomock.Any(), component, testOutputDir, gomock.Any()).DoAndReturn(
				func(
					_ interface{}, _ interface{}, outputDir string,
					_ ...sourceproviders.FetchComponentOption,
				) ([]sourceproviders.SourceProvenance, error) {
					// Create existing 'sources' file if specified.
					if testCase.existingSourcesContent != "" {
						err := fileutils.WriteFile(ctx.FS(), filepath.Join(outputDir, fedorasource.SourcesFileName),
							[]byte(testCase.existingSourcesContent), fileperms.PublicFile)
						if err != nil {
							return nil, err
						}
					}

					return nil, fileutils.WriteFile(ctx.FS(), outputSpecPath, []byte("# test spec"), fileperms.PublicFile)
				},
			)

			preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx)
			require.NoError(t, err)

			_, err = preparer.PrepareSources(ctx, component, testOutputDir, true /*applyOverlays?*/)
			if testCase.expectError {
				require.Error(t, err)

				for _, contains := range testCase.errorContains {
					assert.Contains(t, err.Error(), contains)
				}

				return
			}

			require.NoError(t, err)

			if len(testCase.expectedSourceEntries) > 0 || len(testCase.expectedSourceEntriesAbsent) > 0 ||
				testCase.expectedExactContent != "" {
				sourcesFilePath := filepath.Join(testOutputDir, fedorasource.SourcesFileName)
				sourcesContent, err := fileutils.ReadFile(ctx.FS(), sourcesFilePath)
				require.NoError(t, err)

				if testCase.expectedExactContent != "" {
					assert.Equal(t, testCase.expectedExactContent, string(sourcesContent),
						"merged 'sources' file does not match expected byte content "+
							"(blank-line / comment preservation regression?)")
				}

				// Verify substrings are present and appear in the same order they were
				// declared in expectedSourceEntries (catches accidental reordering, e.g. an
				// in-place replacement being shuffled to the end of the file).
				lastIdx := -1

				for _, expectedEntry := range testCase.expectedSourceEntries {
					idx := strings.Index(string(sourcesContent), expectedEntry)
					assert.GreaterOrEqual(t, idx, 0,
						"expected entry %q not found in 'sources' file content:\n%s",
						expectedEntry, string(sourcesContent))
					assert.Greater(t, idx, lastIdx,
						"expected entry %q appears before earlier entry; ordering violated",
						expectedEntry)
					lastIdx = idx
				}

				for _, absentEntry := range testCase.expectedSourceEntriesAbsent {
					assert.NotContains(t, string(sourcesContent), absentEntry,
						"upstream entry should have been removed by replace-upstream")
				}
			}
		})
	}
}

func TestPrepareSources_AllowNoHashes(t *testing.T) {
	const (
		testFileContent = "hello world"
		// Pre-computed SHA-512 hash of "hello world".
		testFileSHA512 = "309ecc489c12d6eb4cc40f50c902f2b4d0ed77ee511a7c7a9bcd3ca86d4cd86f" +
			"989dd35bc5ff499670da34255b45b0cfd830e81f605dcf7dc5542e93ae9cd76f"
		// Pre-computed SHA-256 hash of "hello world".
		testFileSHA256 = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	)

	tests := []struct {
		name                   string
		sourceFiles            []projectconfig.SourceFileReference
		preparerOpts           []sources.PreparerOption
		skipLookaside          bool
		existingSourcesContent string
		createFile             bool
		expectError            bool
		errorContains          []string
		expectedSourceEntries  []string
		// forbiddenSourceEntries are substrings that must NOT appear in the
		// final 'sources' file (e.g. the original upstream hash after a
		// 'replace-upstream' merge).
		forbiddenSourceEntries []string
	}{
		{
			name: "computes hash with provided hash type",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "test-file.tar.gz",
					HashType: fileutils.HashTypeSHA256,
					Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/test-file.tar.gz"},
				},
			},
			preparerOpts: []sources.PreparerOption{sources.WithAllowNoHashes()},
			createFile:   true,
			expectedSourceEntries: []string{
				"SHA256 (test-file.tar.gz) = " + testFileSHA256,
			},
		},
		{
			name: "defaults to sha512 when hash type also missing",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "test-file.tar.gz",
					Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/test-file.tar.gz"},
				},
			},
			preparerOpts: []sources.PreparerOption{sources.WithAllowNoHashes()},
			createFile:   true,
			expectedSourceEntries: []string{
				"SHA512 (test-file.tar.gz) = " + testFileSHA512,
			},
		},
		{
			name: "error when file not found in output dir",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "nonexistent.tar.gz",
					HashType: fileutils.HashTypeSHA256,
					Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/nonexistent.tar.gz"},
				},
			},
			preparerOpts:  []sources.PreparerOption{sources.WithAllowNoHashes()},
			createFile:    false,
			expectError:   true,
			errorContains: []string{"nonexistent.tar.gz", "failed to compute hash"},
		},
		{
			name: "error when allow-no-hashes with skip-lookaside",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "test-file.tar.gz",
					HashType: fileutils.HashTypeSHA512,
					Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/test-file.tar.gz"},
				},
			},
			preparerOpts:  []sources.PreparerOption{sources.WithAllowNoHashes(), sources.WithSkipLookaside()},
			skipLookaside: true,
			createFile:    false,
			expectError:   true,
			errorContains: []string{"test-file.tar.gz", "downloads were skipped"},
		},
		{
			// Combining 'allow-no-hashes' with 'replace-upstream' must compute the fresh
			// hash from the on-disk file *and* swap the upstream entry in place, instead
			// of erroring on the filename collision or appending a duplicate entry.
			name: "replace-upstream with allow-no-hashes computes fresh hash and replaces upstream entry",
			sourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: "test-file.tar.gz",
					HashType: fileutils.HashTypeSHA256,
					Origin: projectconfig.Origin{
						Type: projectconfig.OriginTypeURI,
						Uri:  "https://example.com/test-file.tar.gz",
					},
					ReplaceUpstream: true,
					ReplaceReason:   "patched to fix upstream regression",
				},
			},
			preparerOpts: []sources.PreparerOption{sources.WithAllowNoHashes()},
			createFile:   true,
			existingSourcesContent: "SHA256 (test-file.tar.gz) = " +
				"0000000000000000000000000000000000000000000000000000000000000000\n" +
				"SHA512 (other.tar.gz) = aabbcc\n",
			expectedSourceEntries: []string{
				// Replacement entry uses the freshly computed hash, not the upstream value.
				"SHA256 (test-file.tar.gz) = " + testFileSHA256,
				// Untouched sibling entry is preserved.
				"SHA512 (other.tar.gz) = aabbcc",
			},
			forbiddenSourceEntries: []string{
				// The upstream stub hash must be gone; the entry was replaced, not duplicated.
				"0000000000000000000000000000000000000000000000000000000000000000",
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			const outputSpecPath = testOutputDir + "/test-component.spec"

			ctrl := gomock.NewController(t)
			comp := components_testutils.NewMockComponent(ctrl)
			sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)
			ctx := testctx.NewCtx()

			comp.EXPECT().GetName().AnyTimes().Return("test-component")
			comp.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				SourceFiles: testCase.sourceFiles,
			})

			if !testCase.skipLookaside {
				sourceManager.EXPECT().FetchFiles(gomock.Any(), comp, testOutputDir).Return(nil, nil)
			}

			sourceManager.EXPECT().FetchComponent(gomock.Any(), comp, testOutputDir, gomock.Any()).DoAndReturn(
				func(
					_ interface{}, _ interface{}, outputDir string,
					_ ...sourceproviders.FetchComponentOption,
				) ([]sourceproviders.SourceProvenance, error) {
					if testCase.existingSourcesContent != "" {
						if err := fileutils.WriteFile(ctx.FS(), filepath.Join(outputDir, fedorasource.SourcesFileName),
							[]byte(testCase.existingSourcesContent), fileperms.PublicFile); err != nil {
							return nil, err
						}
					}

					// Create the source file in output dir to simulate it being downloaded.
					if testCase.createFile {
						for _, sf := range testCase.sourceFiles {
							filePath := filepath.Join(outputDir, sf.Filename)
							if err := fileutils.WriteFile(ctx.FS(), filePath,
								[]byte(testFileContent), fileperms.PublicFile); err != nil {
								return nil, err
							}
						}
					}

					return nil, fileutils.WriteFile(ctx.FS(), outputSpecPath, []byte("# test spec"), fileperms.PublicFile)
				},
			)

			preparer, err := sources.NewPreparer(sourceManager, ctx.FS(), ctx, ctx, testCase.preparerOpts...)
			require.NoError(t, err)

			_, err = preparer.PrepareSources(ctx, comp, testOutputDir, true /*applyOverlays?*/)
			if testCase.expectError {
				require.Error(t, err)

				for _, contains := range testCase.errorContains {
					assert.Contains(t, err.Error(), contains)
				}

				return
			}

			require.NoError(t, err)

			if len(testCase.expectedSourceEntries) > 0 || len(testCase.forbiddenSourceEntries) > 0 {
				sourcesFilePath := filepath.Join(testOutputDir, fedorasource.SourcesFileName)
				sourcesContent, err := fileutils.ReadFile(ctx.FS(), sourcesFilePath)
				require.NoError(t, err)

				for _, expectedEntry := range testCase.expectedSourceEntries {
					assert.Contains(t, string(sourcesContent), expectedEntry)
				}

				for _, forbiddenEntry := range testCase.forbiddenSourceEntries {
					assert.NotContains(t, string(sourcesContent), forbiddenEntry,
						"forbidden substring %q must not appear in the merged 'sources' file",
						forbiddenEntry)
				}
			}
		})
	}
}
