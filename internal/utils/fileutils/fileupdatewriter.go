// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils

import (
	"fmt"
	"io"

	"github.com/google/renameio"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/spf13/afero"
)

// A file writer capable of atomically applying its updates to files (provided that the
// underlying filesystem supports it).
type FileUpdateWriter struct {
	destFilePath string
	fileToWrite  opctx.File

	// Only present when we are on a "real" OS filesystem. In other cases, we are running on an
	// alternate filesystem and degrade to a non-atomic update. This should only happen in test
	// scenarios.
	tempFile *renameio.PendingFile
}

// FileUpdateWriter implements [io.Writer] for ease of use.
var _ io.Writer = &FileUpdateWriter{}

// Used for updating files. On "real" system filesystems, uses [renameio] package to
// atomically update files. On in-memory and other filesystems, degrades to a non-atomic
// update.
func NewFileUpdateWriter(fs opctx.FS, destFilePath string) (result *FileUpdateWriter, err error) {
	result = &FileUpdateWriter{
		destFilePath: destFilePath,
	}

	// Special-case *real* host files.
	if isRealOSFS(fs) {
		result.tempFile, err = renameio.TempFile("", destFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to create temp file:\n%w", err)
		}

		result.fileToWrite = result.tempFile
	} else {
		result.fileToWrite, err = fs.Create(destFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to create file '%s':\n%w", destFilePath, err)
		}
	}

	return result, nil
}

// Implements [io.Writer], delegating to the inner file writer.
func (a *FileUpdateWriter) Write(p []byte) (n int, err error) {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return a.fileToWrite.Write(p)
}

// Commits pending writes to the destination file.
func (a *FileUpdateWriter) Commit() (err error) {
	if a.tempFile != nil {
		err = a.tempFile.CloseAtomicallyReplace()
		if err != nil {
			return fmt.Errorf("failed to commit updates to file '%s':\n%w", a.destFilePath, err)
		}
	} else {
		err = a.fileToWrite.Close()
		if err != nil {
			return fmt.Errorf("failed to close file '%s':\n%w", a.destFilePath, err)
		}
	}

	a.fileToWrite = nil

	return nil
}

func isRealOSFS(fs afero.Fs) bool {
	_, ok := fs.(*afero.OsFs)

	return ok
}
