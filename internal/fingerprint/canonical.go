// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"fmt"
	"reflect"
)

// maxSafeInteger is the largest integer (2^53) that survives RFC 8785's
// ECMAScript number serialization without precision loss. An integer of greater
// magnitude is rejected rather than silently coerced - such a value is almost
// always an identifier or hash that belongs in the config as a string.
const maxSafeInteger = 1 << 53

// treeBuilder accumulates the canonical projection of a struct as a JSON-able
// map[string]any. The omit predicates mirror the frozen projection semantics:
//
//   - emit: scalar leaf, omit-if-zero (the common "active field" path).
//   - emitAlways: scalar leaf emitted even at its zero value (the '!' case, for a
//     field whose zero is build-meaningful).
//   - emitMap: scalar-valued map measuring key membership - every entry is kept
//     (so {"k":""} differs from {}); the whole map is omitted only when empty.
//   - emitComposite: nested-struct projection, omitted on projected emptiness (the
//     sub-projector produced no measured keys).
//   - emitSlice: struct-slice projection, omitted when empty; order is significant.
//
// Map key ordering is irrelevant here: RFC 8785 sorts object keys at
// serialization, so the builder need not sort. The first error is deferred
// (bufio-style) and surfaces from result, keeping the projectors readable.
type treeBuilder struct {
	out map[string]any
	err error
}

func (b *treeBuilder) set(key string, value any) {
	if b.out == nil {
		b.out = map[string]any{}
	}

	b.out[key] = value
}

// emit adds a scalar-leaf field, omitting it when its resolved value is zero.
func (b *treeBuilder) emit(key string, value any) {
	b.emitScalar(key, value, false)
}

// emitAlways adds a scalar-leaf field even when its value is zero (the '!' case).
func (b *treeBuilder) emitAlways(key string, value any) {
	b.emitScalar(key, value, true)
}

func (b *treeBuilder) emitScalar(key string, value any, always bool) {
	if b.err != nil {
		return
	}

	rval := reflect.ValueOf(value)
	if !rval.IsValid() {
		b.err = fmt.Errorf("emit %#q: a nil value has no fingerprint encoding", key)

		return
	}

	if !isScalarLeaf(rval) {
		b.err = fmt.Errorf(
			"emit %#q: kind %s is not an encodable scalar leaf; composites use emitComposite, emitSlice, or emitMap",
			key, rval.Kind())

		return
	}

	if !always && isScalarZero(rval) {
		return
	}

	encoded, err := scalarToJSON(rval)
	if err != nil {
		b.err = fmt.Errorf("encoding field %#q:\n%w", key, err)

		return
	}

	b.set(key, encoded)
}

// emitMap adds a scalar-valued map, measuring key membership. Struct-valued maps
// are a composite and must be projected entry-by-entry through emitComposite.
func (b *treeBuilder) emitMap(key string, mapValue any) {
	if b.err != nil {
		return
	}

	rval := reflect.ValueOf(mapValue)
	if !rval.IsValid() || rval.Kind() != reflect.Map {
		b.err = fmt.Errorf("emitMap %#q: value is not a map", key)

		return
	}

	if rval.Type().Key().Kind() != reflect.String {
		b.err = fmt.Errorf("emitMap %#q: map key kind %s is not string", key, rval.Type().Key().Kind())

		return
	}

	if rval.Len() == 0 {
		return
	}

	entries := make(map[string]any, rval.Len())

	iter := rval.MapRange()
	for iter.Next() {
		encoded, err := scalarToJSON(iter.Value())
		if err != nil {
			b.err = fmt.Errorf("encoding map %#q entry %#q:\n%w", key, iter.Key().String(), err)

			return
		}

		entries[iter.Key().String()] = encoded
	}

	b.set(key, entries)
}

// emitComposite adds a nested-struct projection, omitting it when the
// sub-projector produced no measured keys (projected emptiness).
func (b *treeBuilder) emitComposite(key string, sub map[string]any) {
	if len(sub) == 0 {
		return
	}

	b.set(key, sub)
}

// emitSlice adds a struct-slice projection, omitting it when empty. A JSON array
// preserves order, so reordering elements is a different encoding.
func (b *treeBuilder) emitSlice(key string, elems []any) {
	if len(elems) == 0 {
		return
	}

	b.set(key, elems)
}

// result returns the accumulated projection map (nil when nothing was emitted)
// or the first deferred error.
func (b *treeBuilder) result() (map[string]any, error) {
	if b.err != nil {
		return nil, b.err
	}

	return b.out, nil
}

// scalarToJSON converts a scalar leaf to its JSON-able Go value by underlying
// kind, so a named type is encoded by kind rather than via any MarshalJSON /
// MarshalText it might carry. An integer beyond the JSON-safe range is rejected
// rather than silently coerced by RFC 8785's number model (see maxSafeInteger).
func scalarToJSON(rval reflect.Value) (any, error) {
	//exhaustive:ignore // the default branch deliberately rejects every un-pinned kind.
	switch rval.Kind() {
	case reflect.String:
		return rval.String(), nil
	case reflect.Bool:
		return rval.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number := rval.Int()
		if number > maxSafeInteger || number < -maxSafeInteger {
			return nil, fmt.Errorf(
				"integer %d exceeds the JSON-safe magnitude 2^53; represent it as a string in the config schema", number)
		}

		return number, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number := rval.Uint()
		if number > maxSafeInteger {
			return nil, fmt.Errorf(
				"integer %d exceeds the JSON-safe magnitude 2^53; represent it as a string in the config schema", number)
		}

		return number, nil
	case reflect.Slice:
		out := make([]any, 0, rval.Len())
		for i := range rval.Len() {
			elem, err := scalarToJSON(rval.Index(i))
			if err != nil {
				return nil, fmt.Errorf("slice element %d:\n%w", i, err)
			}

			out = append(out, elem)
		}

		return out, nil
	default:
		return nil, fmt.Errorf(
			"kind %s is not encodable: only string, bool, JSON-safe integers, and slices of those are pinned",
			rval.Kind())
	}
}

// isScalarZero is the omit-if-zero predicate for a scalar leaf. A nil AND a
// non-nil empty scalar slice both count as zero, so emit cannot distinguish them
// (resolution's mergo WithAppendSlice merge yields either for the same intent).
// emitAlways bypasses this, so an explicit empty '!' slice still emits. Non-slice
// scalars fall back to plain IsZero, unchanged.
func isScalarZero(rval reflect.Value) bool {
	if rval.Kind() == reflect.Slice {
		return rval.Len() == 0
	}

	return rval.IsZero()
}

// isScalarLeaf reports whether v is a scalar leaf for omit-predicate purposes:
// a scalar kind, or a slice whose element kind is scalar. Composites (struct,
// map, slice-of-struct, pointer, interface, ...) are not scalar leaves.
func isScalarLeaf(rval reflect.Value) bool {
	if isScalarKind(rval.Kind()) {
		return true
	}

	if rval.Kind() == reflect.Slice {
		return isScalarKind(rval.Type().Elem().Kind())
	}

	return false
}

// isScalarKind reports whether k is a scalar kind pinned by the v1 encoding:
// string, bool, and the sized signed/unsigned integers. Named
// scalar types (e.g. SpecSourceType) report their underlying kind here, so they
// are measured by their underlying kind rather than failing.
func isScalarKind(k reflect.Kind) bool {
	//exhaustive:ignore // the default branch deliberately rejects every other kind.
	switch k {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}
