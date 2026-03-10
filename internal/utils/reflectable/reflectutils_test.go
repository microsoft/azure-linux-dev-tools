// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // We intentionally want to test internal functions.
package reflectable

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnwrapPointersFromType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    reflect.Type
		expected reflect.Type
	}{
		{
			name:     "non-pointer type",
			input:    reflect.TypeOf(42),
			expected: reflect.TypeOf(42),
		},
		{
			name:     "single pointer",
			input:    reflect.TypeOf((*int)(nil)),
			expected: reflect.TypeOf(42),
		},
		{
			name:     "double pointer",
			input:    reflect.TypeOf((**int)(nil)),
			expected: reflect.TypeOf(42),
		},
		{
			name:     "triple pointer",
			input:    reflect.TypeOf((***int)(nil)),
			expected: reflect.TypeOf(42),
		},
		{
			name:     "pointer to struct",
			input:    reflect.TypeOf((*struct{ X int })(nil)),
			expected: reflect.TypeOf(struct{ X int }{}),
		},
		{
			name:     "pointer to slice",
			input:    reflect.TypeOf((*[]int)(nil)),
			expected: reflect.TypeOf([]int{}),
		},
		{
			name:     "pointer to map",
			input:    reflect.TypeOf((*map[string]int)(nil)),
			expected: reflect.TypeOf(map[string]int{}),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := unwrapPointersFromType(testCase.input)
			assert.Equal(t, testCase.expected, result)
		})
	}
}

func TestUnwrapPointersFromValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    reflect.Value
		expected interface{}
	}{
		{
			name:     "non-pointer value",
			input:    reflect.ValueOf(42),
			expected: 42,
		},
		{
			name:     "single pointer",
			input:    reflect.ValueOf(intPtr(42)),
			expected: 42,
		},
		{
			name:     "double pointer",
			input:    reflect.ValueOf(intPtrPtr(42)),
			expected: 42,
		},
		{
			name:     "triple pointer",
			input:    reflect.ValueOf(intPtrPtrPtr(42)),
			expected: 42,
		},
		{
			name:     "pointer to struct",
			input:    reflect.ValueOf(&struct{ X int }{X: 42}),
			expected: struct{ X int }{X: 42},
		},
		{
			name:     "nil pointer",
			input:    reflect.ValueOf((*int)(nil)),
			expected: (*int)(nil),
		},
		{
			name:     "pointer to nil pointer",
			input:    reflect.ValueOf((**int)(nil)),
			expected: (**int)(nil),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := unwrapPointersFromValue(testCase.input)
			if testCase.expected == nil ||
				(reflect.ValueOf(testCase.expected).Kind() == reflect.Ptr &&
					reflect.ValueOf(testCase.expected).IsNil()) {
				assert.True(t, result.IsZero() || result.IsNil())
			} else {
				assert.Equal(t, testCase.expected, result.Interface())
			}
		})
	}
}

func TestIsIntegerType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    reflect.Type
		expected bool
	}{
		// Signed integers
		{
			name:     "int",
			input:    reflect.TypeOf(int(0)),
			expected: true,
		},
		{
			name:     "int8",
			input:    reflect.TypeOf(int8(0)),
			expected: true,
		},
		{
			name:     "int16",
			input:    reflect.TypeOf(int16(0)),
			expected: true,
		},
		{
			name:     "int32",
			input:    reflect.TypeOf(int32(0)),
			expected: true,
		},
		{
			name:     "int64",
			input:    reflect.TypeOf(int64(0)),
			expected: true,
		},
		// Unsigned integers
		{
			name:     "uint",
			input:    reflect.TypeOf(uint(0)),
			expected: true,
		},
		{
			name:     "uint8",
			input:    reflect.TypeOf(uint8(0)),
			expected: true,
		},
		{
			name:     "uint16",
			input:    reflect.TypeOf(uint16(0)),
			expected: true,
		},
		{
			name:     "uint32",
			input:    reflect.TypeOf(uint32(0)),
			expected: true,
		},
		{
			name:     "uint64",
			input:    reflect.TypeOf(uint64(0)),
			expected: true,
		},
		// Non-integer types
		{
			name:     "float32",
			input:    reflect.TypeOf(float32(0)),
			expected: false,
		},
		{
			name:     "float64",
			input:    reflect.TypeOf(float64(0)),
			expected: false,
		},
		{
			name:     "string",
			input:    reflect.TypeOf(""),
			expected: false,
		},
		{
			name:     "bool",
			input:    reflect.TypeOf(false),
			expected: false,
		},
		{
			name:     "slice",
			input:    reflect.TypeOf([]int{}),
			expected: false,
		},
		{
			name:     "map",
			input:    reflect.TypeOf(map[string]int{}),
			expected: false,
		},
		{
			name:     "struct",
			input:    reflect.TypeOf(struct{}{}),
			expected: false,
		},
		{
			name:     "pointer",
			input:    reflect.TypeOf((*int)(nil)),
			expected: false,
		},
		{
			name:     "uintptr",
			input:    reflect.TypeOf(uintptr(0)),
			expected: false,
		},
		{
			name:     "byte (uint8 alias)",
			input:    reflect.TypeOf(byte(0)),
			expected: true,
		},
		{
			name:     "rune (int32 alias)",
			input:    reflect.TypeOf(rune(0)),
			expected: true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := isIntegerType(testCase.input)
			assert.Equal(t, testCase.expected, result)
		})
	}
}

