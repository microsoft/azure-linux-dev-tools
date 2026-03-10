// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reflectable_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/reflectable"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/reflectable/reflectable_testutils"
	"go.uber.org/mock/gomock"
)

func TestWriteValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        interface{}
		expectedCall interface{}
	}{
		{
			name:         "simple string",
			input:        "hello",
			expectedCall: "hello",
		},
		{
			name:         "simple integer",
			input:        42,
			expectedCall: 42,
		},
		{
			name:         "boolean true",
			input:        true,
			expectedCall: "true",
		},
		{
			name:         "boolean false",
			input:        false,
			expectedCall: "false",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			writer := reflectable_testutils.NewMockTableWriter(ctrl)
			writer.EXPECT().WriteValue(testCase.expectedCall).Times(1)

			reflectable.FormatValueUsingWriter(writer, testCase.input)
		})
	}
}

//nolint:dupl
func TestWriteReflectedValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     interface{}
		setupMock func(*reflectable_testutils.MockTableWriter)
	}{
		{
			name:  "string value",
			input: "test",
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().WriteValue("test").Times(1)
			},
		},
		{
			name:  "integer value",
			input: 123,
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().WriteValue(123).Times(1)
			},
		},
		{
			name:  "boolean true",
			input: true,
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().WriteValue("true").Times(1)
			},
		},
		{
			name:  "boolean false",
			input: false,
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().WriteValue("false").Times(1)
			},
		},
		{
			name:  "nil pointer",
			input: (*int)(nil),
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				// No expectations - nil pointers don't generate any calls
			},
		},
		{
			name:  "non-nil pointer",
			input: intPtr(42),
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().WriteValue(42).Times(1)
			},
		},
		{
			name:  "empty slice",
			input: []int{},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(false).AnyTimes()

				gomock.InOrder(
					writer.EXPECT().StartTable(gomock.Any()).Times(1),
					writer.EXPECT().EndTable().Times(1),
				)
			},
		},
		{
			name:  "simple slice",
			input: []int{1, 2, 3},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(false).AnyTimes()

				gomock.InOrder(
					writer.EXPECT().StartTable(gomock.Any()).Times(1),

					// First row
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(1).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					// Second row
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(2).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					// Third row
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(3).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					writer.EXPECT().EndTable().Times(1),
				)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			writer := reflectable_testutils.NewMockTableWriter(ctrl)
			testCase.setupMock(writer)

			reflectable.FormatValueUsingWriter(writer, testCase.input)
		})
	}
}

func TestWriteReflectedValueInTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     interface{}
		setupMock func(*reflectable_testutils.MockTableWriter)
	}{
		{
			name:  "simple slice inside table",
			input: []int{1, 2, 3},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(true).AnyTimes()

				gomock.InOrder(
					writer.EXPECT().WriteValue(1).Times(1),
					writer.EXPECT().WriteValue("\n").Times(1),
					writer.EXPECT().WriteValue(2).Times(1),
					writer.EXPECT().WriteValue("\n").Times(1),
					writer.EXPECT().WriteValue(3).Times(1),
				)
			},
		},
		{
			name:  "empty slice inside table",
			input: []string{},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(true).AnyTimes()
				// No expectations - empty slices don't generate any calls
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			writer := reflectable_testutils.NewMockTableWriter(ctrl)
			testCase.setupMock(writer)

			reflectable.FormatValueUsingWriter(writer, testCase.input)
		})
	}
}

//nolint:dupl
func TestWriteReflectedSliceWithoutTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     interface{}
		setupMock func(*reflectable_testutils.MockTableWriter)
	}{
		{
			name:  "empty slice",
			input: []int{},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(false).AnyTimes()

				gomock.InOrder(
					writer.EXPECT().StartTable(gomock.Any()).Times(1),
					writer.EXPECT().EndTable().Times(1),
				)
			},
		},
		{
			name:  "single element slice",
			input: []string{"hello"},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(false).AnyTimes()

				gomock.InOrder(
					writer.EXPECT().StartTable(gomock.Any()).Times(1),
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("hello").Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),
					writer.EXPECT().EndTable().Times(1),
				)
			},
		},
		{
			name:  "multi-element slice",
			input: []int{1, 2, 3},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(false).AnyTimes()

				gomock.InOrder(
					writer.EXPECT().StartTable(gomock.Any()).Times(1),

					// First element
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(1).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					// Second element
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(2).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					// Third element
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(3).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					writer.EXPECT().EndTable().Times(1),
				)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			writer := reflectable_testutils.NewMockTableWriter(ctrl)
			testCase.setupMock(writer)

			reflectable.FormatValueUsingWriter(writer, testCase.input)
		})
	}
}

