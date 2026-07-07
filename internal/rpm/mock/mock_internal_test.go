// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mock

import (
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
)

func TestStyleMockStderrLine(t *testing.T) {
	oldNoColor := color.NoColor
	color.NoColor = false

	t.Cleanup(func() {
		color.NoColor = oldNoColor
	})

	line := "mock stderr line"

	assert.Equal(t, line, styleMockStderrLine(true, line))

	styledLine := styleMockStderrLine(false, line)
	assert.NotEqual(t, line, styledLine)
	assert.Contains(t, styledLine, line)
}
