// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// ReadLocklessComponents reads the exact component names listed in
// 'lockless-components.txt' beside the project's top-level 'azldev.toml'.
func ReadLocklessComponents(fs opctx.FS, projectDir string) (map[string]bool, error) {
	names := make(map[string]bool)
	if projectDir == "" {
		return names, nil
	}

	path := filepath.Join(projectDir, "lockless-components.txt")

	info, err := fs.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return names, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %#q:\n%w", path, err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("expected a file at %#q, found a directory", path)
	}

	content, err := fileutils.ReadFile(fs, path)
	if err != nil {
		return nil, fmt.Errorf("reading %#q:\n%w", path, err)
	}

	for line := range strings.SplitSeq(string(content), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names[name] = true
		}
	}

	return names, nil
}
