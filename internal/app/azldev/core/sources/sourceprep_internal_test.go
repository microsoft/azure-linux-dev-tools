// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoveSubmoduleEntries_StripsGitlinks(t *testing.T) {
	const repoDir = "/fakerepo"

	memFS := afero.NewMemMapFs()
	storer := memory.NewStorage()

	// Initialize a repo with in-memory storage only; this test exercises the
	// index/storer and uses memFS separately for directory cleanup assertions.
	repo, err := gogit.Init(storer, nil)
	require.NoError(t, err)

	// Manually build an index with a normal file entry and a submodule entry.
	idx := &index.Index{
		Version: 2,
		Entries: []*index.Entry{
			{
				Name: "regular-file.spec",
				Mode: filemode.Regular,
				Hash: plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			},
			{
				Name: "tests/at",
				Mode: filemode.Submodule,
				Hash: plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			},
		},
	}

	require.NoError(t, storer.SetIndex(idx))

	// Create the empty directory that the bogus submodule leaves behind.
	submoduleDir := filepath.Join(repoDir, "tests/at")
	require.NoError(t, memFS.MkdirAll(submoduleDir, fileperms.PublicDir))

	// Verify the directory exists before calling removeSubmoduleEntries.
	dirExists, err := fileutils.Exists(memFS, submoduleDir)
	require.NoError(t, err)
	require.True(t, dirExists, "submodule directory should exist before removal")

	// Act
	err = removeSubmoduleEntries(memFS, repo, repoDir)
	require.NoError(t, err)

	// Assert: index should only have the regular file.
	updatedIdx, err := storer.Index()
	require.NoError(t, err)
	require.Len(t, updatedIdx.Entries, 1)
	assert.Equal(t, "regular-file.spec", updatedIdx.Entries[0].Name)
	assert.Equal(t, filemode.Regular, updatedIdx.Entries[0].Mode)

	// Assert: empty directory was removed.
	dirExists, err = fileutils.Exists(memFS, submoduleDir)
	require.NoError(t, err)
	assert.False(t, dirExists, "submodule directory should be removed")
}

func TestRemoveSubmoduleEntries_NoOpWithoutSubmodules(t *testing.T) {
	const repoDir = "/fakerepo"

	memFS := afero.NewMemMapFs()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, nil)
	require.NoError(t, err)

	// Index with only normal entries.
	idx := &index.Index{
		Version: 2,
		Entries: []*index.Entry{
			{
				Name: "file-a.spec",
				Mode: filemode.Regular,
				Hash: plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			},
			{
				Name: "file-b.patch",
				Mode: filemode.Regular,
				Hash: plumbing.NewHash("cccccccccccccccccccccccccccccccccccccccc"),
			},
		},
	}

	require.NoError(t, storer.SetIndex(idx))

	err = removeSubmoduleEntries(memFS, repo, repoDir)
	require.NoError(t, err)

	// Index should be untouched.
	updatedIdx, err := storer.Index()
	require.NoError(t, err)
	require.Len(t, updatedIdx.Entries, 2)
}

func TestRemoveSubmoduleEntries_PreservesNormalEntriesWithMixedModes(t *testing.T) {
	const repoDir = "/fakerepo"

	memFS := afero.NewMemMapFs()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, nil)
	require.NoError(t, err)

	// Mix of regular files, executable, and submodule entries.
	idx := &index.Index{
		Version: 2,
		Entries: []*index.Entry{
			{
				Name: "build.sh",
				Mode: filemode.Executable,
				Hash: plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			},
			{
				Name: "tests/submod1",
				Mode: filemode.Submodule,
				Hash: plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			},
			{
				Name: "pkg.spec",
				Mode: filemode.Regular,
				Hash: plumbing.NewHash("cccccccccccccccccccccccccccccccccccccccc"),
			},
			{
				Name: "tests/submod2",
				Mode: filemode.Submodule,
				Hash: plumbing.NewHash("dddddddddddddddddddddddddddddddddddddddd"),
			},
		},
	}

	require.NoError(t, storer.SetIndex(idx))

	// Create empty dirs for both submodules.
	require.NoError(t, memFS.MkdirAll(filepath.Join(repoDir, "tests/submod1"), fileperms.PublicDir))
	require.NoError(t, memFS.MkdirAll(filepath.Join(repoDir, "tests/submod2"), fileperms.PublicDir))

	err = removeSubmoduleEntries(memFS, repo, repoDir)
	require.NoError(t, err)

	updatedIdx, err := storer.Index()
	require.NoError(t, err)
	require.Len(t, updatedIdx.Entries, 2)
	assert.Equal(t, "build.sh", updatedIdx.Entries[0].Name)
	assert.Equal(t, "pkg.spec", updatedIdx.Entries[1].Name)
}

