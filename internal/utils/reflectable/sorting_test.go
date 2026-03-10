// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // We intentionally want to test internal functions.
package reflectable

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBestEffortSort(t *testing.T) {
	t.Parallel()

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()

		values := []reflect.Value{}
		bestEffortSort(values)
		assert.Empty(t, values)
	})

	t.Run("string sorting", func(t *testing.T) {
		t.Parallel()

		values := []reflect.Value{
			reflect.ValueOf("zebra"),
			reflect.ValueOf("apple"),
			reflect.ValueOf("banana"),
		}

		bestEffortSort(values)

		expected := []string{"apple", "banana", "zebra"}
		for i, val := range values {
			assert.Equal(t, expected[i], val.String())
		}
	})

	t.Run("integer sorting", func(t *testing.T) {
		t.Parallel()

		values := []reflect.Value{
			reflect.ValueOf(42),
			reflect.ValueOf(1),
			reflect.ValueOf(100),
			reflect.ValueOf(-5),
		}

		bestEffortSort(values)

		expected := []int64{-5, 1, 42, 100}
		for i, val := range values {
			assert.Equal(t, expected[i], val.Int())
		}
	})

	t.Run("unsigned integer sorting", func(t *testing.T) {
		t.Parallel()

		values := []reflect.Value{
			reflect.ValueOf(uint(42)),
			reflect.ValueOf(uint(1)),
			reflect.ValueOf(uint(100)),
		}

		bestEffortSort(values)

		expected := []uint64{1, 42, 100}
		for i, val := range values {
			assert.Equal(t, expected[i], val.Uint())
		}
	})

	t.Run("float sorting", func(t *testing.T) {
		t.Parallel()

		values := []reflect.Value{
			reflect.ValueOf(3.14),
			reflect.ValueOf(1.1),
			reflect.ValueOf(2.7),
			reflect.ValueOf(-1.5),
		}

		bestEffortSort(values)

		expected := []float64{-1.5, 1.1, 2.7, 3.14}
		for i, val := range values {
			assert.InDelta(t, expected[i], val.Float(), 0.0001)
		}
	})

	t.Run("non-comparable types", func(t *testing.T) {
		t.Parallel()

		// Using slices which are not comparable
		values := []reflect.Value{
			reflect.ValueOf([]int{3, 2, 1}),
			reflect.ValueOf([]int{1, 2, 3}),
		}

		originalOrder := make([][]int, len(values))

		for i, val := range values {
			slice, ok := val.Interface().([]int)
			assert.True(t, ok, "expected []int type")

			originalOrder[i] = slice
		}

		bestEffortSort(values)

		// Order should remain unchanged since slices are not comparable
		for i, val := range values {
			slice, ok := val.Interface().([]int)
			assert.True(t, ok, "expected []int type")
			assert.Equal(t, originalOrder[i], slice)
		}
	})

	t.Run("mixed types with same kind", func(t *testing.T) {
		t.Parallel()

		// Test with different integer types that should all use CanInt()
		values := []reflect.Value{
			reflect.ValueOf(int32(100)),
			reflect.ValueOf(int64(50)),
			reflect.ValueOf(int16(200)),
		}

		bestEffortSort(values)

		expected := []int64{50, 100, 200}
		for i, val := range values {
			assert.Equal(t, expected[i], val.Int())
		}
	})

	t.Run("stable sort property", func(t *testing.T) {
		t.Parallel()

		// Test that equal elements maintain their relative order
		values := []reflect.Value{
			reflect.ValueOf("a"),
			reflect.ValueOf("b"),
			reflect.ValueOf("a"), // This should stay after the first "a"
			reflect.ValueOf("b"), // This should stay after the first "b"
		}

		bestEffortSort(values)

		// Since stable sort is used, equal elements should maintain relative order
		expected := []string{"a", "a", "b", "b"}
		for i, val := range values {
			assert.Equal(t, expected[i], val.String())
		}
	})

	t.Run("single element", func(t *testing.T) {
		t.Parallel()

		values := []reflect.Value{
			reflect.ValueOf("single"),
		}

		bestEffortSort(values)

		assert.Len(t, values, 1)
		assert.Equal(t, "single", values[0].String())
	})

	t.Run("unsupported comparable types - boolean", func(t *testing.T) {
		t.Parallel()

		// Boolean values are comparable but don't fit into string/uint/int/float categories
		// This should trigger the default case which returns 0, maintaining original order
		values := []reflect.Value{
			reflect.ValueOf(false),
			reflect.ValueOf(true),
			reflect.ValueOf(false),
			reflect.ValueOf(true),
		}

		originalOrder := make([]bool, len(values))
		for i, val := range values {
			originalOrder[i] = val.Bool()
		}

		bestEffortSort(values)

		// Order should remain unchanged since default case returns 0
		for i, val := range values {
			assert.Equal(t, originalOrder[i], val.Bool())
		}
	})

	t.Run("unsupported comparable types - struct", func(t *testing.T) {
		t.Parallel()

		// Define a simple comparable struct
		type Point struct {
			X, Y int
		}

		values := []reflect.Value{
			reflect.ValueOf(Point{X: 3, Y: 4}),
			reflect.ValueOf(Point{X: 1, Y: 2}),
			reflect.ValueOf(Point{X: 5, Y: 6}),
		}

		originalOrder := make([]Point, len(values))

		for i, val := range values {
			point, ok := val.Interface().(Point)
			assert.True(t, ok, "expected Point type")

			originalOrder[i] = point
		}

		bestEffortSort(values)

		// Order should remain unchanged since default case returns 0
		for i, val := range values {
			point, ok := val.Interface().(Point)

			assert.True(t, ok, "expected Point type")
			assert.Equal(t, originalOrder[i], point)
		}
	})
}
