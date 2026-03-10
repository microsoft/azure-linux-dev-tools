// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reflectable

import (
	"cmp"
	"reflect"
	"slices"
)

// bestEffortSort sorts a slice of reflect.Value elements if they are comparable.
// It determines the sorting strategy based on the type of the first element.
// Supported types include strings, integers, unsigned integers, and floats.
func bestEffortSort(values []reflect.Value) {
	if len(values) == 0 {
		return
	}

	sampleValue := values[0]
	if !sampleValue.Comparable() {
		return
	}

	eltType := sampleValue.Type()

	slices.SortStableFunc(values, func(left, right reflect.Value) int {
		switch {
		case eltType.Kind() == reflect.String:
			return cmp.Compare(left.String(), right.String())
		case left.CanUint():
			return cmp.Compare(left.Uint(), right.Uint())
		case left.CanInt():
			return cmp.Compare(left.Int(), right.Int())
		case left.CanFloat():
			return cmp.Compare(left.Float(), right.Float())
		default:
			return 0
		}
	})
}
