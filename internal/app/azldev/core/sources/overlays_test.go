// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applyOverlayToSpecContents(
	t *testing.T, overlay projectconfig.ComponentOverlay, specContents string,
) (string, error) {
	t.Helper()

	spec, err := spec.OpenSpec(strings.NewReader(specContents))
	require.NoError(t, err)

	err = sources.ApplySpecOverlay(overlay, spec)
	if err != nil {
		return "", err
	}

	outputBuffer := new(bytes.Buffer)

	require.NoError(t, spec.Serialize(outputBuffer))

	return outputBuffer.String(), nil
}

func TestApplySpecOverlay(t *testing.T) {
	testCases := []struct {
		name          string
		overlay       projectconfig.ComponentOverlay
		spec          string
		errorExpected bool
		result        string
	}{
		{
			name: "set existing tag",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlaySetSpecTag,
				Tag:   "Name",
				Value: "updated",
			},
			spec: `Name: original
`,
			result: `Name: updated
`,
		},
		{
			name: "set tag in non-existent package",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySetSpecTag,
				Tag:         "Name",
				Value:       "updated",
				PackageName: "i-do-not-exist",
			},
			spec: `Name: original
`,
			errorExpected: true,
		},
		{
			name: "update existing tag",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayUpdateSpecTag,
				Tag:   "Name",
				Value: "updated",
			},
			spec: `Name: original
`,
			result: `Name: updated
`,
		},
		{
			name: "update non-existing tag",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayUpdateSpecTag,
				Tag:   "Vendor",
				Value: "updated",
			},
			spec: `Name: original
`,
			errorExpected: true,
		},
		{
			name: "add tag",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayAddSpecTag,
				Tag:   "BuildRequires",
				Value: "added-package",
			},
			spec: `Name: name
BuildRequires: existing-package
`,
			result: `Name: name
BuildRequires: existing-package
BuildRequires: added-package
`,
		},
		{
			name: "add tag to non-existent package",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayAddSpecTag,
				Tag:         "BuildRequires",
				Value:       "added-package",
				PackageName: "i-do-not-exist",
			},
			spec: `Name: name
`,
			errorExpected: true,
		},
		{
			name: "insert tag after same family",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayInsertSpecTag,
				Tag:   "Source9999",
				Value: "macros.azl.macros",
			},
			spec: `Name: name
Source0: test.tar.gz
BuildRequires: gcc
`,
			result: `Name: name
Source0: test.tar.gz
Source9999: macros.azl.macros
BuildRequires: gcc
`,
		},
		{
			name: "insert tag to non-existent package",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayInsertSpecTag,
				Tag:         "Source9999",
				Value:       "macros.azl.macros",
				PackageName: "i-do-not-exist",
			},
			spec: `Name: name
`,
			errorExpected: true,
		},
		{
			name: "remove tag",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayRemoveSpecTag,
				Tag:   "BuildRequires",
				Value: "problematic-package",
			},
			spec: `Name: name
BuildRequires: okay-package
BuildRequires: problematic-package
BuildRequires: another-okay-package
`,
			result: `Name: name
BuildRequires: okay-package
BuildRequires: another-okay-package
`,
		},
		{
			name: "remove non-existent tag",
			overlay: projectconfig.ComponentOverlay{
				Type:  projectconfig.ComponentOverlayRemoveSpecTag,
				Tag:   "BuildRequires",
				Value: "problematic-package",
			},
			spec: `Name: name
BuildRequires: okay-package
`,
			errorExpected: true,
		},
		{
			name: "prepend lines",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayPrependSpecLines,
				SectionName: "%files",
				PackageName: "mine",
				Lines:       []string{"file1", "file2"},
			},
			spec: `Name: name

%files
some-file

%files -n mine
other-file

%changelog
`,
			result: `Name: name

%files
some-file

%files -n mine
file1
file2
other-file

%changelog
`,
		},
		{
			name: "prepend lines to non-existent section",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayPrependSpecLines,
				SectionName: "%prep",
				Lines:       []string{"do-something"},
			},
			spec: `Name: name
`,
			errorExpected: true,
		},
		{
			name: "append lines",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayAppendSpecLines,
				SectionName: "%description",
				Lines:       []string{"line1"},
			},
			spec: `Name: name

%description
some-description
that keeps going

%changelog
`,
			result: `Name: name

%description
some-description
that keeps going

line1
%changelog
`,
		},
		{
			name: "append lines to non-existent section",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayAppendSpecLines,
				SectionName: "%prep",
				Lines:       []string{"do-something"},
			},
			spec: `Name: name
`,
			errorExpected: true,
		},
		{
			name: "search and replace",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInSpec,
				SectionName: "%build",
				Regex:       `--enable-feature\w*\s*`,
				Replacement: "",
			},
			spec: `Name: name

%build
./configure --enable-feature1 --enable-feature2 --enable-other-thing

%changelog
`,
			result: `Name: name

%build
./configure --enable-other-thing

%changelog
`,
		},
		{
			name: "search and replace with no matches",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInSpec,
				SectionName: "%build",
				Regex:       `--enable-foo`,
				Replacement: "",
			},
			spec: `Name: name

%build
./configure --enable-baz

%changelog
`,
			errorExpected: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			updatedContents, err := applyOverlayToSpecContents(t, testCase.overlay, testCase.spec)
			if testCase.errorExpected {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.result, updatedContents)
		})
	}
}

