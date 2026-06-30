// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestCustomFileSourceProvider_GetFile_NonCustomOriginReturnsNotFound(t *testing.T) {
	provider := &customFileSourceProvider{
		fs:     afero.NewMemMapFs(),
		runner: nil, // never reached
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

func TestCustomFileSourceProvider_GetFile_MissingScriptReturnsError(t *testing.T) {
	provider := &customFileSourceProvider{
		fs:     afero.NewMemMapFs(),
		runner: nil, // never reached — script stat check fails first
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
		Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeCustom},
		Script:   "gen.sh",
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

	// Script should have been copied into the staging dir.
	data, readErr := afero.ReadFile(afero.NewOsFs(), filepath.Join(scriptDir, "gen.sh"))
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
