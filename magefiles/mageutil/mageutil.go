// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mageutil

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

type MessageType int

const (
	MsgStart MessageType = iota
	MsgInfo
	MsgWarning
	MsgError
	MsgSuccess
)

const (
	GocovTool               = "gocov"
	GocovXMLTool            = "gocov-xml"
	GolangciLintTool        = "golangci-lint"
	EditorconfigCheckerTool = "editorconfig-checker"
	GoLicenseTool           = "go-licenses"
	GoStringerTool          = "stringer"
)

var (
	ErrCmdLookup = errors.New("command lookup error")
	ErrDir       = errors.New("directory error")
	ErrFile      = errors.New("file error")
)

var cwd string //nolint:gochecknoglobals

func setCwdToProjectRoot() {
	initialWd, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("Error getting current working directory: %v", err))
	}

	// First check if we are already in the root of the repository.
	if _, err := os.Stat(path.Join(initialWd, "magefiles", "magefile.go")); err == nil {
		cwd = initialWd

		return
	}

	// If we are not in the root, next see if we are in <repo_root>/magefiles/.
	if _, err := os.Stat(path.Join(initialWd, "..", "magefiles", "magefile.go")); err == nil {
		cwd = path.Join(initialWd, "..")

		err = os.Chdir(cwd)
		if err != nil {
			panic(fmt.Sprintf("Error changing working directory to %s: %v", cwd, err))
		}

		return
	}

	// Give up and just use the current working directory. Hopefully the user knows what they are doing.
	cwd = initialWd
	fmt.Fprintf(os.Stderr, "Could not find root of azldev repository, using current working directory: %s\n", cwd)
}

// While init() is not ideal, Getwd() is very unlikely to fail in practice. If it does, it's likely a critical error
// anyways and there is no point in continuing.
//
//nolint:gochecknoinits // Using init with a global saves a huge amount of error handling code.
func init() {
	// We want to always base paths off the root of the azldev repository, so we need to find the root of the
	// repository. This is the directory where the magefiles directory is located. Unfortunately, it doesn't seem
	// like mage exposes this information (the user might use -w to change the working directory), so we need to do our
	// best to guess it. If we do find it, change the working directory to match.
	setCwdToProjectRoot()

	// BASH-COMPLETION: This is scanned by the bash completion script, keep it in sync with the script.
	fmt.Printf("Current working directory: %s\n", cwd)
}

func OutDir() string {
	return path.Join(cwd, "out")
}

func BinDir() string {
	return path.Join(OutDir(), "bin")
}

func BuildDir() string {
	return path.Join(cwd, "build")
}

func AzldevProjectDir() string {
	return cwd
}

func ScenarioDir() string {
	return path.Join(cwd, "scenario")
}

func LicenseDir() string {
	return path.Join(OutDir(), "licenses")
}

func CreateOutDir() error {
	return createDir(OutDir())
}

func CreateBinDir() error {
	return createDir(BinDir())
}

func CreateBuildDir() error {
	return createDir(BuildDir())
}

func CreateLicenseDir() error {
	return createDir(LicenseDir())
}

// getCallerFunctionName returns the name of the function that called the function that called it.
// fnDepth is the number of functions to go back in the call stack to find the user's function name.
func getCallerFunctionName(fnDepth int) string {
	programCounter, _, _, ok := runtime.Caller(fnDepth)

	if !ok {
		return "UNKNOWN FUNC"
	}

	function := runtime.FuncForPC(programCounter)
	if function == nil {
		return "UNKNOWN FUNC"
	}

	fullName := function.Name()
	// Split the full name by the period.
	nameParts := strings.Split(fullName, ".")
	// The last part of the name is the actual function name.
	return nameParts[len(nameParts)-1]
}

func magePrint(text string, msgType MessageType) {
	const fnDepth = 3

	messageTypeToIcon := map[MessageType]string{
		MsgStart:   "🚀",
		MsgInfo:    "ℹ️",
		MsgWarning: "⚠️",
		MsgError:   "❌",
		MsgSuccess: "✅",
	}

	funcName := getCallerFunctionName(fnDepth)
	fmt.Printf("\t%s %s: %s", messageTypeToIcon[msgType], funcName, text)
}

// MagePrintln prints a message with a message type and text without formatting.
func MagePrintln(msgType MessageType, a ...any) {
	formatted := fmt.Sprintln(a...)

	magePrint(formatted, msgType)
}

// MagePrintf prints a message with a message type and formatted text.
func MagePrintf(msgType MessageType, text string, args ...interface{}) {
	formatText := fmt.Sprintf(text, args...)

	magePrint(formatText, msgType)
}

// PrintAndReturnError prints an error message, then returns a wrapped error. generalError should usually be a
// step error (e.g., ErrCheck, ErrBuild, etc.), while specificError should be the error that caused the step
// error (e.g., file not found, etc.). If specificError is nil, generalError is returned without wrapping.
//
// i.e.,
// PrintAndReturnError("Can't do step", "step failed", "file not found"):
//
// prints "Can't do step"
// returns error("step failed: file not found")
//
// PrintAndReturnError("Can't do step", "step failed", nil):
//
// prints "Can't do step"
// returns error("step failed").
func PrintAndReturnError(text string, generalError, specificError error) error {
	magePrint(text+"\n", MsgError)

	if specificError == nil {
		return generalError
	}

	return fmt.Errorf("%w: %w", generalError, specificError)
}

func createDir(dir string) error {
	var err error
	if _, err = os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		err = os.MkdirAll(dir, os.ModePerm)
	}

	if err != nil {
		return fmt.Errorf("%w: %w", ErrDir, err)
	}

	return nil
}

// GetToolAbsPath returns the command to run a go tool that is managed within
// one of the tools-focused modules in this repository.
func GetToolAbsPath(toolName string) (toolPath string, err error) {
	var origWd string

	// Save the current working directory so we can restore it later.
	origWd, err = os.Getwd()
	if err != nil {
		return "", fmt.Errorf("%w: could not get current working directory: %w", ErrCmdLookup, err)
	}

	// Restore the original working directory before returning.
	defer func() {
		chdirErr := os.Chdir(origWd)
		if chdirErr != nil {
			if err == nil {
				err = fmt.Errorf("%w: could not restore original working directory %q: %w",
					ErrCmdLookup, origWd, chdirErr)
			} else {
				err = fmt.Errorf("%w: could not restore original working directory %q: %w; original error: %w",
					ErrCmdLookup, origWd, chdirErr, err)
			}
		}
	}()

	// NOTE: We've found that we typically get a temporary-and-no-longer-resolvable path back
	// the first time we invoke "go tool". Thereafter, it's in the cache. We call a second
	// time for the *good* path.
	for range 2 {
		var err error

		toolWd := filepath.Join(origWd, "tools", toolName)

		// NOTE: To avoid errors with "go" not being willing to handle the -modfile flag in case
		// a go workspace (go.work) is present, we chdir to the directory where we expect to find
		// the go.mod.
		err = os.Chdir(filepath.Join(origWd, "tools", toolName))
		if err != nil {
			return "", fmt.Errorf("%w: could not change directory to %q: %w", ErrCmdLookup, toolWd, err)
		}

		toolPath, err = sh.Output(mg.GoCmd(), "tool", "-n", toolName)
		if err != nil {
			return "", fmt.Errorf("%w: could not find the executable path for tool %q using cwd %q: %w",
				ErrCmdLookup, toolName, toolWd, err)
		}

		if toolPath == "" {
			return "", fmt.Errorf("%w: tool %q not found in tools module", ErrCmdLookup, toolName)
		}
	}

	return toolPath, nil
}
