// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/microsoft/azure-linux-dev-tools/internal/fingerprint"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// canonicalSeed is the deterministic seed used to derive per-field values
// for the per-field table and as the canonical entry in the fuzz corpus.
const canonicalSeed = int64(0)

// baselineRowKey is the table-row key for the all-zero ComponentConfig
// baseline. Sorted as the first row by virtue of leading "<".
const baselineRowKey = "<baseline-all-zero>"

// TestComputeIdentity_PerFieldTable is the canonical regression guard for
// [fingerprint.ComputeIdentity] stability across schema changes.
//
// The test enumerates every fingerprinted leaf field reachable from
// [projectconfig.ComponentConfig] (recursing through structs, slices, maps,
// and pointers, skipping anything tagged `fingerprint:"-"`). For each leaf
// path P it builds a ComponentConfig in which:
//
//   - Containers along the path from root to P are allocated (slices get
//     one element; maps get one entry; pointers are non-nil).
//   - The leaf at P is set to a deterministic non-zero value derived from
//     (path, seed). Every other leaf is left at its zero value.
//
// It then computes the fingerprint and records `path: fingerprint` as a
// row in a table. The full table — plus a baseline row for an all-zero
// ComponentConfig — is asserted against a snapshot via go-snaps.
//
// Properties this guarantees:
//
//   - Adding a fingerprinted field appears as a NEW row. Reviewer sees
//     exactly which field changed and what its single-field hash is.
//   - Adding an excluded field (`fingerprint:"-"`) does NOT add a row;
//     the snapshot is unchanged.
//   - Removing or renaming a fingerprinted field removes / renames a row.
//   - Retyping a fingerprinted field changes that row's hash (different
//     value type produces a different hash) but leaves siblings alone.
//   - A change to ComputeIdentity that only affects the empty-collection
//     branch shifts the baseline row.
//   - A change that only affects the non-empty-collection branch (the
//     original bug class — fingerprint regression hidden because most
//     components had empty SourceFiles) shifts the rows for any path
//     inside that collection.
//
// Companion: TestAllFingerprintedFieldsHaveDecision in package
// projectconfig forces every field to carry an explicit include/exclude
// decision so no field ever defaults silently into the fingerprint.
//
// To accept legitimate changes, run with UPDATE_SNAPS=true.
func TestComputeIdentity_PerFieldTable(t *testing.T) {
	paths := collectFingerprintedLeafPaths(
		reflect.TypeFor[projectconfig.ComponentConfig](),
		"ComponentConfig",
	)

	rows := make([]string, 0, len(paths)+1)
	rows = append(rows, fmt.Sprintf("%s: %s", baselineRowKey, computeFingerprintWithOnly(t, "", canonicalSeed)))

	for _, p := range paths {
		fp := computeFingerprintWithOnly(t, p, canonicalSeed)
		rows = append(rows, fmt.Sprintf("%s: %s", p, fp))
	}

	// Snapshot rows are pre-sorted by row key; the baseline sorts first.
	sort.Strings(rows[1:])

	snaps.MatchSnapshot(t, strings.Join(rows, "\n"))
}

// FuzzComputeIdentity_Exhaustive runs property checks against fingerprints
// computed from exhaustively-populated components.
//
// Seed corpus mode (default `go test`):
//   - Runs the explicit corpus entries below. Cheap; runs on every CI build.
//
// Fuzz mode (`go test -fuzz=FuzzComputeIdentity_Exhaustive`):
//   - The fuzz engine explores random seed values. We verify the properties
//     below hold for every seed. Useful for catching non-determinism
//     (e.g., accidental dependence on map iteration order).
//
// Properties checked for every seed:
//
//   - Determinism: computing the fingerprint twice from the same populated
//     component yields the same value.
//   - Sensitivity: a fully populated component has a fingerprint different
//     from the all-zero component (sanity check that population is reaching
//     fingerprinted fields).
//   - Format: the fingerprint is a 64-character hex string (SHA256).
//   - Uniqueness: different seeds produce different fingerprints (a
//     collision within the fuzz run would be highly suspicious).
func FuzzComputeIdentity_Exhaustive(f *testing.F) {
	f.Add(canonicalSeed)
	f.Add(int64(1))
	f.Add(int64(-1))
	f.Add(int64(0x0123456789ABCDEF))

	zeroFP := computeFingerprintWithOnly(f, "", canonicalSeed)

	f.Fuzz(func(t *testing.T, seed int64) {
		fp1 := computeFingerprintExhaustive(t, seed)
		fp2 := computeFingerprintExhaustive(t, seed)

		assert.Equal(t, fp1, fp2,
			"determinism: same seed must produce same fingerprint (seed=%d)", seed)

		assert.NotEqual(t, zeroFP, fp1,
			"sensitivity: populated component must differ from zero component (seed=%d)", seed)

		assert.Len(t, fp1, 71,
			"format: fingerprint should be 'sha256:' + 64 hex chars (seed=%d)", seed)
		assert.True(t, strings.HasPrefix(fp1, "sha256:"),
			"format: fingerprint should have 'sha256:' prefix (seed=%d)", seed)

		fp3 := computeFingerprintExhaustive(t, seed+1)
		assert.NotEqual(t, fp1, fp3,
			"uniqueness: different seeds should produce different fingerprints (seed=%d)", seed)
	})
}

