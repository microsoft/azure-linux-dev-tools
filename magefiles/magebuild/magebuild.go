// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magebuild

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/magecheckfix"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/magesrc"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
	"github.com/samber/lo"
)

var (
	ErrBuild    = errors.New("build failed")
	ErrCoverage = errors.New("error generating coverage report")
	ErrInstall  = errors.New("install failed")
	ErrLicenses = errors.New("error collecting licenses")
	ErrToolPath = errors.New("path not found for tool")
	ErrUnit     = errors.New("unit test failed")
)

// gocovExcludesRegexp is a regular expression used to exclude magefiles
// and folders with test-only code from code coverage calculations.
var gocovExcludesRegexp = regexp.MustCompile(`(/magefiles/|_test(/|$))`)

// getBuildLdflags returns the ldflags string with build information.
func getBuildLdflags() (string, error) {
	buildTime := time.Now().UTC()

	// Check for SOURCE_DATE_EPOCH environment variable for reproducible builds.
	if epoch := os.Getenv("SOURCE_DATE_EPOCH"); epoch != "" {
		epochTime, err := strconv.ParseInt(epoch, 10, 64)
		if err != nil {
			return "", fmt.Errorf("%#q is an invalid value of the 'SOURCE_DATE_EPOCH' env variable", epoch)
		}

		buildTime = time.Unix(epochTime, 0).UTC()
	}

	return fmt.Sprintf(`-X 'go.szostok.io/version.buildDate=%s'`, buildTime.Format(time.RFC3339)), nil
}

// Build all the go code.
func Build() error {
	mg.SerialDeps(checkForBuildErrors, mageutil.CreateBinDir, magesrc.Generate)

	mageutil.MagePrintln(mageutil.MsgStart, "Building...")

	ldflags, err := getBuildLdflags()
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to get build ldflags.", ErrBuild, err)
	}

	err = sh.Run(mg.GoCmd(), "build", "-ldflags", ldflags, "-o", mageutil.BinDir(), "./...")
	if err != nil {
		return mageutil.PrintAndReturnError("Build failed.", ErrBuild, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "Placed tools in %s\n", mageutil.BinDir())

	// Generate CLI reference docs from the built binary.
	err = magesrc.GenerateDocs()
	if err != nil {
		return fmt.Errorf("generating CLI reference docs:\n%w", err)
	}

	return nil
}

