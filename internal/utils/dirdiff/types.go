// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package dirdiff

// FileStatus describes whether a file was added, removed, modified, or changed type
// relative to the original (pre-overlay) directory.
type FileStatus string

const (
	// FileStatusModified indicates a file exists in both directories but with different content.
	FileStatusModified FileStatus = "modified"
	// FileStatusAdded indicates a file exists only in the "after" (overlaid) directory.
	FileStatusAdded FileStatus = "added"
	// FileStatusRemoved indicates a file exists only in the "before" (original) directory.
	FileStatusRemoved FileStatus = "removed"
	// FileStatusTypeChanged indicates a file exists in both directories but changed its
	// filesystem type (e.g., from symlink to regular file or vice versa).
	FileStatusTypeChanged FileStatus = "type-changed"
)

// FileDiff holds the diff information for a single file.
type FileDiff struct {
	// Path is the file path relative to the compared directory roots.
	Path string
	// Status indicates whether the file was modified, added, removed, or type-changed.
	Status FileStatus
	// IsBinary is true when the file appears to contain binary content. When true,
	// [UnifiedDiff] will be empty and [Message] will contain a descriptive message.
	IsBinary bool
	// UnifiedDiff holds the complete unified diff text for this file, including
	// file headers (--- / +++) and hunk headers (@@ ... @@). Empty for binary files
	// and special files where [Message] is set instead.
	UnifiedDiff string
	// Message holds a human-readable description for non-diffable entries (binary files,
	// special files, empty files). Empty for normal text diffs where [UnifiedDiff] carries the diff content.
	Message string
}

// DiffResult holds the complete diff between two directory trees.
type DiffResult struct {
	// Files contains one entry for each file that differs between the two directories.
	Files []FileDiff `json:"files"`
}

// DiffOption is a functional option for configuring [DiffDirs] behavior.
type DiffOption func(*diffConfig)

const (
	// defaultContextLines is the number of unchanged lines shown around each changed
	// hunk, matching the conventional diff -u default.
	defaultContextLines = 3

	// defaultMaxBinaryScanBytes is the maximum number of bytes to scan when detecting
	// binary content. Only the first N bytes are checked for NUL bytes.
	defaultMaxBinaryScanBytes = 8192
)

// diffConfig holds the resolved configuration for a [DiffDirs] invocation.
type diffConfig struct {
	contextLines      int
	maxBinaryScanSize int
}

func newDiffConfig(opts []DiffOption) *diffConfig {
	cfg := &diffConfig{
		contextLines:      defaultContextLines,
		maxBinaryScanSize: defaultMaxBinaryScanBytes,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	return cfg
}

// WithContextLines sets the number of unchanged context lines shown around each changed
// hunk. The default is 3, matching the conventional diff -u behavior. Negative values
// are clamped to 0.
func WithContextLines(n int) DiffOption {
	return func(cfg *diffConfig) {
		cfg.contextLines = max(n, 0)
	}
}

// WithMaxBinaryScanBytes sets the maximum number of bytes to scan when detecting binary
// content. Only the first N bytes of each file are checked for NUL bytes. The default
// is 8192. Values less than 1 are clamped to 1.
func WithMaxBinaryScanBytes(n int) DiffOption {
	return func(cfg *diffConfig) {
		cfg.maxBinaryScanSize = max(n, 1)
	}
}

// fileKind categorizes directory entries for diffing purposes.
type fileKind int

const (
	// fileKindRegular is a regular file, diffed by content.
	fileKindRegular fileKind = iota
	// fileKindSymlink is a symbolic link, diffed by link target.
	fileKindSymlink
	// fileKindSpecial is a non-regular, non-symlink entry (e.g., pipe, socket),
	// diffed by existence only.
	fileKindSpecial
)

// fileEntry describes a file discovered during directory traversal.
type fileEntry struct {
	kind fileKind
	// linkTarget is the symlink target path, only set when kind is [fileKindSymlink].
	linkTarget string
}
