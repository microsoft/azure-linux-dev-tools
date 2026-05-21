// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Testing unexported parseSpecQueryBatchJSON.
package sources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSpecQueryBatchJSON_Success(t *testing.T) {
	t.Parallel()

	raw := []byte(`[{
		"name": "curl",
		"srpmOut": "name=curl\nepoch=(none)\nversion=8.5.0\nrelease=1.azl3\n",
		"binOut": "subpkg=curl\nsubpkg=libcurl\nsubpkg=curl-devel\n",
		"error": null
	}]`)

	inputs := []SpecQueryInput{{Name: "curl", SpecRelPath: "c/curl/curl.spec"}}

	results, err := parseSpecQueryBatchJSON(raw, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NoError(t, results[0].Error)
	require.NotNil(t, results[0].Info)

	assert.Equal(t, "curl", results[0].Info.Name)
	assert.Equal(t, "8.5.0", results[0].Info.Version.Version())
	assert.Equal(t, "1.azl3", results[0].Info.Version.Release())
	assert.Equal(t, []string{"curl", "libcurl", "curl-devel"}, results[0].Info.Subpackages)
}

func TestParseSpecQueryBatchJSON_PerComponentError(t *testing.T) {
	t.Parallel()

	raw := []byte(`[
		{"name":"broken","srpmOut":"","binOut":"","error":"rpmspec --srpm failed: bad spec"}
	]`)

	inputs := []SpecQueryInput{{Name: "broken", SpecRelPath: "b/broken/broken.spec"}}

	results, err := parseSpecQueryBatchJSON(raw, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Error(t, results[0].Error)
	assert.Contains(t, results[0].Error.Error(), "rpmspec --srpm failed")
	assert.Nil(t, results[0].Info)
}

func TestParseSpecQueryBatchJSON_MissingComponent(t *testing.T) {
	t.Parallel()

	raw := []byte(`[]`)
	inputs := []SpecQueryInput{{Name: "ghost", SpecRelPath: "g/ghost/ghost.spec"}}

	results, err := parseSpecQueryBatchJSON(raw, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Error(t, results[0].Error)
	assert.Contains(t, results[0].Error.Error(), "no result returned")
}

func TestParseSpecQueryBatchJSON_SrpmParseFailure(t *testing.T) {
	t.Parallel()

	// srpmOut is missing required fields, so the per-component parser fails.
	raw := []byte(`[{
		"name": "weird",
		"srpmOut": "name=weird\n",
		"binOut": "subpkg=weird\n",
		"error": null
	}]`)

	inputs := []SpecQueryInput{{Name: "weird", SpecRelPath: "w/weird/weird.spec"}}

	results, err := parseSpecQueryBatchJSON(raw, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Error(t, results[0].Error)
	assert.Contains(t, results[0].Error.Error(), "parsing rpmspec --srpm output")
	assert.Nil(t, results[0].Info)
}

func TestParseSpecQueryBatchJSON_MultipleComponents(t *testing.T) {
	t.Parallel()

	raw := []byte(`[
		{"name":"good","srpmOut":"name=good\nepoch=0\nversion=1.0\nrelease=1\n","binOut":"subpkg=good\n","error":null},
		{"name":"bad","srpmOut":"","binOut":"","error":"rpmspec failed: boom"}
	]`)

	inputs := []SpecQueryInput{
		{Name: "good", SpecRelPath: "g/good/good.spec"},
		{Name: "bad", SpecRelPath: "b/bad/bad.spec"},
	}

	results, err := parseSpecQueryBatchJSON(raw, inputs)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.NoError(t, results[0].Error)
	require.NotNil(t, results[0].Info)
	assert.Equal(t, []string{"good"}, results[0].Info.Subpackages)
	require.Error(t, results[1].Error)
	assert.Contains(t, results[1].Error.Error(), "boom")
}

func TestParseSpecQueryBatchJSON_InvalidJSON(t *testing.T) {
	t.Parallel()

	inputs := []SpecQueryInput{{Name: "any", SpecRelPath: "a/any/any.spec"}}

	_, err := parseSpecQueryBatchJSON([]byte("not json{{{"), inputs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing spec query batch results JSON")
}

func TestParseSpecQueryBatchJSON_ExcludedFromArch(t *testing.T) {
	t.Parallel()

	raw := []byte(`[{
		"name": "shim",
		"srpmOut": "",
		"binOut": "",
		"error": null,
		"excludedFromArch": true
	}]`)

	inputs := []SpecQueryInput{{Name: "shim", SpecRelPath: "s/shim/shim.spec"}}

	results, err := parseSpecQueryBatchJSON(raw, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NoError(t, results[0].Error)
	assert.True(t, results[0].ExcludedFromArch)
	assert.Nil(t, results[0].Info)
}

func TestValidateSpecQueryInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		inputs  []SpecQueryInput
		wantErr bool
		errMsg  string
	}{
		{
			name:   "valid",
			inputs: []SpecQueryInput{{Name: "curl", SpecRelPath: "c/curl/curl.spec"}},
		},
		{
			name:    "empty name",
			inputs:  []SpecQueryInput{{Name: "", SpecRelPath: "c/curl/curl.spec"}},
			wantErr: true, errMsg: "invalid component name",
		},
		{
			name:    "slash in name",
			inputs:  []SpecQueryInput{{Name: "c/curl", SpecRelPath: "c/curl/curl.spec"}},
			wantErr: true, errMsg: "invalid component name",
		},
		{
			name:    "empty rel path",
			inputs:  []SpecQueryInput{{Name: "curl", SpecRelPath: ""}},
			wantErr: true, errMsg: "spec relative path cannot be empty",
		},
		{
			name:    "absolute rel path",
			inputs:  []SpecQueryInput{{Name: "curl", SpecRelPath: "/c/curl/curl.spec"}},
			wantErr: true, errMsg: "must be relative",
		},
		{
			name:    "traversal in rel path",
			inputs:  []SpecQueryInput{{Name: "curl", SpecRelPath: "c/curl/../../etc/passwd"}},
			wantErr: true, errMsg: "must be in canonical form",
		},
		{
			name:    "canonical traversal in rel path",
			inputs:  []SpecQueryInput{{Name: "curl", SpecRelPath: "../etc/passwd"}},
			wantErr: true, errMsg: "must not contain path traversal",
		},
		{
			name:    "non-canonical rel path",
			inputs:  []SpecQueryInput{{Name: "curl", SpecRelPath: "c//curl/curl.spec"}},
			wantErr: true, errMsg: "must be in canonical form",
		},
		{
			name: "duplicate name",
			inputs: []SpecQueryInput{
				{Name: "curl", SpecRelPath: "c/curl/curl.spec"},
				{Name: "curl", SpecRelPath: "c/curl/curl.spec"},
			},
			wantErr: true, errMsg: "duplicate component name",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := validateSpecQueryInputs(testCase.inputs)
			if testCase.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
