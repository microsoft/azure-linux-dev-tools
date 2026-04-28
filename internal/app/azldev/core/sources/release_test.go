// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	memfs "github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReleaseUsesAutorelease(t *testing.T) {
	for _, testCase := range []struct {
		value    string
		expected bool
	}{
		// Basic forms.
		{"%autorelease", true},
		{"%{autorelease}", true},

		// Braced form with arguments (e.g., 389-ds-base).
		{"%{autorelease -n %{?with_asan:-e asan}}%{?dist}", true},
		{"%{autorelease -e asan}", true},

		// Conditional forms (e.g., gnutls, keylime-agent-rust).
		{"%{?autorelease}%{!?autorelease:1%{?dist}}", true},
		{"%{?autorelease}", true},

		// Conditional forms with a fallback value are NOT autorelease — the fallback
		// means we cannot conclusively determine that autorelease is being used.
		{"%{!?autorelease:1%{?dist}}", false},
		{"%{?autorelease:1%{?dist}}", false},

		// False positives (e.g., python-pyodbc).
		{"%{autorelease_suffix}", false},
		{"%{?autorelease_extra}", false},

		// Static release values.
		{"1", false},
		{"1%{?dist}", false},
		{"3%{?dist}.1", false},
		{"", false},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			assert.Equal(t, testCase.expected, sources.ReleaseUsesAutorelease(testCase.value))
		})
	}
}

func TestBumpStaticRelease(t *testing.T) {
	for _, testCase := range []struct {
		name, value string
		commits     int
		expected    string
		wantErr     bool
	}{
		// Accepted forms: bare integer or integer + %{?dist}.
		{"simple integer", "1", 3, "4", false},
		{"with dist tag", "1%{?dist}", 2, "3%{?dist}", false},
		{"larger base", "10%{?dist}", 5, "15%{?dist}", false},
		{"single commit", "1%{?dist}", 1, "2%{?dist}", false},

		// Rejected: no leading integer.
		{"no leading int", "%{?dist}", 1, "", true},
		{"empty string", "", 1, "", true},

		// Rejected: unknown macros in suffix.
		{"other macros", "17%{someothermacro}%{?dist}", 3, "", true},
		{"non-conditional dist", "1%{dist}", 1, "", true},
		{"macro before dist", "0%{rc_subver}%{?dist}", 1, "", true},

		// Rejected: dotted decimal releases.
		{"dotted with beta suffix", "1.39.b1%{?dist}", 3, "", true},
		{"dotted simple", "1.2%{?dist}", 2, "", true},
		{"dotted no suffix", "1.10", 5, "", true},
		{"dotted zero prefix", "0.1", 1, "", true},

		// Rejected: trailing dot.
		{"trailing dot before dist", "1.%{?dist}", 1, "", true},
		{"trailing dot no suffix", "1.", 1, "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := sources.BumpStaticRelease(testCase.value, testCase.commits)
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, result)
			}
		})
	}
}

func TestGetReleaseTagValue(t *testing.T) {
	makeSpec := func(release string) string {
		return "Name: test-package\nVersion: 1.0.0\nRelease: " + release + "\nSummary: Test\n"
	}

	for _, testCase := range []struct {
		name, specContent, expected string
		wantErr                     bool
	}{
		{"static with dist", makeSpec("1%{?dist}"), "1%{?dist}", false},
		{"autorelease", makeSpec("%autorelease"), "%autorelease", false},
		{"braced autorelease", makeSpec("%{autorelease}"), "%{autorelease}", false},
		{"no release tag", "Name: test-package\nVersion: 1.0.0\nSummary: Test\n", "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := testctx.NewCtx()
			specPath := "/test.spec"

			err := fileutils.WriteFile(ctx.FS(), specPath, []byte(testCase.specContent), 0o644)
			require.NoError(t, err)

			result, err := sources.GetReleaseTagValue(ctx.FS(), specPath)
			if testCase.wantErr {
				require.ErrorIs(t, err, spec.ErrNoSuchTag)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, result)
			}
		})
	}
}

func TestGetReleaseTagValue_FileNotFound(t *testing.T) {
	ctx := testctx.NewCtx()
	_, err := sources.GetReleaseTagValue(ctx.FS(), "/nonexistent.spec")
	require.Error(t, err)
}

func TestGetVersionTagFromReader(t *testing.T) {
	for _, testCase := range []struct {
		name, specContent, expected string
		wantErr                     bool
	}{
		{"simple version", "Name: pkg\nVersion: 1.0.0\nRelease: 1\n", "1.0.0", false},
		{"version with macro", "Name: pkg\nVersion: %{base_version}\nRelease: 1\n", "%{base_version}", false},
		{"no version tag", "Name: pkg\nRelease: 1\nSummary: Test\n", "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := sources.GetVersionTagFromReader(strings.NewReader(testCase.specContent))
			if testCase.wantErr {
				require.ErrorIs(t, err, spec.ErrNoSuchTag)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, result)
			}
		})
	}
}

// makeTestSpecContent returns a minimal spec file string with the given version.
// The index parameter ensures unique file content across commits so go-git
// doesn't reject them as empty commits.
func makeTestSpecContent(version string, index int) string {
	return fmt.Sprintf("Name: package\nVersion: %s\nRelease: 1%%{?dist}\nSummary: Test %d\n", version, index)
}