func TestComputeCurrentFingerprint(t *testing.T) {
	memFS := afero.NewMemMapFs()

	lockedConfig := func(commit string, manualBump int) *projectconfig.ComponentConfig {
		return &projectconfig.ComponentConfig{
			Name: "test",
			Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream},
			Locked: &projectconfig.ComponentLockData{
				UpstreamCommit: commit,
				ManualBump:     manualBump,
			},
		}
	}

	tests := []struct {
		name      string
		config    *projectconfig.ComponentConfig
		wantEmpty bool
		wantErr   bool
	}{
		{
			name:      "nil config returns empty",
			config:    nil,
			wantEmpty: true,
		},
		{
			name:      "no upstream commit returns empty",
			config:    &projectconfig.ComponentConfig{Name: "test"},
			wantEmpty: true,
		},
		{
			name: "empty spec upstream commit without lock returns empty",
			config: &projectconfig.ComponentConfig{
				Name: "test",
				Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream},
			},
			wantEmpty: true,
		},
		{
			name:   "locked upstream commit produces fingerprint",
			config: lockedConfig("abc123def456", 0),
		},
		{
			name: "spec upstream commit fallback produces fingerprint",
			config: &projectconfig.ComponentConfig{
				Name: "test",
				Spec: projectconfig.SpecSource{
					SourceType:     projectconfig.SpecSourceTypeUpstream,
					UpstreamCommit: "abc123def456",
				},
			},
		},
		{
			name:   "locked manual bump produces fingerprint",
			config: lockedConfig("abc123def456", 5),
		},
		{
			name: "source file without hash returns error",
			config: &projectconfig.ComponentConfig{
				Name: "test",
				Spec: projectconfig.SpecSource{
					SourceType:     projectconfig.SpecSourceTypeUpstream,
					UpstreamCommit: "abc123def456",
				},
				SourceFiles: []projectconfig.SourceFileReference{
					{Filename: "extra.tar.gz"},
				},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := computeCurrentFingerprint(memFS, test.config, "3.0")

			if test.wantErr {
				assert.Error(t, err)

				return
			}

			require.NoError(t, err)

			if test.wantEmpty {
				assert.Empty(t, result)
			} else {
				assert.NotEmpty(t, result)
			}
		})
	}

	// Determinism: same inputs → same fingerprint.
	fp1, err := computeCurrentFingerprint(memFS, lockedConfig("abc123def456", 0), "3.0")
	require.NoError(t, err)

	fp2, err := computeCurrentFingerprint(memFS, lockedConfig("abc123def456", 0), "3.0")
	require.NoError(t, err)

	require.NotEmpty(t, fp1)
	assert.Equal(t, fp1, fp2, "identical inputs should produce identical fingerprint")

	// Sensitivity: changing any input changes the fingerprint.
	fpDiffRelease, err := computeCurrentFingerprint(memFS, lockedConfig("abc123def456", 0), "4.0")
	require.NoError(t, err)

	fpDiffCommit, err := computeCurrentFingerprint(memFS, lockedConfig("999888777666", 0), "3.0")
	require.NoError(t, err)

	fpDiffBump, err := computeCurrentFingerprint(memFS, lockedConfig("abc123def456", 1), "3.0")
	require.NoError(t, err)

	assert.NotEqual(t, fp1, fpDiffRelease, "different releaseVer should change fingerprint")
	assert.NotEqual(t, fp1, fpDiffCommit, "different upstream commit should change fingerprint")
	assert.NotEqual(t, fp1, fpDiffBump, "different manual bump should change fingerprint")
}

