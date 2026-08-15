// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils_test

import (
	"encoding/json"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSONFile(t *testing.T) {
	const testFile = "/test/data.json"

	t.Run("WritesMarshaledValue", func(t *testing.T) {
		ctx := testctx.NewCtx()

		value := []map[string]string{{"component": "curl"}, {"component": "vim"}}
		require.NoError(t, fileutils.WriteJSONFile(ctx.FS(), testFile, value))

		data, err := fileutils.ReadFile(ctx.FS(), testFile)
		require.NoError(t, err)
		assert.JSONEq(t, `[{"component":"curl"},{"component":"vim"}]`, string(data))

		var roundTripped []map[string]string
		require.NoError(t, json.Unmarshal(data, &roundTripped))
		assert.Equal(t, value, roundTripped)
	})

	t.Run("OverwritesExistingFile", func(t *testing.T) {
		ctx := testctx.NewCtx()

		require.NoError(t, fileutils.WriteJSONFile(ctx.FS(), testFile, map[string]int{"count": 1}))
		require.NoError(t, fileutils.WriteJSONFile(ctx.FS(), testFile, map[string]int{"count": 2}))

		data, err := fileutils.ReadFile(ctx.FS(), testFile)
		require.NoError(t, err)
		assert.JSONEq(t, `{"count":2}`, string(data))
	})

	t.Run("UnmarshalableValue", func(t *testing.T) {
		ctx := testctx.NewCtx()

		err := fileutils.WriteJSONFile(ctx.FS(), testFile, make(chan int))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marshal")

		exists, existsErr := fileutils.Exists(ctx.FS(), testFile)
		require.NoError(t, existsErr)
		assert.False(t, exists, "no file should be written when marshaling fails")
	})

	t.Run("WriteFailure", func(t *testing.T) {
		ctx := testctx.NewCtx(testctx.WithFS(afero.NewReadOnlyFs(afero.NewMemMapFs())))

		err := fileutils.WriteJSONFile(ctx.FS(), testFile, map[string]string{"key": "value"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), testFile)
	})
}
