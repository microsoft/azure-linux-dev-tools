// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
)

// HasGoVendorSources reports whether source preparation must generate a vendor archive.
func (c *ComponentConfig) HasGoVendorSources() bool {
	for _, ref := range c.SourceFiles {
		if ref.Origin.Type == OriginTypeGoVendor {
			return true
		}
	}

	return false
}

// VendorConfig reads explicit native settings without modifying their contents.
// An omitted file reuses upstream settings; an explicit empty file replaces them.
func (o Origin) VendorConfig(fs opctx.FS) (string, error) {
	data, err := fileutils.ReadFile(fs, o.ConfigFile)
	if err != nil {
		return "", fmt.Errorf("reading Go vendor configuration %#q:\n%w", o.ConfigFile, err)
	}

	var config map[string]any
	if err := toml.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("parsing Go vendor configuration %#q:\n%w", o.ConfigFile, err)
	}

	return string(data), nil
}

// VendorIdentity fingerprints native settings and the generated-origin policy.
func (o Origin) VendorIdentity(fs opctx.FS) (string, error) {
	if o.Type != OriginTypeGoVendor {
		return "", nil
	}

	if o.ConfigFile == "" {
		return "go-vendor:upstream", nil
	}

	content, err := o.VendorConfig(fs)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256([]byte(content))

	return "go-vendor:" + hex.EncodeToString(hash[:]), nil
}

func validateGoVendorOrigin(origin Origin) error {
	if origin.Type != OriginTypeGoVendor {
		if origin.ConfigFile != "" || origin.ModuleDir != "" || len(origin.PrepPackages) != 0 {
			return errors.New("'config-file', 'module-dir' and 'prep-packages' require 'origin.type = go-vendor'")
		}

		return nil
	}

	if strings.ContainsAny(origin.ConfigFile, "\x00\r\n") {
		return errors.New("invalid Go vendor 'config-file' path")
	}

	if origin.ModuleDir != "" {
		if err := fileutils.ValidateRelPath(origin.ModuleDir); err != nil {
			return fmt.Errorf("invalid Go vendor 'module-dir':\n%w", err)
		}

		if strings.ContainsAny(origin.ModuleDir, "%\r\n") {
			return errors.New("go vendor 'module-dir' must not contain macros or newlines")
		}
	}

	for _, pkg := range origin.PrepPackages {
		if pkg == "" || strings.HasPrefix(pkg, "-") || strings.ContainsAny(pkg, "\r\n") {
			return fmt.Errorf("invalid Go vendor prep package %#q", pkg)
		}
	}

	return nil
}

func validateVendorSourceCount(refs []SourceFileReference) error {
	count := 0

	for _, ref := range refs {
		if ref.Origin.Type == OriginTypeGoVendor {
			count++
		}
	}

	if count > 1 {
		return errors.New("only one 'go-vendor' source per component is supported")
	}

	return nil
}

func validateSourceFilename(ref SourceFileReference) error {
	if ref.Origin.Type == OriginTypeGoVendor && ref.Filename == "" {
		return nil
	}

	if err := fileutils.ValidateFilename(ref.Filename); err != nil {
		return fmt.Errorf("validating source basename:\n%w", err)
	}

	return nil
}
