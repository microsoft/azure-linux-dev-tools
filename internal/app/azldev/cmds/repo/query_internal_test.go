// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repo

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/repo/repolayout"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionRepoID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "azl4-beta-base", versionRepoID("azl4-beta-base", []string{"x86_64"}, "x86_64"))
	assert.Equal(t, "azl4-beta-base-x86_64",
		versionRepoID("azl4-beta-base", []string{"x86_64", "aarch64"}, "x86_64"))
}

func TestMaterializeVersionRepos_ArchAndKindFilters(t *testing.T) {
	t.Parallel()

	effective := map[string]projectconfig.RpmRepoResource{
		"base":   {BaseURI: "https://example.com/base/$basearch", Arches: []string{"x86_64"}},
		"debug":  {BaseURI: "https://example.com/debug/$basearch"},
		"source": {BaseURI: "https://example.com/source"},
	}
	kinds := map[string]projectconfig.SubrepoKind{
		"base":   projectconfig.SubrepoKindBinary,
		"debug":  projectconfig.SubrepoKindDebug,
		"source": projectconfig.SubrepoKindSource,
	}

	out, err := materializeVersionRepos(
		[]string{"base", "debug", "source"},
		effective, kinds,
		[]string{"x86_64", "aarch64"},
		&QueryOptions{NoDebuginfo: true},
		"azl", "4.0",
	)
	require.NoError(t, err)

	// base: only x86_64 (allowlist drops aarch64).
	// debug: dropped by NoDebuginfo.
	// source: subpath has no $basearch, so per-arch fan-out yields two
	// identical URLs that DedupInputRepos collapses into one.
	urls := make([]string, 0, len(out))
	for _, repo := range out {
		urls = append(urls, repo.URL)
	}

	assert.ElementsMatch(t, []string{
		"https://example.com/base/x86_64",
		"https://example.com/source",
	}, urls)
}

func TestMaterializeVersionRepos_NoSRPMs(t *testing.T) {
	t.Parallel()

	out, err := materializeVersionRepos(
		[]string{"src"},
		map[string]projectconfig.RpmRepoResource{
			"src": {BaseURI: "https://example.com/s"},
		},
		map[string]projectconfig.SubrepoKind{"src": projectconfig.SubrepoKindSource},
		[]string{"x86_64"},
		&QueryOptions{NoSRPMs: true},
		"azl", "4.0",
	)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestMaterializeVersionRepos_GPGKeyForwarded(t *testing.T) {
	t.Parallel()

	effective := map[string]projectconfig.RpmRepoResource{
		"signed":   {BaseURI: "https://example.com/a", GPGKey: "https://example.com/key.asc"},
		"unsigned": {BaseURI: "https://example.com/b", GPGKey: "https://example.com/k", DisableGPGCheck: true},
	}

	out, err := materializeVersionRepos(
		[]string{"signed", "unsigned"},
		effective,
		map[string]projectconfig.SubrepoKind{},
		[]string{"x86_64"},
		&QueryOptions{}, "azl", "4.0",
	)
	require.NoError(t, err)
	require.Len(t, out, 2)

	byID := map[string]repolayout.InputRepo{}
	for _, repo := range out {
		byID[repo.RepoID] = repo
	}

	assert.Equal(t, "https://example.com/key.asc", byID["signed"].GPGKey)
	assert.Empty(t, byID["unsigned"].GPGKey,
		"DisableGPGCheck should suppress GPGKey forwarding")
}

func TestMaterializeVersionRepos_UndefinedRepoErrors(t *testing.T) {
	t.Parallel()

	_, err := materializeVersionRepos(
		[]string{"missing"},
		map[string]projectconfig.RpmRepoResource{},
		map[string]projectconfig.SubrepoKind{},
		[]string{"x86_64"},
		&QueryOptions{}, "azl", "4.0",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestMaterializeVersionRepos_MetalinkOnlyRejected(t *testing.T) {
	t.Parallel()

	_, err := materializeVersionRepos(
		[]string{"ml"},
		map[string]projectconfig.RpmRepoResource{"ml": {Metalink: "https://example.com/m"}},
		map[string]projectconfig.SubrepoKind{},
		[]string{"x86_64"},
		&QueryOptions{}, "azl", "4.0",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metalink-only")
}

func TestBuildDNFArgv_ForwardsGPGSetopts(t *testing.T) {
	t.Parallel()

	argv := buildDNFArgv(
		[]repolayout.InputRepo{
			{RepoID: "signed", URL: "https://example.com/a", GPGKey: "https://example.com/k"},
			{RepoID: "plain", URL: "https://example.com/b"},
		},
		[]string{"repolist"},
	)

	joined := " " + joinArgs(argv) + " "
	assert.Contains(t, joined, " --setopt=signed.gpgkey=https://example.com/k ")
	assert.Contains(t, joined, " --setopt=signed.gpgcheck=1 ")
	assert.NotContains(t, joined, "plain.gpgkey")
	assert.NotContains(t, joined, "plain.gpgcheck")
}

func TestNewQueryCmd_TemplateVersionMutuallyExclusive(t *testing.T) {
	t.Parallel()

	cmd := NewQueryCmd()
	cmd.SetArgs([]string{"--version", "4.0", "--template", "azl-standard"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template")
	assert.Contains(t, err.Error(), "version")
}

// joinArgs is a tiny helper that lets the GPG-setopt assertions match on
// whole-argv tokens (so a substring like "signed.gpgkey=" can't accidentally
// match an unrelated dnf flag).
func joinArgs(args []string) string {
	out := ""

	for i, arg := range args {
		if i > 0 {
			out += " "
		}

		out += arg
	}

	return out
}
