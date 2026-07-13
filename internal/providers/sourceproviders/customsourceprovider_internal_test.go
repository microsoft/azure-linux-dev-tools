// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestCustomFileSourceProvider_GetFile_NonCustomOriginReturnsNotFound(t *testing.T) {
	ctx := testctx.NewCtx()

	provider := &customFileSourceProvider{
		dryRunnable: ctx,
		fs:          ctx.FS(),
		runner:      nil, // never reached
	}

	ctrl := gomock.NewController(t)
	comp := components_testutils.NewMockComponent(ctrl)

	ref := projectconfig.SourceFileReference{
		Filename: "src.tar.gz",
		Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI},
	}

	err := provider.GetFile(context.Background(), comp, ref, "/output")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestOutputTail(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		writes   []string
		expected string
	}{
		{
			name:     "retains complete output within limit",
			limit:    5,
			writes:   []string{"abc", "de"},
			expected: "abcde",
		},
		{
			name:     "retains tail after multiple writes",
			limit:    5,
			writes:   []string{"abc", "defg"},
			expected: "[output truncated; showing last 5 bytes]\ncdefg",
		},
		{
			name:     "retains tail of a single oversized write",
			limit:    4,
			writes:   []string{"abcdef"},
			expected: "[output truncated; showing last 4 bytes]\ncdef",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			tail := newOutputTail(testCase.limit)

			for _, write := range testCase.writes {
				written, err := tail.Write([]byte(write))
				require.NoError(t, err)
				assert.Equal(t, len(write), written)
			}

			assert.Equal(t, testCase.expected, tail.String())
		})
	}
}

func TestCustomFileSourceProvider_GetFile_MissingScriptReturnsError(t *testing.T) {
	ctx := testctx.NewCtx()

	provider := &customFileSourceProvider{
		dryRunnable: ctx,
		fs:          ctx.FS(),
		runner:      nil, // never reached — script stat check fails first
	}

	ctrl := gomock.NewController(t)
	comp := components_testutils.NewMockComponent(ctrl)
	comp.EXPECT().GetName().Return("yara").AnyTimes()
	comp.EXPECT().GetConfig().Return(&projectconfig.ComponentConfig{
		Name: "yara",
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/specs/yara/yara.spec",
		},
	}).AnyTimes()

	ref := projectconfig.SourceFileReference{
		Filename: "gen.tar.gz",
		Origin: projectconfig.Origin{
			Type:   projectconfig.OriginTypeCustom,
			Script: "gen.sh",
		},
	}

	err := provider.GetFile(context.Background(), comp, ref, "/output")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gen.sh")
	assert.NotErrorIs(t, err, ErrNotFound)
}

func TestResolveComponentSpecDir_LocalComponent(t *testing.T) {
	ctrl := gomock.NewController(t)
	comp := components_testutils.NewMockComponent(ctrl)
	comp.EXPECT().GetConfig().Return(&projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/specs/yara/yara.spec",
		},
	}).AnyTimes()

	dir, err := resolveComponentSpecDir(comp)
	require.NoError(t, err)
	assert.Equal(t, filepath.FromSlash("/specs/yara"), dir)
}

func TestResolveComponentSpecDir_NoSpecInfoReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	comp := components_testutils.NewMockComponent(ctrl)
	comp.EXPECT().GetConfig().Return(&projectconfig.ComponentConfig{
		Name: "yara",
		// No Spec.Path, no SourceConfigFile
	}).AnyTimes()

	_, err := resolveComponentSpecDir(comp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "yara")
}

func TestPrepareStagingDirs_ScriptIsStagedAndExecutable(t *testing.T) {
	memFS := afero.NewMemMapFs()

	const scriptContent = "#!/bin/bash\necho hello"

	require.NoError(t, afero.WriteFile(memFS, "/scripts/gen.sh", []byte(scriptContent), fileperms.PublicExecutable))

	scriptDir, outputDir, cleanup, err := prepareStagingDirs(memFS, "/scripts/gen.sh", "gen.sh")
	require.NoError(t, err)
	require.NotEmpty(t, scriptDir)
	require.NotEmpty(t, outputDir)
	require.NotNil(t, cleanup)

	defer cleanup()

	// Script should have been copied into the staging dir on the same FS.
	data, readErr := afero.ReadFile(memFS, filepath.Join(scriptDir, "gen.sh"))
	require.NoError(t, readErr)
	assert.Equal(t, scriptContent, string(data))
}

func TestPrepareStagingDirs_MissingScriptReturnsError(t *testing.T) {
	emptyFS := afero.NewMemMapFs() // empty — no script present

	_, _, cleanup, err := prepareStagingDirs(emptyFS, "/scripts/missing.sh", "missing.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing.sh")
	assert.Nil(t, cleanup)
}

func TestStageInputFiles_FoundInDestDirPreservesMode(t *testing.T) {
	ctx := testctx.NewCtx()
	memFS := ctx.FS()

	const fileContent = "upstream tarball data"

	require.NoError(t, afero.WriteFile(memFS, "/output/upstream.tar.gz", []byte(fileContent), fileperms.PublicExecutable))

	err := stageInputFiles(ctx, memFS, []string{"upstream.tar.gz"}, "/output", "/script", "gen.sh")
	require.NoError(t, err)

	data, readErr := afero.ReadFile(memFS, "/script/upstream.tar.gz")
	require.NoError(t, readErr)
	assert.Equal(t, fileContent, string(data))

	info, statErr := memFS.Stat("/script/upstream.tar.gz")
	require.NoError(t, statErr)
	assert.Equal(t, fileperms.PublicExecutable, info.Mode().Perm())
}

func TestStageInputFiles_NotFoundReturnsError(t *testing.T) {
	ctx := testctx.NewCtx()
	memFS := ctx.FS()

	// No files written — destDirPath is empty.
	err := stageInputFiles(ctx, memFS, []string{"missing.tar.gz"}, "/output", "/script", "gen.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing.tar.gz")
	assert.Contains(t, err.Error(), "/output")
}

func TestStageInputFiles_SymlinkRejected(t *testing.T) {
	fileSystem := afero.NewOsFs()
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "target.tar.gz")
	inputPath := filepath.Join(dir, "input.tar.gz")

	require.NoError(t, os.WriteFile(targetPath, []byte("input"), fileperms.PublicFile))
	require.NoError(t, os.Symlink(targetPath, inputPath))

	err := stageInputFiles(
		testctx.NewCtx(), fileSystem, []string{"input.tar.gz"}, dir, filepath.Join(dir, "script"), "gen.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.tar.gz")
	assert.Contains(t, err.Error(), "symbolic link")
}

func TestStageInputFiles_ScriptNameConflictReturnsError(t *testing.T) {
	ctx := testctx.NewCtx()
	memFS := ctx.FS()

	err := stageInputFiles(ctx, memFS, []string{"gen.sh"}, "/output", "/script", "gen.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts")
}

func TestStageInputFiles_InvalidFilenameReturnsError(t *testing.T) {
	ctx := testctx.NewCtx()
	memFS := ctx.FS()

	err := stageInputFiles(ctx, memFS, []string{"../escape.tar.gz"}, "/output", "/script", "gen.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid input filename")
}

func TestFormatCustomScriptOutput(t *testing.T) {
	output := formatCustomScriptOutput("stdout line\n", "stderr line\n")

	assert.Contains(t, output, "stdout:\nstdout line")
	assert.Contains(t, output, "stderr:\nstderr line")
}
