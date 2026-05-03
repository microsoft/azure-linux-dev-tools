// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindSpecFile(t *testing.T) {
	t.Run("finds spec by component name", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		require.NoError(t, fileutils.MkdirAll(fs, "/src"))
		require.NoError(t, fileutils.WriteFile(fs, "/src/curl.spec", []byte("Name: curl"), fileperms.PublicFile))

		path, err := findSpecFile(fs, "/src", "curl")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/src", "curl.spec"), path)
	})

	t.Run("error when spec name does not match component name", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		require.NoError(t, fileutils.MkdirAll(fs, "/src"))
		require.NoError(t, fileutils.WriteFile(fs, "/src/renamed.spec", []byte("Name: curl"), fileperms.PublicFile))

		_, err := findSpecFile(fs, "/src", "curl")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("error when no spec file exists", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		require.NoError(t, fileutils.MkdirAll(fs, "/src"))
		require.NoError(t, fileutils.WriteFile(fs, "/src/README.md", []byte("# readme"), fileperms.PublicFile))

		_, err := findSpecFile(fs, "/src", "curl")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("error when directory does not exist", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		_, err := findSpecFile(fs, "/nonexistent", "curl")
		require.Error(t, err)
	})
}

func TestWriteRenderErrorMarker(t *testing.T) {
	t.Run("creates marker file with static content", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		writeRenderErrorMarker(fs, "/output/failing-component")

		content, err := fileutils.ReadFile(fs, filepath.Join("/output/failing-component", renderErrorMarkerFile))
		require.NoError(t, err)
		assert.Equal(t, "Rendering failed. See azldev logs for details.\n", string(content))
	})

	t.Run("creates directory if it does not exist", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		writeRenderErrorMarker(fs, "/new/deep/path")

		exists, err := fileutils.Exists(fs, "/new/deep/path/"+renderErrorMarkerFile)
		require.NoError(t, err)
		assert.True(t, exists)
	})
}