func TestRehashModifiedEntriesValidatesOverlayHash(t *testing.T) {
	const (
		outputDir   = "/output"
		archiveName = "pkg.tar.gz"
	)

	memFS := afero.NewMemMapFs()
	require.NoError(t, fileutils.MkdirAll(memFS, outputDir))
	require.NoError(t, fileutils.WriteFile(
		memFS, filepath.Join(outputDir, archiveName), []byte("repacked"), fileperms.PublicFile))
	expectedHash, err := fileutils.ComputeFileHash(
		memFS, fileutils.HashTypeSHA512, filepath.Join(outputDir, archiveName))
	require.NoError(t, err)

	for _, test := range []struct {
		name    string
		hash    string
		wantErr bool
	}{
		{name: "configured result matches", hash: strings.ToUpper(expectedHash)},
		{name: "stale hash fails", hash: "stale", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			lines := []fedorasource.SourcesFileLine{{Entry: &fedorasource.SourcesFileEntry{
				Filename: archiveName, HashType: fileutils.HashTypeSHA512,
			}}}
			refs := []projectconfig.SourceFileReference{{
				Filename: archiveName, HashType: fileutils.HashTypeSHA512, Hash: test.hash,
				Origin: projectconfig.Origin{Type: projectconfig.OriginTypeOverlay},
			}}

			preparer := &sourcePreparerImpl{fs: memFS}

			err := preparer.rehashModifiedEntries(lines, refs, outputDir, []string{archiveName})
			if test.wantErr {
				require.ErrorContains(t, err, "does not match")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, fileutils.HashTypeSHA512, lines[0].Entry.HashType)
			assert.Equal(t, expectedHash, lines[0].Entry.Hash)
		})
	}
}

func TestRehashModifiedEntriesMaterializesBootstrapHashOnlyWhenAllowed(t *testing.T) {
	const (
		outputDir   = "/output"
		archiveName = "pkg.tar.gz"
	)

	memFS := afero.NewMemMapFs()
	require.NoError(t, fileutils.MkdirAll(memFS, outputDir))
	require.NoError(t, fileutils.WriteFile(
		memFS, filepath.Join(outputDir, archiveName), []byte("repacked"), fileperms.PublicFile))

	for _, test := range []struct {
		name          string
		allowNoHashes bool
		wantHash      bool
	}{
		{name: "disabled"},
		{name: "enabled", allowNoHashes: true, wantHash: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			lines := []fedorasource.SourcesFileLine{{Entry: &fedorasource.SourcesFileEntry{
				Filename: archiveName, HashType: fileutils.HashTypeSHA512,
			}}}
			refs := []projectconfig.SourceFileReference{{
				Filename: archiveName, HashType: fileutils.HashTypeSHA512,
				Origin: projectconfig.Origin{Type: projectconfig.OriginTypeOverlay},
			}}

			preparer := &sourcePreparerImpl{fs: memFS, allowNoHashes: test.allowNoHashes}
			require.NoError(t, preparer.rehashModifiedEntries(lines, refs, outputDir, []string{archiveName}))
			assert.Equal(t, test.wantHash, refs[0].Hash != "")
		})
	}
}

func TestPostOverlayHashType(t *testing.T) {
	assert.Equal(t, fileutils.HashTypeMD5,
		postOverlayHashType(fileutils.HashTypeMD5, "", false, true))
	assert.Equal(t, fileutils.HashTypeSHA512,
		postOverlayHashType(fileutils.HashTypeMD5, "", true, true))
	assert.Equal(t, fileutils.HashTypeSHA512,
		postOverlayHashType(fileutils.HashTypeSHA512, fileutils.HashTypeSHA512, true, false))
	assert.Equal(t, fileutils.HashTypeSHA256,
		postOverlayHashType(fileutils.HashTypeSHA512, fileutils.HashTypeSHA256, true, true))
}
