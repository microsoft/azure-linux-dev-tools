// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reflectable

import (
	"fmt"
	"reflect"
)

// Returns given type if not a pointer type; otherwise unwraps all levels of
// pointer indirection and yields the outermost non-pointer type within.
func unwrapPointersFromType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}

	return typ
}

// Returns given value if not a pointer value; otherwise unwraps all levels of
// pointer indirection and yields the inner value. Stops at the first nil pointer.
func unwrapPointersFromValue(value reflect.Value) reflect.Value {
	for value.Kind() == reflect.Ptr && !value.IsZero() {
		value = value.Elem()
	}

	return value
}

func isIntegerType(typ reflect.Type) bool {
	//nolint:exhaustive // We're okay only having cases for integer types.
	switch typ.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}

// Tries to retrieve the Stringer interface from the given value. Returns nil, false
// if the value does not implement the interface.
func tryGetStringerInterface(value reflect.Value) (stringer fmt.Stringer, ok bool) {
	// See if the value itself implements the Stringer interface.
	stringer, ok = value.Interface().(fmt.Stringer)
	if ok {
		return stringer, ok
	}

	// If the value is addressable, the check to see if a pointer to the value implements
	// the Stringer interface.
	if value.CanAddr() {
		stringer, ok = value.Addr().Interface().(fmt.Stringer)
		if ok {
			return stringer, ok
		}
	}

	return nil, false
}
