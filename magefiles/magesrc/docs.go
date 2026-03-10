// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magesrc

import (
	"path"

	"github.com/magefile/mage/sh"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
)

// CLIDocsOutputDir returns the output directory for generated CLI reference docs.
func CLIDocsOutputDir() string {
	return path.Join("docs", "user", "reference", "cli")
}

// GenerateDocs generates CLI reference documentation by running "azldev docs markdown".
func GenerateDocs() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Generating CLI reference docs...")

	azldevBin := path.Join(mageutil.BinDir(), "azldev")

	err := sh.Run(azldevBin, "docs", "markdown", "--include-hidden", "-o", CLIDocsOutputDir(), "-f")
	if err != nil {
		return mageutil.PrintAndReturnError("CLI docs generation failed.", ErrSrcCode, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "CLI reference docs generated.")

	return nil
}