// computeFingerprintWithOnly builds a [projectconfig.ComponentConfig] in
// which only the leaf at targetPath is set (containers along the way are
// allocated; every other leaf is zero), then computes its fingerprint.
// An empty targetPath returns the fingerprint of an all-zero ComponentConfig.
func computeFingerprintWithOnly(tb testing.TB, targetPath string, seed int64) string {
	tb.Helper()

	var comp projectconfig.ComponentConfig

	if targetPath != "" {
		found := setLeafAtPath(
			tb, reflect.ValueOf(&comp).Elem(),
			"ComponentConfig", targetPath, seed,
		)
		require.Truef(tb, found, "target path %q not reachable from ComponentConfig", targetPath)
	}

	ctx := testctx.NewCtx()
	normalizeForCompute(tb, &comp, ctx.FS())

	identity, err := fingerprint.ComputeIdentity(
		ctx.FS(), comp, "release-ver-value",
		fingerprint.IdentityOptions{
			ManualBump:     7,
			SourceIdentity: "test-source-identity",
		})
	require.NoError(tb, err)

	return identity.Fingerprint
}

// computeFingerprintExhaustive populates every fingerprinted leaf and
// returns the fingerprint. Used by [FuzzComputeIdentity_Exhaustive].
func computeFingerprintExhaustive(tb testing.TB, seed int64) string {
	tb.Helper()

	var comp projectconfig.ComponentConfig
	populateExhaustively(tb, reflect.ValueOf(&comp).Elem(), "ComponentConfig", seed)

	ctx := testctx.NewCtx()
	normalizeForCompute(tb, &comp, ctx.FS())

	identity, err := fingerprint.ComputeIdentity(
		ctx.FS(), comp, "release-ver-value",
		fingerprint.IdentityOptions{
			ManualBump:     7,
			SourceIdentity: "test-source-identity",
		})
	require.NoError(tb, err)

	return identity.Fingerprint
}

// normalizeForCompute fills in fields that [fingerprint.ComputeIdentity]
// requires but that the populator skips because they are tagged
// `fingerprint:"-"` or because the target leaf did not happen to be them:
//
//   - Every [projectconfig.SourceFileReference] needs a non-empty Hash,
//     otherwise ComputeIdentity refuses to produce a fingerprint.
//   - Every [projectconfig.ComponentOverlay] whose effective source name
//     is non-empty needs a backing file on disk so SourceContentIdentity
//     can hash its content. Source itself is `fingerprint:"-"`.
//
// Required fields receive a fixed placeholder value (not derived from
// seed/path) so the per-field table only varies in the column under test.
func normalizeForCompute(tb testing.TB, comp *projectconfig.ComponentConfig, fs opctx.FS) {
	tb.Helper()

	for i := range comp.SourceFiles {
		if comp.SourceFiles[i].Hash == "" {
			comp.SourceFiles[i].Hash = "placeholder-hash"
		}
	}

	for i := range comp.Overlays {
		src := fmt.Sprintf("/exhaustive/overlay-%d.src", i)
		comp.Overlays[i].Source = src
		require.NoError(tb, fileutils.WriteFile(
			fs, src,
			[]byte(fmt.Sprintf("overlay-content-%d", i)),
			fileperms.PublicFile,
		))
	}
}

