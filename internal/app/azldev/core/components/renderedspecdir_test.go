// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderedSpecDir(t *testing.T) {
	t.Run("ReturnsLetterPrefixedPath", func(t *testing.T) {
		result, err := components.RenderedSpecDir("/path/to/specs", "vim")
		require.NoError(t, err)
		assert.Equal(t, "/path/to/specs/v/vim", result)
	})

	t.Run("ReturnsEmptyWhenNotConfigured", func(t *testing.T) {
		result, err := components.RenderedSpecDir("", "vim")
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("LowercasesPrefixForUppercaseName", func(t *testing.T) {
		result, err := components.RenderedSpecDir("/specs", "SymCrypt")
		require.NoError(t, err)
		assert.Equal(t, "/specs/s/SymCrypt", result)
	})

	t.Run("HandlesComponentNameWithDashes", func(t *testing.T) {
		result, err := components.RenderedSpecDir("/rendered", "my-component")
		require.NoError(t, err)
		assert.Equal(t, "/rendered/m/my-component", result)
	})

	t.Run("RejectsAbsoluteComponentName", func(t *testing.T) {
		_, err := components.RenderedSpecDir("/rendered", "/tmp")
		assert.Error(t, err)
	})

	t.Run("RejectsTraversalInComponentName", func(t *testing.T) {
		_, err := components.RenderedSpecDir("/rendered", "../escape")
		assert.Error(t, err)
	})

	t.Run("RejectsPathSeparatorInComponentName", func(t *testing.T) {
		_, err := components.RenderedSpecDir("/rendered", "sub/dir")
		assert.Error(t, err)
	})

	t.Run("RejectsEmptyComponentName", func(t *testing.T) {
		_, err := components.RenderedSpecDir("/rendered", "")
		assert.Error(t, err)
	})

	t.Run("RejectsDotComponentName", func(t *testing.T) {
		_, err := components.RenderedSpecDir("/rendered", ".")
		assert.Error(t, err)
	})

	t.Run("RejectsDotDotComponentName", func(t *testing.T) {
		_, err := components.RenderedSpecDir("/rendered", "..")
		assert.Error(t, err)
	})

	t.Run("ValidatesEvenWhenNotConfigured", func(t *testing.T) {
		// Component name is validated even when renderedSpecsDir is empty,
		// so invalid names are caught early regardless of configuration.
		_, err := components.RenderedSpecDir("", "../escape")
		assert.Error(t, err)
	})
}

func TestRenderedSpecDirAliasName(t *testing.T) {
	t.Run("EmptyForPlainAsciiName", func(t *testing.T) {
		assert.Empty(t, components.RenderedSpecDirAliasName("vim"))
	})

	t.Run("EmptyForDashAndUnderscore", func(t *testing.T) {
		assert.Empty(t, components.RenderedSpecDirAliasName("my-component_v2"))
	})

	t.Run("EmptyForDotInName", func(t *testing.T) {
		// '.' is a safe filesystem char and not URL-encoded.
		assert.Empty(t, components.RenderedSpecDirAliasName("foo.bar"))
	})

	t.Run("EncodesPlusCharacters", func(t *testing.T) {
		assert.Equal(t, "libxml%2B%2B", components.RenderedSpecDirAliasName("libxml++"))
	})

	t.Run("EncodesSinglePlus", func(t *testing.T) {
		assert.Equal(t, "gtk%2B", components.RenderedSpecDirAliasName("gtk+"))
	})
}
