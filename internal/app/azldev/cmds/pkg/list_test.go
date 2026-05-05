// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package pkg_test

import (
	"encoding/json"
	"testing"

	pkgcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/pkg"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPackageListCommand(t *testing.T) {
	cmd := pkgcmds.NewPackageListCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, "list [package-name...]", cmd.Use)
	assert.NotNil(t, cmd.RunE)
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

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{})

	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestListPackages_FromPackageGroup(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"devel-packages": {
			Packages: []string{"curl-devel", "wget2-devel"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "devel"},
			},
		},
	}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{
		PackageNames: []string{"curl-devel", "wget2-devel"},
	})

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
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "none"},
			},
		},
	}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{PackageNames: []string{"curl-minimal"}})

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
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "base"},
			},
		},
	}
	testEnv.Config.Components["curl"] = projectconfig.ComponentConfig{
		Name: "curl",
		Packages: map[string]projectconfig.PackageConfig{
			"curl-devel": {
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "none"},
			},
		},
	}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{PackageNames: []string{"curl-devel"}})

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

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{
		PackageNames: []string{"zzz-pkg", "aaa-pkg", "mmm-pkg"},
	})

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
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "devel"},
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
		Publish: projectconfig.PackagePublishConfig{RPMChannel: "default-channel"},
	}

	// Look up a package that has no component publish config or package group.
	// DefaultPackageConfig acts as the lowest-priority fallback, so the channel resolves
	// to the project default when no higher-priority source provides one.
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
			"shared-pkg": {Publish: projectconfig.PackagePublishConfig{RPMChannel: "base"}},
		},
	}
	testEnv.Config.Components["other"] = projectconfig.ComponentConfig{
		Name: "other",
		Packages: map[string]projectconfig.PackageConfig{
			"shared-pkg": {Publish: projectconfig.PackagePublishConfig{RPMChannel: "none"}},
		},
	}

	_, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{PackageNames: []string{"shared-pkg"}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "shared-pkg")
	assert.Contains(t, err.Error(), "component overrides in multiple components")
	assert.Contains(t, err.Error(), "curl")
	assert.Contains(t, err.Error(), "other")
}

func TestListPackages_SynthesizeDebugPackages(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.DefaultPackageConfig = projectconfig.PackageConfig{
		Publish: projectconfig.PackagePublishConfig{RPMChannel: "default-channel"},
	}
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"devel-packages": {
			Packages: []string{"curl-devel"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "devel"},
			},
		},
	}
	testEnv.Config.Components["curl"] = projectconfig.ComponentConfig{Name: "curl"}
	testEnv.Config.Components["wget"] = projectconfig.ComponentConfig{Name: "wget"}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{
		PackageNames:            []string{"curl-devel"},
		SynthesizeDebugPackages: true,
	})

	require.NoError(t, err)

	byName := make(map[string]pkgcmds.PackageListResult, len(results))
	for _, result := range results {
		byName[result.PackageName] = result
	}

	// Original package present.
	require.Contains(t, byName, "curl-devel")
	assert.Equal(t, "devel", byName["curl-devel"].Channel)

	// '-debuginfo' synthesized for each reported package on the parallel debug channel.
	require.Contains(t, byName, "curl-devel-debuginfo")
	assert.Equal(t, "devel-debuginfo", byName["curl-devel-debuginfo"].Channel)
	assert.Equal(t, "devel-packages", byName["curl-devel-debuginfo"].Group)

	// '-debugsource' synthesized for each component, with its channel resolved through the
	// component → project default chain and suffixed onto the parallel debug channel.
	require.Contains(t, byName, "curl-debugsource")
	assert.Equal(t, "default-channel-debuginfo", byName["curl-debugsource"].Channel)
	assert.Equal(t, "curl", byName["curl-debugsource"].Component)
	assert.Empty(t, byName["curl-debugsource"].Group)

	require.Contains(t, byName, "wget-debugsource")
	assert.Equal(t, "default-channel-debuginfo", byName["wget-debugsource"].Channel)
	assert.Equal(t, "wget", byName["wget-debugsource"].Component)
}

func TestListPackages_SynthesizeDebugPackages_SkipsExisting(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"g": {
			Packages: []string{"curl", "curl-debuginfo"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "real", DebugInfoChannel: "real"},
			},
		},
	}
	testEnv.Config.Components["curl"] = projectconfig.ComponentConfig{
		Name: "curl",
		Packages: map[string]projectconfig.PackageConfig{
			"curl-debugsource": {Publish: projectconfig.PackagePublishConfig{DebugInfoChannel: "explicit"}},
		},
	}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{
		PackageNames:            []string{"curl", "curl-debuginfo", "curl-debugsource"},
		SynthesizeDebugPackages: true,
	})

	require.NoError(t, err)

	// No duplicate entries — real config wins for both -debuginfo and -debugsource.
	seen := make(map[string]int)
	for _, result := range results {
		seen[result.PackageName]++
	}

	assert.Equal(t, 1, seen["curl-debuginfo"])
	assert.Equal(t, 1, seen["curl-debugsource"])
	// The doubled-suffix names must NOT be synthesized — guard against recursive synthesis
	// when a real '-debuginfo' / '-debugsource' is already in the listed set.
	assert.NotContains(t, seen, "curl-debuginfo-debuginfo")
	assert.NotContains(t, seen, "curl-debugsource-debuginfo")
	// The pre-existing curl-debuginfo keeps its real channel, not a synthesized override.
	for _, result := range results {
		if result.PackageName == "curl-debuginfo" {
			assert.Equal(t, "real", result.Channel)
		}

		if result.PackageName == "curl-debugsource" {
			assert.Equal(t, "explicit", result.Channel)
		}
	}
}