func TestApplyNonSpecOverlay(t *testing.T) {
	testCases := []struct {
		name           string
		overlay        projectconfig.ComponentOverlay
		existingFile   string
		existingSource string
		errorExpected  bool
		result         string
	}{
		{
			name: "prepend lines to file",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayPrependLinesToFile,
				Filename: "test.txt",
				Lines:    []string{"# Added header", "# Second line"},
			},
			existingFile: "original content\n",
			result:       "# Added header\n# Second line\noriginal content\n",
		},
		{
			name: "prepend lines to non-existent file",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayPrependLinesToFile,
				Filename: "does-not-exist.txt",
				Lines:    []string{"# Header"},
			},
			errorExpected: true,
		},
		{
			name: "prepend to spec file is rejected",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayPrependLinesToFile,
				Filename: "test.spec",
				Lines:    []string{"# Header"},
			},
			existingFile:  "Name: test\n",
			errorExpected: true,
		},
		{
			name: "search and replace in file",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
				Filename:    "config.txt",
				Regex:       `DEBUG=false`,
				Replacement: "DEBUG=true",
			},
			existingFile: "# Config\nDEBUG=false\nOTHER=value\n",
			result:       "# Config\nDEBUG=true\nOTHER=value\n",
		},
		{
			name: "search and replace with regex pattern",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
				Filename:    "version.txt",
				Regex:       `version\s*=\s*\d+\.\d+`,
				Replacement: "version = 2.0",
			},
			existingFile: "version = 1.5\n",
			result:       "version = 2.0\n",
		},
		{
			name: "search and replace no match",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
				Filename:    "test.txt",
				Regex:       `not-found-pattern`,
				Replacement: "replacement",
			},
			existingFile:  "some content\n",
			errorExpected: true,
		},
		{
			name: "search and replace on spec file is rejected",
			overlay: projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
				Filename:    "test.spec",
				Regex:       `Name:`,
				Replacement: "Name:",
			},
			existingFile:  "Name: test\n",
			errorExpected: true,
		},
		{
			name: "add file copies source to destination",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayAddFile,
				Filename: "new-file.txt",
				Source:   "/source/original.txt",
			},
			existingSource: "source file content\n",
			result:         "source file content\n",
		},
		{
			name: "add file with non-existent source",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayAddFile,
				Filename: "new-file.txt",
				Source:   "/source/does-not-exist.txt",
			},
			errorExpected: true,
		},
		{
			name: "add file to spec file is rejected",
			overlay: projectconfig.ComponentOverlay{
				Type:     projectconfig.ComponentOverlayAddFile,
				Filename: "test.spec",
				Source:   "/source/original.txt",
			},
			existingSource: "source content\n",
			errorExpected:  true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := testctx.NewCtx()
			testFS := ctx.FS()

			// Set up source directory.
			sourceDir := "/sources"
			require.NoError(t, testFS.MkdirAll(sourceDir, 0o755))

			// Create existing file if specified.
			if testCase.existingFile != "" {
				filePath := sourceDir + "/" + testCase.overlay.Filename
				require.NoError(t, fileutils.WriteFile(testFS, filePath, []byte(testCase.existingFile), 0o644))
			}

			// Create source file for file-add overlays.
			if testCase.existingSource != "" {
				require.NoError(t, testFS.MkdirAll("/source", 0o755))
				require.NoError(t, fileutils.WriteFile(testFS, testCase.overlay.Source, []byte(testCase.existingSource), 0o644))
			}

			// Apply the overlay.
			err := sources.ApplyOverlayToSources(ctx, testFS, testCase.overlay, sourceDir, "")

			if testCase.errorExpected {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			// Read and verify the result.
			resultPath := sourceDir + "/" + testCase.overlay.Filename
			resultContent, err := fileutils.ReadFile(testFS, resultPath)
			require.NoError(t, err)
			assert.Equal(t, testCase.result, string(resultContent))
		})
	}
}

