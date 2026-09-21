// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"slices"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for 'component changed' in lock-file-free mode, which compares the
// project configuration resolved at each ref instead of stored lock files.

func TestClassifyHistoricalComponent_BuildFieldChange(t *testing.T) {
	fromComponents := map[string]componentComparisonInputs{
		"curl": {
			Config: projectconfig.ComponentConfig{
				Build: projectconfig.ComponentBuildConfig{
					Defines: map[string]string{"feature": "disabled"},
				},
			},
		},
	}
	toComponents := map[string]componentComparisonInputs{
		"curl": {
			Config: projectconfig.ComponentConfig{
				Build: projectconfig.ComponentBuildConfig{
					Defines: map[string]string{"feature": "enabled"},
				},
			},
		},
	}

	result, err := classifyHistoricalComponent("curl", fromComponents, toComponents)
	require.NoError(t, err)
	assert.Equal(t, changeTypeChanged, result.ChangeType)
}

func TestClassifyHistoricalComponent_ExcludedFieldsUnchanged(t *testing.T) {
	base := projectconfig.ComponentConfig{
		Name:             "curl",
		SourceConfigFile: &projectconfig.ConfigFile{},
		RenderedSpecDir:  "/first/SPECS/c/curl",
		Spec: projectconfig.SpecSource{
			Path: "/first/specs/curl.spec",
			UpstreamDistro: projectconfig.DistroReference{
				Snapshot: "2025-01-01T00:00:00Z",
			},
		},
		Build: projectconfig.ComponentBuildConfig{
			Check: projectconfig.CheckConfig{Skip: true, SkipReason: "first reason"},
			Failure: projectconfig.ComponentBuildFailureConfig{
				Expected:       true,
				ExpectedReason: "first failure reason",
			},
			Hints: projectconfig.ComponentBuildHints{Expensive: true},
		},
		OverlayFiles: []string{"first/*.toml"},
		Publish:      projectconfig.ComponentPublishConfig{RPMChannel: "first"},
		Tests: &projectconfig.ComponentTestsConfig{
			Tests: []projectconfig.TestRef{{Name: "first-test"}},
		},
		Overlays: []projectconfig.ComponentOverlay{{
			Type:        projectconfig.ComponentOverlayAppendSpecLines,
			Description: "first description",
			Lines:       []string{"# functional content"},
			Source:      "/first/overlay.patch",
			Metadata:    &projectconfig.OverlayMetadata{},
		}},
		SourceFiles: []projectconfig.SourceFileReference{{
			Filename:      "source.tar.gz",
			Hash:          "abc123",
			Origin:        projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://first.example/source"},
			ReplaceReason: "first replacement reason",
		}},
		Packages: map[string]projectconfig.PackageConfig{
			"curl": {Publish: projectconfig.PackagePublishConfig{RPMChannel: "first"}},
		},
	}
	updated := base
	updated.Name = "renamed metadata"
	updated.SourceConfigFile = nil
	updated.RenderedSpecDir = "/second/SPECS/c/curl"
	updated.Spec.Path = "/second/specs/curl.spec"
	updated.Spec.UpstreamDistro.Snapshot = "2026-01-01T00:00:00Z"
	updated.Build.Check.SkipReason = "second reason"
	updated.Build.Failure.Expected = false
	updated.Build.Failure.ExpectedReason = "second failure reason"
	updated.Build.Hints.Expensive = false
	updated.OverlayFiles = []string{"second/*.toml"}
	updated.Publish.RPMChannel = "second"
	updated.Tests = &projectconfig.ComponentTestsConfig{
		Tests: []projectconfig.TestRef{{Name: "second-test"}},
	}
	updated.Overlays = slices.Clone(base.Overlays)
	updated.Overlays[0].Description = "second description"
	updated.Overlays[0].Source = "/second/overlay.patch"
	updated.Overlays[0].Metadata = nil
	updated.SourceFiles = slices.Clone(base.SourceFiles)
	updated.SourceFiles[0].Origin.Type = projectconfig.OriginTypeCustom
	updated.SourceFiles[0].Origin.Uri = "https://second.example/source"
	updated.SourceFiles[0].ReplaceReason = "second replacement reason"
	updated.Packages = map[string]projectconfig.PackageConfig{
		"curl": {Publish: projectconfig.PackagePublishConfig{RPMChannel: "second"}},
	}

	fromComponents := map[string]componentComparisonInputs{
		"curl": {Config: normalizeComponentForComparison(base)},
	}
	toComponents := map[string]componentComparisonInputs{
		"curl": {Config: normalizeComponentForComparison(updated)},
	}

	result, err := classifyHistoricalComponent("curl", fromComponents, toComponents)
	require.NoError(t, err)
	assert.Equal(t, changeTypeUnchanged, result.ChangeType)
}