func TestListPackages_SynthesizeDebugPackages_ByName(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.DefaultPackageConfig = projectconfig.PackageConfig{
		Publish: projectconfig.PackagePublishConfig{RPMChannel: "default-channel"},
	}
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"devel-packages": {
			Packages: []string{"curl-devel"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "devel"},
			},
		},
	}
	testEnv.Config.Components["curl"] = projectconfig.ComponentConfig{Name: "curl"}

	// The headline CLI path: -p PKG with the synthesize flag.
	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{
		PackageNames:            []string{"curl-devel"},
		SynthesizeDebugPackages: true,
	})

	require.NoError(t, err)

	byName := make(map[string]pkgcmds.PackageListResult, len(results))
	for _, result := range results {
		byName[result.PackageName] = result
	}

	require.Contains(t, byName, "curl-devel")
	assert.Equal(t, "devel", byName["curl-devel"].Channel)

	// '-debuginfo' synthesized on the parallel debug channel for the requested package.
	require.Contains(t, byName, "curl-devel-debuginfo")
	assert.Equal(t, "devel-debuginfo", byName["curl-devel-debuginfo"].Channel)

	// '-debugsource' synthesized for every component in the project, regardless of which
	// packages were requested. Channel resolves to the project default + '-debuginfo'.
	require.Contains(t, byName, "curl-debugsource")
	assert.Equal(t, "default-channel-debuginfo", byName["curl-debugsource"].Channel)
}

func TestListPackages_SynthesizeDebugPackages_ChannelSuffixRules(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.PackageGroups = map[string]projectconfig.PackageGroupConfig{
		"none-grp": {
			Packages: []string{"pkg-none"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "none"},
			},
		},
		"empty-grp": {
			// No DefaultPackageConfig → resolves to "" (no channel configured).
			Packages: []string{"pkg-empty"},
		},
		"already-grp": {
			Packages: []string{"pkg-already"},
			DefaultPackageConfig: projectconfig.PackageConfig{
				Publish: projectconfig.PackagePublishConfig{RPMChannel: "ms-debuginfo"},
			},
		},
	}

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{
		PackageNames:            []string{"pkg-none", "pkg-empty", "pkg-already"},
		SynthesizeDebugPackages: true,
	})

	require.NoError(t, err)

	byName := make(map[string]pkgcmds.PackageListResult, len(results))
	for _, result := range results {
		byName[result.PackageName] = result
	}

	// "none" passes through unchanged — debug artifacts inherit the do-not-publish intent.
	assert.Equal(t, "none", byName["pkg-none-debuginfo"].Channel)
	// Empty passes through unchanged — downstream applies the configured default.
	assert.Empty(t, byName["pkg-empty-debuginfo"].Channel)
	// Already-suffixed channels are not doubled.
	assert.Equal(t, "ms-debuginfo", byName["pkg-already-debuginfo"].Channel)
}

func TestListPackages_ComponentGroupPublishChannel(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	// Project-wide default — lowest priority.
	testEnv.Config.DefaultComponentConfig = projectconfig.ComponentConfig{
		Publish: projectconfig.ComponentPublishConfig{RPMChannel: "rpm-sdk"},
	}

	// Component group with a higher-priority publish channel.
	testEnv.Config.ComponentGroups["base-published"] = projectconfig.ComponentGroupConfig{
		Components: []string{"jq"},
		DefaultComponentConfig: projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{RPMChannel: "rpm-base"},
		},
	}
	testEnv.Config.GroupsByComponent["jq"] = []string{"base-published"}

	// No explicit [components.jq] entry — the component is defined only via group membership.
	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{PackageNames: []string{"jq"}})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "jq", results[0].PackageName)
	// The group's rpm-base channel must win over the project default rpm-sdk.
	assert.Equal(t, "rpm-base", results[0].Channel)
}

