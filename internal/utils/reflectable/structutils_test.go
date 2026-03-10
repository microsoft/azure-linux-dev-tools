// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // We intentionally want to test internal functions.
package reflectable

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFieldOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		field    reflect.StructField
		expected fieldOptions
	}{
		{
			name: "unexported field with no tags",
			field: reflect.StructField{
				Name:    "testField",
				Type:    reflect.TypeOf(string("")),
				PkgPath: "main", // Non-empty PkgPath indicates unexported field
			},
			expected: fieldOptions{
				DisplayName:     "Test Field",
				Omit:            true, // Unexported field
				OmitEmpty:       false,
				RightAlign:      false,
				SortByThisField: false,
			},
		},
		{
			name: "exported field with no tags",
			field: reflect.StructField{
				Name:    "TestField",
				Type:    reflect.TypeOf(string("")),
				PkgPath: "", // Empty PkgPath indicates exported field
			},
			expected: fieldOptions{
				DisplayName:     "Test Field",
				Omit:            false,
				OmitEmpty:       false,
				RightAlign:      false,
				SortByThisField: false,
			},
		},
		{
			name: "field with table tag to omit",
			field: reflect.StructField{
				Name:    "TestField",
				Type:    reflect.TypeOf(string("")),
				Tag:     reflect.StructTag(`table:"-"`),
				PkgPath: "",
			},
			expected: fieldOptions{
				DisplayName:     "Test Field",
				Omit:            true,
				OmitEmpty:       false,
				RightAlign:      false,
				SortByThisField: false,
			},
		},
		{
			name: "field with table tag to rename",
			field: reflect.StructField{
				Name:    "TestField",
				Type:    reflect.TypeOf(string("")),
				Tag:     reflect.StructTag(`table:"Custom Name"`),
				PkgPath: "",
			},
			expected: fieldOptions{
				DisplayName:     "Custom Name",
				Omit:            false,
				OmitEmpty:       false,
				RightAlign:      false,
				SortByThisField: false,
			},
		},
		{
			name: "field with table tag omitempty",
			field: reflect.StructField{
				Name:    "TestField",
				Type:    reflect.TypeOf(string("")),
				Tag:     reflect.StructTag(`table:",omitempty"`),
				PkgPath: "",
			},
			expected: fieldOptions{
				DisplayName:     "Test Field",
				Omit:            false,
				OmitEmpty:       true,
				RightAlign:      false,
				SortByThisField: false,
			},
		},
		{
			name: "field with table tag sortkey",
			field: reflect.StructField{
				Name:    "TestField",
				Type:    reflect.TypeOf(string("")),
				Tag:     reflect.StructTag(`table:",sortkey"`),
				PkgPath: "",
			},
			expected: fieldOptions{
				DisplayName:     "Test Field",
				Omit:            false,
				OmitEmpty:       false,
				RightAlign:      false,
				SortByThisField: true,
			},
		},
		{
			name: "field with combined table tags",
			field: reflect.StructField{
				Name:    "TestField",
				Type:    reflect.TypeOf(string("")),
				Tag:     reflect.StructTag(`table:"CustomName,omitempty,sortkey"`),
				PkgPath: "",
			},
			expected: fieldOptions{
				DisplayName:     "CustomName",
				Omit:            false,
				OmitEmpty:       true,
				RightAlign:      false,
				SortByThisField: true,
			},
		},
		{
			name: "field with json tag fallback",
			field: reflect.StructField{
				Name:    "TestField",
				Type:    reflect.TypeOf(string("")),
				Tag:     reflect.StructTag(`json:"json_name,omitempty"`),
				PkgPath: "",
			},
			expected: fieldOptions{
				DisplayName:     "json_name",
				Omit:            false,
				OmitEmpty:       true,
				RightAlign:      false,
				SortByThisField: false,
			},
		},
		{
			name: "field with json tag to omit",
			field: reflect.StructField{
				Name:    "TestField",
				Type:    reflect.TypeOf(string("")),
				Tag:     reflect.StructTag(`json:"-"`),
				PkgPath: "",
			},
			expected: fieldOptions{
				DisplayName:     "Test Field",
				Omit:            true,
				OmitEmpty:       false,
				RightAlign:      false,
				SortByThisField: false,
			},
		},
		{
			name: "integer field should be right-aligned",
			field: reflect.StructField{
				Name:    "IntField",
				Type:    reflect.TypeOf(int(0)),
				PkgPath: "",
			},
			expected: fieldOptions{
				DisplayName:     "Int Field",
				Omit:            false,
				OmitEmpty:       false,
				RightAlign:      true,
				SortByThisField: false,
			},
		},
		{
			name: "pointer to integer field should be right-aligned",
			field: reflect.StructField{
				Name:    "IntPtrField",
				Type:    reflect.TypeOf((*int)(nil)),
				PkgPath: "",
			},
			expected: fieldOptions{
				DisplayName:     "Int Ptr Field",
				Omit:            false,
				OmitEmpty:       false,
				RightAlign:      true,
				SortByThisField: false,
			},
		},
		{
			name: "table tag overrides json tag",
			field: reflect.StructField{
				Name:    "TestField",
				Type:    reflect.TypeOf(string("")),
				Tag:     reflect.StructTag(`table:"table_name" json:"json_name"`),
				PkgPath: "",
			},
			expected: fieldOptions{
				DisplayName:     "table_name",
				Omit:            false,
				OmitEmpty:       false,
				RightAlign:      false,
				SortByThisField: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := getFieldOptions(tt.field)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDefaultDisplayNameForField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "simpleField",
			expected: "Simple Field",
		},
		{
			input:    "URL",
			expected: "URL",
		},
		{
			input:    "HTTPSConnection",
			expected: "HTTPSConnection",
		},
		{
			input:    "id",
			expected: "Id",
		},
		{
			input:    "ID",
			expected: "ID",
		},
		{
			input:    "APIKey",
			expected: "APIKey",
		},
		{
			input:    "UserID",
			expected: "User ID",
		},
		{
			input:    "XMLParser",
			expected: "XMLParser",
		},
		{
			input:    "firstName",
			expected: "First Name",
		},
		{
			input:    "lastName",
			expected: "Last Name",
		},
		{
			input:    "phoneNumber",
			expected: "Phone Number",
		},
		{
			input:    "OAuth2Token",
			expected: "OAuth2 Token",
		},
		{
			input:    "a",
			expected: "A",
		},
		{
			input:    "AB",
			expected: "AB",
		},
		{
			input:    "ABC",
			expected: "ABC",
		},
		{
			input:    "abcDef",
			expected: "Abc Def",
		},
		{
			input:    "ABCDef",
			expected: "ABCDef",
		},
		{
			input:    "myVeryLongFieldName",
			expected: "My Very Long Field Name",
		},
		{
			input:    "Field123",
			expected: "Field123",
		},
		{
			input:    "field123ABC",
			expected: "Field123 ABC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			result := getDefaultDisplayNameForField(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTypeIsStructAndHasOneOrMoreDisplayableFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		typ      reflect.Type
		expected bool
	}{
		{
			name:     "string type is not a struct",
			typ:      reflect.TypeOf(""),
			expected: false,
		},
		{
			name:     "int type is not a struct",
			typ:      reflect.TypeOf(0),
			expected: false,
		},
		{
			name:     "slice type is not a struct",
			typ:      reflect.TypeOf([]string{}),
			expected: false,
		},
		{
			name: "struct with exported fields",
			typ: reflect.TypeOf(struct {
				Name string
				Age  int
			}{}),
			expected: true,
		},
		{
			name: "struct with only unexported fields",
			typ: reflect.TypeOf(struct {
				name string
				age  int
			}{}),
			expected: false,
		},
		{
			name: "struct with mixed exported and unexported fields",
			typ: reflect.TypeOf(struct {
				Name string
				age  int
			}{}),
			expected: true,
		},
		{
			name: "struct with field tagged to omit",
			typ: reflect.TypeOf(struct {
				Name string `table:"-"`
			}{}),
			expected: false,
		},
		{
			name: "struct with some fields tagged to omit, some not",
			typ: reflect.TypeOf(struct {
				Name string `table:"-"`
				Age  int
			}{}),
			expected: true,
		},
		{
			name: "struct with json tag to omit",
			typ: reflect.TypeOf(struct {
				Name string `json:"-"`
			}{}),
			expected: false,
		},
		{
			name:     "empty struct",
			typ:      reflect.TypeOf(struct{}{}),
			expected: false,
		},
		{
			name: "struct with table tag but not omitted",
			typ: reflect.TypeOf(struct {
				Name string `table:"CustomName"`
			}{}),
			expected: true,
		},
		{
			name: "struct with omitempty tag (not omitted)",
			typ: reflect.TypeOf(struct {
				Name string `table:",omitempty"`
			}{}),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := typeIsStructAndHasOneOrMoreDisplayableFields(tt.typ)
			assert.Equal(t, tt.expected, result)
		})
	}
}