func TestBuildComponentComparisonInputs_ContentIdentities(t *testing.T) {
	testEnv := testutils.NewTestEnvWithoutLockfile(t)
	specPath := "/specs/curl/curl.spec"
	overlayPath := "/overlays/fix.patch"

	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), specPath, []byte("Version: 1\n"), fileperms.PublicFile,
	))
	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), overlayPath, []byte("first patch\n"), fileperms.PublicFile,
	))

	component := projectconfig.ComponentConfig{
		Name: "curl",
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       specPath,
		},
		Overlays: []projectconfig.ComponentOverlay{{
			Type:     projectconfig.ComponentOverlayAddPatch,
			Filename: "fix.patch",
			Source:   overlayPath,
		}},
	}

	first, err := buildComponentComparisonInputs(testEnv.FS(), testEnv.Env, &component)
	require.NoError(t, err)
	assert.Empty(t, first.Config.Spec.Path)
	assert.Contains(t, first.SourceIdentity, "sha256:")
	assert.Regexp(t, `^fix\.patch:[0-9a-f]{64}$`, first.OverlaySourceHashes["0"])

	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), specPath, []byte("Version: 2\n"), fileperms.PublicFile,
	))
	second, err := buildComponentComparisonInputs(testEnv.FS(), testEnv.Env, &component)
	require.NoError(t, err)
	assert.NotEqual(t, first.SourceIdentity, second.SourceIdentity)
	assert.Equal(t, first.OverlaySourceHashes, second.OverlaySourceHashes)

	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), overlayPath, []byte("second patch\n"), fileperms.PublicFile,
	))
	third, err := buildComponentComparisonInputs(testEnv.FS(), testEnv.Env, &component)
	require.NoError(t, err)
	assert.Equal(t, second.SourceIdentity, third.SourceIdentity)
	assert.NotEqual(t, second.OverlaySourceHashes, third.OverlaySourceHashes)
}

func TestLoadHistoricalProject_UsesNormalIncludesAndMerging(t *testing.T) {
	rootConfig := []byte(`includes = [
    "config/components.toml",
    "config/upstream-commits/*.toml",
]

[project]
default-distro = { name = "testdistro", version = "1.0" }
rendered-specs-dir = "SPECS"

[distros.testdistro]
description = "Test distro"

[distros.testdistro.versions."1.0"]
release-ver = "1.0"

[component-groups.core]
components = ["curl"]
`)
	componentConfig := []byte(`[components.curl]
spec = {
    type = "upstream",
    upstream-distro = { name = "testdistro", version = "1.0" },
    upstream-name = "curl",
}
build = { defines = { feature = "enabled" } }
`)
	commitConfig := []byte(`# This file was generated by 'azldev component refresh-upstream-commit'
# Do not edit this file, changes will be lost
# For more details see 'azldev component refresh-upstream-commit --help'
[components.curl.spec]
upstream-commit = "abcdef1234567"
`)

	repo, hashes := testRepoWithCommits(t, []testRepoCommit{
		{files: map[string][]byte{
			"azldev.toml":                       rootConfig,
			"config/components.toml":            componentConfig,
			"config/upstream-commits/curl.toml": commitConfig,
		}},
	})

	tree, err := resolveTree(repo, hashes[0])
	require.NoError(t, err)

	testEnv := testutils.NewTestEnvWithoutLockfile(t)
	project, err := loadHistoricalProject(testEnv.Env, tree, ".")
	require.NoError(t, err)

	curlConfig, ok := project.components["curl"]
	require.True(t, ok)
	assert.Equal(t, "abcdef1234567", curlConfig.Spec.UpstreamCommit)
	assert.Equal(t, "enabled", curlConfig.Build.Defines["feature"])
	assert.Equal(t, "abcdef1234567", project.comparisonInputs["curl"].SourceIdentity)
	assert.Equal(t, "1.0", project.comparisonInputs["curl"].ReleaseVer)
	assert.Equal(t, "SPECS", project.renderedSpecsRelDir)
	assert.Equal(t, []string{"curl"}, project.componentGroups["core"])
}

func TestSelectHistoricalComponentNames_UsesBothRefs(t *testing.T) {
	fromProject := &historicalProject{
		components: map[string]projectconfig.ComponentConfig{
			"deleted": {Name: "deleted"},
			"shared":  {Name: "shared"},
		},
		componentGroups: map[string][]string{
			"historical": {"deleted"},
		},
	}
	toProject := &historicalProject{
		components: map[string]projectconfig.ComponentConfig{
			"added":  {Name: "added"},
			"shared": {Name: "shared"},
		},
		componentGroups: map[string][]string{
			"historical": {"added"},
		},
	}
	testEnv := testutils.NewTestEnvWithoutLockfile(t)

	allNames, err := selectHistoricalComponentNames(
		testEnv.Env,
		&components.ComponentFilter{IncludeAllComponents: true},
		fromProject,
		toProject,
		"/project",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"added", "deleted", "shared"}, allNames)

	groupNames, err := selectHistoricalComponentNames(
		testEnv.Env,
		&components.ComponentFilter{ComponentGroupNames: []string{"historical"}},
		fromProject,
		toProject,
		"/project",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"added", "deleted"}, groupNames)
}

func TestSelectHistoricalComponentNames_UsesHistoricalSpecPaths(t *testing.T) {
	fromProject := &historicalProject{
		components: map[string]projectconfig.ComponentConfig{
			"renamed": {
				Name: "renamed",
				Spec: projectconfig.SpecSource{Path: "/repo/project/specs/old.spec"},
			},
		},
	}
	toProject := &historicalProject{
		components: map[string]projectconfig.ComponentConfig{
			"renamed": {
				Name: "renamed",
				Spec: projectconfig.SpecSource{Path: "/repo/project/specs/new.spec"},
			},
		},
	}
	testEnv := testutils.NewTestEnvWithoutLockfile(t)

	names, err := selectHistoricalComponentNames(
		testEnv.Env,
		&components.ComponentFilter{SpecPaths: []string{"/project/specs/old.spec"}},
		fromProject,
		toProject,
		"/",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"renamed"}, names)
}

// --- resolveTree / readFileFromTree / helpers ---
