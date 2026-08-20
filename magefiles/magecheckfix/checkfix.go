// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magecheckfix

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	TargetDefault  = "default"
	TargetMod      = "mod"
	TargetLint     = "lint"
	TargetStatic   = "static"
	TargetLicenses = "licenses"
	TargetPython   = "python"
)

var (
	ErrCheck         = errors.New("check failed")
	ErrFix           = errors.New("fix failed")
	ErrLint          = errors.New("lint failed")
	ErrToolPath      = errors.New("path not set for tool")
	ErrRuffNotFound  = errors.New("ruff not found on PATH")
	ErrPyrightNotFnd = errors.New("pyright not found on PATH")
	ErrTypeCheck     = errors.New("type check failed")
)

// Check one of: [all, default, mod, lint, static, licenses, python].
// BASH-COMPLETION: This is scanned by the bash completion script, keep it in sync with the script.
func Check(target string) error {
	mg.SerialDeps(magesrc.Generate)

	mageutil.MagePrintln(mageutil.MsgStart, "Checking...")

	switch target {
	case TargetAll:
		mg.SerialDeps(defaultCheck, pythonCheck)
	case TargetDefault:
		mg.SerialDeps(defaultCheck)
	case TargetMod:
		mg.SerialDeps(modCheck)
	case TargetLint:
		mg.SerialDeps(lintCheck)
	case TargetStatic:
		mg.SerialDeps(static)
	case TargetLicenses:
		mg.SerialDeps(licenseCheck)
	case TargetPython:
		mg.SerialDeps(pythonCheck)
	default:
		return fmt.Errorf("%w: unknown check target '%s'. Available targets: %v",
			ErrCheck, target, []string{
				TargetAll,
				TargetDefault,
				TargetMod,
				TargetLint,
				TargetStatic,
				TargetLicenses,
				TargetPython,
			})
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done checking.")

	return nil
}

func defaultCheck() error {
	mg.SerialDeps(modCheck, lintCheck, static, licenseCheck)

	return nil
}

// Fix one of: [all, mod, lint, python].
// BASH-COMPLETION: This is scanned by the bash completion script, keep it in sync with the script.
func Fix(target string) error {
	mg.SerialDeps(magesrc.Generate)

	mageutil.MagePrintln(mageutil.MsgStart, "Fixing...")

	switch target {
	case TargetAll:
		mg.SerialDeps(modFix, lintFix, pythonFix)
	case TargetMod:
		mg.SerialDeps(modFix)
	case TargetLint:
		mg.SerialDeps(lintFix)
	case TargetPython:
		mg.SerialDeps(pythonFix)
	default:
		return fmt.Errorf("%w: unknown fix target '%s'. Available targets: %v",
			ErrFix, target, []string{TargetAll, TargetMod, TargetLint, TargetPython})
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

// findRuff locates the ruff executable on the PATH. ruff is a Python tool and is not managed via the Go
// tools modules, so developers must install it themselves (the VS Code ruff extension bundles a copy that
// is not exposed on the PATH).
func findRuff() (string, error) {
	ruffPath, err := exec.LookPath("ruff")
	if err != nil {
		return "", fmt.Errorf("%w: install it with 'pip install ruff' or see "+
			"https://docs.astral.sh/ruff/installation/: %w", ErrRuffNotFound, err)
	}

	return ruffPath, nil
}

// findPyright locates the pyright executable on the PATH. Like ruff, pyright is a Python tool that is not
// managed via the Go tools modules, so developers must install it themselves.
func findPyright() (string, error) {
	pyrightPath, err := exec.LookPath("pyright")
	if err != nil {
		return "", fmt.Errorf("%w: install it with 'pip install pyright' or see "+
			"https://microsoft.github.io/pyright/#/installation: %w", ErrPyrightNotFnd, err)
	}

	return pyrightPath, nil
}

func pythonCheck() error {
	mg.SerialDeps(ruffCheck, pyrightCheck)

	return nil
}

func ruffCheck() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Linting Python with ruff...")

	ruffPath, err := findRuff()
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to find ruff.", ErrCheck, err)
	}

	// Lint check. -q suppresses the "All checks passed!" banner on success; violations are still
	// printed on failure.
	if err := sh.RunV(ruffPath, "check", "-q", "."); err != nil {
		return mageutil.PrintAndReturnError(
			"Python lint issues found. Please run 'mage fix python' to auto-fix.", ErrCheck, ErrLint)
	}

	// Format check. -q suppresses the "N files already formatted" banner on success.
	if err := sh.RunV(ruffPath, "format", "--check", "-q", "."); err != nil {
		return mageutil.PrintAndReturnError(
			"Python files are not formatted. Please run 'mage fix python' to format them.", ErrCheck, ErrLint)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done linting Python.")

	return nil
}

func pyrightCheck() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Type-checking Python with pyright...")

	pyrightPath, err := findPyright()
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to find pyright.", ErrCheck, err)
	}

	// pyright always prints a summary line even on success, so capture its output and only surface
	// it when there's a problem (mirrors golangciLintCheck above to keep checks quiet on success).
	output, err := sh.Output(pyrightPath)
	if err != nil {
		if output != "" {
			for _, line := range strings.Split(output, "\n") {
				mageutil.MagePrintln(mageutil.MsgInfo, line)
			}
		}

		return mageutil.PrintAndReturnError(
			"Python type errors found; please review the errors displayed above.", ErrCheck, ErrTypeCheck)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done type-checking Python.")

	return nil
}

func pythonFix() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Fixing Python formatting...")

	ruffPath, err := findRuff()
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to find ruff.", ErrFix, err)
	}

	// Auto-fix lint issues.
	if err := sh.RunV(ruffPath, "check", "--fix", "."); err != nil {
		return mageutil.PrintAndReturnError(
			"Failed to auto-fix all Python lint issues; please review any errors displayed above.", ErrFix, err)
	}

	// Format.
	if err := sh.RunV(ruffPath, "format", "."); err != nil {
		return mageutil.PrintAndReturnError("Failed to format Python files.", ErrFix, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Done fixing Python.")

	return nil
}

// licenseCheck checks the licenses of the dependencies and writes the results to a CSV file.
func licenseCheck() error {
	mg.SerialDeps(mageutil.CreateLicenseDir)

	mageutil.MagePrintln(mageutil.MsgStart, "Checking licenses...")

	outFilePath := path.Join(mageutil.OutDir(), "licenses.csv")
	errorText := "Failed to check licenses."

	if err := sh.Run(mg.GoCmd(), "mod", "download"); err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

	cmdAbsPath, err := mageutil.GetToolAbsPath(mageutil.GoLicenseTool)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

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

	// go-licenses complains about various standard library .go files unless we explicitly
	// set the right GOROOT. We invoke "go env" to find the correct GOROOT and then run
	// go-licenses in an environment with it set.
	goRoot, err := sh.Output("go", "env", "GOROOT")
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

	if err := runLicenseCheck(cmdAbsPath, goRoot, commonOptions); err != nil {
		return err
	}

	if err := runLicenseReport(cmdAbsPath, goRoot, commonOptions, outFilePath); err != nil {
		return err
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "License check complete, results written to "+outFilePath)

	return nil
}

// runLicenseCheck runs "go-licenses check" for disallowed license types, suppressing the benign
// non-Go-code warnings on success and only surfacing real errors on failure.
func runLicenseCheck(cmdAbsPath, goRoot string, commonOptions []string) error {
	// per https://github.com/google/go-licenses?tab=readme-ov-file#check
	disallowedLicenseTypes := []string{"unknown", "forbidden", "restricted"}

	checkArgs := append([]string{"check", "--disallowed_types=" + strings.Join(disallowedLicenseTypes, ",")},
		commonOptions...)

	// go-licenses spews a glog warning for every dependency that contains non-Go (assembly) code.
	// These are benign and constant, so capture the combined output, strip those warnings, and only
	// surface what's left (real errors) when the check fails.
	env := map[string]string{"GOROOT": goRoot}

	var out strings.Builder

	if _, err := sh.Exec(env, &out, &out, cmdAbsPath, checkArgs...); err != nil {
		mageutil.PrintLicenseOutput(out.String())

		return mageutil.PrintAndReturnError("Failed to check licenses.", ErrCheck, err)
	}

	return nil
}

// runLicenseReport runs "go-licenses report" and writes the resulting CSV to outFilePath. The same
// non-Go-code warnings are emitted to stderr; they are captured and only surfaced on failure.
func runLicenseReport(cmdAbsPath, goRoot string, commonOptions []string, outFilePath string) error {
	errorText := "Failed to check licenses."

	reportArgs := append([]string{"report"}, commonOptions...)

	outFile, err := os.Create(outFilePath)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}
	defer outFile.Close()

	// Stream stdout (the CSV) straight to the file and capture stderr separately so the benign
	// non-Go-code warnings never reach the terminal; only surface them on failure.
	env := map[string]string{"GOROOT": goRoot}

	var stderr strings.Builder

	if _, err := sh.Exec(env, outFile, &stderr, cmdAbsPath, reportArgs...); err != nil {
		mageutil.PrintLicenseOutput(stderr.String())

		return mageutil.PrintAndReturnError(errorText, ErrCheck, err)
	}

	return nil
}
