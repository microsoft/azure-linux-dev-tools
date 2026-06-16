// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVersionSet_Exclude(t *testing.T) {
	set, err := parseVersionSet("-", 1)
	require.NoError(t, err)
	assert.True(t, set.excluded)

	measured, _ := set.measuredAt(1)
	assert.False(t, measured, "an excluded field is never measured")
}

func TestParseVersionSet_NonContiguousMembership(t *testing.T) {
	// v1..v1,v3..* : measured at v1, dropped at v2, brought back from v3.
	set, err := parseVersionSet("v1..v1,v3..*", 3)
	require.NoError(t, err)

	cases := []struct {
		version  int
		measured bool
	}{
		{1, true},
		{2, false},
		{3, true},
		{4, true},
	}

	for _, want := range cases {
		measured, always := set.measuredAt(want.version)
		assert.Equal(t, want.measured, measured, "measuredAt(v%d)", want.version)
		assert.False(t, always, "no range in this set is always-emit")
	}
}

func TestParseVersionSet_AlwaysEmit(t *testing.T) {
	set, err := parseVersionSet("!v1..*", 1)
	require.NoError(t, err)

	measured, always := set.measuredAt(1)
	assert.True(t, measured)
	assert.True(t, always, "! reports always-emit")
}

func TestParseVersionSet_TemporalAlwaysToggle(t *testing.T) {
	// v1..v4,!v5..* : omit-if-zero through v4, always-emit from v5.
	set, err := parseVersionSet("v1..v4,!v5..*", 5)
	require.NoError(t, err)

	_, alwaysV4 := set.measuredAt(4)
	assert.False(t, alwaysV4, "v4 is omit-if-zero")

	measuredV5, alwaysV5 := set.measuredAt(5)
	assert.True(t, measuredV5)
	assert.True(t, alwaysV5, "v5 onward is always-emit")
}

func TestParseVersionSet_SingleVersionShorthand(t *testing.T) {
	set, err := parseVersionSet("v1", 1)
	require.NoError(t, err)

	measuredV1, _ := set.measuredAt(1)
	assert.True(t, measuredV1)

	measuredV2, _ := set.measuredAt(2)
	assert.False(t, measuredV2, "vN shorthand is the single-version range vN..vN")
}

func TestParseVersionSet_Rejects(t *testing.T) {
	tests := []struct {
		name           string
		tag            string
		currentVersion int
	}{
		{"empty tag", "", 1},
		{"no v prefix", "1..*", 1},
		{"non-numeric version", "vx", 1},
		{"version zero", "v0", 1},
		{"dangling range", "v1..", 1},
		{"inverted range", "v3..v1", 3},
		{"non-numeric high", "v1..vx", 3},
		{"overlapping closed ranges", "v1..v3,v2..*", 5},
		{"overlapping open ranges", "v1..*,v2..*", 5},
		{"touching ranges share an endpoint", "v1..v3,v3..v5", 5},
		{"future-referencing low", "v3..*", 1},
		{"future-referencing high", "v1..v5", 1},
		{"duplicate key override", "key=foo,key=bar", 1},
		{"key override not first", "v1..*,key=foo", 1},
		{"empty key override", "key=,v1..*", 1},
		{"key override without ranges", "key=foo", 1},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := parseVersionSet(testCase.tag, testCase.currentVersion)
			require.Error(t, err)
		})
	}
}

func TestVersionSet_ResolveEmitKey(t *testing.T) {
	withOverride, err := parseVersionSet("key=upstream-name,v1..*", 1)
	require.NoError(t, err)

	key, err := withOverride.resolveEmitKey("upstream")
	require.NoError(t, err)
	assert.Equal(t, "upstream-name", key, "key= override wins over the toml key")

	noOverride, err := parseVersionSet("v1..*", 1)
	require.NoError(t, err)

	key, err = noOverride.resolveEmitKey("upstream")
	require.NoError(t, err)
	assert.Equal(t, "upstream", key, "the toml key is the default emit-key")

	_, err = noOverride.resolveEmitKey("")
	require.Error(t, err, "no toml key and no override is an error")

	_, err = noOverride.resolveEmitKey("-")
	require.Error(t, err, "a '-' toml key is not usable")
}
