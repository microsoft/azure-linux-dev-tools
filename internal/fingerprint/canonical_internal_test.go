// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// namedString and namedInt model the measured named scalar types in the config
// graph (e.g. SpecSourceType, ReleaseCalculation): they must encode by their
// underlying kind, not via any marshaler the named type might carry.
type (
	namedString string
	namedInt    int64
)

func mustResult(t *testing.T, b *treeBuilder) map[string]any {
	t.Helper()

	tree, err := b.result()
	require.NoError(t, err)

	return tree
}

func TestTreeBuilder_OmitIfZeroVsAlways(t *testing.T) {
	tests := []struct {
		name       string
		value      any
		wantEmit   map[string]any // emit (omit-if-zero)
		wantAlways map[string]any // emitAlways
	}{
		{name: "zero bool", value: false, wantEmit: nil, wantAlways: map[string]any{"key": false}},
		{name: "zero int", value: 0, wantEmit: nil, wantAlways: map[string]any{"key": int64(0)}},
		{name: "empty string", value: "", wantEmit: nil, wantAlways: map[string]any{"key": ""}},
		{name: "nil slice", value: []string(nil), wantEmit: nil, wantAlways: map[string]any{"key": []any{}}},
		{name: "non-zero string", value: "v", wantEmit: map[string]any{"key": "v"}, wantAlways: map[string]any{"key": "v"}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var omit treeBuilder
			omit.emit("key", testCase.value)
			assert.Equal(t, testCase.wantEmit, mustResult(t, &omit), "emit (omit-if-zero)")

			var always treeBuilder
			always.emitAlways("key", testCase.value)
			assert.Equal(t, testCase.wantAlways, mustResult(t, &always), "emitAlways")
		})
	}
}

func TestScalarToJSON_ByKind(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  any
	}{
		{name: "string", value: "hello", want: "hello"},
		{name: "bool", value: true, want: true},
		{name: "int", value: 42, want: int64(42)},
		{name: "negative int", value: int64(-7), want: int64(-7)},
		{name: "uint", value: uint(255), want: uint64(255)},
		{name: "named string by underlying kind", value: namedString("x"), want: "x"},
		{name: "named int by underlying kind", value: namedInt(9), want: int64(9)},
		{name: "string slice in order", value: []string{"a", "bb"}, want: []any{"a", "bb"}},
		{name: "int slice", value: []int{1, 22}, want: []any{int64(1), int64(22)}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := scalarToJSON(reflect.ValueOf(testCase.value))
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

// TestScalarToJSON_RejectsUnsafeIntegers pins the one bounded special case the
// JCS substrate needs: an integer outside [-2^53, 2^53] cannot survive RFC 8785's
// ECMAScript number serialization, so it is rejected rather than silently coerced.
func TestScalarToJSON_RejectsUnsafeIntegers(t *testing.T) {
	_, err := scalarToJSON(reflect.ValueOf(int64(maxSafeInteger)))
	require.NoError(t, err, "2^53 is exactly representable and allowed")

	_, err = scalarToJSON(reflect.ValueOf(int64(-maxSafeInteger)))
	require.NoError(t, err, "-2^53 is exactly representable and allowed (the negative boundary)")

	_, err = scalarToJSON(reflect.ValueOf(uint64(maxSafeInteger)))
	require.NoError(t, err, "a uint64 at exactly 2^53 is allowed (the unsigned boundary)")

	_, err = scalarToJSON(reflect.ValueOf(int64(maxSafeInteger + 1)))
	require.Error(t, err, "2^53+1 cannot survive RFC 8785's number model")

	_, err = scalarToJSON(reflect.ValueOf(int64(-(maxSafeInteger + 1))))
	require.Error(t, err, "-(2^53+1) is rejected too")

	_, err = scalarToJSON(reflect.ValueOf(uint64(maxSafeInteger + 1)))
	require.Error(t, err, "a uint64 above 2^53 is rejected")
}

// TestScalarToJSON_RejectsByteSlice pins that a []byte is rejected rather than
// encoded as a JSON number array: the RFC lists []byte-style values as
// fail-generation, so the encoder refuses one (it must be a string instead).
func TestScalarToJSON_RejectsByteSlice(t *testing.T) {
	_, err := scalarToJSON(reflect.ValueOf([]byte("abc")))
	require.Error(t, err, "a byte slice ([]byte) is not encodable at v1")
}

func TestScalarToJSON_UnpinnedKindsFail(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "float64", value: float64(1.5)},
		{name: "float32", value: float32(1.5)},
		{name: "complex", value: complex128(1 + 2i)},
		{name: "uintptr", value: uintptr(8)},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := scalarToJSON(reflect.ValueOf(testCase.value))
			require.Error(t, err, "the default-fail branch must reject un-pinned kinds")
		})
	}
}

