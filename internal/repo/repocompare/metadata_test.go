// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repocompare_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/repo/repocompare"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mapFetcher map[string][]byte

func (f mapFetcher) Fetch(_ context.Context, rawURL string, _ bool) ([]byte, error) {
	return f[rawURL], nil
}

func TestHTTPFetcherCanDisableSSLVerification(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("metadata"))
	}))
	defer server.Close()

	fetcher := &repocompare.HTTPFetcher{Attempts: 1}
	_, err := fetcher.Fetch(t.Context(), server.URL, false)
	require.Error(t, err)

	data, err := fetcher.Fetch(t.Context(), server.URL, true)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestLoadRepositories(t *testing.T) {
	t.Parallel()

	const (
		baseURL = "https://example.com/repo"
		href    = "repodata/primary.xml.gz"
	)

	primary := []byte(`<?xml version="1.0"?>
<metadata>
  <package type="rpm">
    <name>bash</name><arch>x86_64</arch>
    <version epoch="" ver="5.3" rel="1.azl4"/>
  </package>
</metadata>`)

	var compressed bytes.Buffer

	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write(primary)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	repomd := []byte(`<?xml version="1.0"?>
<repomd><data type="primary"><location href="repodata/primary.xml.gz"/></data></repomd>`)

	packages, err := repocompare.LoadRepositories(
		t.Context(),
		mapFetcher{
			baseURL + "/repodata/repomd.xml": repomd,
			baseURL + "/" + href:             compressed.Bytes(),
		},
		[]repocompare.Repository{{
			ID:   "test-base-x86_64",
			Kind: projectconfig.SubrepoKindBinary,
			URL:  baseURL,
		}},
	)
	require.NoError(t, err)
	require.Len(t, packages, 1)

	assert.Equal(t, "0", packages[0].Epoch)
	assert.Equal(t, "bash-5.3-1.azl4.x86_64", packages[0].NEVRA())
	assert.Equal(t, projectconfig.SubrepoKindBinary, packages[0].Kind)
}

func TestLoadRepositoriesRejectsMissingPrimaryMetadata(t *testing.T) {
	t.Parallel()

	const baseURL = "https://example.com/repo"

	_, err := repocompare.LoadRepositories(
		t.Context(),
		mapFetcher{baseURL + "/repodata/repomd.xml": []byte(`<repomd/>`)},
		[]repocompare.Repository{{ID: "test", URL: baseURL}},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test")
	assert.Contains(t, err.Error(), "no primary metadata")
}
