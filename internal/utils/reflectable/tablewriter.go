// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../../tools/mockgen/go.mod mockgen -source=tablewriter.go -destination=reflectable_testutils/tablewriter_mocks.go -package=reflectable_testutils --copyright_file=../../../.license-preamble

package reflectable

import (
	"reflect"
)

type TableProperties struct {
	// Whether the first column is a header
	HeaderColumn bool
}

// TableWriter is an interface for writing tabular data.
// It provides methods to manage tables, rows, columns, and their properties.
//
//nolint:interfacebloat // We think this interface is reasonable for our use.
type TableWriter interface {
	// Starts a new table.
	StartTable(properties TableProperties)
	// Ends the current open table.
	EndTable()

	// Starts a new header row.
	StartHeaderRow()
	// Ends the current open header row.
	EndHeaderRow()
	// Starts a new (non-header) row.
	StartRow()
	// Ends the current (non-header) row.
	EndRow()

	// Starts a new column in the current open row.
	StartColumn()
	// Ends the current open column; does not close the open row.
	EndColumn()

	// Right-aligns the column at the given index.
	RightAlignColumn(index int)
	// Requests sorting by the values in the column at the given index.
	SortByColumn(index int)

	// Writes a value to the current open column.
	WriteValue(value interface{})

	// Checks if we're currently in a table.
	InTable() bool
}

// FormatValueUsingWriter formats and writes a value as a table using the provided tableWriter.
func FormatValueUsingWriter(writer TableWriter, value interface{}) {
	writeReflectedValue(writer, reflect.ValueOf(value))
}

// Uses the given writer to format the given reflected value.
// Handles slices, structs, pointers, booleans, maps, and other types.
func writeReflectedValue(writer TableWriter, value reflect.Value) {
	// Switch on the type of value; dispatch to appropriate helpers for each case.
	switch {
	case value.Kind() == reflect.Slice || value.Kind() == reflect.Array:
		// If we're already inside a table, then avoid nesting another table for the slice.
		if writer.InTable() {
			writeReflectedSliceWithoutTable(writer, value)
		} else {
			writeReflectedSliceAsTable(writer, value)
		}
	case value.Kind() == reflect.Struct:
		writeReflectedStruct(writer, value)
	case value.Kind() == reflect.Pointer:
		// For non-nil pointers, we unwrap and recur.
		if !value.IsZero() {
			writeReflectedValue(writer, value.Elem())
		}
	case value.Kind() == reflect.Bool:
		if value.Bool() {
			writer.WriteValue("true")
		} else {
			writer.WriteValue("false")
		}
	case value.Kind() == reflect.Map:
		writeReflectedMap(writer, value)
	case !value.IsValid():
		// Intentionally do nothing.
	case value.CanInterface():
		// Directly write the value.
		writer.WriteValue(value.Interface())
	default:
	}
}

// Writes the elements of the given slice without using a table, with a newline separating
// consecutive items.
func writeReflectedSliceWithoutTable(writer TableWriter, value reflect.Value) {
	for i := range value.Len() {
		if i > 0 {
			writer.WriteValue("\n")
		}

		element := value.Index(i)
		writeReflectedValue(writer, element)
	}
}

// writeReflectedSliceAsTable formats a slice as a table, with one element per row.
// For slices of structs, it includes a header row with field names.
func writeReflectedSliceAsTable(writer TableWriter, value reflect.Value) {
	writer.StartTable(TableProperties{})

	count := value.Len()

	// For arrays of structs, write a header row first, with field names in the header.
	elemTy := unwrapPointersFromType(value.Type().Elem())

	// Check if it's a struct with >=1 displayable fields.
	displayableStruct := typeIsStructAndHasOneOrMoreDisplayableFields(elemTy)

	// If it is, then write the field display names in a header row.
	if displayableStruct {
		writer.StartHeaderRow()

		// Iterate through fields in default order.
		for fieldIndex := range elemTy.NumField() {
			field := elemTy.Field(fieldIndex)
			fieldOptions := getFieldOptions(field)

			// Skip non-displayable fields.
			if fieldOptions.Omit {
				continue
			}

			// Handle alignment.
			if fieldOptions.RightAlign {
				writer.RightAlignColumn(fieldIndex)
			}

			// Handle sort requests.
			if fieldOptions.SortByThisField {
				writer.SortByColumn(fieldIndex)
			}

			// Recur on the display name.
			writer.StartColumn()
			writer.WriteValue(fieldOptions.DisplayName)
			writer.EndColumn()
		}

		writer.EndHeaderRow()
	}

	// Emit each entry in the slice in its own row.
	for i := range count {
		element := value.Index(i)

		writer.StartRow()

		// If it's a displayable struct, then we write each field in a separate column.
		if displayableStruct {
			// Unwrap any pointers, but note that we might stop at a nil pointer. If we *do*
			// make it all the way to the struct type we expect to find, then handle each field.
			if innerElem := unwrapPointersFromValue(element); innerElem.Kind() == reflect.Struct {
				writeReflectedStructFields(writer, innerElem, true /*fieldsAsColumns*/)
			}
		} else {
			// In all other cases, simply recur on the element in its own column.
			writer.StartColumn()
			writeReflectedValue(writer, element)
			writer.EndColumn()
		}

		writer.EndRow()
	}

	writer.EndTable()
}