func TestApplyDestructiveNonSpecOverlay(t *testing.T) {
	const testSourceDir = "/sources"

	t.Run("remove file", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		filePath := testSourceDir + "/to-delete.txt"
		require.NoError(t, fileutils.WriteFile(testFS, filePath, []byte("content\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemoveFile,
			Filename: "to-delete.txt",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.NoError(t, err)

		exists, err := fileutils.Exists(testFS, filePath)
		require.NoError(t, err)
		assert.False(t, exists, "file should have been removed")
	})

	t.Run("remove non-existent file", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemoveFile,
			Filename: "does-not-exist.txt",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.Error(t, err)
	})

	t.Run("rename file", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		oldPath := testSourceDir + "/old-name.txt"
		newPath := testSourceDir + "/new-name.txt"
		fileContent := "file content\n"
		require.NoError(t, fileutils.WriteFile(testFS, oldPath, []byte(fileContent), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:        projectconfig.ComponentOverlayRenameFile,
			Filename:    "old-name.txt",
			Replacement: "new-name.txt",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.NoError(t, err)

		// Verify old file no longer exists.
		exists, err := fileutils.Exists(testFS, oldPath)
		require.NoError(t, err)
		assert.False(t, exists, "old file should have been removed")

		// Verify new file exists with correct content.
		resultContent, err := fileutils.ReadFile(testFS, newPath)
		require.NoError(t, err)
		assert.Equal(t, fileContent, string(resultContent))
	})

	t.Run("rename non-existent file", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		overlay := projectconfig.ComponentOverlay{
			Type:        projectconfig.ComponentOverlayRenameFile,
			Filename:    "does-not-exist.txt",
			Replacement: "new-name.txt",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.Error(t, err)
	})

	t.Run("remove spec file is rejected", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		filePath := testSourceDir + "/test.spec"
		require.NoError(t, fileutils.WriteFile(testFS, filePath, []byte("Name: test\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemoveFile,
			Filename: "test.spec",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.Error(t, err)
	})

	t.Run("rename spec file is rejected", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		filePath := testSourceDir + "/test.spec"
		require.NoError(t, fileutils.WriteFile(testFS, filePath, []byte("Name: test\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:        projectconfig.ComponentOverlayRenameFile,
			Filename:    "test.spec",
			Replacement: "renamed.spec",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.Error(t, err)
	})
}

func TestApplyNonSpecOverlayWithGlob(t *testing.T) {
	const testSourceDir = "/sources"

	t.Run("glob pattern prepends to multiple files", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		// Create multiple .txt files.
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/file1.txt", []byte("content1\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/file2.txt", []byte("content2\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/other.md", []byte("not matched\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayPrependLinesToFile,
			Filename: "*.txt",
			Lines:    []string{"# Header"},
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.NoError(t, err)

		// Verify both .txt files were modified.
		content1, err := fileutils.ReadFile(testFS, testSourceDir+"/file1.txt")
		require.NoError(t, err)
		assert.Equal(t, "# Header\ncontent1\n", string(content1))

		content2, err := fileutils.ReadFile(testFS, testSourceDir+"/file2.txt")
		require.NoError(t, err)
		assert.Equal(t, "# Header\ncontent2\n", string(content2))

		// Verify .md file was not modified.
		contentMd, err := fileutils.ReadFile(testFS, testSourceDir+"/other.md")
		require.NoError(t, err)
		assert.Equal(t, "not matched\n", string(contentMd))
	})

	t.Run("globstar pattern matches files in subdirectories", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir+"/subdir", 0o755))

		// Create files at different levels.
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/root.conf", []byte("DEBUG=false\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/subdir/nested.conf", []byte("DEBUG=false\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
			Filename:    "**/*.conf",
			Regex:       `DEBUG=false`,
			Replacement: "DEBUG=true",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.NoError(t, err)

		// Verify both files were modified.
		contentRoot, err := fileutils.ReadFile(testFS, testSourceDir+"/root.conf")
		require.NoError(t, err)
		assert.Equal(t, "DEBUG=true\n", string(contentRoot))

		contentNested, err := fileutils.ReadFile(testFS, testSourceDir+"/subdir/nested.conf")
		require.NoError(t, err)
		assert.Equal(t, "DEBUG=true\n", string(contentNested))
	})

	t.Run("glob pattern removes multiple files", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		// Create backup files to remove.
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/file1.bak", []byte("backup1\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/file2.bak", []byte("backup2\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/keep.txt", []byte("keep this\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemoveFile,
			Filename: "*.bak",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.NoError(t, err)

		// Verify .bak files were removed.
		exists1, err := fileutils.Exists(testFS, testSourceDir+"/file1.bak")
		require.NoError(t, err)
		assert.False(t, exists1, "file1.bak should have been removed")

		exists2, err := fileutils.Exists(testFS, testSourceDir+"/file2.bak")
		require.NoError(t, err)
		assert.False(t, exists2, "file2.bak should have been removed")

		// Verify .txt file was kept.
		existsKeep, err := fileutils.Exists(testFS, testSourceDir+"/keep.txt")
		require.NoError(t, err)
		assert.True(t, existsKeep, "keep.txt should still exist")
	})

	t.Run("glob pattern with no matches returns error", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		// Create a file that won't match the pattern.
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/file.txt", []byte("content\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayPrependLinesToFile,
			Filename: "*.nonexistent",
			Lines:    []string{"# Header"},
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.Error(t, err)
	})

	t.Run("single-file overlay type rejects multiple glob matches", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		// Create multiple files that will match the pattern.
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/file1.txt", []byte("content1\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/file2.txt", []byte("content2\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:        projectconfig.ComponentOverlayRenameFile,
			Filename:    "*.txt",
			Replacement: "renamed.txt",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.Error(t, err)
	})

	t.Run("glob pattern skips spec files and applies to other matches", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		// Create a mix of .spec and non-.spec files that match the pattern.
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/config.cfg", []byte("value=old\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/test.spec", []byte("Name: test\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:        projectconfig.ComponentOverlaySearchAndReplaceInFile,
			Filename:    "*",
			Regex:       `value=old`,
			Replacement: "value=new",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.NoError(t, err)

		// Verify non-.spec file was modified.
		contentCfg, err := fileutils.ReadFile(testFS, testSourceDir+"/config.cfg")
		require.NoError(t, err)
		assert.Equal(t, "value=new\n", string(contentCfg))

		// Verify .spec file was NOT modified (skipped, not processed).
		contentSpec, err := fileutils.ReadFile(testFS, testSourceDir+"/test.spec")
		require.NoError(t, err)
		assert.Equal(t, "Name: test\n", string(contentSpec))
	})

	t.Run("glob pattern matching only spec files returns error", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		// Create only .spec files.
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/test.spec", []byte("Name: test\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/other.spec", []byte("Name: other\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayPrependLinesToFile,
			Filename: "*.spec",
			Lines:    []string{"# Header"},
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, "")
		require.Error(t, err)
	})
}

