// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test private functions (i.e., loadOverlayFiles).
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
category = "upstream-backport"
upstream-status = "upstreamed"
commits = [{ url = "https://src.fedoraproject.org/rpms/ant/c/4ca7a3b" }]

[[overlays]]
description = "Remove openjdk21 binding"
type        = "spec-search-replace"
section     = "%install"
regex       = ".*openjdk21.*"

[[overlays]]
type    = "spec-remove-subpackage"
package = "openjdk21"
`

const (
	badOverlayTestDir = "/project/comps/x/overlays"
	antOverlayDir     = "/project/comps/ant/overlays"
)

// overlayGlob returns the conventional `*.overlay.toml` glob beneath dir; used by
// tests to exercise the loader the same way the docs recommend.
func overlayGlob(dir string) string {
	return filepath.Join(dir, "*.overlay.toml")
}

func TestLoadOverlayFiles_StampsMetadataAndPreservesOrder(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := antOverlayDir

	// Files are intentionally out of glob order to confirm filename-sort.
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0002-azl-branding.overlay.toml"),
		[]byte(`
[metadata]
category = "azl-branding-policy"
upstream-status = "inapplicable"

[[overlays]]
type  = "spec-set-tag"
tag   = "Vendor"
value = "Microsoft"
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-backport-drop-openjdk21.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	loaded, err := loadOverlayFiles(ctx.FS(), "/project", []string{overlayGlob(overlayDir)}, false)
	require.NoError(t, err)
	require.Len(t, loaded, 3, "two overlays from 0001 + one from 0002")

	// 0001-* contributes first.
	assert.Equal(t, ComponentOverlaySearchAndReplaceInSpec, loaded[0].Type)
	require.NotNil(t, loaded[0].Metadata)
	assert.Equal(t, OverlayCategoryUpstreamBackport, loaded[0].Metadata.Category)

	assert.Equal(t, ComponentOverlayRemoveSubpackage, loaded[1].Type)
	require.NotNil(t, loaded[1].Metadata)
	assert.Equal(t, OverlayCategoryUpstreamBackport, loaded[1].Metadata.Category)

	// Mutating one overlay's metadata must not affect another (each must own a copy).
	loaded[0].Metadata.Commits = append(loaded[0].Metadata.Commits, URLRef{URL: "https://example.com/mutated"})
	assert.Len(t, loaded[1].Metadata.Commits, 1, "each overlay must own its metadata copy")

	// 0002-* comes second.
	assert.Equal(t, ComponentOverlaySetSpecTag, loaded[2].Type)
	require.NotNil(t, loaded[2].Metadata)
	assert.Equal(t, OverlayCategoryAZLBrandingPolicy, loaded[2].Metadata.Category)
}

