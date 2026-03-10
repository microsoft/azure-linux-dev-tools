// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reflectable

import (
	"reflect"
	"strings"
	"unicode"
)

// fieldOptions encapsulates parsed options for display of a field.
type fieldOptions struct {
	// Name to display for the field.
	DisplayName string
	// Whether to omit the field from display.
	Omit bool
	// Whether to omit the field from display when it's "empty".
	OmitEmpty bool
	// Whether to right-align the field.
	RightAlign bool
	// Whether or not to sort by this field.
	SortByThisField bool
}

// getFieldOptions retrieves the display options for a field. Uses a combination of inspection
// of the field's type and metadata, as well as tags on the field.
func getFieldOptions(field reflect.StructField) (options fieldOptions) {
	options = fieldOptions{
		DisplayName: getDefaultDisplayNameForField(field.Name),
	}

	// Skip unexported fields.
	if !field.IsExported() {
		options.Omit = true

		return options
	}

	// If a table: tag is present, then use that; otherwise, fall back to honoring
	// the json: tag (if one exists).
	tag, ok := field.Tag.Lookup("table")
	if !ok {
		tag, ok = field.Tag.Lookup("json")
	}

	if ok {
		// N.B.: We assume there are no commas in tag components.
		tagParts := strings.Split(tag, ",")

		// Process tags we understand, ignore others.
		for index, tagPart := range tagParts {
			switch {
			case index == 0 && tagPart == "-":
				options.Omit = true
			case index == 0 && tagPart == "":
				// No-op; use defaulted name.
			case index == 0:
				// Override name.
				options.DisplayName = tagPart
			case tagPart == "omitempty":
				options.OmitEmpty = true
			case tagPart == "sortkey":
				options.SortByThisField = true
			}
		}
	}

	// Look at the type of the field.
	if innerFieldTy := unwrapPointersFromType(field.Type); isIntegerType(innerFieldTy) {
		options.RightAlign = true
	}

	return options
}

// getDefaultDisplayNameForField retrieves the *default* display name to use for a field with the provided name.
func getDefaultDisplayNameForField(fieldName string) string {
	var result []rune

	insertSpaceOnNextUpper := false

	for i, currentChar := range fieldName {
		switch {
		case i == 0:
			result = append(result, unicode.ToUpper(currentChar))
			insertSpaceOnNextUpper = false
		case insertSpaceOnNextUpper && unicode.IsUpper(currentChar):
			result = append(result, ' ')
			result = append(result, currentChar)
			insertSpaceOnNextUpper = false
		default:
			result = append(result, currentChar)
			insertSpaceOnNextUpper = !unicode.IsUpper(currentChar)
		}
	}

	return string(result)
}

// Check if the given type is a struct with *at least* one displayable field.
func typeIsStructAndHasOneOrMoreDisplayableFields(typ reflect.Type) bool {
	if typ.Kind() != reflect.Struct {
		return false
	}

	// Go through all fields; return true as soon as we find a single displayable field.
	for i := range typ.NumField() {
		field := typ.Field(i)

		options := getFieldOptions(field)
		if !options.Omit {
			return true
		}
	}

	return false
}
