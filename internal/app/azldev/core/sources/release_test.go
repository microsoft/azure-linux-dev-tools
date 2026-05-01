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

func TestCountCommitsSinceVersionChange(t *testing.T) {
	const (
		nonExistentHash = -1 // valid hex format but not in repo
		malformedHash   = -2 // invalid hash format
		emptyHash       = -3 // empty string (local component, no upstream)
	)

	for _, testCase := range []struct {
		name     string
		versions []string // versions to commit in the upstream repo
		// Index into created hashes; negative values use sentinel literals.
		upstreamCommits []int
		specFile        string // override spec filename (default: "package.spec")
		expected        int
		expectError     bool
	}{
		{"no version change", []string{"1.0"}, []int{0, 0, 0}, "", 3, false},
		{"version change mid-stream", []string{"1.0", "2.0"}, []int{0, 0, 1}, "", 1, false},
		{"multiple version jumps", []string{"1.0", "2.0", "3.0"}, []int{0, 1, 2, 2}, "", 2, false},
		{"same version multiple upstreams", []string{"1.0", "1.0"}, []int{0, 1, 1}, "", 3, false},
		{"single change", []string{"1.0"}, []int{0}, "", 1, false},
		{"empty changes", []string{"1.0"}, nil, "", 0, false},
		{"spec not found", []string{"1.0"}, []int{0, 0}, "nonexistent.spec", 0, false},
		{"non-existent commit hash", []string{"1.0"}, []int{nonExistentHash, nonExistentHash, nonExistentHash}, "", 0, false},
		{"invalid hash string", []string{"1.0"}, []int{malformedHash, malformedHash}, "", 0, false},
		{"partially resolvable", []string{"1.0"}, []int{nonExistentHash, 0, 0}, "", 2, false},
		{"empty upstream (local component)", []string{"1.0"}, []int{emptyHash, emptyHash, emptyHash}, "", 3, false},
		{"macro version errors", []string{"%{base_version}"}, []int{0, 0, 0}, "", 0, true},
		{
			"macro version with multiple upstreams errors",
			[]string{"%{base_version}", "%{base_version}"},
			[]int{0, 0, 1},
			"", 0, true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo, hashes := createRepoWithVersionCommits(t, testCase.versions)

			specFile := testCase.specFile
			if specFile == "" {
				specFile = "package.spec"
			}

			var changes []sources.FingerprintChange

			for changeIdx, idx := range testCase.upstreamCommits {
				var upstream string

				switch {
				case idx >= 0:
					upstream = hashes[idx]
				case idx == nonExistentHash:
					upstream = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
				case idx == malformedHash:
					upstream = "not-a-valid-hash"
				case idx == emptyHash:
					upstream = ""
				}

				changes = append(changes, sources.FingerprintChange{
					CommitMetadata: sources.CommitMetadata{Hash: fmt.Sprintf("a%d", changeIdx+1)},
					UpstreamCommit: upstream,
				})
			}

			count, err := sources.CountCommitsSinceVersionChange(repo, specFile, changes)
			if testCase.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unexpanded macro")
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, count)
			}
		})
	}
}
