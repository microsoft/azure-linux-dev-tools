// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
)

// WriteJSONFile marshals value as JSON and writes it to path.
//
// NOTE: Like [MkdirAll], this provides a single place to decide the correct
// permissions for the written file.
func WriteJSONFile(fs opctx.FS, path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON for %#q:\n%w", path, err)
	}

	if err := WriteFile(fs, path, data, fileperms.PublicFile); err != nil {
		return fmt.Errorf("failed to write %#q:\n%w", path, err)
	}

	return nil
}