func TestApplyAddPatchOverlay(t *testing.T) {
	const (
		testSourceDir = "/sources"
		testSpecPath  = "/sources/test.spec"
	)

	t.Run("adds patch with PatchN tag when no patches exist", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\nVersion: 1.0\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))

		// Create source patch file.
		require.NoError(t, testFS.MkdirAll("/patches", 0o755))
		require.NoError(t, fileutils.WriteFile(testFS, "/patches/fix-foo.patch", []byte("patch content\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:   projectconfig.ComponentOverlayAddPatch,
			Source: "/patches/fix-foo.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.NoError(t, err)

		// Verify spec was updated with Patch0 tag.
		updatedSpec, err := fileutils.ReadFile(testFS, testSpecPath)
		require.NoError(t, err)
		assert.Contains(t, string(updatedSpec), "Patch0: fix-foo.patch")

		// Verify patch file was copied.
		patchContent, err := fileutils.ReadFile(testFS, testSourceDir+"/fix-foo.patch")
		require.NoError(t, err)
		assert.Equal(t, "patch content\n", string(patchContent))
	})

	t.Run("adds patch with next PatchN number", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\nPatch0: existing.patch\nPatch1: other.patch\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))

		require.NoError(t, testFS.MkdirAll("/patches", 0o755))
		require.NoError(t, fileutils.WriteFile(testFS, "/patches/fix-bar.patch", []byte("new patch\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:   projectconfig.ComponentOverlayAddPatch,
			Source: "/patches/fix-bar.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.NoError(t, err)

		updatedSpec, err := fileutils.ReadFile(testFS, testSpecPath)
		require.NoError(t, err)
		assert.Contains(t, string(updatedSpec), "Patch2: fix-bar.patch")
	})

	t.Run("adds patch with non-contiguous PatchN numbers", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\nPatch0: a.patch\nPatch5: b.patch\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))

		require.NoError(t, testFS.MkdirAll("/patches", 0o755))
		require.NoError(t, fileutils.WriteFile(testFS, "/patches/c.patch", []byte("content\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:   projectconfig.ComponentOverlayAddPatch,
			Source: "/patches/c.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.NoError(t, err)

		updatedSpec, err := fileutils.ReadFile(testFS, testSpecPath)
		require.NoError(t, err)
		assert.Contains(t, string(updatedSpec), "Patch6: c.patch")
	})

	t.Run("adds patch to patchlist section", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\n\n%patchlist\nexisting.patch\n\n%build\nmake\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))

		require.NoError(t, testFS.MkdirAll("/patches", 0o755))
		require.NoError(t, fileutils.WriteFile(testFS, "/patches/new.patch", []byte("content\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:   projectconfig.ComponentOverlayAddPatch,
			Source: "/patches/new.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.NoError(t, err)

		updatedSpec, err := fileutils.ReadFile(testFS, testSpecPath)
		require.NoError(t, err)

		specStr := string(updatedSpec)
		assert.Contains(t, specStr, "new.patch")
		// Should NOT contain a PatchN tag since %patchlist was used.
		assert.NotContains(t, specStr, "Patch0:")
	})

	t.Run("uses explicit file field as destination name", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))

		require.NoError(t, testFS.MkdirAll("/patches", 0o755))
		require.NoError(t, fileutils.WriteFile(testFS, "/patches/original-name.patch", []byte("content\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayAddPatch,
			Source:   "/patches/original-name.patch",
			Filename: "custom-name.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.NoError(t, err)

		updatedSpec, err := fileutils.ReadFile(testFS, testSpecPath)
		require.NoError(t, err)
		assert.Contains(t, string(updatedSpec), "Patch0: custom-name.patch")

		// File should be at the custom name.
		patchContent, err := fileutils.ReadFile(testFS, testSourceDir+"/custom-name.patch")
		require.NoError(t, err)
		assert.Equal(t, "content\n", string(patchContent))
	})

	t.Run("fails when patch file already exists", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))

		// Pre-create the destination file.
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/conflict.patch", []byte("existing\n"), 0o644))

		require.NoError(t, testFS.MkdirAll("/patches", 0o755))
		require.NoError(t, fileutils.WriteFile(testFS, "/patches/conflict.patch", []byte("new\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:   projectconfig.ComponentOverlayAddPatch,
			Source: "/patches/conflict.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})
}

func TestApplyRemovePatchOverlay(t *testing.T) {
	const (
		testSourceDir = "/sources"
		testSpecPath  = "/sources/test.spec"
	)

	t.Run("removes patch from PatchN tags", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\nPatch0: keep.patch\nPatch1: remove-me.patch\nPatch2: also-keep.patch\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/remove-me.patch", []byte("patch\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemovePatch,
			Filename: "remove-me.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.NoError(t, err)

		updatedSpec, err := fileutils.ReadFile(testFS, testSpecPath)
		require.NoError(t, err)

		specStr := string(updatedSpec)
		assert.NotContains(t, specStr, "remove-me.patch")
		assert.Contains(t, specStr, "keep.patch")
		assert.Contains(t, specStr, "also-keep.patch")

		// Verify file was removed.
		exists, err := fileutils.Exists(testFS, testSourceDir+"/remove-me.patch")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("removes patch from patchlist", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\n\n%patchlist\nkeep.patch\nremove-me.patch\n\n%build\nmake\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/remove-me.patch", []byte("patch\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemovePatch,
			Filename: "remove-me.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.NoError(t, err)

		updatedSpec, err := fileutils.ReadFile(testFS, testSpecPath)
		require.NoError(t, err)

		specStr := string(updatedSpec)
		assert.NotContains(t, specStr, "remove-me.patch")
		assert.Contains(t, specStr, "keep.patch")
	})

	t.Run("removes from both tags and patchlist", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		// A spec with both PatchN tags and a %patchlist (unusual but possible).
		specContent := "Name: test\nPatch0: fix-a.patch\n\n%patchlist\nfix-a.patch\n\n%build\nmake\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/fix-a.patch", []byte("a\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemovePatch,
			Filename: "fix-a.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.NoError(t, err)

		updatedSpec, err := fileutils.ReadFile(testFS, testSpecPath)
		require.NoError(t, err)

		specStr := string(updatedSpec)
		assert.NotContains(t, specStr, "fix-a.patch")
	})

	t.Run("returns error when no patches match", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\nPatch0: keep.patch\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemovePatch,
			Filename: "nonexistent.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no patches matching")
	})

	t.Run("fails when patch doesn't exist on disk", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		// Patch is declared in spec but file doesn't exist in sources.
		specContent := "Name: test\nPatch0: ghost.patch\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemovePatch,
			Filename: "ghost.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.Error(t, err)
	})

	t.Run("glob removes multiple patches from PatchN tags", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\nPatch0: CVE-001.patch\nPatch1: CVE-002.patch\nPatch2: keep.patch\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/CVE-001.patch", []byte("a\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/CVE-002.patch", []byte("b\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/keep.patch", []byte("c\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemovePatch,
			Filename: "CVE-*.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.NoError(t, err)

		updatedSpec, err := fileutils.ReadFile(testFS, testSpecPath)
		require.NoError(t, err)

		specStr := string(updatedSpec)
		assert.NotContains(t, specStr, "CVE-001.patch")
		assert.NotContains(t, specStr, "CVE-002.patch")
		assert.Contains(t, specStr, "keep.patch")

		// Verify CVE patches were removed from disk.
		exists, err := fileutils.Exists(testFS, testSourceDir+"/CVE-001.patch")
		require.NoError(t, err)
		assert.False(t, exists)

		exists, err = fileutils.Exists(testFS, testSourceDir+"/CVE-002.patch")
		require.NoError(t, err)
		assert.False(t, exists)

		// Verify keep.patch still exists.
		exists, err = fileutils.Exists(testFS, testSourceDir+"/keep.patch")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("glob removes multiple patches from patchlist", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\n\n%patchlist\nCVE-001.patch\nkeep.patch\nCVE-002.patch\n\n%build\nmake\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/CVE-001.patch", []byte("a\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/CVE-002.patch", []byte("b\n"), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/keep.patch", []byte("c\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemovePatch,
			Filename: "CVE-*.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.NoError(t, err)

		updatedSpec, err := fileutils.ReadFile(testFS, testSpecPath)
		require.NoError(t, err)

		specStr := string(updatedSpec)
		assert.NotContains(t, specStr, "CVE-001.patch")
		assert.NotContains(t, specStr, "CVE-002.patch")
		assert.Contains(t, specStr, "keep.patch")
	})

	t.Run("glob with no file matches errors", func(t *testing.T) {
		ctx := testctx.NewCtx()
		testFS := ctx.FS()

		require.NoError(t, testFS.MkdirAll(testSourceDir, 0o755))

		specContent := "Name: test\nPatch0: fix-build.patch\n"
		require.NoError(t, fileutils.WriteFile(testFS, testSpecPath, []byte(specContent), 0o644))
		require.NoError(t, fileutils.WriteFile(testFS, testSourceDir+"/fix-build.patch", []byte("a\n"), 0o644))

		overlay := projectconfig.ComponentOverlay{
			Type:     projectconfig.ComponentOverlayRemovePatch,
			Filename: "CVE-*.patch",
		}

		err := sources.ApplyOverlayToSources(ctx, testFS, overlay, testSourceDir, testSpecPath)
		require.Error(t, err)
	})
}

