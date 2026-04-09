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
	t.Run("ReturnsPathWhenConfigured", func(t *testing.T) {
		result, err := components.RenderedSpecDir("/path/to/specs", "vim")
		require.NoError(t, err)
		assert.Equal(t, "/path/to/specs/vim", result)
	})

	t.Run("ReturnsEmptyWhenNotConfigured", func(t *testing.T) {
		result, err := components.RenderedSpecDir("", "vim")
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("HandlesComponentNameWithDashes", func(t *testing.T) {
		result, err := components.RenderedSpecDir("/rendered", "my-component")
		require.NoError(t, err)
		assert.Equal(t, "/rendered/my-component", result)
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