func TestTreeBuilder_EmitRejectsCompositesAndNil(t *testing.T) {
	type nested struct{ A string }

	var structBuf treeBuilder
	structBuf.emit("build", nested{A: "x"})
	_, err := structBuf.result()
	require.Error(t, err, "a struct must go through emitComposite, not emit")

	var mapBuf treeBuilder
	mapBuf.emit("defines", map[string]string{"a": "1"})
	_, err = mapBuf.result()
	require.Error(t, err, "a map must go through emitMap, not emit")

	var sliceBuf treeBuilder
	sliceBuf.emit("overlays", []nested{{A: "x"}})
	_, err = sliceBuf.result()
	require.Error(t, err, "a slice-of-struct must go through emitSlice, not emit")

	var nilBuf treeBuilder
	nilBuf.emit("key", nil)
	_, err = nilBuf.result()
	require.Error(t, err, "a nil value has no encoding")
}

func TestTreeBuilder_EmitMapMembershipAndOmit(t *testing.T) {
	var withEmptyValue treeBuilder
	withEmptyValue.emitMap("defines", map[string]string{"k": ""})
	assert.Equal(t, map[string]any{"defines": map[string]any{"k": ""}}, mustResult(t, &withEmptyValue),
		`{"k":""} is measured content`)

	var empty treeBuilder
	empty.emitMap("defines", map[string]string{})
	assert.Nil(t, mustResult(t, &empty), "empty map is omitted")

	var nilMap treeBuilder
	nilMap.emitMap("defines", map[string]string(nil))
	assert.Nil(t, mustResult(t, &nilMap), "nil map is omitted")
}

func TestTreeBuilder_EmitMapErrors(t *testing.T) {
	var nonString treeBuilder
	nonString.emitMap("byID", map[int]string{1: "a"})
	_, err := nonString.result()
	require.Error(t, err, "a non-string map key is rejected")

	var unpinned treeBuilder
	unpinned.emitMap("ratios", map[string]float64{"a": 1.5})
	_, err = unpinned.result()
	require.Error(t, err, "a non-scalar map value reaches the default-fail branch")
}

func TestTreeBuilder_CompositeAndSliceProjectedEmptiness(t *testing.T) {
	var omitted treeBuilder
	omitted.emitComposite("build", nil, nil)
	omitted.emitComposite("spec", map[string]any{}, nil)
	omitted.emitSlice("overlays", nil, nil)
	omitted.emitSlice("source-files", []any{}, nil)
	assert.Nil(t, mustResult(t, &omitted), "composites/slices that projected empty are omitted")

	var emitted treeBuilder
	emitted.emitComposite("build", map[string]any{"with": []any{"x"}}, nil)
	emitted.emitSlice("overlays", []any{map[string]any{"tag": "a"}}, nil)
	assert.Equal(t, map[string]any{
		"build":    map[string]any{"with": []any{"x"}},
		"overlays": []any{map[string]any{"tag": "a"}},
	}, mustResult(t, &emitted))
}

// TestTreeBuilder_CompositeSliceErrorThreading verifies that a non-nil
// sub-projector error passed to emitComposite/emitSlice is folded into the
// deferred error (wrapped with the key), and that the first error wins.
func TestTreeBuilder_CompositeSliceErrorThreading(t *testing.T) {
	sentinel := errors.New("sub-projector failed")

	var fromComposite treeBuilder
	fromComposite.emitComposite("spec", nil, sentinel)
	fromComposite.emit("after", "ignored")
	_, err := fromComposite.result()
	require.ErrorIs(t, err, sentinel, "emitComposite folds a sub-projector error into the deferred error")
	assert.Contains(t, err.Error(), "spec", "the threaded error is wrapped with the composite key")

	var fromSlice treeBuilder
	fromSlice.emitSlice("overlays", nil, sentinel)
	_, err = fromSlice.result()
	require.ErrorIs(t, err, sentinel, "emitSlice folds an element-projector error into the deferred error")
	assert.Contains(t, err.Error(), "overlays", "the threaded error is wrapped with the slice key")

	// A non-nil sub-projector error is reported even when the sub-map is non-empty.
	var nonEmptyErr treeBuilder
	nonEmptyErr.emitComposite("spec", map[string]any{"k": "v"}, sentinel)
	_, err = nonEmptyErr.result()
	require.ErrorIs(t, err, sentinel, "the error takes precedence over a would-be emission")

	// First error wins: a later emit failure does not overwrite the threaded one.
	var firstWins treeBuilder
	firstWins.emitComposite("spec", nil, sentinel)
	firstWins.emit("bad", nil)
	_, err = firstWins.result()
	require.ErrorIs(t, err, sentinel, "the first (threaded) error wins")
}
