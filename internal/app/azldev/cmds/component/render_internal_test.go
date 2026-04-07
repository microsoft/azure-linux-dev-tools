// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
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

	t.Run("falls back to any .spec file", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		require.NoError(t, fileutils.MkdirAll(fs, "/src"))
		require.NoError(t, fileutils.WriteFile(fs, "/src/renamed.spec", []byte("Name: curl"), fileperms.PublicFile))

		path, err := findSpecFile(fs, "/src", "curl")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/src", "renamed.spec"), path)
	})

	t.Run("error when no spec file exists", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		require.NoError(t, fileutils.MkdirAll(fs, "/src"))
		require.NoError(t, fileutils.WriteFile(fs, "/src/README.md", []byte("# readme"), fileperms.PublicFile))

		_, err := findSpecFile(fs, "/src", "curl")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no spec file found")
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

func TestCleanupStaleRenders(t *testing.T) {
	t.Run("removes directories not in component set", func(t *testing.T) {
		testFS := afero.NewMemMapFs()
		ctrl := gomock.NewController(t)

		// Create output directories for curl, wget, and stale-pkg.
		for _, name := range []string{"curl", "wget", "stale-pkg"} {
			require.NoError(t, fileutils.MkdirAll(testFS, filepath.Join("/output", name)))
			require.NoError(t, fileutils.WriteFile(testFS,
				filepath.Join("/output", name, name+".spec"),
				[]byte("Name: "+name), fileperms.PublicFile))
		}

		// Only curl and wget are current components.
		compSet := components.NewComponentSet()

		for _, name := range []string{"curl", "wget"} {
			mock := components_testutils.NewMockComponent(ctrl)
			mock.EXPECT().GetName().AnyTimes().Return(name)
			compSet.Add(mock)
		}

		err := cleanupStaleRenders(testFS, compSet, "/output")
		require.NoError(t, err)

		// curl and wget should still exist.
		for _, name := range []string{"curl", "wget"} {
			exists, existsErr := fileutils.Exists(testFS, filepath.Join("/output", name))
			require.NoError(t, existsErr)
			assert.True(t, exists, "%s should still exist", name)
		}

		// stale-pkg should be removed.
		exists, err := fileutils.Exists(testFS, "/output/stale-pkg")
		require.NoError(t, err)
		assert.False(t, exists, "stale-pkg should be removed")
	})

	t.Run("skips non-directory entries", func(t *testing.T) {
		testFS := afero.NewMemMapFs()

		require.NoError(t, fileutils.MkdirAll(testFS, "/output"))
		require.NoError(t, fileutils.WriteFile(testFS, "/output/README.md", []byte("# readme"), fileperms.PublicFile))

		compSet := components.NewComponentSet()

		err := cleanupStaleRenders(testFS, compSet, "/output")
		require.NoError(t, err)

		// File should still exist (only directories are cleaned up).
		exists, err := fileutils.Exists(testFS, "/output/README.md")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("no-op when output directory does not exist", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		compSet := components.NewComponentSet()

		err := cleanupStaleRenders(fs, compSet, "/nonexistent")
		require.NoError(t, err)
	})
}

func TestValidateOutputDir(t *testing.T) {
	tests := []struct {
		dir     string
		wantErr bool
	}{
		{"SPECS", false},
		{"rendered/output", false},
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
