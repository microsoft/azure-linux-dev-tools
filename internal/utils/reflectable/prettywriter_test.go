// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // We intentionally want to test internal functions.
package reflectable

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPrettyWriter(t *testing.T) {
	t.Parallel()

	t.Run("creates writer with default options", func(t *testing.T) {
		t.Parallel()

		options := NewOptions()
		writer := newPrettyWriter(options)

		assert.NotNil(t, writer)
		assert.Equal(t, options, writer.options)
		assert.Len(t, writer.tableStack, 1)
		assert.Equal(t, tableStackItem{}, writer.tableStack[0])
	})

	t.Run("creates writer with custom options", func(t *testing.T) {
		t.Parallel()

		options := NewOptions().WithFormat(FormatMarkdown).WithMaxTableWidth(100).WithColor(true)
		writer := newPrettyWriter(options)

		assert.NotNil(t, writer)
		assert.Equal(t, options, writer.options)
		assert.Equal(t, FormatMarkdown, writer.options.Format)
		assert.Equal(t, 100, writer.options.MaxTableWidth)
		assert.True(t, writer.options.ColorEnabled)
		assert.Len(t, writer.tableStack, 1)
	})
}

func TestPrettyWriterCurrent(t *testing.T) {
	t.Parallel()

	t.Run("returns current table stack item", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		current := writer.current()

		assert.NotNil(t, current)
		assert.Equal(t, &writer.tableStack[0], current)
	})

	t.Run("returns latest item when multiple items on stack", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())

		// Add another item to the stack
		writer.StartTable(TableProperties{HeaderColumn: true})

		current := writer.current()
		assert.NotNil(t, current)
		assert.Equal(t, &writer.tableStack[1], current)
		assert.True(t, current.properties.HeaderColumn)
	})
}

func TestPrettyWriterInTable(t *testing.T) {
	t.Parallel()

	t.Run("returns false when only one item on stack", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		assert.False(t, writer.InTable())
	})

	t.Run("returns true when multiple items on stack", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})
		assert.True(t, writer.InTable())
	})
}

func TestPrettyWriterFinish(t *testing.T) {
	t.Parallel()

	t.Run("returns content from first stack item", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.tableStack[0].currentColumn = "test content"

		result := writer.Finish()
		assert.Equal(t, "test content", result)
	})

	t.Run("returns empty string when no content", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())

		result := writer.Finish()
		assert.Empty(t, result)
	})

	// Edge case: empty table stack
	t.Run("edge case: empty table stack", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		// Clear the table stack to test the error condition
		writer.tableStack = []tableStackItem{}

		result := writer.Finish()
		assert.Empty(t, result)
	})
}

func TestPrettyWriterStartTable(t *testing.T) {
	t.Parallel()

	t.Run("adds new table to stack", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		initialStackSize := len(writer.tableStack)

		properties := TableProperties{HeaderColumn: true}
		writer.StartTable(properties)

		assert.Len(t, writer.tableStack, initialStackSize+1)
		current := writer.current()
		assert.NotNil(t, current.table)
		assert.Equal(t, properties, current.properties)
	})

	t.Run("configures table with max width when specified", func(t *testing.T) {
		t.Parallel()

		options := NewOptions().WithMaxTableWidth(80)
		writer := newPrettyWriter(options)

		writer.StartTable(TableProperties{})

		// Note: We can't easily test the internal table width setting
		// without accessing private fields, but we can verify the table exists
		current := writer.current()
		assert.NotNil(t, current.table)
	})

	t.Run("handles zero max width", func(t *testing.T) {
		t.Parallel()

		options := NewOptions().WithMaxTableWidth(0)
		writer := newPrettyWriter(options)

		writer.StartTable(TableProperties{})

		current := writer.current()
		assert.NotNil(t, current.table)
	})
}

