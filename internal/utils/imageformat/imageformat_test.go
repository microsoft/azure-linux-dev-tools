// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package imageformat_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/imageformat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllImageFormats(t *testing.T) {
	formats := imageformat.All()
	require.NotEmpty(t, formats)
	assert.Contains(t, formats, "raw")
	assert.Contains(t, formats, "qcow2")
	assert.Contains(t, formats, "oci")
}

func TestInferImageFormat(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{name: "raw", path: "/path/to/image.raw", expected: "raw"},
		{name: "qcow2", path: "/path/to/image.qcow2", expected: "qcow2"},
		{name: "vhd", path: "/path/to/image.vhd", expected: "vhd"},
		{name: "vhdfixed", path: "/path/to/image.vhdfixed", expected: "vhd"},
		{name: "vhdx", path: "/path/to/image.vhdx", expected: "vhdx"},
		{name: "oci.tar.xz", path: "/path/to/image.oci.tar.xz", expected: "oci"},
		{name: "oci.tar.gz", path: "/path/to/image.oci.tar.gz", expected: "oci"},
		{name: "oci.tar", path: "/path/to/image.oci.tar", expected: "oci"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			format, err := imageformat.Infer(test.path)
			require.NoError(t, err)
			assert.Equal(t, test.expected, format)
		})
	}
}

func TestInferImageFormat_NoExtension(t *testing.T) {
	_, err := imageformat.Infer("/path/to/imagefile")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no file extension")
}

func TestInferImageFormat_UnsupportedExtension(t *testing.T) {
	_, err := imageformat.Infer("/path/to/image.iso")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported image format")
	assert.Contains(t, err.Error(), "iso")
}
