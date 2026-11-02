// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoVendorConfigFileSelection(t *testing.T) {
	for _, local := range []string{"", "[archive]\ntidy = false\n"} {
		ctx := testctx.NewCtx()
		require.NoError(t, fileutils.WriteFile(ctx.FS(), "/prepared/upstream.toml",
			[]byte("[licensing]\ndetector = 'askalono'\n"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), "/project/local.toml", []byte(local), fileperms.PublicFile))
		preparer := &sourcePreparerImpl{fs: ctx.FS()}
		plan := &spec.GoVendorSpec{AllowReplacement: true, ConfigName: "upstream.toml"}
		config := &projectconfig.GoVendorConfig{ConfigFile: "/project/local.toml"}
		actual, err := preparer.resolveGoVendorConfig("/prepared", plan, config)
		require.NoError(t, err)
		assert.Equal(t, local, actual, "explicit file replaces rather than merges upstream settings")

		config.ConfigFile = ""
		actual, err = preparer.resolveGoVendorConfig("/prepared", plan, config)
		require.NoError(t, err)
		assert.Contains(t, actual, "askalono")

		config.ConfigFile = "/project/missing.toml"
		_, err = preparer.resolveGoVendorConfig("/prepared", plan, config)
		require.Error(t, err, "missing explicit file must not fall back to upstream")
		assert.Contains(t, err.Error(), config.ConfigFile)
	}
}

func TestGoVendorRejectsLookasideConfigReplacement(t *testing.T) {
	ctx := testctx.NewCtx()

	const upstream = "[archive]\ntidy = true\n"

	entry := fedorasource.FormatSourcesEntry("upstream.toml", fileutils.HashTypeSHA512, strings.Repeat("a", 128))
	for path, content := range map[string]string{
		"/prepared/upstream.toml": upstream,
		"/prepared/sources":       entry + "\n",
		"/project/local.toml":     "[archive]\ntidy = false\n",
	} {
		require.NoError(t, fileutils.WriteFile(ctx.FS(), path, []byte(content), fileperms.PublicFile))
	}

	preparer := &sourcePreparerImpl{fs: ctx.FS()}
	plan := &spec.GoVendorSpec{AllowReplacement: true, ConfigName: "upstream.toml"}
	config := &projectconfig.GoVendorConfig{ConfigFile: "/project/local.toml"}
	_, err := preparer.resolveGoVendorConfig("/prepared", plan, config)
	require.ErrorContains(t, err, "'sources' entry")
	data, err := fileutils.ReadFile(ctx.FS(), "/prepared/upstream.toml")
	require.NoError(t, err)
	assert.Equal(t, upstream, string(data))

	config.ConfigFile = ""
	actual, err := preparer.resolveGoVendorConfig("/prepared", plan, config)
	require.NoError(t, err)
	assert.Equal(t, upstream, actual, "unchanged upstream settings remain supported")
}