// collectFingerprintedLeafPaths returns every leaf path reachable from typ
// that would be populated by [populateExhaustively]. Leaves are scalar
// (string / bool / numeric) fields; container types (struct / slice / map
// / pointer) are recursed into. Fields tagged `fingerprint:"-"` are
// skipped. Map types yield `<key>` and `<val>` synthetic path segments.
func collectFingerprintedLeafPaths(typ reflect.Type, path string) []string {
	//nolint:exhaustive // reflect.Kind has many kinds we never expect in fingerprinted configs.
	switch typ.Kind() {
	case reflect.Struct:
		var out []string

		for fldIdx := range typ.NumField() {
			fld := typ.Field(fldIdx)
			if !fld.IsExported() {
				continue
			}

			if fld.Tag.Get("fingerprint") == "-" {
				continue
			}

			out = append(out, collectFingerprintedLeafPaths(fld.Type, path+"."+fld.Name)...)
		}

		return out

	case reflect.Pointer:
		return collectFingerprintedLeafPaths(typ.Elem(), path)

	case reflect.Slice:
		return collectFingerprintedLeafPaths(typ.Elem(), path+"[0]")

	case reflect.Map:
		var out []string

		out = append(out, collectFingerprintedLeafPaths(typ.Key(), path+".<key>")...)
		out = append(out, collectFingerprintedLeafPaths(typ.Elem(), path+".<val>")...)

		return out

	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return []string{path}

	default:
		panic(fmt.Sprintf(
			"collectFingerprintedLeafPaths: unhandled kind %v at %q", typ.Kind(), path))
	}
}

// setLeafAtPath descends val from currentPath toward targetPath, allocating
// containers along the way, and sets the leaf at targetPath to a seeded
// non-zero value. Other leaves are left zero. Returns true if the target
// was found in this subtree.
//
// Container handling:
//   - Struct: recurse into the single field whose path is a prefix of target.
//   - Pointer: allocate and recurse.
//   - Slice: allocate one-element slice and recurse into element [0].
//   - Map: allocate one-entry map; populate key, value, or both depending
//     on which contains target.
func setLeafAtPath(tb testing.TB, val reflect.Value, currentPath, targetPath string, seed int64) bool {
	tb.Helper()

	if !val.CanSet() {
		return false
	}

	if currentPath == targetPath {
		setLeafValue(tb, val, currentPath, seed)

		return true
	}

	// Target must be a descendant of currentPath.
	if !strings.HasPrefix(targetPath, currentPath+".") &&
		!strings.HasPrefix(targetPath, currentPath+"[") {
		return false
	}

	//nolint:exhaustive // Only container kinds reachable here; leaves are handled above.
	switch val.Kind() {
	case reflect.Struct:
		for fldIdx := range val.NumField() {
			fld := val.Type().Field(fldIdx)
			if !fld.IsExported() || fld.Tag.Get("fingerprint") == "-" {
				continue
			}

			if setLeafAtPath(tb, val.Field(fldIdx), currentPath+"."+fld.Name, targetPath, seed) {
				return true
			}
		}

		return false

	case reflect.Pointer:
		val.Set(reflect.New(val.Type().Elem()))

		return setLeafAtPath(tb, val.Elem(), currentPath, targetPath, seed)

	case reflect.Slice:
		slc := reflect.MakeSlice(val.Type(), 1, 1)
		if setLeafAtPath(tb, slc.Index(0), currentPath+"[0]", targetPath, seed) {
			val.Set(slc)

			return true
		}

		return false

	case reflect.Map:
		// Allocate placeholders for key and value; only the side that contains
		// targetPath actually gets non-zero. Map keys are not addressable, so
		// we build into a settable copy and insert.
		keyVal := reflect.New(val.Type().Key()).Elem()
		valVal := reflect.New(val.Type().Elem()).Elem()

		keyFound := setLeafAtPath(tb, keyVal, currentPath+".<key>", targetPath, seed)
		valFound := setLeafAtPath(tb, valVal, currentPath+".<val>", targetPath, seed)

		if !keyFound && !valFound {
			return false
		}

		mapVal := reflect.MakeMap(val.Type())
		mapVal.SetMapIndex(keyVal, valVal)
		val.Set(mapVal)

		return true

	default:
		tb.Fatalf("setLeafAtPath: unexpected non-container kind %v at %q "+
			"(target=%q)", val.Kind(), currentPath, targetPath)

		return false
	}
}

