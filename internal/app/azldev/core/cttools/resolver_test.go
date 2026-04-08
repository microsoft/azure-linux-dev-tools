// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cttools_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/cttools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveTemplates_BasicResolution(t *testing.T) {
	config := &cttools.DistroConfig{
		Distros: map[string]cttools.Distro{
			"testdistro": {
				Description: "Test",
				Versions: map[string]cttools.Version{
					"1.0": {
						Description:       "v1.0",
						ReleaseVer:        "1.0",
						EnvironmentPrefix: "td1-dev",
						RPMMacroDist:      ".td1-dev",
						GitSourceRepos: map[string][]cttools.GitSourceRepo{
							"main": {
								{
									Ref:                  "https://example.com/repo.git",
									DefaultBranch:        "main",
									DefaultKojiRPMTarget: "td1-dev-rpms-target",
									KojiTargets:          "base",
									ParentPrefix:         "td1-dev",
								},
							},
						},
					},
				},
			},
		},
		KojiTargetsTemplates: map[string]map[string][]cttools.KojiTarget{
			"base": {
				"rpms-target": {
					{
						OutputTag:       "rpms-tag",
						ParentTag:       "bootstrap-rpms-tag",
						BuildRoots:      []cttools.BuildRootRef{{Type: "build", Value: "rpm"}},
						MockOptionsBase: "rpm",
						MockDistTag:     "rpm-macro-dist",
					},
				},
			},
		},
		MockOptionsTemplates: map[string]cttools.MockOptionsTemplate{
			"rpm": {Options: []string{"opt1", "opt2"}},
		},
		BuildRootTemplates: map[string]cttools.BuildRootTemplate{
			"rpm": {Packages: []string{"bash", "gcc"}},
		},
	}

	err := cttools.ResolveTemplates(config)
	require.NoError(t, err)

	repos := config.Distros["testdistro"].Versions["1.0"].GitSourceRepos["main"]
	require.Len(t, repos, 1)

	resolved := repos[0].ResolvedKojiTargets
	require.Len(t, resolved, 1)

	target := resolved[0]
	assert.Equal(t, "td1-dev-rpms-target", target.Name)
	assert.Equal(t, "td1-dev-rpms-tag", target.OutputTag)
	assert.Equal(t, "td1-dev-bootstrap-rpms-tag", target.ParentTag)
	assert.Equal(t, []string{"opt1", "opt2"}, target.MockOptions)
	assert.Equal(t, ".td1-dev", target.MockDistTag)

	require.Len(t, target.BuildRoots, 1)
	assert.Equal(t, "build", target.BuildRoots[0].Type)
	assert.Equal(t, []string{"bash", "gcc"}, target.BuildRoots[0].Packages)
}

func TestResolveTemplates_WithRepoPrefix(t *testing.T) {
	config := &cttools.DistroConfig{
		Distros: map[string]cttools.Distro{
			"d": {
				Description: "D",
				Versions: map[string]cttools.Version{
					"1.0": {
						Description:           "v1.0",
						ReleaseVer:            "1.0",
						EnvironmentPrefix:     "azl4-dev",
						RPMMacroDist:          ".azl4-dev",
						RPMMacroDistBootstrap: ".azl4-dev~bootstrap",
						GitSourceRepos: map[string][]cttools.GitSourceRepo{
							"nvidia": {
								{
									Ref:                  "https://example.com/nvidia.git",
									DefaultBranch:        "main",
									DefaultKojiRPMTarget: "azl4-dev-nvidia-rpms-target",
									KojiTargets:          "prop",
									RepoPrefix:           "nvidia",
									ParentPrefix:         "azl4-dev",
								},
							},
						},
					},
				},
			},
		},
		KojiTargetsTemplates: map[string]map[string][]cttools.KojiTarget{
			"prop": {
				"bootstrap-rpms-target": {
					{
						OutputTag:       "bootstrap-rpms-tag",
						ParentTag:       "bootstrap-rpms-tag",
						BuildRoots:      []cttools.BuildRootRef{{Type: "build", Value: "rpm"}},
						MockOptionsBase: "rpm",
						MockDistTag:     "rpm-macro-dist-bootstrap",
					},
				},
				"rpms-target": {
					{
						OutputTag:       "rpms-tag",
						ParentTag:       "rpms-tag",
						BuildRoots:      []cttools.BuildRootRef{{Type: "build", Value: "rpm"}},
						MockOptionsBase: "rpm",
						MockDistTag:     "rpm-macro-dist",
					},
				},
			},
		},
		MockOptionsTemplates: map[string]cttools.MockOptionsTemplate{
			"rpm": {Options: []string{"opt1"}},
		},
		BuildRootTemplates: map[string]cttools.BuildRootTemplate{
			"rpm": {Packages: []string{"bash"}},
		},
	}

	err := cttools.ResolveTemplates(config)
	require.NoError(t, err)

	repos := config.Distros["d"].Versions["1.0"].GitSourceRepos["nvidia"]
	require.Len(t, repos, 1)

	resolved := repos[0].ResolvedKojiTargets
	require.Len(t, resolved, 2)

	names := make(map[string]cttools.ResolvedKojiTarget, len(resolved))
	for _, rt := range resolved {
		names[rt.Name] = rt
	}

	bootstrap := names["azl4-dev-nvidia-bootstrap-rpms-target"]
	assert.Equal(t, "azl4-dev-nvidia-bootstrap-rpms-tag", bootstrap.OutputTag)
	assert.Equal(t, "azl4-dev-bootstrap-rpms-tag", bootstrap.ParentTag)
	assert.Equal(t, ".azl4-dev~bootstrap", bootstrap.MockDistTag)

	rpms := names["azl4-dev-nvidia-rpms-target"]
	assert.Equal(t, "azl4-dev-nvidia-rpms-tag", rpms.OutputTag)
	assert.Equal(t, "azl4-dev-rpms-tag", rpms.ParentTag)
	assert.Equal(t, ".azl4-dev", rpms.MockDistTag)
}