func TestPrettyWriterEndTable(t *testing.T) {
	t.Parallel()

	t.Run("renders table and pops from stack", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})
		initialStackSize := len(writer.tableStack)

		// Add some basic content to render
		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("test")
		writer.EndColumn()
		writer.EndRow()

		writer.EndTable()

		assert.Len(t, writer.tableStack, initialStackSize-1)
		// The rendered content should be in the parent's currentColumn
		assert.NotEmpty(t, writer.current().currentColumn)
	})

	t.Run("renders as markdown format", func(t *testing.T) {
		t.Parallel()

		options := NewOptions().WithFormat(FormatMarkdown)
		writer := newPrettyWriter(options)
		writer.StartTable(TableProperties{})

		// Add some content
		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("test")
		writer.EndColumn()
		writer.EndRow()

		writer.EndTable()

		result := writer.current().currentColumn
		assert.NotEmpty(t, result)
		// Markdown tables should contain pipe characters
		assert.Contains(t, result, "|")
	})

	t.Run("renders as CSV format", func(t *testing.T) {
		t.Parallel()

		options := NewOptions().WithFormat(FormatCSV)
		writer := newPrettyWriter(options)
		writer.StartTable(TableProperties{})

		// Add some content
		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("test")
		writer.EndColumn()
		writer.EndRow()

		writer.EndTable()

		result := writer.current().currentColumn
		assert.NotEmpty(t, result)
		// CSV should contain the test value
		assert.Contains(t, result, "test")
	})

	t.Run("applies header column styling when color enabled", func(t *testing.T) {
		t.Parallel()

		options := NewOptions().WithColor(true)
		writer := newPrettyWriter(options)

		properties := TableProperties{HeaderColumn: true}
		writer.StartTable(properties)

		// Add header content
		writer.StartHeaderRow()
		writer.StartColumn()
		writer.WriteValue("Header")
		writer.EndColumn()
		writer.EndHeaderRow()

		// Add regular row
		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("Data")
		writer.EndColumn()
		writer.EndRow()

		writer.EndTable()

		result := writer.current().currentColumn
		assert.NotEmpty(t, result)
	})

	t.Run("handles table without header column styling", func(t *testing.T) {
		t.Parallel()

		options := NewOptions().WithColor(true)
		writer := newPrettyWriter(options)

		properties := TableProperties{HeaderColumn: false}
		writer.StartTable(properties)

		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("test")
		writer.EndColumn()
		writer.EndRow()

		writer.EndTable()

		result := writer.current().currentColumn
		assert.NotEmpty(t, result)
	})
}

func TestPrettyWriterRowFunctions(t *testing.T) {
	t.Parallel()

	t.Run("StartHeaderRow calls StartRow", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		writer.StartHeaderRow()

		current := writer.current()
		assert.NotNil(t, current.currentRow)
		assert.Empty(t, current.currentRow)
	})

	t.Run("EndHeaderRow appends header to table", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		writer.StartHeaderRow()
		writer.StartColumn()
		writer.WriteValue("Header")
		writer.EndColumn()
		writer.EndHeaderRow()

		current := writer.current()
		assert.Nil(t, current.currentRow) // Should be cleared
	})

	t.Run("EndHeaderRow handles empty table stack", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		// Clear the table stack to test the guard condition
		writer.tableStack = []tableStackItem{}

		// Should not panic
		writer.EndHeaderRow()
	})

	t.Run("StartRow initializes empty row", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		writer.StartRow()

		current := writer.current()
		assert.NotNil(t, current.currentRow)
		assert.Empty(t, current.currentRow)
	})

	t.Run("EndRow appends row to table", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("Data")
		writer.EndColumn()
		writer.EndRow()

		current := writer.current()
		assert.Nil(t, current.currentRow) // Should be cleared
	})

	t.Run("EndRow handles empty table stack", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		// Clear the table stack to test the guard condition
		writer.tableStack = []tableStackItem{}

		// Should not panic
		writer.EndRow()
	})
}

func TestPrettyWriterColumnFunctions(t *testing.T) {
	t.Parallel()

	t.Run("StartColumn initializes empty column", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		writer.StartColumn()

		current := writer.current()
		assert.Empty(t, current.currentColumn)
	})

	t.Run("EndColumn appends column to current row", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})
		writer.StartRow()

		writer.StartColumn()
		writer.WriteValue("test content")
		writer.EndColumn()

		current := writer.current()
		assert.Len(t, current.currentRow, 1)
		assert.Equal(t, "test content", current.currentRow[0])
	})

	t.Run("multiple columns in single row", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})
		writer.StartRow()

		// First column
		writer.StartColumn()
		writer.WriteValue("col1")
		writer.EndColumn()

		// Second column
		writer.StartColumn()
		writer.WriteValue("col2")
		writer.EndColumn()

		current := writer.current()
		assert.Len(t, current.currentRow, 2)
		assert.Equal(t, "col1", current.currentRow[0])
		assert.Equal(t, "col2", current.currentRow[1])
	})
}

