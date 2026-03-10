// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reflectable_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/reflectable"
	"github.com/stretchr/testify/assert"
)

func TestFormatConstants(t *testing.T) {
	t.Parallel()

	// Test that format constants have expected values
	assert.Equal(t, reflectable.FormatTable, reflectable.Format(0))
	assert.Equal(t, reflectable.FormatMarkdown, reflectable.Format(1))
	assert.Equal(t, reflectable.FormatCSV, reflectable.Format(2))

	// Test that constants are distinct
	assert.NotEqual(t, reflectable.FormatTable, reflectable.FormatMarkdown)
	assert.NotEqual(t, reflectable.FormatTable, reflectable.FormatCSV)
	assert.NotEqual(t, reflectable.FormatMarkdown, reflectable.FormatCSV)
}

func TestNewOptions(t *testing.T) {
	t.Parallel()

	options := reflectable.NewOptions()

	// Test default values
	assert.Equal(t, reflectable.FormatTable, options.Format)
	assert.Equal(t, 0, options.MaxTableWidth)
	assert.False(t, options.ColorEnabled)
}

func TestOptionsWithFormat(t *testing.T) {
	t.Parallel()

	originalOptions := reflectable.NewOptions()

	// Test setting FormatMarkdown
	markdownOptions := originalOptions.WithFormat(reflectable.FormatMarkdown)
	assert.Equal(t, reflectable.FormatMarkdown, markdownOptions.Format)
	assert.Equal(t, 0, markdownOptions.MaxTableWidth) // Other fields unchanged
	assert.False(t, markdownOptions.ColorEnabled)     // Other fields unchanged

	// Test setting FormatCSV
	csvOptions := originalOptions.WithFormat(reflectable.FormatCSV)
	assert.Equal(t, reflectable.FormatCSV, csvOptions.Format)
	assert.Equal(t, 0, csvOptions.MaxTableWidth) // Other fields unchanged
	assert.False(t, csvOptions.ColorEnabled)     // Other fields unchanged

	// Test setting FormatTable
	tableOptions := originalOptions.WithFormat(reflectable.FormatTable)
	assert.Equal(t, reflectable.FormatTable, tableOptions.Format)

	// Test that original options are unchanged (immutability)
	assert.Equal(t, reflectable.FormatTable, originalOptions.Format)
}

func TestOptionsWithMaxTableWidth(t *testing.T) {
	t.Parallel()

	originalOptions := reflectable.NewOptions()

	// Test setting positive width
	widthOptions := originalOptions.WithMaxTableWidth(100)
	assert.Equal(t, 100, widthOptions.MaxTableWidth)
	assert.Equal(t, reflectable.FormatTable, widthOptions.Format) // Other fields unchanged
	assert.False(t, widthOptions.ColorEnabled)                    // Other fields unchanged

	// Test setting zero width
	zeroWidthOptions := originalOptions.WithMaxTableWidth(0)
	assert.Equal(t, 0, zeroWidthOptions.MaxTableWidth)

	// Test setting negative width (edge case)
	negativeWidthOptions := originalOptions.WithMaxTableWidth(-10)
	assert.Equal(t, -10, negativeWidthOptions.MaxTableWidth)
}

func TestOptionsWithColor(t *testing.T) {
	t.Parallel()

	originalOptions := reflectable.NewOptions()

	// Test enabling color
	colorEnabledOptions := originalOptions.WithColor(true)
	assert.True(t, colorEnabledOptions.ColorEnabled)
	assert.Equal(t, reflectable.FormatTable, colorEnabledOptions.Format) // Other fields unchanged
	assert.Equal(t, 0, colorEnabledOptions.MaxTableWidth)                // Other fields unchanged

	// Test disabling color
	colorDisabledOptions := originalOptions.WithColor(false)
	assert.False(t, colorDisabledOptions.ColorEnabled)

	// Test that original options are unchanged (immutability)
	assert.False(t, originalOptions.ColorEnabled)
}

func TestOptionsChaining(t *testing.T) {
	t.Parallel()

	// Test method chaining
	options := reflectable.NewOptions().
		WithFormat(reflectable.FormatMarkdown).
		WithMaxTableWidth(80).
		WithColor(true)

	assert.Equal(t, reflectable.FormatMarkdown, options.Format)
	assert.Equal(t, 80, options.MaxTableWidth)
	assert.True(t, options.ColorEnabled)
}

func TestOptionsMultipleTransformations(t *testing.T) {
	t.Parallel()

	baseOptions := reflectable.NewOptions()

	// Apply multiple transformations to the same base options
	option1 := baseOptions.WithFormat(reflectable.FormatMarkdown)
	option2 := baseOptions.WithMaxTableWidth(50)
	option3 := baseOptions.WithColor(true)

	// Each should have all changes.
	assert.Equal(t, reflectable.FormatMarkdown, option1.Format)
	assert.Equal(t, 50, option3.MaxTableWidth)
	assert.True(t, option3.ColorEnabled)

	assert.Equal(t, reflectable.FormatMarkdown, option2.Format)
	assert.Equal(t, 50, option3.MaxTableWidth)
	assert.True(t, option3.ColorEnabled)

	assert.Equal(t, reflectable.FormatMarkdown, option3.Format)
	assert.Equal(t, 50, option3.MaxTableWidth)
	assert.True(t, option3.ColorEnabled)
}