func TestWriteReflectedStruct(t *testing.T) {
	t.Parallel()

	// Test struct with no displayable fields
	type EmptyStruct struct {
		unexported int
	}

	// Test struct with displayable fields
	type SimpleStruct struct {
		Name string
		Age  int
	}

	tests := []struct {
		name      string
		input     interface{}
		setupMock func(*reflectable_testutils.MockTableWriter)
	}{
		{
			name:  "empty struct with no displayable fields",
			input: EmptyStruct{unexported: 42},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(false).AnyTimes()
				// No expectations since no displayable fields
			},
		},
		{
			name:  "simple struct with displayable fields",
			input: SimpleStruct{Name: "John", Age: 30},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(false).AnyTimes()

				gomock.InOrder(
					writer.EXPECT().StartTable(gomock.Any()).Times(1),

					// Name field
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("Name").Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("John").Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					// Age field
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("Age").Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(30).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					writer.EXPECT().EndTable().Times(1),
				)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			writer := reflectable_testutils.NewMockTableWriter(ctrl)
			testCase.setupMock(writer)

			reflectable.FormatValueUsingWriter(writer, testCase.input)
		})
	}
}

func TestWriteReflectedSliceAsTableWithStructs(t *testing.T) {
	t.Parallel()

	type Person struct {
		Name string `table:"Full Name"`
		Age  int    `table:",rightalign"`
		City string `table:",sortkey"`
	}

	tests := []struct {
		name      string
		input     interface{}
		setupMock func(*reflectable_testutils.MockTableWriter)
	}{
		{
			name:  "empty struct slice",
			input: []Person{},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(false).AnyTimes()

				gomock.InOrder(
					writer.EXPECT().StartTable(gomock.Any()).Times(1),
					writer.EXPECT().StartHeaderRow().Times(1),

					// Headers
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("Full Name").Times(1),
					writer.EXPECT().EndColumn().Times(1),

					writer.EXPECT().RightAlignColumn(1).Times(1), // Age field has rightalign
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("Age").Times(1),
					writer.EXPECT().EndColumn().Times(1),

					writer.EXPECT().SortByColumn(2).Times(1), // City field has sortkey
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("City").Times(1),
					writer.EXPECT().EndColumn().Times(1),

					writer.EXPECT().EndHeaderRow().Times(1),
					writer.EXPECT().EndTable().Times(1),
				)
			},
		},
		{
			name: "struct slice with data",
			input: []Person{
				{Name: "John", Age: 30, City: "NYC"},
				{Name: "Jane", Age: 25, City: "LA"},
			},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(false).AnyTimes()

				gomock.InOrder(
					writer.EXPECT().StartTable(gomock.Any()).Times(1),
					writer.EXPECT().StartHeaderRow().Times(1),

					// Headers
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("Full Name").Times(1),
					writer.EXPECT().EndColumn().Times(1),

					writer.EXPECT().RightAlignColumn(1).Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("Age").Times(1),
					writer.EXPECT().EndColumn().Times(1),

					writer.EXPECT().SortByColumn(2).Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("City").Times(1),
					writer.EXPECT().EndColumn().Times(1),

					writer.EXPECT().EndHeaderRow().Times(1),

					// First row (John)
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("John").Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(30).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("NYC").Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					// Second row (Jane)
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("Jane").Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(25).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue("LA").Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					writer.EXPECT().EndTable().Times(1),
				)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			writer := reflectable_testutils.NewMockTableWriter(ctrl)
			testCase.setupMock(writer)

			reflectable.FormatValueUsingWriter(writer, testCase.input)
		})
	}
}

//nolint:dupl
func TestWriteReflectedValueEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     interface{}
		setupMock func(*reflectable_testutils.MockTableWriter)
	}{
		{
			name:  "nil value",
			input: nil,
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				// No expectations - invalid values result in no operations
			},
		},
		{
			name:  "array instead of slice",
			input: [3]int{1, 2, 3},
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().InTable().Return(false).AnyTimes()

				gomock.InOrder(
					writer.EXPECT().StartTable(gomock.Any()).Times(1),

					// First element
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(1).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					// Second element
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(2).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					// Third element
					writer.EXPECT().StartRow().Times(1),
					writer.EXPECT().StartColumn().Times(1),
					writer.EXPECT().WriteValue(3).Times(1),
					writer.EXPECT().EndColumn().Times(1),
					writer.EXPECT().EndRow().Times(1),

					writer.EXPECT().EndTable().Times(1),
				)
			},
		},
		{
			name: "nested pointer",
			input: func() **int {
				i := 42
				p := &i

				return &p
			}(),
			setupMock: func(writer *reflectable_testutils.MockTableWriter) {
				writer.EXPECT().WriteValue(42).Times(1)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			writer := reflectable_testutils.NewMockTableWriter(ctrl)
			testCase.setupMock(writer)

			reflectable.FormatValueUsingWriter(writer, testCase.input)
		})
	}
}

func intPtr(i int) *int {
	return &i
}