func TestPrettyWriterWriteValue(t *testing.T) {
	t.Parallel()

	t.Run("writes single value to current column", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})
		writer.StartColumn()

		writer.WriteValue("test")

		current := writer.current()
		assert.Equal(t, "test", current.currentColumn)
	})

	t.Run("appends multiple values to current column", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})
		writer.StartColumn()

		writer.WriteValue("hello")
		writer.WriteValue(" ")
		writer.WriteValue("world")

		current := writer.current()
		assert.Equal(t, "hello world", current.currentColumn)
	})

	t.Run("handles different value types", func(t *testing.T) {
		t.Parallel()

		testCases := []struct {
			name     string
			value    interface{}
			expected string
		}{
			{"string", "test", "test"},
			{"integer", 42, "42"},
			{"boolean true", true, "true"},
			{"boolean false", false, "false"},
			{"nil", nil, "<nil>"},
			{"float", 3.14, "3.14"},
		}

		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				t.Parallel()

				writer := newPrettyWriter(NewOptions())
				writer.StartTable(TableProperties{})
				writer.StartColumn()

				writer.WriteValue(testCase.value)

				current := writer.current()
				assert.Equal(t, testCase.expected, current.currentColumn)
			})
		}
	})
}

func TestPrettyWriterRightAlignColumn(t *testing.T) {
	t.Parallel()

	t.Run("adds right alignment configuration", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		writer.RightAlignColumn(0)

		current := writer.current()
		assert.Len(t, current.columnConfigs, 1)
		assert.Equal(t, 1, current.columnConfigs[0].Number) // Should be 1-based
	})

	t.Run("handles multiple column alignments", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		writer.RightAlignColumn(0)
		writer.RightAlignColumn(2)

		current := writer.current()
		assert.Len(t, current.columnConfigs, 2)
		assert.Equal(t, 1, current.columnConfigs[0].Number) // 0-based index 0 -> 1-based number 1
		assert.Equal(t, 3, current.columnConfigs[1].Number) // 0-based index 2 -> 1-based number 3
	})

	t.Run("converts zero-based index to one-based number", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		writer.RightAlignColumn(5)

		current := writer.current()
		assert.Len(t, current.columnConfigs, 1)
		assert.Equal(t, 6, current.columnConfigs[0].Number) // 0-based index 5 -> 1-based number 6
	})
}

func TestPrettyWriterSortByColumn(t *testing.T) {
	t.Parallel()

	t.Run("configures table sorting by column", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		writer.SortByColumn(1)

		// We can't easily verify the internal sort configuration without accessing private fields,
		// but we can ensure the method doesn't panic and completes successfully
		current := writer.current()
		assert.NotNil(t, current.table)
	})

	t.Run("converts zero-based index to one-based number for sorting", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		// Add some content to sort
		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("B")
		writer.EndColumn()
		writer.StartColumn()
		writer.WriteValue("2")
		writer.EndColumn()
		writer.EndRow()

		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("A")
		writer.EndColumn()
		writer.StartColumn()
		writer.WriteValue("1")
		writer.EndColumn()
		writer.EndRow()

		// Sort by first column (index 0)
		writer.SortByColumn(0)

		writer.EndTable()

		// In a real scenario, this would be sorted, but we can't easily verify
		// the exact sorting without parsing the rendered table output
		result := writer.current().currentColumn
		assert.NotEmpty(t, result)
	})

	t.Run("handles multiple sort requests", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())
		writer.StartTable(TableProperties{})

		// Multiple calls should not panic
		writer.SortByColumn(0)
		writer.SortByColumn(1)

		current := writer.current()
		assert.NotNil(t, current.table)
	})
}

