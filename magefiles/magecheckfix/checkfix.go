// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magecheckfix

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/magesrc"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
)

// Meta targets for the checks and fixes.
const (
	TargetAll      = "all"
	TargetMod      = "mod"
	TargetLint     = "lint"
	TargetStatic   = "static"
	TargetLicenses = "licenses"
)

var (
	ErrCheck    = errors.New("check failed")
	ErrFix      = errors.New("fix failed")
	ErrLint     = errors.New("lint failed")
	ErrToolPath = errors.New("path not set for tool")
)

// Check one of: [all, mod, lint, static, licenses].
// BASH-COMPLETION: This is scanned by the bash completion script, keep it in sync with the script.
func Check(target string) error {
	mg.SerialDeps(magesrc.Generate)

	mageutil.MagePrintln(mageutil.MsgStart, "Checking...")

	switch target {
	case TargetAll:
		mg.SerialDeps(modCheck, lintCheck, static, licenseCheck)
	case TargetMod:
		mg.SerialDeps(modCheck)
	case TargetLint:
		mg.SerialDeps(lintCheck)
	case TargetStatic:
		mg.SerialDeps(static)
	case TargetLicenses:
		mg.SerialDeps(licenseCheck)
	default:
		return fmt.Errorf("%w: unknown check target '%s'. Available targets: %v",
			ErrCheck, target, []string{TargetAll, TargetMod, TargetLint, TargetStatic, TargetLicenses})
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done checking.")

	return nil
}

// Fix one of: [all, mod, lint].
// BASH-COMPLETION: This is scanned by the bash completion script, keep it in sync with the script.
func Fix(target string) error {
	mg.SerialDeps(magesrc.Generate)

	mageutil.MagePrintln(mageutil.MsgStart, "Fixing...")

	switch target {
	case TargetAll:
		mg.SerialDeps(modFix, lintFix)
	case TargetMod:
		mg.SerialDeps(modFix)
	case TargetLint:
		mg.SerialDeps(lintFix)
	default:
		return fmt.Errorf("%w: unknown fix target '%s'. Available targets: %v",
			ErrFix, target, []string{TargetAll, TargetMod, TargetLint})
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done fixing.")

	return nil
}

func static() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Running static check...")

	err := sh.Run(mg.GoCmd(), "vet", "./...")
	if err != nil {
		return mageutil.PrintAndReturnError("Static check failed.", ErrCheck, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Static check passed.")

	return nil
}

func modFix() error {
	// Run 'go mod tidy' to clean up go.mod and go.sum automatically.
	mageutil.MagePrintln(mageutil.MsgStart, "Fixing go.mod and go.sum...")

	err := sh.Run(mg.GoCmd(), "mod", "tidy")
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to fix go.mod and go.sum.", ErrFix, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done fixing go.mod and go.sum.")

	return nil
}

func modCheck() error {
	// Check if go.mod is tidy.
	mageutil.MagePrintln(mageutil.MsgStart, "Checking if go.mod is tidy...")
	// Older versions of 'go mod tidy' don't support the -diff flag, so we need to check
	// if it's supported before using it.
	helpText, err := sh.Output(mg.GoCmd(), "help", "mod", "tidy")
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to check if go.mod is tidy.", ErrCheck, err)
	}

	if !strings.Contains(helpText, "-diff") {
		mageutil.MagePrintln(mageutil.MsgWarning, "Go mod tidy does not support the -diff flag. Skipping.")

		return nil
	}

	err = sh.Run(mg.GoCmd(), "mod", "tidy", "-diff")
	if err != nil {
		return mageutil.PrintAndReturnError("go.mod is not tidy. Please run 'mage fix mod' or 'go mod tidy' to "+
			"clean it up.", ErrCheck, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done checking go.mod.")

	return nil
}

func lintFix() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Fixing code formatting...")

	var (
		cmdAbsPath string
		err        error
	)

	if cmdAbsPath, err = mageutil.GetToolAbsPath(mageutil.GolangciLintTool); err != nil || cmdAbsPath == "" {
		return fmt.Errorf("%w: failed to find tool '%s': %w", ErrToolPath, mageutil.GolangciLintTool, err)
	}

	// Sometimes the linter cache can get corrupted and it starts reporting bogus errors. Possibly due to running
	// the linter in parallel with the VSCode extension?
	err = sh.Run(cmdAbsPath, "cache", "clean")
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to clean linter cache.", ErrFix, err)
	}

	err = sh.RunV(cmdAbsPath, "run", "--fix", "--color=always")
	if err != nil {
		return mageutil.PrintAndReturnError(
			"Failed to auto-fix all code formatting issues; please review any errors displayed above.",
			ErrFix, err,
		)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done fixing code formatting.")

	return nil
}

func lintCheck() error {
	mg.SerialDeps(golangciLintCheck, editorconfigCheck)

	return nil
}

func golangciLintCheck() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Linting code with golangci-lint...")

	const errorText = "Failed to lint code."

	var (
		cmdAbsPath string
		err        error
	)
	if cmdAbsPath, err = mageutil.GetToolAbsPath(mageutil.GolangciLintTool); err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

	// Sometimes the linter cache can get corrupted and it starts reporting bogus errors. Possibly due to running
	// the linter in parallel with the VSCode extension?
	err = sh.Run(cmdAbsPath, "cache", "clean")
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to clean linter cache.", ErrFix, err)
	}

	output, err := sh.Output(cmdAbsPath, "run", "--show-stats=false", "--color=always")

	if output != "" {
		// Print each file that is not formatted.
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			mageutil.MagePrintln(mageutil.MsgInfo, line)
		}
	}

	if err != nil {
		return mageutil.PrintAndReturnError("Code is not formatted, or has linting issues. "+
			"Please run 'mage fix lint' or 'golangci-lint run --fix' to clean it up.", ErrCheck, ErrLint)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done linting code with golangci-lint.")

	return nil
}

