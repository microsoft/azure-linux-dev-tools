// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repo

import (
	"context"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/repo/repocompare"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type comparisonMapFetcher map[string][]byte

func (f comparisonMapFetcher) Fetch(_ context.Context, rawURL string, _ bool) ([]byte, error) {
	return f[rawURL], nil
}

func TestComparisonRepositoriesExpandsTemplate(t *testing.T) {
	t.Parallel()

	resources := &projectconfig.ResourcesConfig{
		RpmRepoSetTemplates: map[string]projectconfig.RpmRepoSetTemplate{
			"layout": {
				Subrepos: []projectconfig.SubrepoSpec{
					{Name: "binary", Subpath: "$basearch", Kind: projectconfig.SubrepoKindBinary},
					{Name: "src", Subpath: "src", Kind: projectconfig.SubrepoKindSource},
				},
			},
		},
		RpmRepoSets: map[string]projectconfig.RpmRepoSet{
			"build": {
				Template:         "layout",
				BaseURI:          "https://example.com/repos/build/latest",
				DisableSSLVerify: true,
			},
		},
	}

	repositories, err := comparisonRepositories(resources, "left", "build", []string{"x86_64", "aarch64"})
	require.NoError(t, err)
	require.Len(t, repositories, 3)

	assert.Equal(t, "left-binary-x86_64", repositories[0].ID)
	assert.Equal(t, "https://example.com/repos/build/latest/x86_64", repositories[0].URL)
	assert.Equal(t, projectconfig.SubrepoKindBinary, repositories[0].Kind)
	assert.True(t, repositories[0].DisableSSLVerify)
	assert.Equal(t, "left-binary-aarch64", repositories[1].ID)
	assert.Equal(t, "left-src", repositories[2].ID)
	assert.Equal(t, "https://example.com/repos/build/latest/src", repositories[2].URL)
}

func TestComparisonRepositoriesAppliesSetFilters(t *testing.T) {
	t.Parallel()

	resources := &projectconfig.ResourcesConfig{
		RpmRepoSetTemplates: map[string]projectconfig.RpmRepoSetTemplate{
			"layout": {
				Subrepos: []projectconfig.SubrepoSpec{
					{Name: "binary", Subpath: "$basearch", Kind: projectconfig.SubrepoKindBinary},
					{Name: "src", Subpath: "src", Kind: projectconfig.SubrepoKindSource},
				},
			},
		},
		RpmRepoSets: map[string]projectconfig.RpmRepoSet{
			"build": {
				Template: "layout",
				BaseURI:  "https://example.com/repos/build/latest",
				Arches:   []string{"aarch64"},
				Subrepos: []string{"binary"},
			},
		},
	}

	repositories, err := comparisonRepositories(resources, "left", "build", []string{"x86_64", "aarch64"})
	require.NoError(t, err)
	require.Len(t, repositories, 1)

	assert.Equal(t, "left-binary-aarch64", repositories[0].ID)
}

func TestComparisonRepositoriesRejectsUnknownSet(t *testing.T) {
	t.Parallel()

	_, err := comparisonRepositories(&projectconfig.ResourcesConfig{}, "left", "missing", []string{"x86_64"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpm-repo-set")
	assert.Contains(t, err.Error(), "missing")
}

func TestRunCompareReportsInventoryDifferences(t *testing.T) {
	t.Parallel()

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Resources.RpmRepoSetTemplates = map[string]projectconfig.RpmRepoSetTemplate{
		"left-layout": {
			Subrepos: []projectconfig.SubrepoSpec{{
				Name: "binary", Subpath: "$basearch", Kind: projectconfig.SubrepoKindBinary,
			}},
		},
		"right-layout": {
			Subrepos: []projectconfig.SubrepoSpec{{
				Name: "base", Subpath: "base/$basearch", Kind: projectconfig.SubrepoKindBinary,
			}},
		},
	}
	testEnv.Config.Resources.RpmRepoSets = map[string]projectconfig.RpmRepoSet{
		"left-set": {
			Template: "left-layout",
			BaseURI:  "https://left.example.com/latest",
		},
		"right-set": {
			Template: "right-layout",
			BaseURI:  "https://right.example.com/prod",
		},
	}

	const (
		leftURL  = "https://left.example.com/latest/x86_64"
		rightURL = "https://right.example.com/prod/base/x86_64"
	)

	repomd := []byte(`<repomd><data type="primary"><location href="repodata/primary.xml"/></data></repomd>`)
	primary := func(name string) []byte {
		return []byte(`<metadata><package><name>` + name + `</name><arch>x86_64</arch>` +
			`<version epoch="0" ver="1" rel="1.azl4"/></package></metadata>`)
	}

	findings, err := runCompare(
		testEnv.Env,
		&CompareOptions{
			Left:   "left-set",
			Right:  "right-set",
			Arches: []string{"x86_64"},
		},
		comparisonMapFetcher{
			leftURL + "/repodata/repomd.xml":   repomd,
			leftURL + "/repodata/primary.xml":  primary("left-only"),
			rightURL + "/repodata/repomd.xml":  repomd,
			rightURL + "/repodata/primary.xml": primary("right-only"),
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []repocompare.PackageReport{
		{Name: "left-only", Summary: "missing-from-right", LeftNEVRs: "left-only-1-1.azl4"},
		{Name: "right-only", Summary: "added-in-right", RightNEVRs: "right-only-1-1.azl4"},
	}, findings)
}