// setLeafValue assigns a deterministic seeded non-zero value to a scalar
// reflect.Value. The kind switch mirrors the leaf kinds enumerated by
// [collectFingerprintedLeafPaths].
func setLeafValue(tb testing.TB, val reflect.Value, path string, seed int64) {
	tb.Helper()

	//nolint:exhaustive // Container kinds are handled by callers; only leaves reach here.
	switch val.Kind() {
	case reflect.String:
		val.SetString(seededString(path, seed))
	case reflect.Bool:
		val.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		val.SetInt(seededInt(path, seed))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// #nosec G115 -- value is bounded to 24 bits by seededInt, never negative.
		val.SetUint(uint64(seededInt(path, seed)))
	case reflect.Float32, reflect.Float64:
		val.SetFloat(float64(seededInt(path, seed)))
	default:
		tb.Fatalf("setLeafValue: unhandled kind %v at %q", val.Kind(), path)
	}
}

// populateExhaustively recursively assigns a deterministic non-zero value
// to every fingerprinted, exported, settable field reachable from val.
// Used by [FuzzComputeIdentity_Exhaustive] to stress every fingerprinted
// field simultaneously.
func populateExhaustively(tb testing.TB, val reflect.Value, path string, seed int64) {
	tb.Helper()

	if !val.CanSet() {
		return
	}

	//nolint:exhaustive // Default case delegates remaining kinds to setLeafValue.
	switch val.Kind() {
	case reflect.Struct:
		for fldIdx := range val.NumField() {
			fld := val.Type().Field(fldIdx)
			if !fld.IsExported() || fld.Tag.Get("fingerprint") == "-" {
				continue
			}

			populateExhaustively(tb, val.Field(fldIdx), path+"."+fld.Name, seed)
		}

	case reflect.Pointer:
		val.Set(reflect.New(val.Type().Elem()))
		populateExhaustively(tb, val.Elem(), path, seed)

	case reflect.Slice:
		slc := reflect.MakeSlice(val.Type(), 1, 1)
		populateExhaustively(tb, slc.Index(0), path+"[0]", seed)
		val.Set(slc)

	case reflect.Map:
		mapVal := reflect.MakeMap(val.Type())
		keyVal := reflect.New(val.Type().Key()).Elem()
		innerVal := reflect.New(val.Type().Elem()).Elem()

		populateExhaustively(tb, keyVal, path+".<key>", seed)
		populateExhaustively(tb, innerVal, path+".<val>", seed)
		mapVal.SetMapIndex(keyVal, innerVal)
		val.Set(mapVal)

	default:
		setLeafValue(tb, val, path, seed)
	}
}

// seededHash returns a SHA256 digest of (path | seed). Used to derive
// per-field, per-seed values without bleeding between fields.
func seededHash(path string, seed int64) [sha256.Size]byte {
	var seedBytes [8]byte
	// #nosec G115 -- bit-pattern reinterpretation; signed/unsigned irrelevant for hash input.
	binary.LittleEndian.PutUint64(seedBytes[:], uint64(seed))

	hash := sha256.New()
	hash.Write([]byte(path))
	hash.Write(seedBytes[:])

	var out [sha256.Size]byte
	copy(out[:], hash.Sum(nil))

	return out
}

// seededString returns a short deterministic non-empty string for (path, seed).
func seededString(path string, seed int64) string {
	sum := seededHash(path, seed)

	return "v:" + hex.EncodeToString(sum[:8])
}

// seededInt returns a deterministic non-zero positive int64 for (path, seed).
// Bounded to the low 24 bits so it fits inside any integer kind without
// surprising sign-extension when SetInt truncates.
func seededInt(path string, seed int64) int64 {
	sum := seededHash(path, seed)
	// #nosec G115 -- bounded to 0x1000000, never exceeds int64.
	return int64(uint64(sum[0])|uint64(sum[1])<<8|uint64(sum[2])<<16) + 1
}
