// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reflectable_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/acarl005/stripansi"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/reflectable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultTestOptions() *reflectable.Options {
	return reflectable.NewOptions().WithFormat(reflectable.FormatTable)
}

func TestFormatNil(t *testing.T) {
	t.Parallel()

	result, err := reflectable.FormatValue(defaultTestOptions(), nil)

	if assert.NoError(t, err) {
		assert.Empty(t, result)
	}
}

func TestFormatBool(t *testing.T) {
	t.Parallel()

	result, err := reflectable.FormatValue(defaultTestOptions(), true)
	if assert.NoError(t, err) {
		assert.Equal(t, "true", stripansi.Strip(result))
	}

	result, err = reflectable.FormatValue(defaultTestOptions(), false)
	if assert.NoError(t, err) {
		assert.Equal(t, "false", stripansi.Strip(result))
	}
}

func TestFormatInteger(t *testing.T) {
	t.Parallel()

	result, err := reflectable.FormatValue(defaultTestOptions(), 42)

	if assert.NoError(t, err) {
		assert.Equal(t, "42", stripansi.Strip(result))
	}
}

func TestFormatString(t *testing.T) {
	t.Parallel()

	result, err := reflectable.FormatValue(defaultTestOptions(), "abc")

	if assert.NoError(t, err) {
		assert.Equal(t, "abc", stripansi.Strip(result))
	}
}

func TestFormatArrayOfIntegers(t *testing.T) {
	t.Parallel()

	result, err := reflectable.FormatValue(defaultTestOptions(), []int{1, 2, 3})

	if assert.NoError(t, err) {
		assert.Equal(t, strings.TrimSpace(`
╭───╮
│ 1 │
│ 2 │
│ 3 │
╰───╯
`), stripansi.Strip(result))
	}
}

func TestFormatArrayOfStrings(t *testing.T) {
	t.Parallel()

	result, err := reflectable.FormatValue(defaultTestOptions(), []string{"a", "b", "c"})

	if assert.NoError(t, err) {
		assert.Equal(t, strings.TrimSpace(`
╭───╮
│ a │
│ b │
│ c │
╰───╯
`), stripansi.Strip(result))
	}
}