func TestApplyRemoveSectionOverlay(t *testing.T) {
	t.Run("removes section from spec", func(t *testing.T) {
		specContent := `Name: test
Version: 1.0

%generate_buildrequires
%cargo_generate_buildrequires

%build
make
`
		overlay := projectconfig.ComponentOverlay{
			Type:        projectconfig.ComponentOverlayRemoveSection,
			SectionName: "%generate_buildrequires",
		}

		result, err := applyOverlayToSpecContents(t, overlay, specContent)
		require.NoError(t, err)

		assert.NotContains(t, result, "%generate_buildrequires")
		assert.NotContains(t, result, "%cargo_generate_buildrequires")
		assert.Contains(t, result, "%build")
		assert.Contains(t, result, "make")
	})

	t.Run("removes section scoped by package", func(t *testing.T) {
		specContent := `Name: test

%files
/usr/bin/test

%files devel
/usr/include/test.h

%files libs
/usr/lib/libtest.so
`
		overlay := projectconfig.ComponentOverlay{
			Type:        projectconfig.ComponentOverlayRemoveSection,
			SectionName: "%files",
			PackageName: "devel",
		}

		result, err := applyOverlayToSpecContents(t, overlay, specContent)
		require.NoError(t, err)

		assert.Contains(t, result, "/usr/bin/test")
		assert.NotContains(t, result, "/usr/include/test.h")
		assert.Contains(t, result, "/usr/lib/libtest.so")
	})

	t.Run("fails when section does not exist", func(t *testing.T) {
		specContent := `Name: test
Version: 1.0

%build
make
`
		overlay := projectconfig.ComponentOverlay{
			Type:        projectconfig.ComponentOverlayRemoveSection,
			SectionName: "%check",
		}

		_, err := applyOverlayToSpecContents(t, overlay, specContent)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}
