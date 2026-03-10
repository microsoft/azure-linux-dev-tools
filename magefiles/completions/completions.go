// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package completions

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/google/renameio"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
)

var ErrCompletion = errors.New("error installing completions")

// InstallCompletions installs bash completions for mage to the user's personal completions directory.
func InstallCompletions() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Installing bash completions for mage")

	// Packages should install completions to /usr/share/bash-completion/completions, but since this is a user operation
	// we will install to the user's personal directory. On modern versions of bash-completion that would be:
	//  $HOME/.local/share/bash-completion, but in version 2.1 we only have: $HOME/.bash_completion
	userHome, err := os.UserHomeDir()
	if err != nil {
		return mageutil.PrintAndReturnError("could not determine user home directory", mageutil.ErrDir, err)
	}

	inputPath := path.Join(mageutil.AzldevProjectDir(), "magefiles", "completions", "mage.bash")
	outputPath := path.Join(userHome, ".bash_completion")

	mageutil.MagePrintln(mageutil.MsgInfo, "Will install completion to", outputPath)

	// Remove any existing completion script from the user's .bash_completion file. The mage completion script is
	// surrounded by '# --- MAGE COMPLETIONS START ---' and '# --- MAGE COMPLETIONS END ---' comments.
	err = removeLinesFromFile(outputPath, "# --- MAGE COMPLETIONS START ---", "# --- MAGE COMPLETIONS END ---")
	if err != nil {
		return mageutil.PrintAndReturnError("could not remove existing completions from bash completion file",
			mageutil.ErrFile, err)
	}

	// Append the completion script to the user's .bash_completion file, creating it if it doesn't exist.
	fOut, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileperms.PublicFile)
	if err != nil {
		return mageutil.PrintAndReturnError("could not open bash completion file", mageutil.ErrFile, err)
	}
	defer fOut.Close()

	fIn, err := os.Open(inputPath)
	if err != nil {
		return mageutil.PrintAndReturnError("could not open bash completion file", mageutil.ErrFile, err)
	}
	defer fIn.Close()

	_, err = fIn.WriteTo(fOut)
	if err != nil {
		return mageutil.PrintAndReturnError("could not write to bash completion file", mageutil.ErrFile, err)
	}

	mageutil.MagePrintln(mageutil.MsgInfo, "You may need to restart your shell to see the completions.")
	mageutil.MagePrintf(mageutil.MsgInfo, "Or run: 'source %s'\n", outputPath)
	mageutil.MagePrintln(mageutil.MsgSuccess, "Completions installed to", outputPath)

	return nil
}

// readCompletionAndFindMageSection reads the file at filePath and returns all lines. It finds the start and end
// markers' indices (including the markers). If neither marker is found, it returns the original lines and '-1' for
// both start and end. If the markers are misaligned, it returns an error.
func readCompletionAndFindMageSection(filePath, start, end string) ([]string, int, int, error) {
	// Read the file into memory.
	fIn, err := os.OpenFile(filePath, os.O_RDWR, os.ModePerm)
	if err != nil {
		return nil, -1, -1, fmt.Errorf("could not open file: %w", err)
	}
	defer fIn.Close()

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, -1, -1, fmt.Errorf("could not read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	var (
		startLine, endLine   int
		foundStart, foundEnd bool
	)

	for lineNum, line := range lines {
		if strings.Contains(line, start) {
			startLine = lineNum
			foundStart = true
		}

		if strings.Contains(line, end) {
			endLine = lineNum
			foundEnd = true
		}
	}

	// Ensure we found both the start and end lines, or neither.
	if foundStart != foundEnd || startLine > endLine {
		return nil, -1, -1, mageutil.PrintAndReturnError("start and end markers are misaligned",
			ErrCompletion, nil)
	}

	// If we didn't find the start and end lines, there's nothing to remove.
	if !foundStart {
		return lines, -1, -1, nil
	}

	return lines, startLine, endLine, nil
}

func removeLinesFromFile(filePath, start, end string) error {
	var (
		fInStats os.FileInfo
		err      error
	)

	// If the file doesn't exist, there's nothing to remove.
	if fInStats, err = os.Stat(filePath); os.IsNotExist(err) {
		return nil
	}

	mageutil.MagePrintln(mageutil.MsgInfo, "Removing old mage completion from", filePath)

	// Find the start and end lines.
	lines, startLine, endLine, err := readCompletionAndFindMageSection(filePath, start, end)
	if err != nil {
		return fmt.Errorf("could not read file: %w", err)
	}

	// If we didn't find the start and end lines, there's nothing to remove. Any unexpected mismatches should have
	// been caught in readCompletionAndFindMageSection above.
	if startLine == -1 {
		return nil
	}

	// Remove the lines.
	lines = append(lines[:startLine], lines[endLine+1:]...)

	currentPerms := fInStats.Mode()

	// Since the file needs to be modified in place, we will create a temporary file and atomically replace the original
	pendingFile, err := renameio.TempFile("", filePath)
	if err != nil {
		return fmt.Errorf("could not create temporary file: %w", err)
	}

	defer func() {
		err := pendingFile.Cleanup()
		if err != nil {
			mageutil.MagePrintln(mageutil.MsgError, "could not remove temporary file:", err)
		}
	}()

	err = pendingFile.Chmod(currentPerms)
	if err != nil {
		return fmt.Errorf("could not set permissions on temporary completions file: %w", err)
	}

	_, err = pendingFile.WriteString(strings.Join(lines, "\n"))
	if err != nil {
		return fmt.Errorf("could not write to temporary file: %w", err)
	}

	err = pendingFile.CloseAtomicallyReplace()
	if err != nil {
		return fmt.Errorf("could not replace file: %w", err)
	}

	return nil
}
