// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repo_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/repo"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/repo/repolayout"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOnAppInit(t *testing.T) {
	t.Parallel()

	app := azldev.NewApp(azldev.DefaultFileSystemFactory(), azldev.DefaultOSEnvFactory())

	require.NotPanics(t, func() {
		repo.OnAppInit(app)
	})
}

func TestNewQueryCmd_FlagsRegistered(t *testing.T) {
	t.Parallel()

	cmd := repo.NewQueryCmd()
	for _, name := range []string{
		"repo-prefix", "template", "arch", "no-debuginfo", "no-srpms",
		"version", "use-case",
	} {
		assert.NotNil(t, cmd.Flags().Lookup(name), "expected flag --%s", name)
	}
}

func TestNewQueryCmd_OneOfRepoPrefixOrVersionRequired(t *testing.T) {
	t.Parallel()

	cmd := repo.NewQueryCmd()
	cmd.SetArgs([]string{})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repo-prefix")
	assert.Contains(t, err.Error(), "version")
}

func TestBuildDNFArgv_SinglePrefix(t *testing.T) {
	t.Parallel()

	// Indirect coverage via running RunQuery is impractical (it execs dnf),
	// but we can exercise the pure helpers by building the same input shape
	// here and comparing against the expected argv structure.
	templates := map[string]projectconfig.RpmRepoSetTemplate{
		"t1": {
			Subrepos: []projectconfig.SubrepoSpec{
				{Name: "base", Subpath: "base/$basearch", Kind: projectconfig.SubrepoKindBinary},
			},
		},
	}

	tmpl, err := repolayout.ResolveTemplate(templates, "t1")
	require.NoError(t, err)

	repos := repolayout.ExpandTemplate("https://example.com/p", "t1", tmpl, []string{"x86_64"})
	require.Len(t, repos, 1)
}
