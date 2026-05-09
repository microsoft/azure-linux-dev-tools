// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magebuild

import (
	"os"
	"path"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
)

// CLIDocsOutputDir returns the output directory for generated CLI reference docs.
func CLIDocsOutputDir() string {
	return path.Join("docs", "user", "reference", "cli")
}

// SchemasOutputDir returns the output directory for generated JSON schema files.
func SchemasOutputDir() string {
	return "schemas"
}

// Docs builds the azldev binary and regenerates CLI docs and JSON schema.
func Docs() error {
	mg.Deps(Build)
	mg.Deps(generateDocs, generateSchema)

	return nil
}

// generateDocs generates CLI reference documentation by running "azldev docs markdown".
func generateDocs() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Generating CLI reference docs...")

	azldevBin := path.Join(mageutil.BinDir(), "azldev")

	err := sh.Run(azldevBin, "docs", "markdown", "--include-hidden", "-o", CLIDocsOutputDir(), "-f")
	if err != nil {
		return mageutil.PrintAndReturnError("CLI docs generation failed.", ErrBuild, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "CLI reference docs generated.")

	return nil
}

// generateSchema generates the JSON schema for the azldev config file and writes it to the schemas directory.
func generateSchema() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Generating JSON schema...")

	azldevBin := path.Join(mageutil.BinDir(), "azldev")
	schemaPath := path.Join(SchemasOutputDir(), "azldev.schema.json")

	// Run azldev to generate the schema and capture its output.
	output, err := sh.Output(azldevBin, "config", "generate-schema")
	if err != nil {
		return mageutil.PrintAndReturnError("Schema generation failed.", ErrBuild, err)
	}

	// Write the schema to file. Append a trailing newline to match the output of fmt.Println.
	err = os.WriteFile(schemaPath, []byte(output+"\n"), fileperms.PublicFile)
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to write schema file.", ErrBuild, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "JSON schema written to %s\n", schemaPath)

	return nil
}