func TestResolveTemplates_ExternalReposCopied(t *testing.T) {
	config := &cttools.DistroConfig{
		Distros: map[string]cttools.Distro{
			"d": {
				Description: "D",
				Versions: map[string]cttools.Version{
					"1.0": {
						Description:       "v1.0",
						ReleaseVer:        "1.0",
						EnvironmentPrefix: "p",
						RPMMacroDist:      ".p",
						GitSourceRepos: map[string][]cttools.GitSourceRepo{
							"r": {{
								Ref:                  "https://example.com",
								DefaultBranch:        "main",
								DefaultKojiRPMTarget: "p-rpms-target",
								KojiTargets:          "tmpl",
								ParentPrefix:         "p",
							}},
						},
					},
				},
			},
		},
		KojiTargetsTemplates: map[string]map[string][]cttools.KojiTarget{
			"tmpl": {
				"rpms-target": {{
					OutputTag:       "rpms-tag",
					BuildRoots:      []cttools.BuildRootRef{{Type: "build", Value: "rpm"}},
					MockOptionsBase: "rpm",
					ExternalRepos: []cttools.ExternalRepo{
						{Name: "fedora", URL: "https://fedora.example.com", MergeMode: "bare"},
					},
				}},
			},
		},
		MockOptionsTemplates: map[string]cttools.MockOptionsTemplate{
			"rpm": {Options: []string{"opt1"}},
		},
		BuildRootTemplates: map[string]cttools.BuildRootTemplate{
			"rpm": {Packages: []string{"bash"}},
		},
	}

	err := cttools.ResolveTemplates(config)
	require.NoError(t, err)

	resolved := config.Distros["d"].Versions["1.0"].GitSourceRepos["r"][0].ResolvedKojiTargets
	require.Len(t, resolved, 1)
	require.Len(t, resolved[0].ExternalRepos, 1)
	assert.Equal(t, "fedora", resolved[0].ExternalRepos[0].Name)
}

func TestResolveTemplates_MissingTemplate(t *testing.T) {
	config := &cttools.DistroConfig{
		Distros: map[string]cttools.Distro{
			"d": {
				Description: "D",
				Versions: map[string]cttools.Version{
					"1.0": {
						Description:       "v1.0",
						ReleaseVer:        "1.0",
						EnvironmentPrefix: "p",
						GitSourceRepos: map[string][]cttools.GitSourceRepo{
							"r": {{
								Ref:                  "https://example.com",
								DefaultBranch:        "main",
								DefaultKojiRPMTarget: "p-rpms-target",
								KojiTargets:          "nonexistent",
								ParentPrefix:         "p",
							}},
						},
					},
				},
			},
		},
		KojiTargetsTemplates: map[string]map[string][]cttools.KojiTarget{},
	}

	err := cttools.ResolveTemplates(config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestFilterEnvironment_Found(t *testing.T) {
	config := &cttools.DistroConfig{
		Environments: map[string]cttools.Environment{
			"ct-dev":  {Resources: map[string][]map[string]any{"r1": {{"interface-type": "rpm-repo"}}}},
			"ct-prod": {Resources: map[string][]map[string]any{"r2": {{"interface-type": "image-gallery"}}}},
		},
	}

	err := cttools.FilterEnvironment(config, "ct-dev")
	require.NoError(t, err)

	require.Len(t, config.Environments, 1)
	require.Contains(t, config.Environments, "ct-dev")
}

func TestFilterEnvironment_NotFound(t *testing.T) {
	config := &cttools.DistroConfig{
		Environments: map[string]cttools.Environment{
			"ct-dev": {Resources: map[string][]map[string]any{}},
		},
	}

	err := cttools.FilterEnvironment(config, "ct-staging")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ct-staging")
	assert.Contains(t, err.Error(), "not found")
}
