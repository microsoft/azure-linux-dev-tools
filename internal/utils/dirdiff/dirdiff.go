// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package dirdiff provides utilities for computing unified diffs between two directory trees.
package dirdiff

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/spf13/afero"
)

// DiffDirs computes a recursive unified diff between two directory trees on the provided
// filesystem. dirA is treated as the "original" (before) tree and dirB as the "modified"
// (after) tree. Regular files are compared by content, symlinks by target path, and other
// special entries by existence only. The returned [DiffResult] contains one [FileDiff]
// entry per changed file, sorted by relative path.
//
// Note: all regular files are read entirely into memory for diffing. This is designed for
// small-to-moderate file trees (spec files, patches, overlay sources) — not for diffing
// large binary archives.
func DiffDirs(fs opctx.FS, dirA, dirB string, opts ...DiffOption) (*DiffResult, error) {
	cfg := newDiffConfig(opts)

	filesA, err := collectFiles(fs, dirA)
	if err != nil {
		return nil, fmt.Errorf("failed to walk original directory %#q:\n%w", dirA, err)
	}

	filesB, err := collectFiles(fs, dirB)
	if err != nil {
		return nil, fmt.Errorf("failed to walk modified directory %#q:\n%w", dirB, err)
	}

	sortedPaths := mergedSortedKeys(filesA, filesB)

	diffs, err := diffAllPaths(fs, sortedPaths, filesA, filesB, dirA, dirB, cfg)
	if err != nil {
		return nil, err
	}

	return &DiffResult{Files: diffs}, nil
}

// diffAllPaths iterates the sorted, deduplicated set of relative paths and produces a
// [FileDiff] for each path that differs between the two directory trees.
func diffAllPaths(
	fs opctx.FS, sortedPaths []string,
	filesA, filesB map[string]fileEntry,
	dirA, dirB string, cfg *diffConfig,
) ([]FileDiff, error) {
	var diffs []FileDiff

	for _, relPath := range sortedPaths {
		entryA, inA := filesA[relPath]
		entryB, inB := filesB[relPath]

		var (
			status    FileStatus
			entryAPtr *fileEntry
			entryBPtr *fileEntry
		)

		switch {
		case inA && inB:
			entryAPtr = &entryA
			entryBPtr = &entryB

			if entryA.kind != entryB.kind {
				status = FileStatusTypeChanged
			} else {
				status = FileStatusModified
			}
		case inA:
			status = FileStatusRemoved
			entryAPtr = &entryA
		default:
			status = FileStatusAdded
			entryBPtr = &entryB
		}

		fileDiff, diffErr := diffEntry(fs, relPath, status, dirA, entryAPtr, dirB, entryBPtr, cfg)
		if diffErr != nil {
			return nil, fmt.Errorf("failed to diff file %#q:\n%w", relPath, diffErr)
		}

		if fileDiff != nil {
			diffs = append(diffs, *fileDiff)
		}
	}

	return diffs, nil
}

// mergedSortedKeys returns a sorted, deduplicated slice of all keys present in either map.
func mergedSortedKeys(filesA, filesB map[string]fileEntry) []string {
	paths := make([]string, 0, len(filesA)+len(filesB))
	for p := range filesA {
		paths = append(paths, p)
	}

	for p := range filesB {
		paths = append(paths, p)
	}

	slices.Sort(paths)

	return slices.Compact(paths)
}

// collectFiles walks a directory tree using [afero.Walk] and returns a map of relative
// paths to [fileEntry] values describing each non-directory entry found.
//
// Symlink detection requires an Lstat-capable filesystem (e.g., [afero.OsFs]);
// [afero.MemMapFs] does not support symlinks and will report them as regular files.
// [afero.Walk] uses LstatIfPossible internally, so on supported filesystems symlinks
// are correctly detected via [os.ModeSymlink] rather than being followed.
func collectFiles(fs opctx.FS, root string) (map[string]fileEntry, error) {
	result := make(map[string]fileEntry)

	walkErr := afero.Walk(fs, root, func(fullPath string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("error walking %#q:\n%w", fullPath, err)
		}

		mode := info.Mode()

		// Skip directories — we only collect leaf entries. Symlinks to directories
		// are not reported as IsDir() by afero.Walk (which uses LstatIfPossible),
		// so they correctly fall through to the symlink case below.
		if info.IsDir() {
			return nil
		}

		relPath, relErr := filepath.Rel(root, fullPath)
		if relErr != nil {
			return fmt.Errorf("failed to compute relative path for %#q from %#q:\n%w", fullPath, root, relErr)
		}

		switch {
		case mode&os.ModeSymlink != 0:
			target, linkErr := readSymlinkTarget(fs, fullPath)
			if linkErr != nil {
				return fmt.Errorf("failed to read symlink target for %#q:\n%w", fullPath, linkErr)
			}

			result[relPath] = fileEntry{
				kind:       fileKindSymlink,
				linkTarget: target,
			}

		case mode.IsRegular():
			result[relPath] = fileEntry{kind: fileKindRegular}

		default:
			// Non-regular, non-symlink entries (pipes, sockets, devices, etc.).
			result[relPath] = fileEntry{kind: fileKindSpecial}
		}

		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking directory %#q:\n%w", root, walkErr)
	}

	return result, nil
}