func TestFormatStructWithNoPublicFieldsAndNoStringer(t *testing.T) {
	t.Parallel()

	type testStruct struct{}

	value := testStruct{}
	expected := ""

	var (
		result string
		err    error
	)

	result, err = reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatStructWithPrivateFields(t *testing.T) {
	t.Parallel()

	type simpleStruct struct {
		Left    int
		private int
	}

	value := simpleStruct{1, 2}
	expected := strings.TrimSpace(`
╭──────┬───╮
│ Left │ 1 │
╰──────┴───╯
`)

	var (
		result string
		err    error
	)

	result, err = reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}

	result, err = reflectable.FormatValue(defaultTestOptions(), &value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

type structImplementingStringer struct {
	Value int
}

func (s structImplementingStringer) String() string {
	return fmt.Sprintf("Value=%d", s.Value)
}

func TestFormatStructImplementingStringer(t *testing.T) {
	t.Parallel()
	assert.Implements(t, (*fmt.Stringer)(nil), &structImplementingStringer{})

	value := structImplementingStringer{1}
	expected := "Value=1"

	var (
		result string
		err    error
	)

	result, err = reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}

	result, err = reflectable.FormatValue(defaultTestOptions(), &value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

type structImplementingStringerOnPtr struct {
	Value int
}

func (s *structImplementingStringerOnPtr) String() string {
	return fmt.Sprintf("Value=%d", s.Value)
}

func TestFormatStructImplementingStringerOnPtr(t *testing.T) {
	t.Parallel()
	assert.Implements(t, (*fmt.Stringer)(nil), &structImplementingStringerOnPtr{})

	value := &structImplementingStringerOnPtr{1}
	expected := "Value=1"

	var (
		result string
		err    error
	)

	result, err = reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}

	result, err = reflectable.FormatValue(defaultTestOptions(), &value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatSimpleStruct(t *testing.T) {
	t.Parallel()

	type simpleStruct struct {
		Field   int
		MyField int
		URL     string
	}

	value := simpleStruct{1, 2, "abc"}
	expected := strings.TrimSpace(`
╭──────────┬─────╮
│ Field    │ 1   │
│ My Field │ 2   │
│ URL      │ abc │
╰──────────┴─────╯
`)

	var (
		result string
		err    error
	)

	result, err = reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}

	result, err = reflectable.FormatValue(defaultTestOptions(), &value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatStructWithTags(t *testing.T) {
	t.Parallel()

	type testStruct struct {
		Field   int    `table:"-"`
		MyField int    `table:"Renamed"`
		URL     string `table:",omitempty"`
	}

	value := testStruct{1, 2, ""}
	expected := strings.TrimSpace(`
╭─────────┬───╮
│ Renamed │ 2 │
╰─────────┴───╯
`)

	var (
		result string
		err    error
	)

	result, err = reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}

	result, err = reflectable.FormatValue(defaultTestOptions(), &value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatStructContainingArray(t *testing.T) {
	t.Parallel()

	type testStruct struct {
		Scalar int
		Items  []string
	}

	value := testStruct{10, []string{"a", "b", "c"}}
	expected := strings.TrimSpace(`
╭────────┬────╮
│ Scalar │ 10 │
│ Items  │ a  │
│        │ b  │
│        │ c  │
╰────────┴────╯
`)

	result, err := reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}

	result, err = reflectable.FormatValue(defaultTestOptions(), &value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatArrayOfSimpleStructs(t *testing.T) {
	t.Parallel()

	type simpleStruct struct {
		Left  int
		Right int
	}

	value := []simpleStruct{{1, 2}, {3, 4}}
	expected := strings.TrimSpace(`
╭──────┬───────╮
│ LEFT │ RIGHT │
├──────┼───────┤
│    1 │     2 │
│    3 │     4 │
╰──────┴───────╯
`)

	var (
		result string
		err    error
	)

	result, err = reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}

	result, err = reflectable.FormatValue(defaultTestOptions(), &value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatArrayOfStructWithSorting(t *testing.T) {
	t.Parallel()

	type simpleStruct struct {
		Value int `table:",sortkey"`
	}

	value := []simpleStruct{{3}, {1}, {2}, {0}}
	expected := strings.TrimSpace(`
╭───────╮
│ VALUE │
├───────┤
│     0 │
│     1 │
│     2 │
│     3 │
╰───────╯
`)

	result, err := reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatMapFromStringToString(t *testing.T) {
	t.Parallel()

	value := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}

	expected := strings.TrimSpace(`
╭──────┬────────╮
│ key1 │ value1 │
│ key2 │ value2 │
╰──────┴────────╯
`)

	result, err := reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatMapFromIntToString(t *testing.T) {
	t.Parallel()

	value := map[int]string{
		10: "value1",
		5:  "value2",
	}

	expected := strings.TrimSpace(`
╭────┬────────╮
│  5 │ value2 │
│ 10 │ value1 │
╰────┴────────╯
`)

	result, err := reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatMapFromStringToSimpleStruct(t *testing.T) {
	t.Parallel()

	type simpleStruct struct {
		Field1 int
		Field2 string
	}

	value := map[string]simpleStruct{
		"key1": {10, "a"},
		"key2": {-5, "b"},
	}

	expected := strings.TrimSpace(`
╭──────┬────────┬────────╮
│ KEY  │ FIELD1 │ FIELD2 │
├──────┼────────┼────────┤
│ key1 │     10 │ a      │
│ key2 │     -5 │ b      │
╰──────┴────────┴────────╯
`)

	result, err := reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatEmptyInput(t *testing.T) {
	t.Parallel()

	// Test empty array
	result, err := reflectable.FormatValue(defaultTestOptions(), []int{})
	if assert.NoError(t, err) {
		assert.Empty(t, stripansi.Strip(result))
	}

	// Test empty map
	result, err = reflectable.FormatValue(defaultTestOptions(), map[string]string{})
	if assert.NoError(t, err) {
		assert.Empty(t, stripansi.Strip(result))
	}
}

func TestFormatNestedStructures(t *testing.T) {
	t.Parallel()

	type nestedStruct struct {
		Name  string
		Items []int
	}

	value := nestedStruct{
		Name:  "Test",
		Items: []int{1, 2, 3},
	}

	expected := strings.TrimSpace(`
╭───────┬──────╮
│ Name  │ Test │
│ Items │ 1    │
│       │ 2    │
│       │ 3    │
╰───────┴──────╯
`)

	result, err := reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatUnsupportedTypes(t *testing.T) {
	t.Parallel()

	// Test unsupported type: channel
	ch := make(chan int)
	result, err := reflectable.FormatValue(defaultTestOptions(), ch)

	if assert.NoError(t, err) {
		assert.Contains(t, result, "0x") // Pinning the current behavior to expect a pointer address.
	}
}

func TestFormatWithCustomOptions(t *testing.T) {
	t.Parallel()

	options := reflectable.NewOptions().WithFormat(reflectable.FormatMarkdown).WithMaxTableWidth(50).WithColor(true)
	value := []string{"a", "b", "c"}

	expected := strings.TrimSpace(`
| a |
| b |
| c |
`)

	result, err := reflectable.FormatValue(options, value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatLargeData(t *testing.T) {
	t.Parallel()

	largeArray := make([]int, 1000)
	for i := range 1000 {
		largeArray[i] = i + 1
	}

	result, err := reflectable.FormatValue(defaultTestOptions(), largeArray)
	require.NoError(t, err)
	assert.NotEmpty(t, result)
}

func TestFormatSliceOfStructsWithOmittedFields(t *testing.T) {
	t.Parallel()

	type testStruct struct {
		Field1 int    `table:"-"`
		Field2 string `table:"VISIBLEFIELD"`
	}

	value := []testStruct{
		{Field1: 1, Field2: "a"},
		{Field1: 2, Field2: "b"},
	}

	expected := strings.TrimSpace(`
╭──────────────╮
│ VISIBLEFIELD │
├──────────────┤
│ a            │
│ b            │
╰──────────────╯
`)

	result, err := reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}

func TestFormatMapWithIntegerValues(t *testing.T) {
	t.Parallel()

	value := map[string]int{
		"key1": 100,
		"key2": 200,
	}

	expected := strings.TrimSpace(`
╭──────┬─────╮
│ key1 │ 100 │
│ key2 │ 200 │
╰──────┴─────╯
`)

	result, err := reflectable.FormatValue(defaultTestOptions(), value)
	if assert.NoError(t, err) {
		assert.Equal(t, expected, stripansi.Strip(result))
	}
}
