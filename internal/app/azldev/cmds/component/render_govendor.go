// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// Vendoring needs real payloads during preparation, but rendered dist-git output
// must contain their manifest rather than copying source archives into the repository.
func removeGoVendorRenderPayloads(fs opctx.FS, directory string, enabled bool) error {
	if !enabled {
		return nil
	}

	contents, err := fileutils.ReadFile(fs, filepath.Join(directory, fedorasource.SourcesFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading render source manifest:\n%w", err)
	}

	lines, err := fedorasource.ReadSourcesFile(string(contents))
	if err != nil {
		return fmt.Errorf("parsing render source manifest:\n%w", err)
	}

	for _, line := range lines {
		if line.Entry == nil {
			continue
		}

		if err := fileutils.ValidateFilename(line.Entry.Filename); err != nil {
			return fmt.Errorf("invalid render source filename:\n%w", err)
		}

		if err := fs.Remove(filepath.Join(directory, line.Entry.Filename)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing render source payload %#q:\n%w", line.Entry.Filename, err)
		}
	}

	return nil
}