// Test types for TestTryGetStringerInterface.
type stringerStruct struct {
	value string
}

func (s stringerStruct) String() string {
	return s.value
}

type pointerStringerStruct struct {
	value string
}

func (s *pointerStringerStruct) String() string {
	return s.value
}

type nonStringerStruct struct {
	value string
}

func TestTryGetStringerInterface(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          reflect.Value
		expectStringer bool
		expectedString string
	}{
		{
			name:           "value implements Stringer",
			input:          reflect.ValueOf(stringerStruct{value: "test"}),
			expectStringer: true,
			expectedString: "test",
		},
		{
			name:           "pointer implements Stringer",
			input:          reflect.ValueOf(&stringerStruct{value: "test"}),
			expectStringer: true,
			expectedString: "test",
		},
		{
			name:           "value with pointer receiver Stringer",
			input:          reflect.ValueOf(pointerStringerStruct{value: "test"}),
			expectStringer: false, // Value is not addressable, so pointer receiver won't work
		},
		{
			name:           "pointer with pointer receiver Stringer",
			input:          reflect.ValueOf(&pointerStringerStruct{value: "test"}),
			expectStringer: true,
			expectedString: "test",
		},
		{
			name:           "non-Stringer value",
			input:          reflect.ValueOf(nonStringerStruct{value: "test"}),
			expectStringer: false,
		},
		{
			name:           "non-Stringer pointer",
			input:          reflect.ValueOf(&nonStringerStruct{value: "test"}),
			expectStringer: false,
		},
		{
			name:           "built-in type int",
			input:          reflect.ValueOf(42),
			expectStringer: false,
		},
		{
			name:           "built-in type string",
			input:          reflect.ValueOf("hello"),
			expectStringer: false,
		},
		{
			name:           "nil pointer",
			input:          reflect.ValueOf((*stringerStruct)(nil)),
			expectStringer: true, // nil pointer implements Stringer interface
			expectedString: "",   // but we won't test the string since calling String() would panic
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			stringer, success := tryGetStringerInterface(testCase.input)

			if testCase.expectStringer {
				require.True(t, success, "Expected to get Stringer interface")

				// Special handling for nil pointer case - don't call String() as it would panic
				if testCase.name == "nil pointer" {
					// For nil pointer, we just verify that we got the interface but don't call String()
					// The stringer itself will be nil (even though ok=true)
					return
				}

				require.NotNil(t, stringer, "Expected non-nil Stringer")
				assert.Equal(t, testCase.expectedString, stringer.String())
			} else {
				assert.False(t, success, "Expected not to get Stringer interface")
				assert.Nil(t, stringer, "Expected nil Stringer")
			}
		})
	}
}

// Helper functions for creating nested pointers.
func intPtr(i int) *int {
	return &i
}

func intPtrPtr(i int) **int {
	p := &i

	return &p
}

func intPtrPtrPtr(i int) ***int {
	p := &i
	pp := &p

	return &pp
}
