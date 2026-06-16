// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseDocument() map[string]any {
	return map[string]any{
		"config":         map[string]any{"spec": map[string]any{"upstream-name": "x"}},
		"sourceIdentity": "abc123",
		"manualBump":     0,
		"releaseVer":     "4.0",
		"overlays":       map[string]any{"0": "ovl0"},
	}
}

func TestCanonicalDigest_Deterministic(t *testing.T) {
	digest1, err := canonicalDigest(baseDocument())
	require.NoError(t, err)

	digest2, err := canonicalDigest(baseDocument())
	require.NoError(t, err)

	assert.Equal(t, digest1, digest2, "identical documents must produce identical digests")
	assert.Contains(t, digest1, "sha256:", "digest carries the sha256: prefix")
}

func TestCanonicalDigest_InputsChangeDigest(t *testing.T) {
	base, err := canonicalDigest(baseDocument())
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "config", mutate: func(d map[string]any) {
			d["config"] = map[string]any{"spec": map[string]any{"upstream-name": "y"}}
		}},
		{name: "source identity", mutate: func(d map[string]any) { d["sourceIdentity"] = "def456" }},
		{name: "manual bump", mutate: func(d map[string]any) { d["manualBump"] = 1 }},
		{name: "release ver", mutate: func(d map[string]any) { d["releaseVer"] = "5.0" }},
		{name: "changed overlay", mutate: func(d map[string]any) {
			d["overlays"] = map[string]any{"0": "ovl0-changed"}
		}},
		{name: "added overlay", mutate: func(d map[string]any) {
			d["overlays"] = map[string]any{"0": "ovl0", "1": "ovl1"}
		}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			doc := baseDocument()
			testCase.mutate(doc)

			got, err := canonicalDigest(doc)
			require.NoError(t, err)
			assert.NotEqual(t, base, got)
		})
	}
}

// TestCanonicalDigest_KeyOrderIndependent confirms RFC 8785 sorts object keys, so
// Go map insertion order cannot affect the digest - the property that lets the
// document's keys provide domain separation without manual length-prefixing.
func TestCanonicalDigest_KeyOrderIndependent(t *testing.T) {
	a := map[string]any{"config": map[string]any{}, "manualBump": 0, "releaseVer": "4.0", "sourceIdentity": "id"}
	b := map[string]any{"sourceIdentity": "id", "releaseVer": "4.0", "config": map[string]any{}, "manualBump": 0}

	digestA, err := canonicalDigest(a)
	require.NoError(t, err)

	digestB, err := canonicalDigest(b)
	require.NoError(t, err)

	assert.Equal(t, digestA, digestB, "RFC 8785 key sorting makes the digest independent of map order")
}
