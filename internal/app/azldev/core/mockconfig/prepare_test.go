// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mockconfig

import (
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPyRepr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "hello", want: `"hello"`},
		{name: "double-quote", in: `it"s`, want: `"it\"s"`},
		{name: "single-quote-untouched", in: "it's", want: `"it's"`},
		{name: "backslash", in: `a\b`, want: `"a\\b"`},
		{name: "newline", in: "a\nb", want: `"a\nb"`},
		{name: "tab", in: "a\tb", want: `"a\tb"`},
		{name: "nul-byte", in: "a\x00b", want: `"a\u0000b"`},
		{name: "u2028-line-sep", in: "a\u2028b", want: `"a\u2028b"`},
		{name: "u2029-para-sep", in: "a\u2029b", want: `"a\u2029b"`},
		{name: "dollar-sign-untouched", in: "a/$basearch", want: `"a/$basearch"`},
		{name: "empty", in: "", want: `""`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, pyRepr(tc.in))
		})
	}
}

func TestRenderSiteDefaults_Empty(t *testing.T) {
	t.Parallel()

	got := renderSiteDefaults(nil)

	assert.Contains(t, got, "config_opts['azl_repos'] = [\n]\n")
}

func TestRenderSiteDefaults_BaseURIRepo(t *testing.T) {
	t.Parallel()

	repos := []namedRepo{{
		Name: "test-base",
		Repo: projectconfig.RpmRepoResource{
			Description:     "ignored",
			BaseURI:         "https://example.com/$basearch",
			DisableGPGCheck: true,
		},
	}}

	got := renderSiteDefaults(repos)

	assert.Contains(t, got, `"name": "test-base"`)
	assert.Contains(t, got, `"baseurl": "https://example.com/$basearch"`)
	assert.Contains(t, got, `"gpgcheck": False`)
	// Description must NOT be projected into dnf.
	assert.NotContains(t, got, "ignored")
	// Removed fields must not appear.
	assert.NotContains(t, got, "priority")
	assert.NotContains(t, got, "description")
}

func TestRenderSiteDefaults_GPGCheckEnabledByDefault(t *testing.T) {
	t.Parallel()

	repos := []namedRepo{{
		Name: "signed",
		Repo: projectconfig.RpmRepoResource{
			BaseURI: "https://example.com/repo",
			GPGKey:  "file:///etc/keys/example.gpg",
		},
	}}

	got := renderSiteDefaults(repos)

	// DisableGPGCheck is the zero value -> dnf gpgcheck must be True.
	assert.Contains(t, got, `"gpgcheck": True`)
	assert.Contains(t, got, `"gpgkey": "file:///etc/keys/example.gpg"`)
}

func TestRenderSiteDefaults_MetalinkRepo(t *testing.T) {
	t.Parallel()

	repos := []namedRepo{{
		Name: "ml",
		Repo: projectconfig.RpmRepoResource{
			Metalink:        "https://mirrors.example.com/metalink?repo=foo",
			DisableGPGCheck: true,
		},
	}}

	got := renderSiteDefaults(repos)

	assert.Contains(t, got, `"metalink": "https://mirrors.example.com/metalink?repo=foo"`)
	assert.NotContains(t, got, "baseurl")
}

func TestPrepareForRPMBuild_StagesAndGenerates(t *testing.T) {
	t.Parallel()

	testEnv := testutils.NewTestEnv(t)
	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, "/work"))

	cfgPath, err := PrepareForRPMBuild(testEnv.Env)
	require.NoError(t, err)

	// Returned cfg path lives under WorkDir, not at the source location.
	assert.True(t, strings.HasPrefix(cfgPath, "/work/"), "expected staged cfg under work dir, got %q", cfgPath)
	assert.True(t, strings.HasSuffix(cfgPath, "/mock.cfg"))

	// site-defaults.cfg must be present and contain azl_repos.
	siteDefaults, readErr := fileutils.ReadFile(testEnv.TestFS, strings.TrimSuffix(cfgPath, "mock.cfg")+"site-defaults.cfg")
	require.NoError(t, readErr)
	assert.Contains(t, string(siteDefaults), "config_opts['azl_repos']")
	assert.Contains(t, string(siteDefaults), "test-repo")
	assert.Contains(t, string(siteDefaults), "https://example.com/test-repo/$basearch")
}

func TestPrepareForRPMBuild_NoInputs_StagesEmptyAndWarns(t *testing.T) {
	t.Parallel()

	testEnv := testutils.NewTestEnv(t)
	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, "/work"))

	// Drop the configured inputs.
	dv := testEnv.Config.Distros["test-distro"].Versions["1.0"]
	dv.Inputs.RpmBuild = nil
	testEnv.Config.Distros["test-distro"].Versions["1.0"] = dv

	cfgPath, err := PrepareForRPMBuild(testEnv.Env)
	require.NoError(t, err)

	// Even with no inputs, we still stage and generate an (empty) site-defaults.cfg.
	assert.True(t, strings.HasPrefix(cfgPath, "/work/"))

	siteDefaults, readErr := fileutils.ReadFile(testEnv.TestFS, strings.TrimSuffix(cfgPath, "mock.cfg")+"site-defaults.cfg")
	require.NoError(t, readErr)
	assert.Contains(t, string(siteDefaults), "config_opts['azl_repos'] = [\n]")
}

func TestPrepareForRPMBuild_UndefinedRepoErrors(t *testing.T) {
	t.Parallel()

	testEnv := testutils.NewTestEnv(t)

	dv := testEnv.Config.Distros["test-distro"].Versions["1.0"]
	dv.Inputs.RpmBuild = []string{"does-not-exist"}
	testEnv.Config.Distros["test-distro"].Versions["1.0"] = dv

	_, err := PrepareForRPMBuild(testEnv.Env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist")
	assert.Contains(t, err.Error(), "[resources.rpm-repos]")
}