// readSymlinkTarget reads a symlink's target using the [afero.LinkReader] interface.
// Returns an error if the filesystem does not support reading symlink targets or if
// reading the target fails.
func readSymlinkTarget(fs opctx.FS, path string) (string, error) {
	linkReader, ok := fs.(afero.LinkReader)
	if !ok {
		return "", fmt.Errorf("filesystem %T does not support reading symlink targets", fs)
	}

	target, err := linkReader.ReadlinkIfPossible(path)
	if err != nil {
		return "", fmt.Errorf("ReadlinkIfPossible failed for %#q:\n%w", path, err)
	}

	return target, nil
}

// diffEntry compares a single entry across the two directory trees and returns a
// [FileDiff] if the entry differs. Returns nil when the entries are identical.
// This is the unified handler for added, removed, modified, and type-changed entries
// of all kinds (regular files, symlinks, and special files). A nil entry pointer
// indicates the entry does not exist on that side (added or removed).
func diffEntry(
	fs opctx.FS, relPath string,
	status FileStatus,
	dirA string, entryA *fileEntry,
	dirB string, entryB *fileEntry,
	cfg *diffConfig,
) (*FileDiff, error) {
	contentA, err := entryContent(fs, filepath.Join(dirA, relPath), entryA)
	if err != nil {
		return nil, err
	}

	contentB, err := entryContent(fs, filepath.Join(dirB, relPath), entryB)
	if err != nil {
		return nil, err
	}

	// For modified entries (same type), skip if content is identical.
	if status == FileStatusModified && bytes.Equal(contentA, contentB) {
		return nil, nil //nolint:nilnil // nil result signals "no difference" to the caller.
	}

	// Handle non-diffable entries (binary, special, empty) with an existence-only message.
	if fd := nonDiffableEntry(relPath, status, entryA, entryB, contentA, contentB, cfg); fd != nil {
		return fd, nil
	}

	return textDiff(relPath, status, contentA, contentB, cfg)
}

// nonDiffableEntry returns a [FileDiff] with a descriptive [FileDiff.Message] when the
// entry cannot be represented as a unified text diff (binary content, special filesystem
// entry, or empty file). Returns nil when normal text diffing should proceed.
func nonDiffableEntry(
	relPath string, status FileStatus,
	entryA, entryB *fileEntry,
	contentA, contentB []byte,
	cfg *diffConfig,
) *FileDiff {
	if isBinary(contentA, cfg.maxBinaryScanSize) || isBinary(contentB, cfg.maxBinaryScanSize) {
		return &FileDiff{
			Path:     relPath,
			Status:   status,
			IsBinary: true,
			Message:  binaryDiffMessage(relPath, status),
		}
	}

	// Special files with no readable content get an existence-only message.
	if (entryA != nil && entryA.kind == fileKindSpecial) ||
		(entryB != nil && entryB.kind == fileKindSpecial) {
		return &FileDiff{
			Path:    relPath,
			Status:  status,
			Message: specialDiffMessage(relPath, status),
		}
	}

	// Empty regular files have no textual diff — emit an existence-only message.
	// This prevents the empty unified diff from being silently dropped as "no difference".
	if len(contentA) == 0 && len(contentB) == 0 {
		return &FileDiff{
			Path:    relPath,
			Status:  status,
			Message: emptyFileDiffMessage(relPath, status),
		}
	}

	return nil
}