// Formats the given struct.
func writeReflectedStruct(writer TableWriter, value reflect.Value) {
	structTy := value.Type()

	//
	// If the struct implements the standard Stringer interface, then prefer using that.
	// Failing that, check if the struct has *at least* one displayable field. If it does,
	// then we go through each such field and display it appropriately via reflection.
	// Otherwise, there's nothing else we can do.
	//
	if stringer, ok := tryGetStringerInterface(value); ok {
		FormatValueUsingWriter(writer, stringer.String())
	} else if typeIsStructAndHasOneOrMoreDisplayableFields(structTy) {
		// Write each field in its own row.
		writer.StartTable(TableProperties{HeaderColumn: true})
		writeReflectedStructFields(writer, value, false /*fieldsAsColumns*/)
		writer.EndTable()
	}
}

// Formats each field of the struct; depending on parameters, may either format each field
// in its own row (with a header column containing the field's display name), *or* with
// each field in its own column (in whatever the current row is).
func writeReflectedStructFields(writer TableWriter, value reflect.Value, fieldsAsColumns bool) {
	fieldCount := value.NumField()

	// Iterate through each field in default order.
	for index := range fieldCount {
		fieldTy := value.Type().Field(index)
		fieldValue := value.Field(index)

		// Retrieve options for this field. Skip non-displayable fields.
		fieldOptions := getFieldOptions(fieldTy)
		if fieldOptions.Omit {
			continue
		}

		if fieldsAsColumns {
			// We're formatting each struct's field in its own column.
			writer.StartColumn()
			writeReflectedValue(writer, fieldValue)
			writer.EndColumn()
		} else {
			// If we were asked to omit empty fields, then first check if the field is "empty".
			if fieldOptions.OmitEmpty && fieldValue.IsZero() {
				continue
			}

			// We're formatting each struct's field in its own row, with a header column
			// with the field's display name.
			writer.StartRow()

			// Write the field display name in the first column.
			writer.StartColumn()
			writer.WriteValue(fieldOptions.DisplayName)
			writer.EndColumn()

			// Recur on the value itself in the second column.
			writer.StartColumn()
			writeReflectedValue(writer, fieldValue)
			writer.EndColumn()

			writer.EndRow()
		}
	}
}

func writeReflectedMap(writer TableWriter, value reflect.Value) {
	// Figure out the type of the map's keys and values.
	mapKeyType := unwrapPointersFromType(value.Type().Key())
	mapValueType := unwrapPointersFromType(value.Type().Elem())

	// Specially handle maps whose values are structs.
	if mapValueType.Kind() == reflect.Struct {
		writeReflectedMapToStructs(writer, value)

		return
	}

	writer.StartTable(TableProperties{})

	// Find all the keys. Make a *best effort* attempt to sort them in a generic way,
	// accepting that the key type may not be comparable (in which case, we leave the
	// keys in whichever order they came in).
	keys := value.MapKeys()
	bestEffortSort(keys)

	// Iterate through all keys; display each entry in its own row.
	for _, key := range keys {
		value := value.MapIndex(key)

		writer.StartRow()

		// Recur on the key in the first column.
		writer.StartColumn()
		writeReflectedValue(writer, key)
		writer.EndColumn()

		// Recur on the value in the second column.
		writer.StartColumn()
		writeReflectedValue(writer, value)
		writer.EndColumn()

		writer.EndRow()
	}

	if isIntegerType(mapKeyType) {
		writer.RightAlignColumn(0)
	}

	if isIntegerType(mapValueType) {
		writer.RightAlignColumn(1)
	}

	writer.EndTable()
}

func writeReflectedMapToStructs(writer TableWriter, value reflect.Value) {
	writer.StartTable(TableProperties{})

	//
	// Start with a header row that contains the field names of the struct.
	//

	writer.StartHeaderRow()

	writer.StartColumn()
	writer.WriteValue("Key")
	writer.EndColumn()

	structTy := unwrapPointersFromType(value.Type().Elem())
	foundSortKey := false

	for fieldIndex := range structTy.NumField() {
		field := structTy.Field(fieldIndex)
		fieldOptions := getFieldOptions(field)

		// Skip non-displayable fields.
		if fieldOptions.Omit {
			continue
		}

		// Handle alignment.
		if fieldOptions.RightAlign {
			writer.RightAlignColumn(fieldIndex + 1)
		}

		// Handle sort
		if fieldOptions.SortByThisField {
			writer.SortByColumn(fieldIndex + 1)

			foundSortKey = true
		}

		writer.StartColumn()
		writer.WriteValue(fieldOptions.DisplayName)
		writer.EndColumn()
	}

	// If none of the fields were sort keys, then at least sort by the map key.
	if !foundSortKey {
		writer.SortByColumn(0)
	}

	writer.EndHeaderRow()

	// Iterate through all keys in default order; display each entry in its own row, with
	// the key formatted in the first column, and the struct's fields formatted in the
	// remaining columns.
	for _, key := range value.MapKeys() {
		writer.StartRow()

		// Recur on the key in the first column.
		writer.StartColumn()
		writeReflectedValue(writer, key)
		writer.EndColumn()

		// Format the struct into the remaining columns of this row.
		value := unwrapPointersFromValue(value.MapIndex(key))
		writeReflectedStructFields(writer, value, true /*fieldsAsColumns*/)

		writer.EndRow()
	}

	writer.EndTable()
}
