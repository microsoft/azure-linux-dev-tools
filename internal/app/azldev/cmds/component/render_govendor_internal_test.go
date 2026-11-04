// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoveGoVendorRenderPayloads(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		ctx := testctx.NewCtx()

		manifest := "# keep manifest\n" +
			fedorasource.FormatSourcesEntry("upstream.tar.gz", fileutils.HashTypeSHA512, strings.Repeat("a", 128)) + "\n" +
			fedorasource.FormatSourcesEntry("vendor.tar.bz2", fileutils.HashTypeSHA512, strings.Repeat("b", 128)) + "\n"
		for name, content := range map[string]string{
			"app.spec": "spec", "config.toml": "[archive]", "sources": manifest,
			"upstream.tar.gz": "source", "vendor.tar.bz2": "vendor",
		} {
			require.NoError(t, fileutils.WriteFile(ctx.FS(), "/render/"+name, []byte(content), fileperms.PublicFile))
		}

		require.NoError(t, removeGoVendorRenderPayloads(ctx.FS(), "/render", enabled))

		for _, name := range []string{"upstream.tar.gz", "vendor.tar.bz2"} {
			exists, err := fileutils.Exists(ctx.FS(), "/render/"+name)
			require.NoError(t, err)
			assert.Equal(t, !enabled, exists)
		}

		for _, name := range []string{"app.spec", "config.toml", "sources"} {
			exists, err := fileutils.Exists(ctx.FS(), "/render/"+name)
			require.NoError(t, err)
			assert.True(t, exists)
		}

		data, err := fileutils.ReadFile(ctx.FS(), "/render/sources")
		require.NoError(t, err)
		assert.Equal(t, manifest, string(data))
		require.NoError(t, removeGoVendorRenderPayloads(ctx.FS(), "/render", enabled))
	}
}
