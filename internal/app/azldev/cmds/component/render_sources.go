// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

func removeVendorRenderPayloads(fs opctx.FS, directory string) error {
	path := filepath.Join(directory, fedorasource.SourcesFileName)

	data, err := fileutils.ReadFile(fs, path)
	if err != nil {
		return fmt.Errorf("reading generated source manifest:\n%w", err)
	}

	lines, err := fedorasource.ReadSourcesFile(string(data))
	if err != nil {
		return fmt.Errorf("parsing generated source manifest:\n%w", err)
	}

	for _, line := range lines {
		if line.Entry == nil {
			continue
		}

		if err := fileutils.ValidateFilename(line.Entry.Filename); err != nil {
			return fmt.Errorf("validating payload filename:\n%w", err)
		}

		file := filepath.Join(directory, line.Entry.Filename)

		exists, err := fileutils.Exists(fs, file)
		if err != nil {
			return fmt.Errorf("checking rendered payload:\n%w", err)
		}

		if exists {
			if err := fs.Remove(file); err != nil {
				return fmt.Errorf("excluding generated source payload %#q:\n%w", file, err)
			}
		}
	}

	return nil
}