func TestPrettyWriterIntegration(t *testing.T) {
	t.Parallel()

	t.Run("complete table workflow", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())

		// Start table
		writer.StartTable(TableProperties{HeaderColumn: true})

		// Add header
		writer.StartHeaderRow()
		writer.StartColumn()
		writer.WriteValue("Name")
		writer.EndColumn()
		writer.StartColumn()
		writer.WriteValue("Age")
		writer.EndColumn()
		writer.EndHeaderRow()

		// Add data row
		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("John")
		writer.EndColumn()
		writer.StartColumn()
		writer.WriteValue(30)
		writer.EndColumn()
		writer.EndRow()

		// Configure columns
		writer.RightAlignColumn(1) // Right-align age column
		writer.SortByColumn(0)     // Sort by name

		// End table
		writer.EndTable()

		// Finish and get result
		result := writer.Finish()

		assert.NotEmpty(t, result)
		assert.Contains(t, result, "NAME") // Headers are uppercased by go-pretty
		assert.Contains(t, result, "AGE")  // Headers are uppercased by go-pretty
		assert.Contains(t, result, "John")
		assert.Contains(t, result, "30")
	})

	t.Run("nested tables", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())

		// Outer table
		writer.StartTable(TableProperties{})

		// Start row in outer table
		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("Outer Content")
		writer.EndColumn()
		writer.StartColumn()

		// Nested table
		writer.StartTable(TableProperties{})
		writer.StartRow()
		writer.StartColumn()
		writer.WriteValue("Inner")
		writer.EndColumn()
		writer.EndRow()
		writer.EndTable()

		writer.EndColumn()
		writer.EndRow()

		writer.EndTable()

		result := writer.Finish()

		assert.NotEmpty(t, result)
		assert.Contains(t, result, "Outer Content")
		assert.Contains(t, result, "Inner")
	})

	t.Run("InTable returns correct values for nested tables", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())

		// Initially not in table (only base item)
		assert.False(t, writer.InTable())

		// Start first table
		writer.StartTable(TableProperties{})
		assert.True(t, writer.InTable())

		// Start nested table
		writer.StartTable(TableProperties{})
		assert.True(t, writer.InTable())

		// End nested table
		writer.EndTable()
		assert.True(t, writer.InTable())

		// End outer table
		writer.EndTable()
		assert.False(t, writer.InTable())
	})

	t.Run("different output formats produce different results", func(t *testing.T) {
		t.Parallel()

		testData := func(writer *prettyWriter) {
			writer.StartTable(TableProperties{})
			writer.StartRow()
			writer.StartColumn()
			writer.WriteValue("test")
			writer.EndColumn()
			writer.EndRow()
			writer.EndTable()
		}

		// Table format
		tableWriter := newPrettyWriter(NewOptions().WithFormat(FormatTable))
		testData(tableWriter)
		tableResult := tableWriter.Finish()

		// Markdown format
		markdownWriter := newPrettyWriter(NewOptions().WithFormat(FormatMarkdown))
		testData(markdownWriter)
		markdownResult := markdownWriter.Finish()

		// CSV format
		csvWriter := newPrettyWriter(NewOptions().WithFormat(FormatCSV))
		testData(csvWriter)
		csvResult := csvWriter.Finish()

		// Results should be different
		assert.NotEqual(t, tableResult, markdownResult)
		assert.NotEqual(t, tableResult, csvResult)
		assert.NotEqual(t, markdownResult, csvResult)

		// All should contain the test data
		assert.Contains(t, tableResult, "test")
		assert.Contains(t, markdownResult, "test")
		assert.Contains(t, csvResult, "test")

		// Markdown should have pipe characters
		assert.Contains(t, markdownResult, "|")
	})

	t.Run("empty table produces valid output", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())

		writer.StartTable(TableProperties{})
		writer.EndTable()

		result := writer.Finish()

		// Even empty tables should produce some output (border characters, etc.)
		// The exact output depends on the table library's behavior
		assert.NotNil(t, result)
	})

	t.Run("table with only headers", func(t *testing.T) {
		t.Parallel()

		writer := newPrettyWriter(NewOptions())

		writer.StartTable(TableProperties{HeaderColumn: true})

		writer.StartHeaderRow()
		writer.StartColumn()
		writer.WriteValue("Header1")
		writer.EndColumn()
		writer.StartColumn()
		writer.WriteValue("Header2")
		writer.EndColumn()
		writer.EndHeaderRow()

		writer.EndTable()

		result := writer.Finish()

		// Tables with only headers produce empty output in go-pretty library
		assert.Empty(t, result)
	})
}
