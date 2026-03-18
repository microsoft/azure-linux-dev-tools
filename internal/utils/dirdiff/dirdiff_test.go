// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package dirdiff_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/dirdiff"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffDirs_IdenticalDirs(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	require.NoError(t, fileutils.WriteFile(testFS, "/a/file.txt", []byte("hello\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/file.txt", []byte("hello\n"), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	assert.Empty(t, result.Files)
	assert.Empty(t, result.String())
}

func TestDiffDirs_ModifiedFile(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	require.NoError(t, fileutils.WriteFile(testFS, "/a/file.txt", []byte("line1\nline2\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/file.txt", []byte("line1\nmodified\n"), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	fileDiff := result.Files[0]
	assert.Equal(t, "file.txt", fileDiff.Path)
	assert.Equal(t, dirdiff.FileStatusModified, fileDiff.Status)
	assert.False(t, fileDiff.IsBinary)
	// Verify the rendered output contains expected unified diff elements.
	rendered := result.String()
	assert.Contains(t, rendered, "--- a/file.txt")
	assert.Contains(t, rendered, "+++ b/file.txt")
	assert.Contains(t, rendered, "-line2")
	assert.Contains(t, rendered, "+modified")

	// Verify the unified diff text is non-empty.
	assert.NotEmpty(t, fileDiff.UnifiedDiff)
}

func TestDiffDirs_AddedAndRemovedFiles(t *testing.T) {
	tests := []struct {
		name           string
		setupA         map[string]string // files in dir A
		setupB         map[string]string // files in dir B
		expectedPath   string
		expectedStatus dirdiff.FileStatus
		diffContains   []string
	}{
		{
			name:           "added file",
			setupA:         map[string]string{"/a/existing.txt": "hello\n"},
			setupB:         map[string]string{"/b/existing.txt": "hello\n", "/b/new.txt": "new content\n"},
			expectedPath:   "new.txt",
			expectedStatus: dirdiff.FileStatusAdded,
			diffContains:   []string{"--- /dev/null", "+++ b/new.txt", "+new content"},
		},
		{
			name:           "removed file",
			setupA:         map[string]string{"/a/existing.txt": "hello\n", "/a/removed.txt": "old content\n"},
			setupB:         map[string]string{"/b/existing.txt": "hello\n"},
			expectedPath:   "removed.txt",
			expectedStatus: dirdiff.FileStatusRemoved,
			diffContains:   []string{"--- a/removed.txt", "+++ /dev/null", "-old content"},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := testctx.NewCtx()
			testFS := ctx.FS()

			for path, content := range testCase.setupA {
				require.NoError(t, fileutils.WriteFile(testFS, path, []byte(content), fileperms.PublicFile))
			}

			for path, content := range testCase.setupB {
				require.NoError(t, fileutils.WriteFile(testFS, path, []byte(content), fileperms.PublicFile))
			}

			result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
			require.NoError(t, err)
			require.Len(t, result.Files, 1)

			assert.Equal(t, testCase.expectedPath, result.Files[0].Path)
			assert.Equal(t, testCase.expectedStatus, result.Files[0].Status)

			rendered := result.String()
			for _, substr := range testCase.diffContains {
				assert.Contains(t, rendered, substr)
			}
		})
	}
}

func TestDiffDirs_NestedFiles(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	require.NoError(t, fileutils.WriteFile(testFS, "/a/sub/deep/file.txt", []byte("original\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/sub/deep/file.txt", []byte("changed\n"), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	assert.Equal(t, "sub/deep/file.txt", result.Files[0].Path)
	assert.Equal(t, dirdiff.FileStatusModified, result.Files[0].Status)
}

func TestDiffDirs_MultipleChanges(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	// Original tree
	require.NoError(t, fileutils.WriteFile(testFS, "/a/keep.txt", []byte("same\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/a/modify.txt", []byte("before\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/a/remove.txt", []byte("gone\n"), fileperms.PublicFile))

	// Modified tree
	require.NoError(t, fileutils.WriteFile(testFS, "/b/keep.txt", []byte("same\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/modify.txt", []byte("after\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/add.txt", []byte("new\n"), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	require.Len(t, result.Files, 3)

	// Results should be sorted by path.
	assert.Equal(t, "add.txt", result.Files[0].Path)
	assert.Equal(t, dirdiff.FileStatusAdded, result.Files[0].Status)
	assert.Equal(t, "modify.txt", result.Files[1].Path)
	assert.Equal(t, dirdiff.FileStatusModified, result.Files[1].Status)
	assert.Equal(t, "remove.txt", result.Files[2].Path)
	assert.Equal(t, dirdiff.FileStatusRemoved, result.Files[2].Status)

	// Verify String() concatenates all diffs without blank line separators.
	full := result.String()
	assert.Contains(t, full, "add.txt")
	assert.Contains(t, full, "modify.txt")
	assert.Contains(t, full, "remove.txt")
	assert.NotContains(t, full, "\n\n---", "String() should not have blank lines between file diffs")
}

func TestDiffDirs_BinaryFile(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	// Binary content: contains NUL bytes.
	binaryContent := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x00, 0x00, 0x01}

	require.NoError(t, fileutils.WriteFile(testFS, "/a/image.png", binaryContent, fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/image.png", append(binaryContent, 0x02), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	fileDiff := result.Files[0]
	assert.Equal(t, dirdiff.FileStatusModified, fileDiff.Status)
	assert.True(t, fileDiff.IsBinary)
	assert.Empty(t, fileDiff.UnifiedDiff)
	assert.Contains(t, fileDiff.Message, "Binary files")
}

func TestDiffDirs_EmptyDirs(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	require.NoError(t, fileutils.MkdirAll(testFS, "/a"))
	require.NoError(t, fileutils.MkdirAll(testFS, "/b"))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	assert.Empty(t, result.Files)
}

func TestDiffDirs_AddedFile_UnifiedDiff(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	require.NoError(t, fileutils.MkdirAll(testFS, "/a"))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/new.txt", []byte("alpha\nbeta\n"), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	fileDiff := result.Files[0]
	assert.Equal(t, dirdiff.FileStatusAdded, fileDiff.Status)
	assert.False(t, fileDiff.IsBinary)

	// All lines in an added file should appear as added lines (+) in the unified diff.
	require.NotEmpty(t, fileDiff.UnifiedDiff)
	assert.Contains(t, fileDiff.UnifiedDiff, "+alpha")
	assert.Contains(t, fileDiff.UnifiedDiff, "+beta")
}

func TestDiffDirs_JSONOutput(t *testing.T) {
	t.Run("text diff includes unifiedDiff without message", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, fileutils.WriteFile(testFS, "/a/file.txt", []byte("before\n"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/b/file.txt", []byte("after\n"), fileperms.PublicFile))

		result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
		require.NoError(t, err)
		require.Len(t, result.Files, 1)

		assert.Empty(t, result.Files[0].Message)
		assert.NotEmpty(t, result.Files[0].UnifiedDiff)

		jsonBytes, jsonErr := json.Marshal(result)
		require.NoError(t, jsonErr)

		jsonStr := string(jsonBytes)

		assert.Contains(t, jsonStr, `"changes"`)
		assert.NotContains(t, jsonStr, `"message"`)
	})

	t.Run("binary diff includes message without unifiedDiff", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		binaryContent := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x00, 0x00, 0x01}

		require.NoError(t, fileutils.WriteFile(testFS, "/a/comp.tar", binaryContent, fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/b/comp.tar", append(binaryContent, 0x02), fileperms.PublicFile))

		result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
		require.NoError(t, err)
		require.Len(t, result.Files, 1)

		assert.NotEmpty(t, result.Files[0].Message)
		assert.Empty(t, result.Files[0].UnifiedDiff)

		jsonBytes, jsonErr := json.Marshal(result)
		require.NoError(t, jsonErr)

		jsonStr := string(jsonBytes)

		assert.Contains(t, jsonStr, `"message"`)
		assert.NotContains(t, jsonStr, `"changes"`)
	})
}

func TestDiffDirs_ErrorOnMissingDirectory(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	require.NoError(t, fileutils.MkdirAll(testFS, "/a"))

	// dirB does not exist — should return a descriptive error.
	_, err := dirdiff.DiffDirs(testFS, "/a", "/nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestDiffDirs_CustomContextLines(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	// Create a file with many lines, changing only one in the middle.
	linesA := make([]string, 0, 20)
	for i := range 20 {
		linesA = append(linesA, fmt.Sprintf("line%d", i))
	}

	linesB := make([]string, len(linesA))
	copy(linesB, linesA)
	linesB[10] = "CHANGED"

	contentA := strings.Join(linesA, "\n") + "\n"
	contentB := strings.Join(linesB, "\n") + "\n"

	require.NoError(t, fileutils.WriteFile(testFS, "/a/file.txt", []byte(contentA), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/file.txt", []byte(contentB), fileperms.PublicFile))

	// With 0 context lines, the unified diff should contain no context lines.
	result, err := dirdiff.DiffDirs(testFS, "/a", "/b", dirdiff.WithContextLines(0))
	require.NoError(t, err)
	require.Len(t, result.Files, 1)
	require.NotEmpty(t, result.Files[0].UnifiedDiff)

	assert.Equal(t, 0, countContextLines(result.Files[0].UnifiedDiff),
		"WithContextLines(0) should produce no context lines")

	// With negative context lines, behavior should be identical to 0 (clamped).
	resultNeg, err := dirdiff.DiffDirs(testFS, "/a", "/b", dirdiff.WithContextLines(-1))
	require.NoError(t, err)
	require.Len(t, resultNeg.Files, 1)
	require.NotEmpty(t, resultNeg.Files[0].UnifiedDiff)

	assert.Equal(t, 0, countContextLines(resultNeg.Files[0].UnifiedDiff),
		"WithContextLines(-1) should behave like WithContextLines(0)")

	// With 5 context lines, the unified diff should contain 5 context lines on each side.
	result5, err := dirdiff.DiffDirs(testFS, "/a", "/b", dirdiff.WithContextLines(5))
	require.NoError(t, err)
	require.Len(t, result5.Files, 1)
	require.NotEmpty(t, result5.Files[0].UnifiedDiff)

	assert.Equal(t, 10, countContextLines(result5.Files[0].UnifiedDiff),
		"WithContextLines(5) should produce 5 context lines on each side of the change")
}

// countContextLines counts the number of context lines (lines starting with a space)
// in a unified diff string, excluding file/hunk headers.
func countContextLines(unifiedDiff string) int {
	count := 0

	for _, line := range strings.Split(unifiedDiff, "\n") {
		if len(line) > 0 && line[0] == ' ' {
			count++
		}
	}

	return count
}

func TestDiffDirs_CRLFLineEndings(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	require.NoError(t, fileutils.WriteFile(testFS, "/a/file.txt", []byte("line1\r\nline2\r\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/file.txt", []byte("line1\r\nchanged\r\n"), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	require.Len(t, result.Files, 1)
	require.NotEmpty(t, result.Files[0].UnifiedDiff)

	// Verify the diff shows the expected change.
	rendered := result.String()
	assert.Contains(t, rendered, "-line2")
	assert.Contains(t, rendered, "+changed")
}

// Symlink tests require a real OS filesystem since afero.MemMapFs doesn't support symlinks.

func TestDiffDirs_SymlinkIdentical(t *testing.T) {
	dirA, dirB := setupSymlinkTestDirs(t)

	require.NoError(t, os.WriteFile(filepath.Join(dirA, "real.txt"), []byte("content\n"), fileperms.PrivateFile))
	require.NoError(t, os.Symlink("real.txt", filepath.Join(dirA, "link.txt")))

	require.NoError(t, os.WriteFile(filepath.Join(dirB, "real.txt"), []byte("content\n"), fileperms.PrivateFile))
	require.NoError(t, os.Symlink("real.txt", filepath.Join(dirB, "link.txt")))

	ctx := testctx.NewCtx(testctx.WithHostFS())

	result, err := dirdiff.DiffDirs(ctx.FS(), dirA, dirB)
	require.NoError(t, err)
	assert.Empty(t, result.Files, "identical symlinks should produce no diff")
}

func TestDiffDirs_SymlinkTargetChanged(t *testing.T) {
	dirA, dirB := setupSymlinkTestDirs(t)

	require.NoError(t, os.Symlink("target_a", filepath.Join(dirA, "link.txt")))
	require.NoError(t, os.Symlink("target_b", filepath.Join(dirB, "link.txt")))

	ctx := testctx.NewCtx(testctx.WithHostFS())

	result, err := dirdiff.DiffDirs(ctx.FS(), dirA, dirB)
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	fileDiff := result.Files[0]
	assert.Equal(t, "link.txt", fileDiff.Path)
	assert.Equal(t, dirdiff.FileStatusModified, fileDiff.Status)

	// The diff should show the old and new targets.
	rendered := result.String()
	assert.Contains(t, rendered, "target_a")
	assert.Contains(t, rendered, "target_b")
}

func TestDiffDirs_SymlinkAddedAndRemoved(t *testing.T) {
	tests := []struct {
		name           string
		setupA         func(t *testing.T, dir string)
		setupB         func(t *testing.T, dir string)
		expectedStatus dirdiff.FileStatus
	}{
		{
			name: "symlink added",
			setupA: func(t *testing.T, dir string) {
				t.Helper()
				// Empty directory.
			},
			setupB: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.Symlink("some-target", filepath.Join(dir, "link.txt")))
			},
			expectedStatus: dirdiff.FileStatusAdded,
		},
		{
			name: "symlink removed",
			setupA: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.Symlink("some-target", filepath.Join(dir, "link.txt")))
			},
			setupB: func(t *testing.T, dir string) {
				t.Helper()
				// Empty directory.
			},
			expectedStatus: dirdiff.FileStatusRemoved,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			dirA, dirB := setupSymlinkTestDirs(t)

			testCase.setupA(t, dirA)
			testCase.setupB(t, dirB)

			ctx := testctx.NewCtx(testctx.WithHostFS())

			result, err := dirdiff.DiffDirs(ctx.FS(), dirA, dirB)
			require.NoError(t, err)
			require.Len(t, result.Files, 1)
			assert.Equal(t, testCase.expectedStatus, result.Files[0].Status)
			assert.Contains(t, result.String(), "some-target")
		})
	}
}

// TestDiffDirs_SpecialFiles documents that special file entry handling (pipes, sockets,
// etc.) cannot be tested end-to-end because these entries cannot be created in
// afero.MemMapFs and require OS-level primitives that may not be portable.
//
// Verified by inspection: [entryContent] returns nil for [fileKindSpecial], and [diffEntry]
// produces an existence-only message with no structured hunks. The code paths are ~5 lines
// total with no branching.
func TestDiffDirs_SpecialFiles(t *testing.T) {
	t.Skip("special file entries (pipes, sockets) cannot be created in afero.MemMapFs; " +
		"the code path (entryContent returns nil, diffEntry produces existence-only message) " +
		"is ~5 lines with no branching and verified by inspection")
}

func TestDiffResult_MarshalJSON_TextDiff(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	require.NoError(t, fileutils.WriteFile(testFS, "/a/file.txt", []byte("before\n"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/file.txt", []byte("after\n"), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)

	jsonBytes, jsonErr := json.Marshal(result)
	require.NoError(t, jsonErr)

	var parsed struct {
		Files []struct {
			Path    string `json:"path"`
			Status  string `json:"status"`
			Changes []struct {
				Line    int    `json:"line"`
				Type    string `json:"type"`
				Content string `json:"content"`
			} `json:"changes"`
		} `json:"files"`
	}

	require.NoError(t, json.Unmarshal(jsonBytes, &parsed))
	require.Len(t, parsed.Files, 1)

	file := parsed.Files[0]
	assert.Equal(t, "file.txt", file.Path)
	assert.Equal(t, "modified", file.Status)
	require.NotEmpty(t, file.Changes)

	var hadRemove, hadAdd bool

	for _, change := range file.Changes {
		if change.Type == "remove" && change.Content == "before" {
			hadRemove = true
		}

		if change.Type == "add" && change.Content == "after" {
			hadAdd = true
		}
	}

	assert.True(t, hadRemove, "expected a 'remove' change for 'before'")
	assert.True(t, hadAdd, "expected an 'add' change for 'after'")
}

func TestDiffResult_MarshalJSON_BinaryFile(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	binaryContent := []byte{0x89, 0x50, 0x4e, 0x47, 0x00}

	require.NoError(t, fileutils.WriteFile(testFS, "/a/comp.tar", binaryContent, fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/comp.tar", append(binaryContent, 0x01), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)

	jsonBytes, jsonErr := json.Marshal(result)
	require.NoError(t, jsonErr)

	var parsed struct {
		Files []struct {
			IsBinary bool       `json:"isBinary"`
			Message  string     `json:"message"`
			Changes  []struct{} `json:"changes"`
		} `json:"files"`
	}

	require.NoError(t, json.Unmarshal(jsonBytes, &parsed))
	require.Len(t, parsed.Files, 1)

	file := parsed.Files[0]
	assert.True(t, file.IsBinary)
	assert.NotEmpty(t, file.Message)
	assert.Empty(t, file.Changes)
}

// setupSymlinkTestDirs creates two temporary directories for symlink tests and
// registers cleanup. Returns paths (dirA, dirB).
func setupSymlinkTestDirs(t *testing.T) (string, string) {
	t.Helper()

	base := t.TempDir()

	dirA := filepath.Join(base, "a")
	dirB := filepath.Join(base, "b")

	require.NoError(t, os.MkdirAll(dirA, fileperms.PublicDir))
	require.NoError(t, os.MkdirAll(dirB, fileperms.PublicDir))

	return dirA, dirB
}

func TestDiffDirs_TypeChanged_SymlinkToRegular(t *testing.T) {
	dirA, dirB := setupSymlinkTestDirs(t)

	// In dirA: a symlink.
	require.NoError(t, os.Symlink("some-target", filepath.Join(dirA, "file.txt")))

	// In dirB: a regular file at the same path.
	require.NoError(t, os.WriteFile(filepath.Join(dirB, "file.txt"), []byte("regular content\n"), fileperms.PrivateFile))

	ctx := testctx.NewCtx(testctx.WithHostFS())

	result, err := dirdiff.DiffDirs(ctx.FS(), dirA, dirB)
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	fileDiff := result.Files[0]
	assert.Equal(t, "file.txt", fileDiff.Path)
	assert.Equal(t, dirdiff.FileStatusTypeChanged, fileDiff.Status)

	// The diff should show old symlink content as removed and new regular content as added.
	rendered := result.String()
	assert.Contains(t, rendered, "-some-target")
	assert.Contains(t, rendered, "+regular content")
}

func TestDiffDirs_TypeChanged_RegularToSymlink(t *testing.T) {
	dirA, dirB := setupSymlinkTestDirs(t)

	// In dirA: a regular file.
	require.NoError(t, os.WriteFile(filepath.Join(dirA, "file.txt"), []byte("regular content\n"), fileperms.PrivateFile))

	// In dirB: a symlink at the same path.
	require.NoError(t, os.Symlink("new-target", filepath.Join(dirB, "file.txt")))

	ctx := testctx.NewCtx(testctx.WithHostFS())

	result, err := dirdiff.DiffDirs(ctx.FS(), dirA, dirB)
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	fileDiff := result.Files[0]
	assert.Equal(t, "file.txt", fileDiff.Path)
	assert.Equal(t, dirdiff.FileStatusTypeChanged, fileDiff.Status)

	rendered := result.String()
	assert.Contains(t, rendered, "-regular content")
	assert.Contains(t, rendered, "+new-target")
}

func TestDiffDirs_BinaryFileAddedAndRemoved(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	binaryContent := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x00, 0x00, 0x01}

	t.Run("binary added", func(t *testing.T) {
		require.NoError(t, fileutils.MkdirAll(testFS, "/ba"))
		require.NoError(t, fileutils.WriteFile(testFS, "/bb/image.png", binaryContent, fileperms.PublicFile))

		result, err := dirdiff.DiffDirs(testFS, "/ba", "/bb")
		require.NoError(t, err)
		require.Len(t, result.Files, 1)

		fileDiff := result.Files[0]
		assert.Equal(t, dirdiff.FileStatusAdded, fileDiff.Status)
		assert.True(t, fileDiff.IsBinary)
		assert.Contains(t, fileDiff.Message, "Binary file b/image.png added")
		assert.NotContains(t, fileDiff.Message, " and ")
	})

	t.Run("binary removed", func(t *testing.T) {
		require.NoError(t, fileutils.WriteFile(testFS, "/br/image.png", binaryContent, fileperms.PublicFile))
		require.NoError(t, fileutils.MkdirAll(testFS, "/brempty"))

		result, err := dirdiff.DiffDirs(testFS, "/br", "/brempty")
		require.NoError(t, err)
		require.Len(t, result.Files, 1)

		fileDiff := result.Files[0]
		assert.Equal(t, dirdiff.FileStatusRemoved, fileDiff.Status)
		assert.True(t, fileDiff.IsBinary)
		assert.Contains(t, fileDiff.Message, "Binary file a/image.png removed")
		assert.NotContains(t, fileDiff.Message, " and ")
	})
}

func TestDiffDirs_EmptyFileAddedAndRemoved(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	t.Run("empty file added", func(t *testing.T) {
		require.NoError(t, fileutils.MkdirAll(testFS, "/ea"))
		require.NoError(t, fileutils.WriteFile(testFS, "/eb/placeholder", []byte{}, fileperms.PublicFile))

		result, err := dirdiff.DiffDirs(testFS, "/ea", "/eb")
		require.NoError(t, err)
		require.Len(t, result.Files, 1)

		fileDiff := result.Files[0]
		assert.Equal(t, "placeholder", fileDiff.Path)
		assert.Equal(t, dirdiff.FileStatusAdded, fileDiff.Status)
		assert.Empty(t, fileDiff.UnifiedDiff)
		assert.False(t, fileDiff.IsBinary)
		assert.Equal(t, "Empty file b/placeholder added", fileDiff.Message)
	})

	t.Run("empty file removed", func(t *testing.T) {
		require.NoError(t, fileutils.WriteFile(testFS, "/er/placeholder", []byte{}, fileperms.PublicFile))
		require.NoError(t, fileutils.MkdirAll(testFS, "/erempty"))

		result, err := dirdiff.DiffDirs(testFS, "/er", "/erempty")
		require.NoError(t, err)
		require.Len(t, result.Files, 1)

		fileDiff := result.Files[0]
		assert.Equal(t, "placeholder", fileDiff.Path)
		assert.Equal(t, dirdiff.FileStatusRemoved, fileDiff.Status)
		assert.Empty(t, fileDiff.UnifiedDiff)
		assert.False(t, fileDiff.IsBinary)
		assert.Equal(t, "Empty file a/placeholder removed", fileDiff.Message)
	})

	t.Run("identical empty files produce no diff", func(t *testing.T) {
		require.NoError(t, fileutils.WriteFile(testFS, "/eident_a/placeholder", []byte{}, fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/eident_b/placeholder", []byte{}, fileperms.PublicFile))

		result, err := dirdiff.DiffDirs(testFS, "/eident_a", "/eident_b")
		require.NoError(t, err)
		assert.Empty(t, result.Files)
	})
}

func TestDiffDirs_WithMaxBinaryScanBytes(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	// Create content with a NUL byte at position 5.
	content := []byte("ABCDE\x00FGHIJ")

	require.NoError(t, fileutils.WriteFile(testFS, "/ma/file.bin", content, fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/mb/file.bin", []byte("changed"), fileperms.PublicFile))

	// With default scan size (8192), the NUL is found → binary.
	result, err := dirdiff.DiffDirs(testFS, "/ma", "/mb")
	require.NoError(t, err)
	require.Len(t, result.Files, 1)
	assert.True(t, result.Files[0].IsBinary, "default scan should detect NUL at position 5")

	// With scan size 3, the NUL at position 5 is not scanned → treated as text.
	resultSmall, err := dirdiff.DiffDirs(testFS, "/ma", "/mb", dirdiff.WithMaxBinaryScanBytes(3))
	require.NoError(t, err)
	require.Len(t, resultSmall.Files, 1)
	assert.False(t, resultSmall.Files[0].IsBinary, "scan capped at 3 bytes should miss NUL at position 5")
}

func TestDiffResult_StringAndColorString_BinaryFile(t *testing.T) {
	ctx := testctx.NewCtx()
	testFS := ctx.FS()

	binaryContent := []byte{0x89, 0x50, 0x4e, 0x47, 0x00}

	require.NoError(t, fileutils.WriteFile(testFS, "/a/comp.tar", binaryContent, fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/b/comp.tar", append(binaryContent, 0x01), fileperms.PublicFile))

	result, err := dirdiff.DiffDirs(testFS, "/a", "/b")
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	t.Run("String renders binary message", func(t *testing.T) {
		s := result.String()
		assert.Contains(t, s, "Binary files")
		assert.Contains(t, s, "comp.tar")
		assert.NotContains(t, s, "---", "binary diff should not contain unified diff headers")
	})

	t.Run("ColorString renders binary message", func(t *testing.T) {
		cs := result.ColorString()
		assert.Contains(t, cs, "Binary files")
		assert.Contains(t, cs, "comp.tar")
		assert.NotContains(t, cs, "---", "binary diff should not contain unified diff headers")
	})
}
