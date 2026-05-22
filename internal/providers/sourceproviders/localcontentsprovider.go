// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
)

// FetchLocalComponent retrieves the `.spec` file and any sidecar files for the specified component
// from the local filesystem, placing the fetched files in the provided directory.
// If resolveRequiredFiles is true, files referenced by the spec's contents (e.g., patches,
// additional sources) will be searched for recursively in the source directory.
func FetchLocalComponent(
	dryRunnable opctx.DryRunnable, eventListener opctx.EventListener,
	fileSystem opctx.FS, component components.Component, destDirPath string,
	resolveRequiredFiles bool,
) error {
	if dryRunnable == nil {
		return errors.New("dry runnable cannot be nil")
	}

	if fileSystem == nil {
		return errors.New("filesystem cannot be nil")
	}

	if component.GetName() == "" {
		return errors.New("component name cannot be empty")
	}

	if destDirPath == "" {
		return errors.New("destination path cannot be empty")
	}

	// Verify this is a local component
	if component.GetConfig().Spec.SourceType != projectconfig.SpecSourceTypeLocal {
		return fmt.Errorf("component %#q is not a local component (source type: %s)",
			component.GetName(), component.GetConfig().Spec.SourceType)
	}

	// Get the source directory path where the spec and sidecar files are located
	sourceDirPath, err := acquireSourceDirPath(component)
	if err != nil {
		return fmt.Errorf("failed to acquire source directory for component %#q:\n%w",
			component.GetName(), err)
	}

	// Copy all files from the source directory to the destination directory
	err = copySourceDirectory(dryRunnable, fileSystem, sourceDirPath, destDirPath)
	if err != nil {
		return fmt.Errorf("failed to copy source directory for component %#q:\n%w",
			component.GetName(), err)
	}

	// Resolve and copy any required files that may be stored separately in local filesystem
	if resolveRequiredFiles {
		err = copyRequiredFiles(dryRunnable, fileSystem, eventListener, component, sourceDirPath, destDirPath)
		if err != nil {
			return fmt.Errorf("failed to copy required files for component %#q:\n%w",
				component.GetName(), err)
		}
	}

	return nil
}

// acquireSourceDirPath determines the directory containing the component's spec and sidecar files.
func acquireSourceDirPath(component components.Component) (string, error) {
	spec := component.GetSpec()

	specPath, err := spec.GetPath()
	if err != nil {
		return "", fmt.Errorf("failed to get spec path for component %#q:\n%w",
			component.GetName(), err)
	}

	return filepath.Dir(specPath), nil
}

// copySourceDirectory copies all files from the source directory to the destination directory,
// preserving file mode bits (e.g., executable bits for scripts).
func copySourceDirectory(dryRunnable opctx.DryRunnable, fs opctx.FS, sourceDirPath, destDirPath string) error {
	copyOptions := fileutils.CopyDirOptions{
		CopyFileOptions: fileutils.CopyFileOptions{
			PreserveFileMode: true,
		},
	}

	err := fileutils.CopyDirRecursive(dryRunnable, fs, sourceDirPath, destDirPath, copyOptions)
	if err != nil {
		return fmt.Errorf("failed to copy files from %#q to %#q:\n%w",
			sourceDirPath, destDirPath, err)
	}

	return nil
}

// copyRequiredFiles resolves and copies any required files that may be stored separately
// from the spec file's directory.
func copyRequiredFiles(
	dryRunnable opctx.DryRunnable, fs opctx.FS, eventListener opctx.EventListener,
	component components.Component, sourceDirPath, destDirPath string,
) error {
	// Get the list of required files from the spec
	requiredFilenames, err := getRequiredFilesForComponent(dryRunnable, eventListener, component)
	if err != nil {
		return fmt.Errorf("failed to find files required by component %#q:\n%w",
			component.GetName(), err)
	}

	// Copy each required file
	for _, filename := range requiredFilenames {
		err = resolveAndCopyFileForSpec(dryRunnable, fs, component, filename, sourceDirPath, destDirPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// getRequiredFilesForComponent reads names of all files required by the component's spec file.
func getRequiredFilesForComponent(
	dryRunnable opctx.DryRunnable, eventListener opctx.EventListener, component components.Component,
) ([]string, error) {
	if dryRunnable.DryRun() {
		slog.Info("Dry run: would have queried spec for required files",
			"component", component.GetName())

		return nil, nil
	}

	evt := eventListener.StartEvent("Querying spec for required files", "component", component.GetName())
	defer evt.End()

	// Parse the spec to get the list of required files
	specDetails, err := component.GetSpec().Parse()
	if err != nil {
		return nil, fmt.Errorf("failed to query spec for component %#q:\n%w",
			component.GetName(), err)
	}

	return specDetails.RequiredFiles, nil
}

// resolveAndCopyFileForSpec finds and copies a required file for the component's spec.
func resolveAndCopyFileForSpec(
	dryRunnable opctx.DryRunnable, fs opctx.FS,
	component components.Component,
	filename, sourceDirPath, destDirPath string,
) error {
	destPath := path.Join(destDirPath, filename)

	// Try to find the file in the source directory
	localPath, err := tryFindMatchingFileRecursively(fs, sourceDirPath, filename)
	if err != nil {
		return err
	}

	if localPath == "" {
		return fmt.Errorf("could not find required file %#q for component %#q",
			filename, component.GetName())
	}

	slog.Debug("Found required file locally", "filename", filename, "path", localPath)

	// Copy the file to the destination directory, preserving file mode bits
	err = fileutils.CopyFile(dryRunnable, fs, localPath, destPath,
		fileutils.CopyFileOptions{PreserveFileMode: true})
	if err != nil {
		return fmt.Errorf("failed to copy local file %#q required by spec for %#q:\n%w",
			filename, component.GetName(), err)
	}

	return nil
}

// tryFindMatchingFileRecursively searches recursively for a file with the given name
// within the specified directory.
func tryFindMatchingFileRecursively(
	fileSystem opctx.FS, dirPath, filename string,
) (localPath string, err error) {
	err = afero.Walk(fileSystem, dirPath, func(path string, info fs.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !info.IsDir() && info.Name() == filename {
			localPath = path

			return filepath.SkipAll
		}

		return nil
	})

	if errors.Is(err, filepath.SkipAll) {
		err = nil
	}

	return localPath, err
}
