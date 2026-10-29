// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package imageformat

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/samber/lo"
)

const (
	Raw   = "raw"
	Qcow2 = "qcow2"
	Vhd   = "vhd"
	Vhdx  = "vhdx"
	OCI   = "oci"
)

// All returns all supported image formats in priority order.
func All() []string {
	return []string{
		string(Raw), string(Qcow2),
		string(Vhdx), string(Vhd),
		string(OCI),
	}
}

// Infer determines the image format from the file extension.
// Returns an error if the extension does not match a supported format.
func Infer(imagePath string) (string, error) {
	lower := strings.ToLower(imagePath)

	// Check multi-part extensions first (e.g., ".oci.tar.xz").
	for _, ext := range []string{".oci.tar.xz", ".oci.tar.gz", ".oci.tar"} {
		if strings.HasSuffix(lower, ext) {
			return string(OCI), nil
		}
	}

	ext := strings.ToLower(filepath.Ext(imagePath))
	if ext == "" {
		return "", fmt.Errorf(
			"cannot infer image format from %#q: no file extension",
			imagePath,
		)
	}

	// Strip the leading dot (e.g., ".vhdfixed" -> "vhdfixed").
	format := ext[1:]
	if format == "vhdfixed" {
		format = string(Vhd)
	}

	// Validate the inferred format is supported.
	if !lo.Contains(All(), format) {
		return "", fmt.Errorf(
			"unsupported image format %#q inferred from %#q; supported formats: %v",
			format, imagePath, All(),
		)
	}

	return format, nil
}