func TestListPackages_SRPMFile_UsesSRPMChannel(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	// Project default component config sets both RPM and SRPM channels.
	testEnv.Config.DefaultComponentConfig = projectconfig.ComponentConfig{
		Publish: projectconfig.ComponentPublishConfig{
			RPMChannel:  "rpm-sdk",
			SRPMChannel: "rpm-sdk-srpm",
		},
	}

	// Component with a higher-priority SRPM channel override.
	testEnv.Config.Components["curl"] = projectconfig.ComponentConfig{
		Name: "curl",
		Publish: projectconfig.ComponentPublishConfig{
			RPMChannel:  "rpm-base",
			SRPMChannel: "rpm-base-srpm",
		},
	}

	const srpmMapPath = "/test-srpm-map.json"

	entries := []map[string]string{
		// 389-ds-base has no component entry — resolves from project default.
		{"packageName": "389-ds-base", "sourcePackageName": "389-ds-base"},
		{"packageName": "389-ds-base-devel", "sourcePackageName": "389-ds-base"},
		// curl has an explicit component entry — resolves from component config.
		{"packageName": "curl", "sourcePackageName": "curl"},
		{"packageName": "curl-devel", "sourcePackageName": "curl"},
	}

	data, jsonErr := json.Marshal(entries)
	require.NoError(t, jsonErr)
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, srpmMapPath, data, fileperms.PublicFile))

	results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{RPMFile: srpmMapPath})
	require.NoError(t, err)

	byName := make(map[string]pkgcmds.PackageListResult, len(results))
	for _, r := range results {
		byName[r.PackageName+"#"+r.Type] = r
	}

	// SRPMs must use SRPMChannel, not RPMChannel.
	srpm389 := byName["389-ds-base#"+pkgcmds.PackageTypeSRPM]
	assert.Equal(t, pkgcmds.PackageTypeSRPM, srpm389.Type)
	assert.Equal(t, "rpm-sdk-srpm", srpm389.Channel, "SRPM should use SRPMChannel from project default")

	srpmCurl := byName["curl#"+pkgcmds.PackageTypeSRPM]
	assert.Equal(t, pkgcmds.PackageTypeSRPM, srpmCurl.Type)
	assert.Equal(t, "rpm-base-srpm", srpmCurl.Channel, "SRPM should use SRPMChannel from component config")

	// RPMs must use RPMChannel, not SRPMChannel.
	rpm389 := byName["389-ds-base#"+pkgcmds.PackageTypeRPM]
	assert.Equal(t, pkgcmds.PackageTypeRPM, rpm389.Type)
	assert.Equal(t, "rpm-sdk", rpm389.Channel, "binary RPM should use RPMChannel from project default")

	rpmCurlDevel := byName["curl-devel#"+pkgcmds.PackageTypeRPM]
	assert.Equal(t, pkgcmds.PackageTypeRPM, rpmCurlDevel.Type)
	// curl-devel's component is resolved from the JSON (sourcePackageName = "curl"),
	// so it correctly inherits the curl component's RPMChannel.
	assert.Equal(t, "rpm-base", rpmCurlDevel.Channel, "binary RPM should use RPMChannel from its SRPM's component")
}

// TestListPackages_RPMFile_Validation exercises the JSON parsing and validation
// error paths in 'loadRPMFile' (invalid JSON, missing fields, conflicting
// mappings) and the silent dedup path for repeated identical mappings.
func TestListPackages_RPMFile_Validation(t *testing.T) {
	const path = "/test-rpm-map.json"

	cases := []struct {
		name        string
		body        string // raw file contents (not necessarily valid JSON)
		wantErrSub  string // expected substring in the returned error; empty means success
		wantResults int    // expected number of results on success
	}{
		{
			name:       "invalid json",
			body:       "not json",
			wantErrSub: "parsing RPM source map",
		},
		{
			name:       "empty packageName",
			body:       `[{"packageName":"","sourcePackageName":"bash"}]`,
			wantErrSub: "missing non-empty 'packageName'",
		},
		{
			name:       "empty sourcePackageName",
			body:       `[{"packageName":"bash","sourcePackageName":""}]`,
			wantErrSub: "missing non-empty 'sourcePackageName'",
		},
		{
			// Conflicting source package names must dedup (first mapping wins):
			// one SRPM result for "bash" + one RPM result, not two of either.
			name: "conflicting source package names dedup",
			body: `[
				{"packageName":"bash","sourcePackageName":"bash"},
				{"packageName":"bash","sourcePackageName":"other"}
			]`,
			wantResults: 2,
		},
		{
			// Identical duplicate mappings must not produce duplicate entries:
			// one SRPM result + one RPM result, not one SRPM + two RPMs.
			name: "duplicate identical mappings dedup",
			body: `[
				{"packageName":"bash","sourcePackageName":"bash"},
				{"packageName":"bash","sourcePackageName":"bash"}
			]`,
			wantResults: 2,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			testEnv := testutils.NewTestEnv(t)
			require.NoError(t, fileutils.WriteFile(testEnv.TestFS, path, []byte(testCase.body), fileperms.PublicFile))

			results, err := pkgcmds.ListPackages(testEnv.Env, &pkgcmds.ListPackageOptions{RPMFile: path})

			if testCase.wantErrSub != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErrSub)
				assert.Nil(t, results)

				return
			}

			require.NoError(t, err)
			assert.Len(t, results, testCase.wantResults)
		})
	}
}
