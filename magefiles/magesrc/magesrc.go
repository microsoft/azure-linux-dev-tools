// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magesrc

import (
	"errors"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
)

var ErrSrcCode = errors.New("source code generation failed")

// Generate runs the code generation tools.
func Generate() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Running code generation...")

	// We run code generation for all packages in parallel, up to the number of CPU cores.
	// This saves meaningful time now that we have many packages with code generation.
	err := generateForAllPackagesInParallel()
	if err != nil {
		return mageutil.PrintAndReturnError("Code generation failed.", ErrSrcCode, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Code generation complete.")

	return nil
}

func generateForAllPackagesInParallel() error {
	paths, err := listPackages()
	if err != nil {
		return err
	}

	generateDeps := []interface{}{}

	// Build up a list of dependencies to run in parallel; we'll let [mg.Deps] take care of
	// the parallel execution.
	for _, path := range paths {
		generateDeps = append(generateDeps, mg.F(generateForPackage, path))
	}

	mg.Deps(generateDeps...)

	return nil
}

// listPackages lists all Go packages in the current module.
func listPackages() ([]string, error) {
	out, err := sh.Output(mg.GoCmd(), "list", "./...")
	if err != nil {
		return nil, mageutil.PrintAndReturnError("Failed to list Go packages.", ErrSrcCode, err)
	}

	return strings.Split(strings.TrimSpace(out), "\n"), nil
}

// generateForPackage runs code generation for a specific package.
func generateForPackage(path string) error {
	err := sh.Run(mg.GoCmd(), "generate", path)
	if err != nil {
		return mageutil.PrintAndReturnError("Code generation failed.", ErrSrcCode, err)
	}

	return nil
}