// textDiff generates a unified text diff for two content slices and returns a [FileDiff].
// Returns nil when the content is identical (empty unified diff).
func textDiff(relPath string, status FileStatus, contentA, contentB []byte, cfg *diffConfig) (*FileDiff, error) {
	var linesA, linesB []string

	if len(contentA) > 0 {
		linesA = difflib.SplitLines(string(contentA))
	}

	if len(contentB) > 0 {
		linesB = difflib.SplitLines(string(contentB))
	}

	oldPath, newPath := fileHeaderPaths(relPath, status)

	unifiedDiff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        linesA,
		B:        linesB,
		FromFile: oldPath,
		ToFile:   newPath,
		Context:  cfg.contextLines,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate unified diff for %#q:\n%w", relPath, err)
	}

	// An empty diff means the entries are identical after all.
	if unifiedDiff == "" {
		return nil, nil //nolint:nilnil // nil result signals "no difference" to the caller.
	}

	return &FileDiff{
		Path:        relPath,
		Status:      status,
		UnifiedDiff: unifiedDiff,
	}, nil
}

// binaryDiffMessage returns a status-appropriate message for binary file diffs.
func binaryDiffMessage(relPath string, status FileStatus) string {
	switch status {
	case FileStatusAdded:
		return fmt.Sprintf("Binary file b/%s added", relPath)
	case FileStatusRemoved:
		return fmt.Sprintf("Binary file a/%s removed", relPath)
	case FileStatusModified, FileStatusTypeChanged:
		return fmt.Sprintf("Binary files a/%s and b/%s differ", relPath, relPath)
	default:
		panic(fmt.Sprintf("unreachable: unhandled file status %q", status))
	}
}

// specialDiffMessage returns a status-appropriate message for special file diffs
// (pipes, sockets, devices, etc.).
func specialDiffMessage(relPath string, status FileStatus) string {
	switch status {
	case FileStatusAdded:
		return fmt.Sprintf("Special file b/%s added", relPath)
	case FileStatusRemoved:
		return fmt.Sprintf("Special file a/%s removed", relPath)
	case FileStatusModified, FileStatusTypeChanged:
		return fmt.Sprintf("Special files a/%s and b/%s differ", relPath, relPath)
	default:
		panic(fmt.Sprintf("unreachable: unhandled file status %q", status))
	}
}

// emptyFileDiffMessage returns a status-appropriate message for empty file diffs.
func emptyFileDiffMessage(relPath string, status FileStatus) string {
	switch status {
	case FileStatusAdded:
		return fmt.Sprintf("Empty file b/%s added", relPath)
	case FileStatusRemoved:
		return fmt.Sprintf("Empty file a/%s removed", relPath)
	case FileStatusModified, FileStatusTypeChanged:
		return fmt.Sprintf("Empty files a/%s and b/%s differ", relPath, relPath)
	default:
		panic(fmt.Sprintf("unreachable: unhandled file status %q", status))
	}
}

// entryContent returns the diffable content for a file entry. Regular files are read
// from disk, symlinks return a descriptive text line with the link target, and special
// files return nil (existence-only diffing). A nil entry (used for the missing side of
// added/removed entries) returns nil.
func entryContent(fs opctx.FS, fullPath string, entry *fileEntry) ([]byte, error) {
	if entry == nil {
		return nil, nil
	}

	switch entry.kind {
	case fileKindRegular:
		content, err := fileutils.ReadFile(fs, fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read %#q:\n%w", fullPath, err)
		}

		return content, nil

	case fileKindSymlink:
		// Represent the symlink target as a diffable text line.
		return []byte(entry.linkTarget + "\n"), nil

	case fileKindSpecial:
		// Special files — no readable content.
		return nil, nil
	default:
		panic(fmt.Sprintf("unreachable: unhandled file kind %d", entry.kind))
	}
}

// fileHeaderPaths returns the --- and +++ header paths for a unified diff based on
// the file's status. Uses standard unified diff conventions:
//   - a/ and b/ prefixes follow the git diff convention for old/new file paths.
//   - /dev/null is the standard convention for absent files (git diff, GNU diff).
func fileHeaderPaths(relPath string, status FileStatus) (oldPath, newPath string) {
	switch status {
	case FileStatusAdded:
		return "/dev/null", "b/" + relPath
	case FileStatusRemoved:
		return "a/" + relPath, "/dev/null"
	case FileStatusModified, FileStatusTypeChanged:
		return "a/" + relPath, "b/" + relPath
	default:
		panic(fmt.Sprintf("unhandled FileStatus %q", status))
	}
}

// isBinary returns true if the content appears to be binary (contains NUL bytes within
// the first maxScanBytes bytes). A nil or empty slice is not considered binary.
func isBinary(content []byte, maxScanBytes int) bool {
	if len(content) == 0 {
		return false
	}

	scanLen := min(len(content), maxScanBytes)

	return bytes.IndexByte(content[:scanLen], 0) >= 0
}