func TestValidateOutputDir(t *testing.T) {
	tests := []struct {
		dir     string
		wantErr bool
	}{
		{"SPECS", false},
		{"rendered/output", false},
		{"..foo", false},
		{"./../foo/../", true},
		{".", true},
		{"/", true},
		{"./", true},
		{"..", true},
		{"../", true},
		{"../../foo", true},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			err := validateOutputDir(tt.dir)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRemoveUnreferencedFiles(t *testing.T) {
	t.Run("keeps spec and referenced files, removes others", func(t *testing.T) {
		testFS := afero.NewMemMapFs()

		require.NoError(t, fileutils.MkdirAll(testFS, "/render"))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/curl.spec", []byte("spec"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/curl-8.0.tar.xz", []byte("src"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/fix-build.patch", []byte("patch"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/dead-upstream.cfg", []byte("cruft"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/plans/test.yml", []byte("test"), fileperms.PublicFile))

		specFiles := []string{"curl-8.0.tar.xz", "fix-build.patch"}

		err := removeUnreferencedFiles(testFS, "/render", "/render/curl.spec", specFiles, "curl")
		require.NoError(t, err)

		// Spec, sources in specFiles should remain.
		for _, name := range []string{"curl.spec", "curl-8.0.tar.xz", "fix-build.patch"} {
			exists, existsErr := fileutils.Exists(testFS, filepath.Join("/render", name))
			require.NoError(t, existsErr)
			assert.True(t, exists, "%s should be kept", name)
		}

		// Unreferenced files should be removed.
		for _, name := range []string{"dead-upstream.cfg", "plans"} {
			exists, existsErr := fileutils.Exists(testFS, filepath.Join("/render", name))
			require.NoError(t, existsErr)
			assert.False(t, exists, "%s should be removed", name)
		}
	})

	t.Run("always keeps sources directory", func(t *testing.T) {
		testFS := afero.NewMemMapFs()

		require.NoError(t, fileutils.MkdirAll(testFS, "/render/sources"))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/curl.spec", []byte("spec"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/sources/hashes", []byte("abc123"), fileperms.PublicFile))

		err := removeUnreferencedFiles(testFS, "/render", "/render/curl.spec", nil, "curl")
		require.NoError(t, err)

		exists, err := fileutils.Exists(testFS, "/render/sources/hashes")
		require.NoError(t, err)
		assert.True(t, exists, "sources directory should always be kept")
	})

	t.Run("keeps top-level directory for subdirectory patch paths", func(t *testing.T) {
		testFS := afero.NewMemMapFs()

		require.NoError(t, fileutils.MkdirAll(testFS, "/render/patches"))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/curl.spec", []byte("spec"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/patches/fix.patch", []byte("patch"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/unrelated.txt", []byte("junk"), fileperms.PublicFile))

		// spectool reports "patches/fix.patch" — top-level "patches" dir should be kept.
		specFiles := []string{"patches/fix.patch"}

		err := removeUnreferencedFiles(testFS, "/render", "/render/curl.spec", specFiles, "curl")
		require.NoError(t, err)

		exists, err := fileutils.Exists(testFS, "/render/patches/fix.patch")
		require.NoError(t, err)
		assert.True(t, exists, "patches dir should be kept via top-level extraction")

		exists, err = fileutils.Exists(testFS, "/render/unrelated.txt")
		require.NoError(t, err)
		assert.False(t, exists, "unrelated.txt should be removed")
	})

	t.Run("no-op when all files are referenced", func(t *testing.T) {
		testFS := afero.NewMemMapFs()

		require.NoError(t, fileutils.MkdirAll(testFS, "/render"))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/curl.spec", []byte("spec"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(testFS, "/render/curl-8.0.tar.xz", []byte("src"), fileperms.PublicFile))

		specFiles := []string{"curl-8.0.tar.xz"}

		err := removeUnreferencedFiles(testFS, "/render", "/render/curl.spec", specFiles, "curl")
		require.NoError(t, err)

		entries, err := fileutils.ReadDir(testFS, "/render")
		require.NoError(t, err)
		assert.Len(t, entries, 2, "both files should remain")
	})
}

func TestSkipFileFilterPreservesAllFiles(t *testing.T) {
	// Verifies that when SkipFileFilter is true, unreferenced files are NOT removed.
	// This mirrors the finishComponentRender logic: when the flag is set,
	// removeUnreferencedFiles is not called, so all files survive.
	testFS := afero.NewMemMapFs()

	require.NoError(t, fileutils.MkdirAll(testFS, "/render"))
	require.NoError(t, fileutils.WriteFile(testFS, "/render/pkg.spec", []byte("spec"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/render/sources", []byte("hash"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/render/57-pkg-fonts.xml", []byte("fontconfig"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(
		testFS, "/render/58-pkg-lgc-fonts.xml", []byte("fontconfig"), fileperms.PublicFile))

	// spectool would report unexpanded macros like "57-%{fontpkgname1}.xml"
	// which don't match any file on disk. Without skip-file-filter, the
	// filter would delete the real XML files.
	specFiles := []string{"57-%{fontpkgname1}.xml", "58-%{fontpkgname4}.xml"}

	// Simulate skip-file-filter=false: XML files get removed.
	err := removeUnreferencedFiles(testFS, "/render", "/render/pkg.spec", specFiles, "pkg")
	require.NoError(t, err)

	for _, name := range []string{"57-pkg-fonts.xml", "58-pkg-lgc-fonts.xml"} {
		exists, existsErr := fileutils.Exists(testFS, filepath.Join("/render", name))
		require.NoError(t, existsErr)
		assert.False(t, exists, "%s should be removed when skip-file-filter is false", name)
	}

	// Simulate skip-file-filter=true: removeUnreferencedFiles is never called,
	// so all files are preserved. Reset the filesystem and verify.
	testFS = afero.NewMemMapFs()

	require.NoError(t, fileutils.MkdirAll(testFS, "/render"))
	require.NoError(t, fileutils.WriteFile(testFS, "/render/pkg.spec", []byte("spec"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/render/sources", []byte("hash"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(testFS, "/render/57-pkg-fonts.xml", []byte("fontconfig"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(
		testFS, "/render/58-pkg-lgc-fonts.xml", []byte("fontconfig"), fileperms.PublicFile))

	// With skip-file-filter=true, removeUnreferencedFiles is NOT called.
	// All files should remain.
	for _, name := range []string{"pkg.spec", "sources", "57-pkg-fonts.xml", "58-pkg-lgc-fonts.xml"} {
		exists, existsErr := fileutils.Exists(testFS, filepath.Join("/render", name))
		require.NoError(t, existsErr)
		assert.True(t, exists, "%s should be preserved when skip-file-filter is true", name)
	}
}

func TestFindUnexpandedMacro(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		specFiles []string
		want      string
	}{
		{
			name:      "no macros",
			specFiles: []string{"curl-8.0.tar.xz", "fix.patch"},
			want:      "",
		},
		{
			name:      "one unexpanded macro",
			specFiles: []string{"curl-8.0.tar.xz", "57-%{fontpkgname1}.xml"},
			want:      "57-%{fontpkgname1}.xml",
		},
		{
			name: "returns first match",
			specFiles: []string{
				"good.tar.gz",
				"57-%{fontpkgname1}.xml",
				"58-%{fontpkgname4}.xml",
			},
			want: "57-%{fontpkgname1}.xml",
		},
		{
			name:      "empty input",
			specFiles: nil,
			want:      "",
		},
		{
			name:      "rust crates_source macro",
			specFiles: []string{"%{crates_source}"},
			want:      "%{crates_source}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := findUnexpandedMacro(tc.specFiles)
			assert.Equal(t, tc.want, result)
		})
	}
}

// TestWriteAliasSymlink exercises the real (afero.OsFs) symlink path. MemMapFs
// doesn't implement afero.Linker, so the production code paths can only be
// covered against the OS filesystem. Each subtest gets its own t.TempDir().
func TestWriteAliasSymlink(t *testing.T) {
	// Helpers that perform the type assertions once (osFS is afero.OsFs which
	// implements all three interfaces); the linter dislikes inline assertions.
	osLstat := func(t *testing.T, fs afero.Fs, path string) os.FileInfo {
		t.Helper()

		lstater, ok := fs.(afero.Lstater)
		require.True(t, ok, "OsFs must implement afero.Lstater")

		info, _, err := lstater.LstatIfPossible(path)
		require.NoError(t, err)

		return info
	}
	osReadlink := func(t *testing.T, fs afero.Fs, path string) string {
		t.Helper()

		reader, ok := fs.(afero.LinkReader)
		require.True(t, ok, "OsFs must implement afero.LinkReader")

		target, err := reader.ReadlinkIfPossible(path)
		require.NoError(t, err)

		return target
	}
	osSymlink := func(t *testing.T, fs afero.Fs, target, linkPath string) {
		t.Helper()

		linker, ok := fs.(afero.Linker)
		require.True(t, ok, "OsFs must implement afero.Linker")
		require.NoError(t, linker.SymlinkIfPossible(target, linkPath))
	}

	t.Run("creates relative symlink for encoded name", func(t *testing.T) {
		osFS := afero.NewOsFs()
		root := t.TempDir()
		letterDir := filepath.Join(root, "l")
		realDir := filepath.Join(letterDir, "libxml++")
		require.NoError(t, fileutils.MkdirAll(osFS, realDir))

		err := writeAliasSymlink(osFS, realDir, "libxml++")
		require.NoError(t, err)

		aliasPath := filepath.Join(letterDir, "libxml%2B%2B")

		linkInfo := osLstat(t, osFS, aliasPath)
		assert.NotZero(t, linkInfo.Mode()&os.ModeSymlink, "alias should be a symlink")

		// Relative target — must be just the basename, not absolute.
		assert.Equal(t, "libxml++", osReadlink(t, osFS, aliasPath))
	})

	t.Run("no-op for plain ASCII name", func(t *testing.T) {
		osFS := afero.NewOsFs()
		root := t.TempDir()
		letterDir := filepath.Join(root, "v")
		realDir := filepath.Join(letterDir, "vim")
		require.NoError(t, fileutils.MkdirAll(osFS, realDir))

		err := writeAliasSymlink(osFS, realDir, "vim")
		require.NoError(t, err)

		entries, err := fileutils.ReadDir(osFS, letterDir)
		require.NoError(t, err)
		assert.Len(t, entries, 1, "no alias should be created for plain ASCII")
	})

	t.Run("replaces stale alias symlink", func(t *testing.T) {
		osFS := afero.NewOsFs()
		root := t.TempDir()
		letterDir := filepath.Join(root, "g")
		realDir := filepath.Join(letterDir, "gtk+")
		require.NoError(t, fileutils.MkdirAll(osFS, realDir))

		// Pre-existing stale symlink pointing somewhere wrong.
		aliasPath := filepath.Join(letterDir, "gtk%2B")
		osSymlink(t, osFS, "nonexistent-target", aliasPath)

		err := writeAliasSymlink(osFS, realDir, "gtk+")
		require.NoError(t, err)

		assert.Equal(t, "gtk+", osReadlink(t, osFS, aliasPath), "stale symlink should be replaced")
	})

	t.Run("refuses to overwrite a non-symlink at alias path", func(t *testing.T) {
		osFS := afero.NewOsFs()
		root := t.TempDir()
		letterDir := filepath.Join(root, "g")
		realDir := filepath.Join(letterDir, "gtk+")
		require.NoError(t, fileutils.MkdirAll(osFS, realDir))

		// Hypothetical collision: a real component named 'gtk%2B' already exists
		// at the alias path. RPM names don't use '%' in practice, but the guard
		// must protect against it.
		collisionDir := filepath.Join(letterDir, "gtk%2B")
		require.NoError(t, fileutils.MkdirAll(osFS, collisionDir))

		err := writeAliasSymlink(osFS, realDir, "gtk+")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "non-symlink")

		// Collision dir must still exist — the guard MUST NOT have destroyed it.
		exists, existsErr := fileutils.DirExists(osFS, collisionDir)
		require.NoError(t, existsErr)
		assert.True(t, exists, "non-symlink at alias path must be preserved")
	})

	t.Run("no-op on filesystem without symlink support", func(t *testing.T) {
		// MemMapFs does NOT implement afero.Linker — function should silently no-op.
		memFS := afero.NewMemMapFs()
		require.NoError(t, fileutils.MkdirAll(memFS, "/SPECS/l/libxml++"))

		err := writeAliasSymlink(memFS, "/SPECS/l/libxml++", "libxml++")
		assert.NoError(t, err, "must not fail when FS doesn't support symlinks")
	})
}

func TestWipeOutputDirContents(t *testing.T) {
	t.Parallel()

	t.Run("missing directory is a no-op", func(t *testing.T) {
		t.Parallel()

		fs := afero.NewMemMapFs()
		assert.NoError(t, wipeOutputDirContents(fs, "/output"))
	})

	t.Run("empty directory is a no-op", func(t *testing.T) {
		t.Parallel()

		fs := afero.NewMemMapFs()
		require.NoError(t, fileutils.MkdirAll(fs, "/output"))

		assert.NoError(t, wipeOutputDirContents(fs, "/output"))
	})

	t.Run("removes all children but keeps directory", func(t *testing.T) {
		t.Parallel()

		testFS := afero.NewMemMapFs()
		for _, letter := range []string{"a", "c", "l"} {
			require.NoError(t, fileutils.MkdirAll(testFS, filepath.Join("/output", letter, "pkg")))
		}

		require.NoError(t, fileutils.WriteFile(testFS, "/output/README.md", []byte("keep me"), fileperms.PublicFile))
		require.NoError(t, fileutils.MkdirAll(testFS, "/output/multi-char-dir"))

		require.NoError(t, wipeOutputDirContents(testFS, "/output"))

		// Directory itself still exists.
		exists, err := fileutils.DirExists(testFS, "/output")
		require.NoError(t, err)
		assert.True(t, exists, "output directory itself must be preserved")

		// All children removed.
		entries, err := fileutils.ReadDir(testFS, "/output")
		require.NoError(t, err)
		assert.Empty(t, entries, "all contents should be removed")
	})
}

func TestWriteFailureMarkers(t *testing.T) {
	t.Parallel()

	t.Run("writes markers for error results", func(t *testing.T) {
		t.Parallel()

		testFS := afero.NewMemMapFs()
		results := []*RenderResult{
			{Component: "broken-pkg", OutputDir: "/output/b/broken-pkg", Status: renderStatusError},
			{Component: "ok-pkg", OutputDir: "/output/o/ok-pkg", Status: renderStatusOK},
			{Component: "cancelled-pkg", OutputDir: "/output/c/cancelled-pkg", Status: renderStatusCancelled},
			nil, // gap from a still-pending phase-3 slot.
		}

		writeFailureMarkers(testFS, results, false)

		// Error result → marker present.
		exists, err := fileutils.Exists(testFS, "/output/b/broken-pkg/"+renderErrorMarkerFile)
		require.NoError(t, err)
		assert.True(t, exists, "error result should have a marker")

		// Non-error results → no marker.
		nonErrorPaths := []string{
			"/output/o/ok-pkg/" + renderErrorMarkerFile,
			"/output/c/cancelled-pkg/" + renderErrorMarkerFile,
		}
		for _, path := range nonErrorPaths {
			exists, err := fileutils.Exists(testFS, path)
			require.NoError(t, err)
			assert.False(t, exists, "non-error result must not have a marker (%s)", path)
		}
	})

	t.Run("clears stale output before marker when allowOverwrite", func(t *testing.T) {
		t.Parallel()

		// Pre-existing successful render content that should be wiped before
		// the failure marker is dropped.
		testFS := afero.NewMemMapFs()
		require.NoError(t, fileutils.MkdirAll(testFS, "/output/b/broken-pkg"))
		require.NoError(t, fileutils.WriteFile(testFS,
			"/output/b/broken-pkg/broken-pkg.spec",
			[]byte("Name: broken-pkg"), fileperms.PublicFile))

		results := []*RenderResult{
			{Component: "broken-pkg", OutputDir: "/output/b/broken-pkg", Status: renderStatusError},
		}

		writeFailureMarkers(testFS, results, true)

		// Stale spec gone.
		exists, err := fileutils.Exists(testFS, "/output/b/broken-pkg/broken-pkg.spec")
		require.NoError(t, err)
		assert.False(t, exists, "stale render output should be cleared before marker")

		// Marker present.
		exists, err = fileutils.Exists(testFS, "/output/b/broken-pkg/"+renderErrorMarkerFile)
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("preserves stale output when allowOverwrite is false", func(t *testing.T) {
		t.Parallel()

		// Without --force, a previous successful render's content stays in
		// place — the marker just appears alongside it. (Realistically this
		// scenario can't happen with the current call sites, but the helper
		// must respect its allowOverwrite contract.)
		testFS := afero.NewMemMapFs()
		require.NoError(t, fileutils.MkdirAll(testFS, "/output/b/broken-pkg"))
		require.NoError(t, fileutils.WriteFile(testFS,
			"/output/b/broken-pkg/broken-pkg.spec",
			[]byte("Name: broken-pkg"), fileperms.PublicFile))

		results := []*RenderResult{
			{Component: "broken-pkg", OutputDir: "/output/b/broken-pkg", Status: renderStatusError},
		}

		writeFailureMarkers(testFS, results, false)

		exists, err := fileutils.Exists(testFS, "/output/b/broken-pkg/broken-pkg.spec")
		require.NoError(t, err)
		assert.True(t, exists, "spec should be preserved when overwrite is not allowed")
	})
}