// Install the go code to $GOPATH/bin.
func Install() error {
	mg.SerialDeps(Build)

	mageutil.MagePrintln(mageutil.MsgStart, "Installing...")

	ldflags, err := getBuildLdflags()
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to get build ldflags.", ErrInstall, err)
	}

	err = sh.Run(mg.GoCmd(), "install", "-ldflags", ldflags, "./cmd/azldev")
	if err != nil {
		return mageutil.PrintAndReturnError("Install failed.", ErrInstall, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Install complete")

	return nil
}

// PublishPrep prepares the build artifacts for publishing.
func PublishPrep() error {
	mg.SerialDeps(Build, mg.F(magecheckfix.Check, magecheckfix.TargetLicenses), collectLicenses)

	mageutil.MagePrintln(mageutil.MsgSuccess, "Artifacts prepared for publishing")

	return nil
}

// Clean up the build artifacts.
func Clean() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Cleaning...")

	mageutil.MagePrintf(mageutil.MsgInfo, "Cleaning up '%s'...\n", mageutil.OutDir())

	err := os.RemoveAll(mageutil.OutDir())
	if err != nil {
		return mageutil.PrintAndReturnError(fmt.Sprintf("Error cleaning up '%s'", mageutil.OutDir()), ErrBuild, err)
	}

	mageutil.MagePrintf(mageutil.MsgInfo, "Cleaning up '%s'...\n", mageutil.BuildDir())

	err = os.RemoveAll(mageutil.BuildDir())
	if err != nil {
		return mageutil.PrintAndReturnError(fmt.Sprintf("Error cleaning up '%s'", mageutil.BuildDir()), ErrBuild, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Clean complete")

	return nil
}

// Unit runs the unit tests.
func Unit() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Testing...")

	mg.SerialDeps(magesrc.Generate)

	output, err := sh.Output(mg.GoCmd(), "test", "./...")
	if err != nil {
		// Show the test output when tests fail so users can see which tests failed
		displayTestFailures(output)

		return mageutil.PrintAndReturnError("Test failed.", ErrUnit, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Test passed.")

	return nil
}

// displayTestFailures displays test output and a helpful message when tests fail.
func displayTestFailures(testOutput string) {
	// Display the test output
	mageutil.MagePrintln(mageutil.MsgError, testOutput)

	// Display a helpful message
	mageutil.MagePrintln(mageutil.MsgError, "Run 'go test ./...' to see detailed test failures.")
}

// checkForBuildErrors checks for build and syntax errors in the source code.
func checkForBuildErrors() error {
	mg.SerialDeps(magesrc.Generate)

	mageutil.MagePrintln(mageutil.MsgStart, "Checking for build errors...")

	err := sh.Run(mg.GoCmd(), "build", "./...")
	if err != nil {
		return mageutil.PrintAndReturnError("Build failed.", ErrBuild, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, "Build check complete.")

	return nil
}

// selectTestPackages returns a list of packages to test, omitting magefiles
// and folders with test-only code.
func selectTestPackages() ([]string, error) {
	packages, err := sh.Output(mg.GoCmd(), "list", "./...")
	if err != nil {
		return []string{}, fmt.Errorf("%w: error listing packages: %w", ErrCoverage, err)
	}

	lines := strings.Split(packages, "\n")

	return lo.Filter(lines, func(line string, _ int) bool {
		return !gocovExcludesRegexp.MatchString(line)
	}), nil
}

// testData Generate the raw test data for coverage.
func testData() error {
	mg.SerialDeps(mageutil.CreateBuildDir, magesrc.Generate)

	mageutil.MagePrintln(mageutil.MsgStart, "Generating test data...")

	outPath := path.Join(mageutil.BuildDir(), "coverage.out")

	const errorText = "Error generating test data."

	// Need to filter out the build system from the actual tools.
	packages, err := selectTestPackages()
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}

	args := []string{"test", "-covermode=atomic", "-coverprofile=" + outPath}
	args = append(args, packages...)

	output, err := sh.Output(mg.GoCmd(), args...)
	if err != nil {
		// Show the test output when tests fail so users can see which tests failed
		displayTestFailures(output)

		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "Wrote coverage data to '%s'\n", outPath)

	return nil
}

// Coverage generates a coverage report in HTML.
func Coverage() error {
	mg.SerialDeps(testData, mageutil.CreateOutDir)

	mageutil.MagePrintln(mageutil.MsgStart, "Testing with coverage...")

	err := sh.Run(mg.GoCmd(), "tool", "cover", "-html", path.Join(mageutil.BuildDir(), "coverage.out"), "-o",
		path.Join(mageutil.OutDir(), "coverage.html"))
	if err != nil {
		return mageutil.PrintAndReturnError("Error generating coverage report.", ErrCoverage, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "Generated coverage report at %s\n",
		path.Join(mageutil.OutDir(), "coverage.html"))

	return nil
}

// testGocovIntermediateData Generate the raw test data for coverage using gocov.
func testGocovIntermediateData() error {
	mg.SerialDeps(mageutil.CreateBuildDir)

	mageutil.MagePrintln(mageutil.MsgStart, "Generating gocov test data...")

	outPath := path.Join(mageutil.BuildDir(), "coverage.json")

	const errorText = "Error generating gocov test data."

	// Need to filter out the build system from the actual tools.
	packages, err := selectTestPackages()
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}

	args := []string{"test", "-covermode=atomic"}
	args = append(args, packages...)

	var cmdAbsPath string

	if cmdAbsPath, err = mageutil.GetToolAbsPath(mageutil.GocovTool); err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}

	// Capture test output to display in case of failure
	testOutput, err := sh.Output(mg.GoCmd(), args...)
	if err != nil {
		// Show the test output so users can see which tests failed
		displayTestFailures(testOutput)

		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}

	output, err := sh.Output(cmdAbsPath, args...)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}

	outFile, err := os.Create(outPath)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}

	defer outFile.Close()

	_, err = outFile.WriteString(output)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "Wrote coverage data to '%s'\n", outPath)

	return nil
}

// Gocov generates a coverage report in XML.
func Gocov(ctx context.Context) error {
	mg.SerialDeps(
		testGocovIntermediateData,
		mageutil.CreateOutDir,
	)

	mageutil.MagePrintln(mageutil.MsgStart, "Converting coverage data to XML...")

	outPath := path.Join(mageutil.OutDir(), "coverage.xml")

	const errorText = "Error converting coverage data to XML."

	// Need to use pipes for both input and output, so can't use sh.Run().
	inFile, err := os.Open(path.Join(mageutil.BuildDir(), "coverage.json"))
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}
	defer inFile.Close()

	outFile, err := os.Create(outPath)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}
	defer outFile.Close()

	var cmdAbsPath string

	if cmdAbsPath, err = mageutil.GetToolAbsPath(mageutil.GocovXMLTool); err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}

	cmd := exec.CommandContext(ctx, cmdAbsPath)
	cmd.Stdin = inFile
	cmd.Stdout = outFile
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrCoverage, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "Generated coverage report at %s\n", outPath)

	return nil
}

func collectLicenses() error {
	mg.SerialDeps(mageutil.CreateLicenseDir)

	mageutil.MagePrintln(mageutil.MsgStart, "Collecting licenses")

	errorText := "Failed to collect licenses."
	successText := "License collection complete, results written to " + mageutil.LicenseDir()

	err := sh.Run(mg.GoCmd(), "mod", "download")
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrLicenses, err)
	}

	cmdAbsPath, err := mageutil.GetToolAbsPath(mageutil.GoLicenseTool)
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrLicenses, err)
	}

	// go-licenses complains about various standard library .go files unless we explicitly
	// set the right GOROOT. We invoke "go env" to find the correct GOROOT and then run
	// go-licenses in an environment with it set.
	goRoot, err := sh.Output("go", "env", "GOROOT")
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrLicenses, err)
	}

	env := map[string]string{"GOROOT": goRoot}

	err = sh.RunWith(env, cmdAbsPath, "save", "--save_path", mageutil.LicenseDir(), "--force", "./...")
	if err != nil {
		return mageutil.PrintAndReturnError(errorText, ErrLicenses, err)
	}

	mageutil.MagePrintln(mageutil.MsgSuccess, successText)

	return nil
}