func editorconfigCheck() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Running editorconfig check...")

	cmdAbsPath, err := mageutil.GetToolAbsPath(mageutil.EditorconfigCheckerTool)
	if err != nil || cmdAbsPath == "" {
		return fmt.Errorf("%w: failed to find tool '%s': %w", ErrToolPath, mageutil.EditorconfigCheckerTool, err)
	}

	// Run the editorconfig checker.
	err = sh.RunV(cmdAbsPath)
	if err != nil {
		return mageutil.PrintAndReturnError(
			"Failed to run editorconfig checker; please review any errors displayed above.",
			ErrCheck, err,
		)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done checking editorconfig.")

	return nil
}

// licenseCheck checks the licenses of the dependencies and writes the results to a CSV file.
func licenseCheck() error {
	mg.SerialDeps(mageutil.CreateLicenseDir)

	mageutil.MagePrintln(mageutil.MsgStart, "Checking licenses...")

	outFilePath := path.Join(mageutil.OutDir(), "licenses.csv")

	errorText := "Failed to check licenses."
	successText := "License check complete, results written to " + outFilePath

	err := sh.Run(mg.GoCmd(), "mod", "download")
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

	cmdAbsPath, err := mageutil.GetToolAbsPath(mageutil.GoLicenseTool)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

	// per https://github.com/google/go-licenses?tab=readme-ov-file#check
	disallowedLicenseTypes := []string{"unknown", "forbidden", "restricted"}

	commonOptions := []string{
		// Ignore golang.org/x dependencies to avoid spurious errors about .s files that can't be checked
		// and some odd errors we started seeing with standard library packages post 1.24.0.
		// Per docs, dependencies of those packages should still be checked. We recognize that this
		// leaves open the risk of not having licenses collected for standard library packages.
		"--ignore", "golang.org/x",
		// Ignore our own packages since they're not yet published.
		"--ignore", "github.com/microsoft/azure-linux-dev-tools",
		"./...",
	}

	checkArgs := append([]string{"check", "--disallowed_types=" + strings.Join(disallowedLicenseTypes, ",")},
		commonOptions...)

	// go-licenses complains about various standard library .go files unless we explicitly
	// set the right GOROOT. We invoke "go env" to find the correct GOROOT and then run
	// go-licenses in an environment with it set.
	goRoot, err := sh.Output("go", "env", "GOROOT")
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

	env := map[string]string{"GOROOT": goRoot}

	err = sh.RunWithV(env, cmdAbsPath, checkArgs...)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

	reportArgs := append([]string{"report"}, commonOptions...)

	report, err := sh.OutputWith(env, cmdAbsPath, reportArgs...)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

	outFile, err := os.Create(outFilePath)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}
	defer outFile.Close()

	_, err = io.WriteString(outFile, report)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, successText)

	return nil
}
