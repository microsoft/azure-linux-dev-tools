// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lockfile_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProjectDir = "/project"

func TestNew(t *testing.T) {
	lf := lockfile.New()
	assert.Equal(t, 1, lf.Version)
	assert.NotNil(t, lf.Components)
	assert.Empty(t, lf.Components)
}

func TestSetAndGetUpstreamCommit(t *testing.T) {
	lf := lockfile.New()

	lf.SetUpstreamCommit("curl", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2")

	commit, ok := lf.GetUpstreamCommit("curl")
	assert.True(t, ok)
	assert.Equal(t, "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", commit)
}

func TestGetUpstreamCommitMissing(t *testing.T) {
	lf := lockfile.New()

	commit, ok := lf.GetUpstreamCommit("nonexistent")
	assert.False(t, ok)
	assert.Empty(t, commit)
}

func TestSaveAndLoad(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := filepath.Join(testProjectDir, lockfile.FileName)

	require.NoError(t, fileutils.MkdirAll(memFS, testProjectDir))

	// Create and save a lock file.
	original := lockfile.New()
	original.SetUpstreamCommit("curl", "aaaa")
	original.SetUpstreamCommit("bash", "bbbb")
	original.SetUpstreamCommit("vim", "cccc")

	require.NoError(t, original.Save(memFS, lockPath))

	// Load it back.
	loaded, err := lockfile.Load(memFS, lockPath)
	require.NoError(t, err)

	assert.Equal(t, 1, loaded.Version)

	commit, found := loaded.GetUpstreamCommit("curl")
	assert.True(t, found)
	assert.Equal(t, "aaaa", commit)

	commit, found = loaded.GetUpstreamCommit("bash")
	assert.True(t, found)
	assert.Equal(t, "bbbb", commit)

	commit, found = loaded.GetUpstreamCommit("vim")
	assert.True(t, found)
	assert.Equal(t, "cccc", commit)
}

func TestSaveSortsComponents(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := filepath.Join(testProjectDir, lockfile.FileName)

	require.NoError(t, fileutils.MkdirAll(memFS, testProjectDir))

	lockFile := lockfile.New()
	// Insert in non-alphabetical order.
	lockFile.SetUpstreamCommit("zlib", "zzzz")
	lockFile.SetUpstreamCommit("curl", "aaaa")
	lockFile.SetUpstreamCommit("bash", "bbbb")

	require.NoError(t, lockFile.Save(memFS, lockPath))

	data, err := fileutils.ReadFile(memFS, lockPath)
	require.NoError(t, err)

	content := string(data)

	// bash should appear before curl, which should appear before zlib.
	bashIdx := strings.Index(content, "[components.bash]")
	curlIdx := strings.Index(content, "[components.curl]")
	zlibIdx := strings.Index(content, "[components.zlib]")

	assert.Less(t, bashIdx, curlIdx, "bash should come before curl")
	assert.Less(t, curlIdx, zlibIdx, "curl should come before zlib")
}

func TestLoadUnsupportedVersion(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := filepath.Join(testProjectDir, lockfile.FileName)

	content := "version = 99\n"

	require.NoError(t, fileutils.MkdirAll(memFS, testProjectDir))
	require.NoError(t, fileutils.WriteFile(memFS, lockPath, []byte(content), fileperms.PublicFile))

	_, err := lockfile.Load(memFS, lockPath)
	assert.ErrorContains(t, err, "unsupported lock file version")
}

func TestLoadMissingFile(t *testing.T) {
	fs := afero.NewMemMapFs()

	_, err := lockfile.Load(fs, "/nonexistent/azldev.lock")
	assert.Error(t, err)
}

func TestLoadInvalidTOML(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := filepath.Join(testProjectDir, lockfile.FileName)

	require.NoError(t, fileutils.MkdirAll(memFS, testProjectDir))
	require.NoError(t, fileutils.WriteFile(memFS, lockPath, []byte("not valid toml {{{"), fileperms.PublicFile))

	_, err := lockfile.Load(memFS, lockPath)
	assert.ErrorContains(t, err, "parsing lock file")
}

func TestSaveContainsVersion(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := filepath.Join(testProjectDir, lockfile.FileName)

	require.NoError(t, fileutils.MkdirAll(memFS, testProjectDir))

	lockFile := lockfile.New()
	require.NoError(t, lockFile.Save(memFS, lockPath))

	data, err := fileutils.ReadFile(memFS, lockPath)
	require.NoError(t, err)

	assert.Contains(t, string(data), "version = 1")
	assert.Contains(t, string(data), "# azldev.lock")
}

func TestRoundTripLocalComponent(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := filepath.Join(testProjectDir, lockfile.FileName)

	require.NoError(t, fileutils.MkdirAll(memFS, testProjectDir))

	// Create a lock file with a local component (empty upstream commit)
	// alongside an upstream component.
	original := lockfile.New()
	original.SetUpstreamCommit("curl", "aaaa")
	original.Components["local-pkg"] = lockfile.ComponentLock{}

	require.NoError(t, original.Save(memFS, lockPath))

	// Load it back and verify both entries survived.
	loaded, err := lockfile.Load(memFS, lockPath)
	require.NoError(t, err)

	// Upstream component round-trips with its commit.
	commit, found := loaded.GetUpstreamCommit("curl")
	assert.True(t, found)
	assert.Equal(t, "aaaa", commit)

	// Local component has an entry but no upstream commit.
	_, hasEntry := loaded.Components["local-pkg"]
	assert.True(t, hasEntry, "local component entry should survive round-trip")

	commit, found = loaded.GetUpstreamCommit("local-pkg")
	assert.False(t, found, "local component should not have an upstream commit")
	assert.Empty(t, commit)
}
