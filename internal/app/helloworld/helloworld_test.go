// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package helloworld_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/helloworld"
	"github.com/stretchr/testify/assert"
)

// TestHelloDoesNotPanic is a very basic test with no extra nesting. It tests only one thing: that
// the Hello function does not panic.
func TestHelloDoesNotPanic(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		helloworld.Hello()
	})
}

// TestGoodbye is a more complex test. It is intended to test the Goodbye function all up. It has a
// nested sub-test of "basic" tests, which in turn has a table-driven test containing a single entry:
// "Goodbye does not panic".
func TestGoodbye(t *testing.T) {
	t.Parallel()
	t.Run("Goodbye basics", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
		}{
			{
				name: "Goodbye does not panic",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert.NotPanics(t, func() {
					helloworld.Goodbye()
				})
			})
		}
	})
}