// createRepoWithVersionCommits creates an in-memory git repo with commits that have
// spec files at specified versions. Returns the repo and the commit hashes in order.
func createRepoWithVersionCommits(t *testing.T, versions []string) (*gogit.Repository, []string) {
	t.Helper()

	memFS := memfs.New()
	storer := memory.NewStorage()

	repo, err := gogit.Init(storer, memFS)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	hashes := make([]string, 0, len(versions))

	for versionIdx, version := range versions {
		file, err := memFS.Create("package.spec")
		require.NoError(t, err)

		_, err = file.Write([]byte(makeTestSpecContent(version, versionIdx)))
		require.NoError(t, err)
		require.NoError(t, file.Close())

		_, err = worktree.Add("package.spec")
		require.NoError(t, err)

		hash, err := worktree.Commit("upstream: v"+version, &gogit.CommitOptions{
			Author: &object.Signature{
				Name:  "Upstream",
				Email: "upstream@fedora.org",
				When:  time.Date(2024, 1, 1+versionIdx, 0, 0, 0, 0, time.UTC),
			},
		})
		require.NoError(t, err)

		hashes = append(hashes, hash.String())
	}

	return repo, hashes
}

func TestCountCommitsSinceVersionChange_NoVersionChange(t *testing.T) {
	// All synthetic commits reference the same upstream commit → all count.
	repo, hashes := createRepoWithVersionCommits(t, []string{"1.0"})

	changes := []sources.FingerprintChange{
		{CommitMetadata: sources.CommitMetadata{Hash: "a1"}, UpstreamCommit: hashes[0]},
		{CommitMetadata: sources.CommitMetadata{Hash: "a2"}, UpstreamCommit: hashes[0]},
		{CommitMetadata: sources.CommitMetadata{Hash: "a3"}, UpstreamCommit: hashes[0]},
	}

	count := sources.CountCommitsSinceVersionChange(repo, "package.spec", changes)
	assert.Equal(t, 3, count)
}

func TestCountCommitsSinceVersionChange_VersionChangeMidStream(t *testing.T) {
	// Upstream goes 1.0 → 2.0. Two synthetic commits for 1.0, one for 2.0.
	// Only the one after 2.0 should count.
	repo, hashes := createRepoWithVersionCommits(t, []string{"1.0", "2.0"})

	changes := []sources.FingerprintChange{
		{CommitMetadata: sources.CommitMetadata{Hash: "a1"}, UpstreamCommit: hashes[0]},
		{CommitMetadata: sources.CommitMetadata{Hash: "a2"}, UpstreamCommit: hashes[0]},
		{CommitMetadata: sources.CommitMetadata{Hash: "a3"}, UpstreamCommit: hashes[1]},
	}

	count := sources.CountCommitsSinceVersionChange(repo, "package.spec", changes)
	assert.Equal(t, 1, count)
}

func TestCountCommitsSinceVersionChange_MultipleVersionJumps(t *testing.T) {
	// Upstream goes 1.0 → 2.0 → 3.0. Synth commits: 1 for 1.0, 1 for 2.0, 2 for 3.0.
	// Only the 2 after 3.0 should count.
	repo, hashes := createRepoWithVersionCommits(t, []string{"1.0", "2.0", "3.0"})

	changes := []sources.FingerprintChange{
		{CommitMetadata: sources.CommitMetadata{Hash: "a1"}, UpstreamCommit: hashes[0]},
		{CommitMetadata: sources.CommitMetadata{Hash: "a2"}, UpstreamCommit: hashes[1]},
		{CommitMetadata: sources.CommitMetadata{Hash: "a3"}, UpstreamCommit: hashes[2]},
		{CommitMetadata: sources.CommitMetadata{Hash: "a4"}, UpstreamCommit: hashes[2]},
	}

	count := sources.CountCommitsSinceVersionChange(repo, "package.spec", changes)
	assert.Equal(t, 2, count)
}

func TestCountCommitsSinceVersionChange_SameVersionMultipleUpstreams(t *testing.T) {
	// Upstream has two commits but Version stays the same → all count (no version change).
	repo, hashes := createRepoWithVersionCommits(t, []string{"1.0", "1.0"})

	changes := []sources.FingerprintChange{
		{CommitMetadata: sources.CommitMetadata{Hash: "a1"}, UpstreamCommit: hashes[0]},
		{CommitMetadata: sources.CommitMetadata{Hash: "a2"}, UpstreamCommit: hashes[1]},
		{CommitMetadata: sources.CommitMetadata{Hash: "a3"}, UpstreamCommit: hashes[1]},
	}

	count := sources.CountCommitsSinceVersionChange(repo, "package.spec", changes)
	assert.Equal(t, 3, count)
}

func TestCountCommitsSinceVersionChange_SingleChange(t *testing.T) {
	repo, hashes := createRepoWithVersionCommits(t, []string{"1.0"})

	changes := []sources.FingerprintChange{
		{CommitMetadata: sources.CommitMetadata{Hash: "a1"}, UpstreamCommit: hashes[0]},
	}

	count := sources.CountCommitsSinceVersionChange(repo, "package.spec", changes)
	assert.Equal(t, 1, count)
}

func TestCountCommitsSinceVersionChange_EmptyChanges(t *testing.T) {
	repo, _ := createRepoWithVersionCommits(t, []string{"1.0"})

	count := sources.CountCommitsSinceVersionChange(repo, "package.spec", nil)
	assert.Equal(t, 0, count)
}

func TestCountCommitsSinceVersionChange_SpecNotFound(t *testing.T) {
	// When the spec file doesn't exist at a commit, fall back to total count.
	repo, hashes := createRepoWithVersionCommits(t, []string{"1.0"})

	changes := []sources.FingerprintChange{
		{CommitMetadata: sources.CommitMetadata{Hash: "a1"}, UpstreamCommit: hashes[0]},
		{CommitMetadata: sources.CommitMetadata{Hash: "a2"}, UpstreamCommit: hashes[0]},
	}

	// Use a wrong spec filename to trigger fallback.
	count := sources.CountCommitsSinceVersionChange(repo, "nonexistent.spec", changes)
	assert.Equal(t, 2, count)
}
