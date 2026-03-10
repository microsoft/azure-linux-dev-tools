// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reflectable

import (
	"fmt"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

func newPrettyWriter(options *Options) *prettyWriter {
	return &prettyWriter{
		options: options,
		tableStack: []tableStackItem{
			{},
		},
	}
}

func (w *prettyWriter) Finish() string {
	if len(w.tableStack) == 0 {
		return ""
	}

	return w.tableStack[0].currentColumn
}

type tableStackItem struct {
	table         table.Writer
	properties    TableProperties
	currentRow    table.Row
	currentColumn string
	columnConfigs []table.ColumnConfig
}

type prettyWriter struct {
	options    *Options
	tableStack []tableStackItem
}

func (w *prettyWriter) current() *tableStackItem {
	return &w.tableStack[len(w.tableStack)-1]
}

func (w *prettyWriter) StartTable(properties TableProperties) {
	tableWriter := table.NewWriter()
	tableStyle := table.StyleRounded

	tableWriter.SuppressEmptyColumns()
	tableWriter.SuppressTrailingSpaces()
	tableWriter.SetStyle(tableStyle)

	// Pass along max width.
	if w.options.MaxTableWidth != 0 {
		tableWriter.Style().Size.WidthMax = w.options.MaxTableWidth
	}

	w.tableStack = append(w.tableStack, tableStackItem{
		table:      tableWriter,
		properties: properties,
	})
}

func (w *prettyWriter) EndTable() {
	tableEntry := w.current()
	w.tableStack = w.tableStack[:len(w.tableStack)-1]

	if w.options.ColorEnabled && tableEntry.properties.HeaderColumn {
		tableEntry.columnConfigs = append(tableEntry.columnConfigs, table.ColumnConfig{
			Number: 1,
			Colors: text.Colors{text.Bold, text.Italic},
		})
	}

	tableEntry.table.SetColumnConfigs(tableEntry.columnConfigs)

	var rendered string

	switch w.options.Format {
	case FormatTable:
		rendered = tableEntry.table.Render()
	case FormatMarkdown:
		rendered = tableEntry.table.RenderMarkdown()
	case FormatCSV:
		rendered = tableEntry.table.RenderCSV()
	}

	w.current().currentColumn = rendered
}

func (w *prettyWriter) StartHeaderRow() {
	w.StartRow()
}

func (w *prettyWriter) EndHeaderRow() {
	if len(w.tableStack) == 0 {
		return
	}

	c := w.current()

	c.table.AppendHeader(c.currentRow)
	c.currentRow = nil
}

func (w *prettyWriter) StartRow() {
	w.current().currentRow = table.Row{}
}

func (w *prettyWriter) EndRow() {
	if len(w.tableStack) == 0 {
		return
	}

	c := w.current()

	c.table.AppendRow(c.currentRow)
	c.currentRow = nil
}

func (w *prettyWriter) StartColumn() {
	w.current().currentColumn = ""
}

func (w *prettyWriter) EndColumn() {
	c := w.current()
	c.currentRow = append(c.currentRow, c.currentColumn)
}

func (w *prettyWriter) WriteValue(value interface{}) {
	w.current().currentColumn += fmt.Sprintf("%v", value)
}

func (w *prettyWriter) RightAlignColumn(index int) {
	c := w.current()
	c.columnConfigs = append(c.columnConfigs, table.ColumnConfig{
		Number: index + 1, // N.B. these numbers seem to be 1-based, so we add 1 from our 0-based index.
		Align:  text.AlignRight,
	})
}

func (w *prettyWriter) SortByColumn(index int) {
	c := w.current()
	c.table.SortBy([]table.SortBy{
		{
			Number: index + 1, // N.B. these numbers seem to be 1-based, so we add 1 from our 0-based index.
			Mode:   table.AscAlphaNumeric,
		},
	})
}

func (w *prettyWriter) InTable() bool {
	return len(w.tableStack) > 1
}
