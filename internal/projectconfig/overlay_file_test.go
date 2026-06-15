// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test private functions (i.e., applyOverlayDirs).
package projectconfig

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validBackportOverlayFile = `
[metadata]
category = "backport-fedora"
commits = ["https://src.fedoraproject.org/rpms/ant/c/4ca7a3b"]

[[overlays]]
description = "Remove openjdk21 binding"
type        = "spec-search-replace"
section     = "%install"
regex       = ".*openjdk21.*"

[[overlays]]
type    = "spec-remove-subpackage"
package = "openjdk21"
`

const badOverlayTestDir = "/project/comps/x/overlays"

func TestLoadOverlayDir_StampsMetadataAndPreservesOrder(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := "/project/comps/ant/overlays"

	// Files are intentionally out of glob order to confirm filename-sort.
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0002-azl-branding.overlay.toml"),
		[]byte(`
[metadata]
category = "azl-branding-policy"

[[overlays]]
type  = "spec-set-tag"
tag   = "Vendor"
value = "Microsoft"
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-backport-drop-openjdk21.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	loaded, err := loadOverlayDir(ctx.FS(), overlayDir, false)
	require.NoError(t, err)
	require.Len(t, loaded, 3, "two overlays from 0001 + one from 0002")

	// 0001-* contributes first.
	assert.Equal(t, ComponentOverlaySearchAndReplaceInSpec, loaded[0].Type)
	require.NotNil(t, loaded[0].Metadata)
	assert.Equal(t, OverlayCategoryBackportFedora, loaded[0].Metadata.Category)

	assert.Equal(t, ComponentOverlayRemoveSubpackage, loaded[1].Type)
	require.NotNil(t, loaded[1].Metadata)
	assert.Equal(t, OverlayCategoryBackportFedora, loaded[1].Metadata.Category)

	// Mutating one overlay's metadata must not affect another (each must own a copy).
	loaded[0].Metadata.Commits = append(loaded[0].Metadata.Commits, "mutated")
	assert.Len(t, loaded[1].Metadata.Commits, 1, "each overlay must own its metadata copy")

	// 0002-* comes second.
	assert.Equal(t, ComponentOverlaySetSpecTag, loaded[2].Type)
	require.NotNil(t, loaded[2].Metadata)
	assert.Equal(t, OverlayCategoryAZLBrandingPolicy, loaded[2].Metadata.Category)
}

func TestLoadOverlayDir_EmptyDirReturnsNothing(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := "/project/comps/empty/overlays"

	// A non-overlay file alongside must not be picked up.
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "README.md"),
		[]byte("not an overlay"), fileperms.PrivateFile))

	loaded, err := loadOverlayDir(ctx.FS(), overlayDir, false)
	require.NoError(t, err)
	assert.Empty(t, loaded)
}

func TestLoadOverlayDir_RejectsPerOverlayMetadata(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := badOverlayTestDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-bad.overlay.toml"),
		[]byte(`
[metadata]
category = "azl-build"

[[overlays]]
type = "spec-set-tag"
tag = "Vendor"
value = "Microsoft"
metadata = { category = "azl-branding-policy" }
`), fileperms.PrivateFile))

	_, err := loadOverlayDir(ctx.FS(), overlayDir, false)
	require.ErrorIs(t, err, ErrOverlayFilePerOverlayMetadata)
}

func TestLoadOverlayDir_RejectsEmptyFile(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := badOverlayTestDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-empty.overlay.toml"),
		[]byte(`
[metadata]
category = "azl-build"
`), fileperms.PrivateFile))

	_, err := loadOverlayDir(ctx.FS(), overlayDir, false)
	require.ErrorIs(t, err, ErrOverlayFileEmpty)
}

func TestLoadOverlayDir_RejectsInvalidMetadata(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := badOverlayTestDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-bad.overlay.toml"),
		[]byte(`
[metadata]
# Missing category.
commits = ["abc"]

[[overlays]]
type = "spec-set-tag"
tag = "Vendor"
value = "Microsoft"
`), fileperms.PrivateFile))

	_, err := loadOverlayDir(ctx.FS(), overlayDir, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "category")
}

func TestLoadOverlayDir_ResolvesSourceRelativeToOverlayFile(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := "/project/comps/foo/overlays"

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-patch.overlay.toml"),
		[]byte(`
[metadata]
category = "backport-fedora"
commits = ["https://src.fedoraproject.org/rpms/foo/c/abc"]

[[overlays]]
type   = "patch-add"
source = "patches/fix.patch"
`), fileperms.PrivateFile))

	loaded, err := loadOverlayDir(ctx.FS(), overlayDir, false)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t,
		filepath.Join(overlayDir, "patches/fix.patch"),
		loaded[0].Source,
		"source paths in .overlay.toml resolve relative to the overlay file")
}

func TestApplyOverlayDirs_AppendsAfterInlineOverlays(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := "/project/comps/ant/overlays"

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-backport.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	cfg := &ConfigFile{
		dir: "/project",
		Components: map[string]ComponentConfig{
			"ant": {
				OverlayDir: "comps/ant/overlays",
				Overlays: []ComponentOverlay{
					{
						Type:  ComponentOverlaySetSpecTag,
						Tag:   "Vendor",
						Value: "Microsoft",
						Metadata: &OverlayMetadata{
							Category: OverlayCategoryAZLBrandingPolicy,
						},
					},
				},
			},
		},
	}

	require.NoError(t, applyOverlayDirs(ctx.FS(), cfg, false))

	ant := cfg.Components["ant"]
	require.Len(t, ant.Overlays, 3, "inline overlay + two file-sourced overlays")

	// Inline first.
	assert.Equal(t, ComponentOverlaySetSpecTag, ant.Overlays[0].Type)
	assert.Equal(t, OverlayCategoryAZLBrandingPolicy, ant.Overlays[0].Metadata.Category)

	// File-sourced overlays appended in file/declaration order, with the file's
	// metadata stamped onto each.
	assert.Equal(t, ComponentOverlaySearchAndReplaceInSpec, ant.Overlays[1].Type)
	require.NotNil(t, ant.Overlays[1].Metadata)
	assert.Equal(t, OverlayCategoryBackportFedora, ant.Overlays[1].Metadata.Category)

	assert.Equal(t, ComponentOverlayRemoveSubpackage, ant.Overlays[2].Type)
	require.NotNil(t, ant.Overlays[2].Metadata)
	assert.Equal(t, OverlayCategoryBackportFedora, ant.Overlays[2].Metadata.Category)
}

func TestApplyOverlayDirs_NoopWhenOverlayDirUnset(t *testing.T) {
	ctx := testctx.NewCtx()

	cfg := &ConfigFile{
		dir: "/project",
		Components: map[string]ComponentConfig{
			"ant": {
				Overlays: []ComponentOverlay{
					{Type: ComponentOverlayAddSpecTag, Tag: "Vendor", Value: "Microsoft"},
				},
			},
		},
	}

	require.NoError(t, applyOverlayDirs(ctx.FS(), cfg, false))

	ant := cfg.Components["ant"]
	require.Len(t, ant.Overlays, 1, "no overlay-dir, no merging")
}

func TestApplyOverlayDirs_AcceptsAbsoluteOverlayDir(t *testing.T) {
	ctx := testctx.NewCtx()
	absDir := "/elsewhere/overlays"

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(absDir, "0001-backport.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	cfg := &ConfigFile{
		dir: "/project",
		Components: map[string]ComponentConfig{
			"ant": {OverlayDir: absDir},
		},
	}

	require.NoError(t, applyOverlayDirs(ctx.FS(), cfg, false))

	ant := cfg.Components["ant"]
	require.Len(t, ant.Overlays, 2, "absolute overlay-dir is not re-rooted under cfg.dir")
	require.NotNil(t, ant.Overlays[0].Metadata)
	assert.Equal(t, OverlayCategoryBackportFedora, ant.Overlays[0].Metadata.Category)
}

// TestLoadAndResolveProjectConfig_OverlayDir exercises the full loader pipeline
// (loadAndResolveProjectConfig -> loadProjectConfigFile -> applyOverlayDirs) and
// guards against regressions that drop the overlay-dir hook from the loader.
func TestLoadAndResolveProjectConfig_OverlayDir(t *testing.T) {
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(`
[components.ant]
overlay-dir = "comps/ant/overlays"
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		"/project/comps/ant/overlays/0001-backport.overlay.toml",
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	cfg, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	ant, ok := cfg.Components["ant"]
	require.True(t, ok, "ant component should be present")
	require.Len(t, ant.Overlays, 2)
	require.NotNil(t, ant.Overlays[0].Metadata)
	assert.Equal(t, OverlayCategoryBackportFedora, ant.Overlays[0].Metadata.Category)
}
