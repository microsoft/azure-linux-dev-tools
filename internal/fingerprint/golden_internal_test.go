// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// updateGolden, when set, appends newly-named golden vectors to the frozen table.
// It MUST NOT change an existing entry's digest - see goldenPath's header comment.
// Bootstrap a missing table with: go test ./internal/fingerprint -run TestGoldenVectors -update-golden
//
//nolint:gochecknoglobals // a test flag must be a package-level var to register at init.
var updateGolden = flag.Bool("update-golden", false, "append new golden fingerprint vectors")

// goldenPath is the golden-vector file for the current content version. Each
// version pins its own file (golden_v1.json, golden_v2.json, ...); a new version
// adds a file rather than rewriting an existing one.
//
//nolint:gochecknoglobals // derived from currentContentVersion; cannot be const.
var goldenPath = fmt.Sprintf("testdata/golden_v%d.json", currentContentVersion)

// goldenConfigs is the freeze corpus for the current content version: one cutover
// config (every measured field set) plus named edge configs the cutover case
// cannot express at once.
//
// # Managing golden vectors (read before editing this map or testdata/golden_v<N>.json)
//
// A golden vector is a frozen (config -> digest) pair that pins a content
// version's projection digest irreversibly. The freeze is append-only and these
// rules are load-bearing because the encoding becomes a one-way door the moment a
// version ships (see the lazy-schema-migration RFC):
//
//   - One file per content version: testdata/golden_v1.json pins v1, a future
//     testdata/golden_v2.json pins v2, and so on. A new version ADDS its own file
//     and never rewrites an existing one; this harness reads the file for
//     currentContentVersion. (The future projection generator follows the same
//     rule - it emits golden_v<N>.json for the version it adds and leaves older
//     files untouched.)
//   - NEVER edit an existing entry's digest in testdata/golden_v<N>.json. The
//     -update-golden flag only APPENDS; a moved existing digest is a deliberate
//     FATAL. If an existing digest moves, your change was NOT output-preserving for
//     the fleet - that is a bug to fix, not a value to -update away.
//   - cutoverFieldSet (the "maximal" vector) is FROZEN: it is a corpus config, so
//     changing what it returns moves its pinned digest. Do not add fields to it.
//   - emissionProbeConfig GROWS instead: it sets every measured field so the
//     emission probe can assert each measured key is emitted. The two are split
//     precisely because one config cannot be both frozen and growing.
//
// Adding an omit-if-zero field "foo" (fingerprint:"v<N>..*", no version bump):
//  1. add the field + tag and its projectV<N> emit line;
//  2. set foo non-zero in emissionProbeConfig (the probe fails until you do - that
//     failure is the "you forgot to emit the new key" guard working);
//  3. append a new named vector that sets foo in isolation, then run:
//     go test ./internal/fingerprint -run TestGoldenVectors -update-golden
//  4. confirm the run APPENDS ONLY - every existing digest must be byte-identical.
//     Any movement of an existing entry is the bug, not the fix.
//
// NEVER retroactively measure a new field at a superseded version (e.g. editing a
// frozen projectV<N> to read foo once v<N+1> exists). That changes the bytes that
// version emitted in production, so every stored lock at that version becomes
// unreproducible on replay (Problem 6) -> mass false-Stale. Omit-if-zero means no
// pre-existing lock ever set foo, so each replays byte-identically without it; a
// lock that later needs foo measured gets it by FORWARD migration, never by editing
// the old version.
//
// Naming convention (semantic, not chronological - the date is already in git, and
// a name must say what broke when a vector moves):
//   - "maximal" / "minimal": the frozen baselines, never changed.
//   - edge cases: name the property exercised ("defines-empty-value",
//     "single-overlay", ...).
//   - an additive field: "<toml-key>-set", one isolated vector per field.
func goldenConfigs() map[string]projectconfig.ComponentConfig {
	return map[string]projectconfig.ComponentConfig{
		"maximal":             cutoverFieldSet(),
		"minimal":             {},
		"defines-empty-value": {Build: projectconfig.ComponentBuildConfig{Defines: map[string]string{"k": ""}}},
		"single-overlay": {Overlays: []projectconfig.ComponentOverlay{{
			Type: projectconfig.ComponentOverlayAddPatch, Filename: "p.patch",
		}}},
	}
}

// TestGoldenVectors pins the v1 projection encoding irreversibly. The expected
// digests are append-only: a commit that changes an existing entry fails (a
// frozen v1 vector cannot move - a new encoding is a new version, not an edit).
func TestGoldenVectors(t *testing.T) {
	computed := make(map[string]string)
	for name, cfg := range goldenConfigs() {
		computed[name] = projectionDigest(t, cfg)
	}

	// S2: every vector that sets a measured field must differ from the empty
	// projection. A vector that collapses to `minimal` means a measured emit was
	// dropped or mis-wired (e.g. a duplicate emit-key the global probe misses, or a
	// '!' field that used emit instead of emitAlways and so dropped its zero value).
	// A deliberately projected-empty vector (none today) would opt out here.
	expectedEmptyProjection := map[string]bool{"minimal": true}
	for name, digest := range computed {
		if expectedEmptyProjection[name] {
			continue
		}

		assert.NotEqualf(t, computed["minimal"], digest,
			"golden vector %q collapsed to the empty projection - a measured emit was dropped or mis-wired", name)
	}

	frozen := loadGolden(t)

	if *updateGolden {
		for name, digest := range computed {
			if old, ok := frozen[name]; ok && old != digest {
				t.Fatalf("append-only violation: vector %q would change\n  from %s\n  to   %s\n"+
					"a frozen v1 vector cannot move; introduce a new version instead", name, old, digest)
			}

			frozen[name] = digest
		}

		writeGolden(t, frozen)

		return
	}

	for name, digest := range computed {
		expected, ok := frozen[name]
		require.Truef(t, ok, "golden vector %q is missing; bootstrap with -update-golden", name)
		assert.Equalf(t, expected, digest, "v1 projection for %q changed - this is a frozen byte encoding", name)
	}
}

func projectionDigest(t *testing.T, cfg projectconfig.ComponentConfig) string {
	t.Helper()

	digest := sha256.Sum256(mustProject(t, cfg))

	return "sha256:" + hex.EncodeToString(digest[:])
}

func loadGolden(t *testing.T) map[string]string {
	t.Helper()

	data, err := os.ReadFile(goldenPath)
	if os.IsNotExist(err) {
		return make(map[string]string)
	}

	require.NoError(t, err)

	golden := make(map[string]string)
	require.NoError(t, json.Unmarshal(data, &golden))

	return golden
}

func writeGolden(t *testing.T, golden map[string]string) {
	t.Helper()

	golden["_README"] = fmt.Sprintf("Frozen v%d projection digests. APPEND-ONLY: never edit an "+
		"existing digest - a new encoding is a new content version, not an edit here. "+
		"Management rules and the additive-field workflow live in goldenConfigs's doc "+
		"comment in golden_internal_test.go.", currentContentVersion)

	// json.Marshal emits map keys sorted, giving a stable diff.
	data, err := json.MarshalIndent(golden, "", "  ")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(goldenPath, append(data, '\n'), 0o644)) //nolint:gosec // golden fixture, not a secret.
}
