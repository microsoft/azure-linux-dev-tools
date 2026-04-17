// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package pkg_test

import (
	"testing"

	pkgcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/pkg"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPackageListCommand(t *testing.T) {
	cmd := pkgcmds.NewPackageListCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, "list [package-name...]", cmd.Use)
	assert.NotNil(t, cmd.RunE)
	assert.NotNil(t, cmd.Flags().Lookup("omit-group"), "expected --omit-group flag to be registered")
}

func TestListPackages_NoCriteria(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"g": {Packages: []string{"curl"}},
	}

	// Neither All nor PackageNames → empty result (packages exist but no selection made).
	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{})

	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestListPackages_Empty(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{All: true})

	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestListPackages_FromPackageGroup(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"devel-packages": {
			Packages: []string{"curl-devel", "wget2-devel"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{Channel: "devel"},
			},
		},
	}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{All: true})

	require.NoError(t, err)
	require.Len(t, results, 2)

	// Results are sorted by package name.
	assert.Equal(t, "curl-devel", results[0].PackageName)
	assert.Equal(t, "devel-packages", results[0].Group)
	assert.Empty(t, results[0].Component)
	assert.Equal(t, "devel", results[0].Channel)

	assert.Equal(t, "wget2-devel", results[1].PackageName)
	assert.Equal(t, "devel-packages", results[1].Group)
	assert.Equal(t, "devel", results[1].Channel)
}

func TestListPackages_FromComponentPackageOverride(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Components["curl"] = projectconfig.ComponentConfig{
		Name: "curl",
		Packages: map[string]projectconfig.PackageConfig{
			"curl-minimal": {
				Publish: projectconfig.PackagePublishConfig{Channel: "none"},
			},
		},
	}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{All: true})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "curl-minimal", results[0].PackageName)
	assert.Empty(t, results[0].Group)
	assert.Equal(t, "curl", results[0].Component)
	assert.Equal(t, "none", results[0].Channel)
}

func TestListPackages_ComponentOverrideWinsOverGroup(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"base-packages": {
			Packages: []string{"curl-devel"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{Channel: "base"},
			},
		},
	}
	testEnv.Config.Components["curl"] = projectconfig.ComponentConfig{
		Name: "curl",
		Packages: map[string]projectconfig.PackageConfig{
			"curl-devel": {
				Publish: projectconfig.PackagePublishConfig{Channel: "none"},
			},
		},
	}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{All: true})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "curl-devel", results[0].PackageName)
	assert.Equal(t, "base-packages", results[0].Group)
	assert.Equal(t, "curl", results[0].Component)
	// Component override (none) wins over group (base).
	assert.Equal(t, "none", results[0].Channel)
}

func TestListPackages_SortedByName(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"g": {
			Packages: []string{"zzz-pkg", "aaa-pkg", "mmm-pkg"},
		},
	}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{All: true})

	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, "aaa-pkg", results[0].PackageName)
	assert.Equal(t, "mmm-pkg", results[1].PackageName)
	assert.Equal(t, "zzz-pkg", results[2].PackageName)
}

func TestListPackages_ByName_InExplicitConfig(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"devel-packages": {
			Packages: []string{"curl-devel", "wget2-devel"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{Channel: "devel"},
			},
		},
	}

	// Only ask for one of the two packages in the group.
	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{PackageNames: []string{"curl-devel"}})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "curl-devel", results[0].PackageName)
	assert.Equal(t, "devel-packages", results[0].Group)
	assert.Empty(t, results[0].Component)
	assert.Equal(t, "devel", results[0].Channel)
}

func TestListPackages_ByName_NotInExplicitConfig(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.DefaultPackageConfig = projectconfig.PackageConfig{
		Publish: projectconfig.PackagePublishConfig{Channel: "default-channel"},
	}

	// Look up a package that has no explicit config; it still resolves via project defaults.
	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{PackageNames: []string{"unknown-pkg"}})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "unknown-pkg", results[0].PackageName)
	assert.Empty(t, results[0].Group)
	assert.Empty(t, results[0].Component)
	assert.Equal(t, "default-channel", results[0].Channel)
}

func TestListPackages_ByName_MultipleNames(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	// Ask for two packages; neither has explicit config, so both resolve from project defaults.
	opts := &pkgcmds.ListPackageOptions{PackageNames: []string{"zzz", "aaa"}}
	results, err := pkgcmds.ListPackages(testEnv.Env, opts)

	require.NoError(t, err)
	require.Len(t, results, 2)
	// Results are sorted by name even when supplied in reverse order.
	assert.Equal(t, "aaa", results[0].PackageName)
	assert.Equal(t, "zzz", results[1].PackageName)
}

func TestListPackages_DuplicatePackageAcrossComponents_ReturnsError(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Components["curl"] = projectconfig.ComponentConfig{
		Name: "curl",
		Packages: map[string]projectconfig.PackageConfig{
			"shared-pkg": {Publish: projectconfig.PackagePublishConfig{Channel: "base"}},
		},
	}
	testEnv.Config.Components["other"] = projectconfig.ComponentConfig{
		Name: "other",
		Packages: map[string]projectconfig.PackageConfig{
			"shared-pkg": {Publish: projectconfig.PackagePublishConfig{Channel: "none"}},
		},
	}

	_, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{All: true})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "shared-pkg")
	assert.Contains(t, err.Error(), "component overrides in multiple components")
	assert.Contains(t, err.Error(), "curl")
	assert.Contains(t, err.Error(), "other")
}

func TestListPackages_OmitGroup_ClearsGroupField(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"devel-packages": {
			Packages: []string{"curl-devel"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{Channel: "devel"},
			},
		},
	}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{
		All:       true,
		OmitGroup: true,
	})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "curl-devel", results[0].PackageName)
	assert.Empty(t, results[0].Group, "group should be empty when OmitGroup is true")
	assert.Equal(t, "devel", results[0].Channel)
}