func TestLoadOverlayFiles_SortsMatchesByBasename(t *testing.T) {
	ctx := testctx.NewCtx()

	// If matches were sorted by full path, "a/0002" would load before "b/0001".
	// The documented order is by filename within each glob, using full path as a
	// tie-breaker for matching filenames.
	relativeToFullPathFirstDir := "/project/comps/ant/overlays/a"
	filenameFirstDir := "/project/comps/ant/overlays/b"

	relativeToFullPathFirstOverlay := filepath.Join(relativeToFullPathFirstDir, "0002-second.overlay.toml")
	filenameFirstOverlay := filepath.Join(filenameFirstDir, "0001-first.overlay.toml")
	tiedBasenameFirstOverlay := filepath.Join(relativeToFullPathFirstDir, "0003-tied.overlay.toml")
	tiedBasenameSecondOverlay := filepath.Join(filenameFirstDir, "0003-tied.overlay.toml")

	require.NoError(t, fileutils.WriteFile(ctx.FS(), relativeToFullPathFirstOverlay, []byte(`
[metadata]
category = "azl-branding-policy"
upstream-status = "inapplicable"

[[overlays]]
type  = "spec-set-tag"
tag   = "Order"
value = "second"
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(), filenameFirstOverlay, []byte(`
[metadata]
category = "azl-branding-policy"
upstream-status = "inapplicable"

[[overlays]]
type  = "spec-set-tag"
tag   = "Order"
value = "first"
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(), tiedBasenameFirstOverlay, []byte(`
[metadata]
category = "azl-branding-policy"
upstream-status = "inapplicable"

[[overlays]]
type  = "spec-set-tag"
tag   = "Order"
value = "third"
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(), tiedBasenameSecondOverlay, []byte(`
[metadata]
category = "azl-branding-policy"
upstream-status = "inapplicable"

[[overlays]]
type  = "spec-set-tag"
tag   = "Order"
value = "fourth"
`), fileperms.PrivateFile))

	loaded, err := loadOverlayFiles(ctx.FS(), "/project", []string{"/project/comps/ant/overlays/**/*.overlay.toml"}, false)
	require.NoError(t, err)
	require.Len(t, loaded, 4)
	assert.Equal(t, "first", loaded[0].Value)
	assert.Equal(t, "second", loaded[1].Value)
	assert.Equal(t, "third", loaded[2].Value)
	assert.Equal(t, "fourth", loaded[3].Value)
}

func TestLoadOverlayFiles_NoMatchesIsNoop(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := "/project/comps/empty/overlays"

	// A non-overlay file alongside must not be picked up; a glob that matches no
	// files contributes no overlays.
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "README.md"),
		[]byte("not an overlay"), fileperms.PrivateFile))

	loaded, err := loadOverlayFiles(ctx.FS(), "/project", []string{overlayGlob(overlayDir)}, false)
	require.NoError(t, err)
	assert.Empty(t, loaded)
}

func TestLoadOverlayFiles_MissingDirIsNoop(t *testing.T) {
	ctx := testctx.NewCtx()

	loaded, err := loadOverlayFiles(
		ctx.FS(), "/project",
		[]string{overlayGlob("/project/comps/does-not-exist/overlays")}, false,
	)
	require.NoError(t, err)
	assert.Empty(t, loaded)
}

func TestLoadOverlayFiles_MissingLiteralPathErrors(t *testing.T) {
	ctx := testctx.NewCtx()

	_, err := loadOverlayFiles(
		ctx.FS(), "/project",
		[]string{"/project/comps/does-not-exist/overlays/0001-missing.overlay.toml"}, false,
	)
	require.ErrorIs(t, err, ErrOverlayFilesNoMatches)
}

func TestLoadOverlayFiles_NoMatchDoesNotBlockLaterMatches(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := antOverlayDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-backport.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	loaded, err := loadOverlayFiles(ctx.FS(), "/project", []string{
		overlayGlob("/project/comps/does-not-exist/overlays"),
		overlayGlob(overlayDir),
	}, false)
	require.NoError(t, err)
	require.Len(t, loaded, 2)
}

func TestLoadOverlayFiles_RejectsPerOverlayMetadata(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := badOverlayTestDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-bad.overlay.toml"),
		[]byte(`
[metadata]
category = "azl-compatibility"
upstream-status = "inapplicable"

[[overlays]]
type = "spec-set-tag"
tag = "Vendor"
value = "Microsoft"
metadata = { category = "azl-branding-policy", upstream-status = "inapplicable" }
`), fileperms.PrivateFile))

	_, err := loadOverlayFiles(ctx.FS(), "/project", []string{overlayGlob(overlayDir)}, false)
	require.ErrorIs(t, err, ErrOverlayFilePerOverlayMetadata)
}

func TestLoadOverlayFiles_RejectsEmptyFile(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := badOverlayTestDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-empty.overlay.toml"),
		[]byte(`
[metadata]
category = "azl-compatibility"
upstream-status = "inapplicable"
`), fileperms.PrivateFile))

	_, err := loadOverlayFiles(ctx.FS(), "/project", []string{overlayGlob(overlayDir)}, false)
	require.ErrorIs(t, err, ErrOverlayFileEmpty)
}

func TestLoadOverlayFiles_RejectsInvalidMetadata(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := badOverlayTestDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-bad.overlay.toml"),
		[]byte(`
[metadata]
# Missing category.
commits = [{ url = "https://example.com/abc" }]

[[overlays]]
type = "spec-set-tag"
tag = "Vendor"
value = "Microsoft"
`), fileperms.PrivateFile))

	_, err := loadOverlayFiles(ctx.FS(), "/project", []string{overlayGlob(overlayDir)}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "category")
}

func TestLoadOverlayFiles_RejectsInvalidOverlay(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := badOverlayTestDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-bad.overlay.toml"),
		[]byte(`
[metadata]
category = "azl-branding-policy"
upstream-status = "inapplicable"

[[overlays]]
type = "spec-set-tag"
tag = "Vendor"
`), fileperms.PrivateFile))

	_, err := loadOverlayFiles(ctx.FS(), "/project", []string{overlayGlob(overlayDir)}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid overlay 1")
	assert.Contains(t, err.Error(), "requires")
	assert.Contains(t, err.Error(), "value")
}

func TestLoadOverlayFile_PermissiveTolerates_InvalidMetadata(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayPath := filepath.Join(badOverlayTestDir, "0001-bad.overlay.toml")

	// Same fixture as the strict counterpart above: category is missing, which
	// fails OverlayMetadata.Validate(). Under permissive parsing the load must
	// still succeed so older configs targeting a stricter newer schema can be
	// inspected.
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		overlayPath,
		[]byte(`
[metadata]
# Missing category.
commits = [{ url = "https://example.com/abc" }]

[[overlays]]
type = "spec-set-tag"
tag = "Vendor"
value = "Microsoft"
`), fileperms.PrivateFile))

	loaded, err := loadOverlayFile(ctx.FS(), overlayPath, true)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, ComponentOverlaySetSpecTag, loaded[0].Type)
	assert.Nil(t, loaded[0].Metadata,
		"invalid file-level metadata must be dropped under permissive parsing so overlay validation does not re-fail")
}

func TestLoadOverlayFiles_ResolvesSourceRelativeToOverlayFile(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := "/project/comps/foo/overlays"

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-patch.overlay.toml"),
		[]byte(`
[metadata]
category = "upstream-backport"
upstream-status = "upstreamed"
commits = [{ url = "https://src.fedoraproject.org/rpms/foo/c/abc" }]

[[overlays]]
type   = "patch-add"
source = "patches/fix.patch"
`), fileperms.PrivateFile))

	loaded, err := loadOverlayFiles(ctx.FS(), "/project", []string{overlayGlob(overlayDir)}, false)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t,
		filepath.Join(overlayDir, "patches/fix.patch"),
		loaded[0].Source,
		"source paths in overlay files resolve relative to the file itself")
}

func TestLoadOverlayFiles_DedupsAcrossPatterns(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := antOverlayDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-backport.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	// Two overlapping globs both match the same file. The file must contribute
	// its overlays only once.
	loaded, err := loadOverlayFiles(ctx.FS(), "/project", []string{
		overlayGlob(overlayDir),
		filepath.Join(overlayDir, "0001-*.overlay.toml"),
	}, false)
	require.NoError(t, err)
	require.Len(t, loaded, 2, "overlapping globs must not double-apply matched files")
}

func TestLoadOverlayFiles_DoubleStarMatchesNested(t *testing.T) {
	ctx := testctx.NewCtx()
	root := "/project/comps/ant/overlays"

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(root, "security", "0001-backport.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	loaded, err := loadOverlayFiles(
		ctx.FS(), "/project",
		[]string{filepath.Join(root, "**", "*.overlay.toml")}, false,
	)
	require.NoError(t, err)
	require.Len(t, loaded, 2, "**-style glob must descend into subdirectories")
}

func TestExpandResolvedOverlayFiles_AppendsAfterInlineOverlays(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := antOverlayDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-backport.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	cfg := &ConfigFile{dir: "/project"}
	component := ComponentConfig{
		Name:             "ant",
		SourceConfigFile: cfg,
		OverlayFiles:     []string{"comps/ant/overlays/*.overlay.toml"},
		Overlays: []ComponentOverlay{
			{
				Type:  ComponentOverlaySetSpecTag,
				Tag:   "Vendor",
				Value: "Microsoft",
				Metadata: &OverlayMetadata{
					Category:       OverlayCategoryAZLBrandingPolicy,
					UpstreamStatus: OverlayUpstreamStatusInapplicable,
				},
			},
		},
	}

	expanded, err := ExpandResolvedOverlayFiles(ctx.FS(), component, "/project", false)
	require.NoError(t, err)

	require.Len(t, expanded.Overlays, 3, "inline overlay + two file-sourced overlays")
	assert.Empty(t, expanded.OverlayFiles, "overlay-files should be consumed after expansion")

	// Inline first.
	assert.Equal(t, ComponentOverlaySetSpecTag, expanded.Overlays[0].Type)
	assert.Equal(t, OverlayCategoryAZLBrandingPolicy, expanded.Overlays[0].Metadata.Category)

	// File-sourced overlays appended in file/declaration order, with the file's
	// metadata stamped onto each.
	assert.Equal(t, ComponentOverlaySearchAndReplaceInSpec, expanded.Overlays[1].Type)
	require.NotNil(t, expanded.Overlays[1].Metadata)
	assert.Equal(t, OverlayCategoryUpstreamBackport, expanded.Overlays[1].Metadata.Category)

	assert.Equal(t, ComponentOverlayRemoveSubpackage, expanded.Overlays[2].Type)
	require.NotNil(t, expanded.Overlays[2].Metadata)
	assert.Equal(t, OverlayCategoryUpstreamBackport, expanded.Overlays[2].Metadata.Category)
}

func TestExpandResolvedOverlayFiles_NoopWhenUnset(t *testing.T) {
	ctx := testctx.NewCtx()

	component := ComponentConfig{
		Name: "ant",
		Overlays: []ComponentOverlay{
			{Type: ComponentOverlayAddSpecTag, Tag: "Vendor", Value: "Microsoft"},
		},
	}

	expanded, err := ExpandResolvedOverlayFiles(ctx.FS(), component, "", false)
	require.NoError(t, err)
	require.Len(t, expanded.Overlays, 1, "no overlay-files, no merging")
}

func TestExpandResolvedOverlayFiles_AcceptsAbsoluteGlob(t *testing.T) {
	ctx := testctx.NewCtx()
	absDir := "/elsewhere/overlays"

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(absDir, "0001-backport.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	component := ComponentConfig{Name: "ant", OverlayFiles: []string{overlayGlob(absDir)}}

	expanded, err := ExpandResolvedOverlayFiles(ctx.FS(), component, "", false)
	require.NoError(t, err)

	require.Len(t, expanded.Overlays, 2, "absolute glob is not re-rooted under cfg.dir")
	require.NotNil(t, expanded.Overlays[0].Metadata)
	assert.Equal(t, OverlayCategoryUpstreamBackport, expanded.Overlays[0].Metadata.Category)
}

func TestExpandResolvedOverlayFiles_RequiresReferenceDirForRelativePattern(t *testing.T) {
	ctx := testctx.NewCtx()

	_, err := ExpandResolvedOverlayFiles(ctx.FS(), ComponentConfig{
		Name:         "ant",
		OverlayFiles: []string{"overlays/*.overlay.toml"},
	}, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reference directory")
}

func TestExpandResolvedOverlayFiles_DefaultPatternUsesConcreteComponentConfigDir(t *testing.T) {
	ctx := testctx.NewCtx()
	componentConfig := &ConfigFile{dir: "/project/comps/ant"}

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		"/project/comps/ant/overlays/0001-backport.overlay.toml",
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	resolved, err := ResolveComponentConfig(
		ComponentConfig{Name: "ant", SourceConfigFile: componentConfig},
		ComponentConfig{OverlayFiles: []string{"overlays/*.overlay.toml"}},
		ComponentConfig{},
		nil,
		nil,
	)
	require.NoError(t, err)

	expanded, err := ExpandResolvedOverlayFiles(ctx.FS(), resolved, componentConfig.Dir(), false)
	require.NoError(t, err)
	require.Len(t, expanded.Overlays, 2)
	assert.Empty(t, expanded.OverlayFiles)
	require.NotNil(t, expanded.Overlays[0].Metadata)
	assert.Equal(t, OverlayCategoryUpstreamBackport, expanded.Overlays[0].Metadata.Category)
}
