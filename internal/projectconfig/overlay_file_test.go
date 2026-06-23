// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test private functions (i.e., applyOverlayFiles).
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
category = "backport-dist-git"
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
	assert.Equal(t, OverlayCategoryBackportDistGit, loaded[0].Metadata.Category)

	assert.Equal(t, ComponentOverlayRemoveSubpackage, loaded[1].Type)
	require.NotNil(t, loaded[1].Metadata)
	assert.Equal(t, OverlayCategoryBackportDistGit, loaded[1].Metadata.Category)

	// Mutating one overlay's metadata must not affect another (each must own a copy).
	loaded[0].Metadata.Commits = append(loaded[0].Metadata.Commits, "mutated")
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

[[overlays]]
type  = "spec-set-tag"
tag   = "Order"
value = "second"
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(), filenameFirstOverlay, []byte(`
[metadata]
category = "azl-branding-policy"

[[overlays]]
type  = "spec-set-tag"
tag   = "Order"
value = "first"
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(), tiedBasenameFirstOverlay, []byte(`
[metadata]
category = "azl-branding-policy"

[[overlays]]
type  = "spec-set-tag"
tag   = "Order"
value = "third"
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(), tiedBasenameSecondOverlay, []byte(`
[metadata]
category = "azl-branding-policy"

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

func TestLoadOverlayFiles_NoMatchesIsError(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := "/project/comps/empty/overlays"

	// A non-overlay file alongside must not be picked up; a glob that matches no
	// files is a misconfiguration and must error.
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "README.md"),
		[]byte("not an overlay"), fileperms.PrivateFile))

	_, err := loadOverlayFiles(ctx.FS(), "/project", []string{overlayGlob(overlayDir)}, false)
	require.ErrorIs(t, err, ErrOverlayFilesNoMatches)
}

func TestLoadOverlayFiles_MissingDirIsError(t *testing.T) {
	ctx := testctx.NewCtx()

	_, err := loadOverlayFiles(
		ctx.FS(), "/project",
		[]string{overlayGlob("/project/comps/does-not-exist/overlays")}, false,
	)
	require.ErrorIs(t, err, ErrOverlayFilesNoMatches)
}

func TestLoadOverlayFiles_RejectsPerOverlayMetadata(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := badOverlayTestDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-bad.overlay.toml"),
		[]byte(`
[metadata]
category = "azl-compatibility"

[[overlays]]
type = "spec-set-tag"
tag = "Vendor"
value = "Microsoft"
metadata = { category = "azl-branding-policy" }
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
commits = ["abc"]

[[overlays]]
type = "spec-set-tag"
tag = "Vendor"
value = "Microsoft"
`), fileperms.PrivateFile))

	_, err := loadOverlayFiles(ctx.FS(), "/project", []string{overlayGlob(overlayDir)}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "category")
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
commits = ["abc"]

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
category = "backport-dist-git"
commits = ["https://src.fedoraproject.org/rpms/foo/c/abc"]

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

func TestApplyOverlayFiles_AppendsAfterInlineOverlays(t *testing.T) {
	ctx := testctx.NewCtx()
	overlayDir := antOverlayDir

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(overlayDir, "0001-backport.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	cfg := &ConfigFile{
		dir: "/project",
		Components: map[string]ComponentConfig{
			"ant": {
				OverlayFiles: []string{"comps/ant/overlays/*.overlay.toml"},
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

	require.NoError(t, applyOverlayFiles(ctx.FS(), cfg, false))

	ant := cfg.Components["ant"]
	require.Len(t, ant.Overlays, 3, "inline overlay + two file-sourced overlays")

	// Inline first.
	assert.Equal(t, ComponentOverlaySetSpecTag, ant.Overlays[0].Type)
	assert.Equal(t, OverlayCategoryAZLBrandingPolicy, ant.Overlays[0].Metadata.Category)

	// File-sourced overlays appended in file/declaration order, with the file's
	// metadata stamped onto each.
	assert.Equal(t, ComponentOverlaySearchAndReplaceInSpec, ant.Overlays[1].Type)
	require.NotNil(t, ant.Overlays[1].Metadata)
	assert.Equal(t, OverlayCategoryBackportDistGit, ant.Overlays[1].Metadata.Category)

	assert.Equal(t, ComponentOverlayRemoveSubpackage, ant.Overlays[2].Type)
	require.NotNil(t, ant.Overlays[2].Metadata)
	assert.Equal(t, OverlayCategoryBackportDistGit, ant.Overlays[2].Metadata.Category)
}

func TestApplyOverlayFiles_NoopWhenUnset(t *testing.T) {
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

	require.NoError(t, applyOverlayFiles(ctx.FS(), cfg, false))

	ant := cfg.Components["ant"]
	require.Len(t, ant.Overlays, 1, "no overlay-files, no merging")
}

func TestRejectOverlayFilesInDefaults(t *testing.T) {
	const overlayFilePattern = "overlays/*.overlay.toml"

	testCases := []struct {
		name          string
		cfg           ConfigFile
		errorContains string
	}{
		{
			name: "project-level default-component-config",
			cfg: ConfigFile{
				DefaultComponentConfig: &ComponentConfig{
					OverlayFiles: []string{overlayFilePattern},
				},
			},
			errorContains: "project-level default-component-config",
		},
		{
			name: "component-group default-component-config",
			cfg: ConfigFile{
				ComponentGroups: map[string]ComponentGroupConfig{
					"core": {
						DefaultComponentConfig: ComponentConfig{
							OverlayFiles: []string{overlayFilePattern},
						},
					},
				},
			},
			errorContains: "component-group `core`",
		},
		{
			name: "distro version default-component-config",
			cfg: ConfigFile{
				Distros: map[string]DistroDefinition{
					"azl": {
						Versions: map[string]DistroVersionDefinition{
							"3.0": {
								DefaultComponentConfig: ComponentConfig{
									OverlayFiles: []string{overlayFilePattern},
								},
							},
						},
					},
				},
			},
			errorContains: "distro `azl` version `3.0`",
		},
		{
			name: "unset defaults",
			cfg: ConfigFile{
				DefaultComponentConfig: &ComponentConfig{},
				ComponentGroups: map[string]ComponentGroupConfig{
					"core": {},
				},
				Distros: map[string]DistroDefinition{
					"azl": {
						Versions: map[string]DistroVersionDefinition{
							"3.0": {},
						},
					},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := rejectOverlayFilesInDefaults(&testCase.cfg)

			if testCase.errorContains == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, ErrOverlayFilesInDefaultConfig)
			assert.Contains(t, err.Error(), testCase.errorContains)
		})
	}
}

func TestApplyOverlayFiles_AcceptsAbsoluteGlob(t *testing.T) {
	ctx := testctx.NewCtx()
	absDir := "/elsewhere/overlays"

	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(absDir, "0001-backport.overlay.toml"),
		[]byte(validBackportOverlayFile), fileperms.PrivateFile))

	cfg := &ConfigFile{
		dir: "/project",
		Components: map[string]ComponentConfig{
			"ant": {OverlayFiles: []string{overlayGlob(absDir)}},
		},
	}

	require.NoError(t, applyOverlayFiles(ctx.FS(), cfg, false))

	ant := cfg.Components["ant"]
	require.Len(t, ant.Overlays, 2, "absolute glob is not re-rooted under cfg.dir")
	require.NotNil(t, ant.Overlays[0].Metadata)
	assert.Equal(t, OverlayCategoryBackportDistGit, ant.Overlays[0].Metadata.Category)
}

// TestLoadAndResolveProjectConfig_OverlayFiles exercises the full loader pipeline
// (loadAndResolveProjectConfig -> loadProjectConfigFile -> applyOverlayFiles) and
// guards against regressions that drop the overlay-files hook from the loader.
func TestLoadAndResolveProjectConfig_OverlayFiles(t *testing.T) {
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(`
[components.ant]
overlay-files = ["comps/ant/overlays/*.overlay.toml"]
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
	assert.Equal(t, OverlayCategoryBackportDistGit, ant.Overlays[0].Metadata.Category)
}
