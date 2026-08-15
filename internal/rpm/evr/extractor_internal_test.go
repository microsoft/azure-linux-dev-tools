// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package evr

import (
	"encoding/json"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeExtractManifest(t *testing.T) {
	inputs := []Input{{
		Component: "nss",
		SpecPath:  "/work/staging/from/nss/nss.spec",
		BuildOptions: rpm.BuildOptions{
			With:      []string{"asan"},
			Without:   []string{"cockpit"},
			Defines:   map[string]string{"dist": ".azl4"},
			Undefines: []string{"_with_asan", "dist"},
		},
	}}

	manifest, err := makeExtractManifest("/work/staging", inputs)
	require.NoError(t, err)
	require.Len(t, manifest, 1)
	assert.Equal(t, "from/nss/nss.spec", manifest[0].SpecPath)
	assert.Equal(t, []string{"asan"}, manifest[0].With)
	assert.Equal(t, []string{"cockpit"}, manifest[0].Without)
	assert.Equal(t, map[string]string{"dist": ".azl4"}, manifest[0].Defines)
	assert.Equal(t, []string{"_with_asan", "dist"}, manifest[0].Undefines)
}

func TestNewExtractorUsesIsolatedMockBaseDir(t *testing.T) {
	extractor := NewExtractor(
		testctx.NewCtx(),
		"/mock/config",
		WithIsolatedMockBaseDir("/work/azldev-check-evr-mock"),
	)

	assert.Equal(t, "/work/azldev-check-evr-mock", extractor.runner.BaseDir())
}

func TestNewExtractorDoesNotEnableChrootNetwork(t *testing.T) {
	extractor := NewExtractor(testctx.NewCtx(), "/mock/config")

	assert.False(t, extractor.runner.HasNetworkEnabled())
}

func TestMakeExtractManifestRejectsSpecOutsideStaging(t *testing.T) {
	_, err := makeExtractManifest("/work/staging", []Input{{
		Component: "nss",
		SpecPath:  "/work/other/nss.spec",
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes staging directory")
}

func TestParseExtractResults(t *testing.T) {
	inputs := []Input{{Component: "nss"}, {Component: "broken"}, {Component: "missing"}}
	data := []byte(`[
  {"component":"nss","evr":{"epoch":"(none)","version":"3.123.1","release":"1.azl4"},"error":null},
  {"component":"broken","evr":null,"error":"rpmspec failed"}
]`)

	results, err := parseExtractResults(data, inputs)
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, EVR{Epoch: "0", Version: "3.123.1", Release: "1.azl4"}, *results[0].EVR)
	require.Error(t, results[1].Error)
	require.Error(t, results[2].Error)
}

func TestParseComparisonResults(t *testing.T) {
	comparisons := []Comparison{
		{
			Component: "nss",
			Previous:  EVR{Epoch: "0", Version: "1", Release: "1"},
			Current:   EVR{Epoch: "0", Version: "1", Release: "2"},
		},
		{Component: "broken", Previous: EVR{}, Current: EVR{}},
	}
	data := []byte(`[
  {"component":"nss","compare":-1,"error":null},
  {"component":"broken","compare":null,"error":"invalid EVR"}
]`)

	results, err := parseComparisonResults(data, comparisons)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, -1, results[0].Compare)
	require.Error(t, results[1].Error)
}

func TestComparisonManifestMarshalsEVRs(t *testing.T) {
	manifest, err := makeComparisonManifest([]Comparison{{
		Component: "nss",
		Previous:  EVR{Epoch: "0", Version: "3.123.1", Release: "1.azl4"},
		Current:   EVR{Epoch: "0", Version: "3.123.1", Release: "2.azl4"},
	}})
	require.NoError(t, err)

	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	assert.JSONEq(t, `[
	{
		"component": "nss",
		"previous": {"epoch": "0", "version": "3.123.1", "release": "1.azl4"},
		"current": {"epoch": "0", "version": "3.123.1", "release": "2.azl4"}
	}
]`, string(data))
}

func TestEVRString(t *testing.T) {
	assert.Equal(t, "1.2-3", (EVR{Epoch: "0", Version: "1.2", Release: "3"}).String())
	assert.Equal(t, "2:1.2-3", (EVR{Epoch: "2", Version: "1.2", Release: "3"}).String())
}
