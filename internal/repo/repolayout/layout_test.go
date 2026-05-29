// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repolayout_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/repo/repolayout"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleTemplates() map[string]projectconfig.RpmRepoSetTemplate {
	return map[string]projectconfig.RpmRepoSetTemplate{
		repolayout.DefaultTemplateName: {
			Subrepos: []projectconfig.SubrepoSpec{
				{Name: "base", Subpath: "base/$basearch", Kind: projectconfig.SubrepoKindBinary},
				{Name: "base-debug", Subpath: "base/debuginfo/$basearch", Kind: projectconfig.SubrepoKindDebug},
				{Name: "base-src", Subpath: "base/srpms", Kind: projectconfig.SubrepoKindSource},
				{Name: "sdk", Subpath: "sdk/$basearch", Kind: projectconfig.SubrepoKindBinary},
				{Name: "sdk-debug", Subpath: "sdk/debuginfo/$basearch", Kind: projectconfig.SubrepoKindDebug},
				{Name: "sdk-src", Subpath: "sdk/srpms", Kind: projectconfig.SubrepoKindSource},
			},
		},
	}
}

func TestResolveTemplate_Found(t *testing.T) {
	t.Parallel()

	tmpl, err := repolayout.ResolveTemplate(sampleTemplates(), repolayout.DefaultTemplateName)
	require.NoError(t, err)
	assert.Len(t, tmpl.Subrepos, 6)
}

func TestResolveTemplate_NotFound(t *testing.T) {
	t.Parallel()

	_, err := repolayout.ResolveTemplate(sampleTemplates(), "no-such-template")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not defined")
}

func TestResolveTemplate_EmptyName(t *testing.T) {
	t.Parallel()

	_, err := repolayout.ResolveTemplate(sampleTemplates(), "")
	require.Error(t, err)
}

func TestExpandTemplate(t *testing.T) {
	t.Parallel()

	tmpl, err := repolayout.ResolveTemplate(sampleTemplates(), repolayout.DefaultTemplateName)
	require.NoError(t, err)

	repos := repolayout.ExpandTemplate(
		"https://example.com/prefix/",
		repolayout.DefaultTemplateName,
		tmpl,
		[]string{"x86_64", "aarch64"},
	)

	// 2 channels x (2 per-arch binary + 2 per-arch debug + 1 source) = 10.
	require.Len(t, repos, 10)

	for _, repo := range repos {
		assert.NotContains(t, repo.URL, "$basearch", "$basearch must be expanded")
		assert.Equal(t, repolayout.DefaultTemplateName, repo.TemplateName)
	}

	// Spot-check the base/binary x86_64 row.
	var foundBase bool

	for _, repo := range repos {
		if repo.URL == "https://example.com/prefix/base/x86_64" {
			foundBase = true

			assert.Equal(t, "base", repo.SubrepoName)
			assert.Equal(t, projectconfig.SubrepoKindBinary, repo.Kind)
			assert.Equal(t, "x86_64", repo.Arch)
		}
	}

	assert.True(t, foundBase, "expected base/x86_64 row")

	// Source subrepo has empty Arch (no $basearch in subpath).
	var foundSource bool

	for _, repo := range repos {
		if repo.SubrepoName == "base-src" {
			foundSource = true

			assert.Empty(t, repo.Arch)
			assert.Equal(t, projectconfig.SubrepoKindSource, repo.Kind)
		}
	}

	assert.True(t, foundSource, "expected base-src row")
}

func TestDedupInputRepos(t *testing.T) {
	t.Parallel()

	repos := []repolayout.InputRepo{
		{SubrepoName: "base", Arch: "x86_64", URL: "https://a/x86_64"},
		{SubrepoName: "base", Arch: "x86_64", URL: "https://a/x86_64"},
		{SubrepoName: "base", Arch: "aarch64", URL: "https://a/aarch64"},
	}

	got := repolayout.DedupInputRepos(repos)
	require.Len(t, got, 2)
	assert.Equal(t, "https://a/x86_64", got[0].URL)
	assert.Equal(t, "https://a/aarch64", got[1].URL)
}

func TestNormalizePrefix(t *testing.T) {
	t.Parallel()

	got, err := repolayout.NormalizePrefix("https://example.com/foo/")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/foo", got)

	got, err = repolayout.NormalizePrefix("file:///tmp/repo/")
	require.NoError(t, err)
	assert.Equal(t, "file:///tmp/repo", got)

	_, err = repolayout.NormalizePrefix("./testdata/repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http://")

	_, err = repolayout.NormalizePrefix("")
	require.Error(t, err)
}
