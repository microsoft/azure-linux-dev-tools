// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/sourceproviders_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func renderTestSpec(version, release, changelog string) string {
	return fmt.Sprintf("Name: curl\nVersion: %s\nRelease: %s\nSummary: Test\nLicense: MIT\n"+
		"\n%%description\nTest\n\n%%changelog\n%s", version, release, changelog)
}

func TestReadLastRender_CommittedConfigAndSpec(t *testing.T) {
	root := []byte(`includes = ["component.toml", "pins/*.toml"]
[project]
default-distro = { name = "test", version = "3" }
rendered-specs-dir = "SPECS"
[distros.test.versions."1"]
release-ver = "1"
dist-git-branch = "f40"
[distros.test.versions."2"]
release-ver = "2"
dist-git-branch = "f41"
[distros.test.versions."3"]
release-ver = "3"
dist-git-branch = "f42"
`)
	oldSpec := renderTestSpec("1", "9%{?dist}", "* Previous\n- Keep exactly\n")
	repo, hashes := testRepoWithCommits(t, []testRepoCommit{
		{files: map[string][]byte{
			"azldev.toml": root,
			"component.toml": []byte("[components.curl.spec]\ntype = 'upstream'\n" +
				"upstream-distro = { name = 'test', version = '1' }\n"),
			"pins/curl.toml":         []byte("[components.curl.spec]\nupstream-commit = 'aaaaaaa'\n"),
			"SPECS/c/curl/curl.spec": []byte(oldSpec),
		}},
		{files: map[string][]byte{
			"pins/curl.toml": []byte("[components.curl.spec]\nupstream-commit = 'bbbbbbb'\n"),
			"component.toml": []byte("[components.curl.spec]\ntype = 'upstream'\n" +
				"upstream-distro = { name = 'test', version = '2' }\n"),
		}},
		{files: map[string][]byte{"README": []byte("unrelated later commit")}},
	})
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	dirty, err := worktree.Filesystem.Create("SPECS/c/curl/curl.spec")
	require.NoError(t, err)
	_, err = dirty.Write([]byte("uncommitted spec must not be used"))
	require.NoError(t, err)
	require.NoError(t, dirty.Close())

	env := testutils.NewTestEnv(t)
	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		assert.Equal(t, []string{
			"git", "-C", "/project", "--literal-pathspecs", "log", "-1", "--format=%H",
			"--end-of-options", hashes[2], "--", "SPECS/c/curl/",
		}, cmd.Args)

		return hashes[0] + "\n", nil
	}
	history, err := readLastRender(env.Env, repo, "/project", "SPECS/c/curl", "curl")
	require.NoError(t, err)
	assert.Equal(t, oldSpec, history.spec)
	require.NotNil(t, history.tree)
	committed, err := history.tree.File("curl.spec")
	require.NoError(t, err)
	content, err := committed.Contents()
	require.NoError(t, err)
	assert.Equal(t, oldSpec, content)
	assert.Equal(t, "aaaaaaa", history.previous)
	assert.Equal(t, "bbbbbbb", history.current)
	assert.Equal(t, "f40", history.previousBranch)
	assert.Equal(t, "f41", history.currentBranch)
}

func TestReadLastRender_NativeHistoryErrors(t *testing.T) {
	const renderPath = "SPECS/c/curl[1]"

	logErr := errors.New("git log failed")

	for _, testCase := range []struct {
		name, output, wantError string
		commandErr              error
	}{
		{name: "no matching commit"},
		{name: "git failure", commandErr: logErr, wantError: "finding last render commit"},
		{name: "unknown commit", output: strings.Repeat("a", 40), wantError: "reading last render commit"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			previous := renderTestSpec("1", "9", "old\n")
			repo, hashes := testRepoWithCommits(t, []testRepoCommit{{files: map[string][]byte{
				renderPath + "/curl.spec": []byte(previous),
			}}})
			env := testutils.NewTestEnv(t)
			called := false
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				called = true

				assert.Equal(t, []string{
					"git", "-C", "/project", "--literal-pathspecs", "log", "-1", "--format=%H",
					"--end-of-options", hashes[0], "--", renderPath + "/",
				}, cmd.Args)

				return testCase.output, testCase.commandErr
			}

			history, err := readLastRender(env.Env, repo, "/project", renderPath, "curl")
			if testCase.wantError != "" {
				require.ErrorContains(t, err, testCase.wantError)
			} else {
				require.NoError(t, err)
			}

			if testCase.commandErr != nil {
				require.ErrorIs(t, err, testCase.commandErr)
			}

			assert.True(t, called)
			assert.Equal(t, previous, history.spec)
			assert.Empty(t, history.previous)
			assert.Empty(t, history.current)
		})
	}
}

func TestReadLastRender_HistoricalLockFallback(t *testing.T) {
	const (
		oldLock = "version = 1\nupstream-commit = 'aaaaaaa'\n"
		config  = `[project]
default-distro = { name = "test", version = "1" }
lock-dir = "../historical-locks"
[distros.test.versions."1"]
release-ver = "1"
dist-git-branch = "f40"
`
	)
	for _, testCase := range []struct {
		name, selection, lock, pin, want string
		noSelection, noLock, noLockDir   bool
		missingCurrent                   bool
	}{
		{name: "historically locked", selection: "rust-asyncband\n", lock: oldLock, want: "aaaaaaa"},
		{name: "empty selection", lock: oldLock, want: "aaaaaaa"},
		{name: "exact names", selection: "Curl\ncurl-extra\n", lock: oldLock, want: "aaaaaaa"},
		{name: "listed component", selection: " curl \r\n", lock: oldLock},
		{name: "missing selection", noSelection: true, lock: oldLock, want: "aaaaaaa"},
		{name: "missing selection and lock", noSelection: true, noLock: true},
		{name: "missing lock", noLock: true},
		{name: "corrupt lock", lock: "invalid = ["},
		{name: "empty lock pin", lock: "version = 1\n"},
		{name: "no configured lock directory", noLockDir: true, lock: oldLock},
		{name: "configured pin takes precedence", pin: "ccccccc", lock: oldLock, want: "ccccccc"},
		{name: "no fallback for current pin", lock: oldLock, missingCurrent: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			oldConfig := config
			if testCase.noLockDir {
				oldConfig = strings.ReplaceAll(oldConfig, `lock-dir = "../historical-locks"`, `lock-dir = ""`)
			}

			component := "[components.curl.spec]\ntype = 'upstream'\n"

			files := map[string][]byte{
				"azldev.toml":           []byte(`includes = ["settings/project.toml", "components.toml"]`),
				"settings/project.toml": []byte(oldConfig),
				"components.toml": []byte(component +
					"upstream-commit = '" + testCase.pin + "'\n"),
				"SPECS/c/curl/curl.spec": []byte(renderTestSpec("1", "9", "old\n")),
			}
			if !testCase.noSelection {
				files["lockless-components.txt"] = []byte(testCase.selection)
			}

			if !testCase.noLock {
				files["historical-locks/curl.lock"] = []byte(testCase.lock)
			}

			current := component
			selection := "rust-asyncband\n"

			if !testCase.missingCurrent {
				current += "upstream-commit = 'bbbbbbb'\n"
				selection = "curl\n"
			}

			repo, hashes := testRepoWithCommits(t, []testRepoCommit{
				{files: files},
				{files: map[string][]byte{
					"components.toml": []byte(current),
					"settings/project.toml": []byte(strings.ReplaceAll(
						strings.ReplaceAll(config, "historical-locks", "current-locks"), "f40", "f41")),
					"lockless-components.txt":    []byte(selection),
					"historical-locks/curl.lock": []byte("version = 1\nupstream-commit = 'changed-old-lock'\n"),
					"current-locks/curl.lock":    []byte("version = 1\nupstream-commit = 'current-lock'\n"),
				}},
			})
			worktree, err := repo.Worktree()
			require.NoError(t, err)
			dirty, err := worktree.Filesystem.Create("historical-locks/curl.lock")
			require.NoError(t, err)
			_, err = dirty.Write([]byte("uncommitted lock"))
			require.NoError(t, err)
			require.NoError(t, dirty.Close())
			env := testutils.NewTestEnv(t)
			env.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
				return hashes[0], nil
			}

			history, err := readLastRender(env.Env, repo, "/project", "SPECS/c/curl", "curl")
			if testCase.missingCurrent {
				require.ErrorContains(t, err, "current render upstream commit")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, history.previous)
			assert.Equal(t, "bbbbbbb", history.current)
			assert.Equal(t, "f41", history.currentBranch)

			if testCase.want != "" {
				assert.Equal(t, "f40", history.previousBranch)
			}
		})
	}
}

func TestCollectRenderUpstreamChanges_Branches(t *testing.T) {
	for _, testCase := range []struct {
		name, previous, current, oldBranch, newBranch string
		wantFetch, fetchFails, wantLog                bool
	}{
		{
			name: "same branch", previous: "old", current: "new",
			oldBranch: "f41", newBranch: "f41", wantLog: true,
		},
		{
			name: "branch change", previous: "old", current: "new",
			oldBranch: "f40", newBranch: "f41", wantFetch: true, wantLog: true,
		},
		{
			name: "branch fetch fails", previous: "old", current: "new",
			oldBranch: "f40", newBranch: "f41", wantFetch: true, fetchFails: true,
		},
		{
			name: "same commit on different branch", previous: "same", current: "same",
			oldBranch: "f40", newBranch: "f41",
		},
		{name: "initial render", current: "new", newBranch: "f41"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			fetchErr := errors.New("fetch failed")

			var fetched, logged bool

			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				if cmd.Args[3] == "fetch" {
					fetched = true

					assert.False(t, logged, "fetch must run before comparing commits")
					assert.Equal(t, []string{
						"git", "-C", "/working", "fetch", "--no-tags", "--", "origin",
						"+refs/heads/f40:refs/remotes/origin/f40",
					}, cmd.Args)

					if testCase.fetchFails {
						return "", fetchErr
					}

					return "", nil
				}

				logged = true

				assert.Equal(t, []string{
					"git", "-C", "/working", "log", "--oneline", "--end-of-options", "old..new", "--",
				}, cmd.Args)

				return "abc123 Upstream change\n", nil
			}
			update := &locklessRenderUpdate{}

			err := collectRenderUpstreamChanges(env.Env, "/working", "curl", "1",
				lastRender{
					previous: testCase.previous, current: testCase.current,
					previousBranch: testCase.oldBranch, currentBranch: testCase.newBranch,
				}, update)
			if testCase.fetchFails {
				require.ErrorIs(t, err, fetchErr)
				require.ErrorContains(t, err, "fetching last render upstream branch")
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, testCase.wantFetch, fetched)
			assert.Equal(t, testCase.wantLog, logged)

			if testCase.wantLog {
				assert.Equal(t, "abc123 Upstream change", update.changes)
			}
		})
	}
}

func TestReadLastRender_AbsentAndInvalidHistory(t *testing.T) {
	valid := []byte(`[project]
default-distro = { name = "test", version = "1" }
[distros.test.versions."1"]
release-ver = "1"
[components.curl.spec]
type = "upstream"
upstream-commit = "bbbbbbb"
`)
	for _, testCase := range []struct {
		name        string
		oldConfig   []byte
		oldSpec     bool
		current     []byte
		wantCurrent string
		wantError   string
	}{
		{name: "no committed directory", oldConfig: valid},
		{
			name: "invalid previous config", oldSpec: true, oldConfig: []byte("invalid = ["),
			current: valid, wantCurrent: "bbbbbbb",
		},
		{
			name: "invalid current config", oldSpec: true, oldConfig: valid,
			current: []byte("invalid = ["), wantError: "current render upstream commit",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			files := map[string][]byte{"azldev.toml": testCase.oldConfig}
			if testCase.oldSpec {
				files["SPECS/c/curl/curl.spec"] = []byte(renderTestSpec("1", "1", "old\n"))
			}

			commits := []testRepoCommit{{files: files}}
			if testCase.current != nil {
				commits = append(commits, testRepoCommit{files: map[string][]byte{"azldev.toml": testCase.current}})
			}

			repo, hashes := testRepoWithCommits(t, commits)
			env := testutils.NewTestEnv(t)
			env.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
				return hashes[0], nil
			}

			history, err := readLastRender(env.Env, repo, "/project", "SPECS/c/curl", "curl")
			if testCase.wantError != "" {
				require.ErrorContains(t, err, testCase.wantError)

				return
			}

			require.NoError(t, err)
			assert.Empty(t, history.previous)
			assert.Equal(t, testCase.wantCurrent, history.current)
		})
	}
}

type renderAutospecStub struct {
	calls  []string
	err    error
	base   string
	expand func(string, string) (string, error)
}

func (s *renderAutospecStub) GenerateChangelog(_ context.Context, _, _ string) (string, error) {
	s.calls = append(s.calls, "changelog")

	return "* Full upstream history\n- Original change\n", s.err
}

func (s *renderAutospecStub) CalculateBaseRelease(_ context.Context, _, _ string) (string, error) {
	s.calls = append(s.calls, "release")

	if s.base != "" {
		return s.base, s.err
	}

	return "Calculated release number: 42\n", s.err
}

func (s *renderAutospecStub) ExpandRelease(_ context.Context, dir, filename string) (string, error) {
	s.calls = append(s.calls, "expand")

	return s.expand(dir, filename)
}

func TestPrepareLocklessSpec_ReleaseChangelogAndUpstreamChanges(t *testing.T) {
	const oldChangelog = "* Previous downstream entry\n- Preserve spaces  \n"
	for _, testCase := range []struct {
		name        string
		version     string
		release     string
		history     lastRender
		wantRelease string
		wantChanges string
		bootstrap   bool
		base        string
	}{
		{
			name: "same upstream", version: "1", release: "1%{?dist}",
			history: lastRender{
				spec: renderTestSpec("1", "9%{?dist}", oldChangelog), previous: "old", current: "old",
			},
			wantRelease: "9%{?dist}",
		},
		{
			name: "upstream changes without version change", version: "1", release: "1%{?dist}",
			history: lastRender{
				spec: renderTestSpec("1", "9%{?dist}", oldChangelog), previous: "old", current: "new",
			},
			wantRelease: "9%{?dist}", wantChanges: "abc123 Upstream fix\ndef456 Another fix",
		},
		{
			name: "version change keeps upstream release", version: "2", release: "1%{?dist}",
			history: lastRender{
				spec: renderTestSpec("1", "9%{?dist}", oldChangelog), previous: "old", current: "new",
			},
			wantRelease: "1%{?dist}", wantChanges: "abc123 Upstream fix\ndef456 Another fix",
		},
		{
			name: "version change without upstream changes", version: "2", release: "1%{?dist}",
			history: lastRender{
				spec: renderTestSpec("1", "9%{?dist}", oldChangelog), previous: "old", current: "old",
			},
			wantRelease: "1%{?dist}",
		},
		{
			name: "initial static spec", version: "2", release: "3%{?dist}",
			wantRelease: "3%{?dist}", wantChanges: "Update curl to version 2",
		},
		{
			name: "initial autorelease", version: "2", release: "%autorelease -b 7",
			wantRelease: "%autorelease -b 42", wantChanges: "Update curl to version 2", bootstrap: true,
		},
		{
			name: "initial default base", version: "2", release: "%autorelease -b 7 -e test",
			wantRelease: "%autorelease -e test", wantChanges: "Update curl to version 2",
			bootstrap: true, base: "Calculated release number: 1\n",
		},
		{
			name: "existing autorelease", version: "1", release: "%autorelease",
			history: lastRender{
				spec: renderTestSpec("1", "9%{?dist}", oldChangelog), previous: "old", current: "old",
			},
			wantRelease: "%autorelease",
		},
		{
			name: "unknown previous upstream", version: "1", release: "1",
			history:     lastRender{spec: renderTestSpec("1", "9", oldChangelog), current: "new"},
			wantRelease: "9", wantChanges: "Update curl to version 1",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)

			const path = "/working/curl.spec"
			require.NoError(t, fileutils.WriteFile(env.TestFS, path,
				[]byte(renderTestSpec(testCase.version, testCase.release, "* Upstream\n- New entry\n")),
				fileperms.PublicFile))
			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/changelog", []byte("stale"),
				fileperms.PublicFile))
			env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.Equal(t, []string{
					"git", "-C", "/working", "log", "--oneline", "--end-of-options",
					"old..new", "--",
				}, cmd.Args)

				return "abc123 Upstream fix\ndef456 Another fix\n", nil
			}
			called := false
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				assert.False(t, called, "only the final release bump should run")
				called = true

				assert.Equal(t, []string{"rpmdev-bumpspec", "-c", "update curl", path}, cmd.Args)
				prepared, err := fileutils.ReadFile(env.TestFS, path)
				require.NoError(t, err)
				assert.Contains(t, string(prepared), "Release: "+testCase.wantRelease+"\n")

				if testCase.history.spec != "" && testCase.release != "%autorelease" {
					assert.True(t, strings.HasSuffix(string(prepared), oldChangelog))
				} else {
					assert.True(t, strings.HasSuffix(string(prepared), "* Upstream\n- New entry\n"))
				}

				return nil
			}
			stub := &renderAutospecStub{base: testCase.base}
			update := &locklessRenderUpdate{subject: "update curl"}
			require.NoError(t, collectRenderUpstreamChanges(env.Env, "/working", "curl",
				testCase.version, testCase.history, update))
			err := prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy,
				testCase.history)
			require.NoError(t, err)
			assert.False(t, called, "carryover must not perform the final bump")

			_, err = bumpLocklessSpec(env.Env, path, update)
			require.NoError(t, err)
			assert.True(t, called)
			assert.Equal(t, testCase.wantChanges, update.changes)

			if testCase.bootstrap {
				assert.Equal(t, []string{"release", "changelog"}, stub.calls)

				changelog, err := fileutils.ReadFile(env.TestFS, "/working/changelog")
				require.NoError(t, err)
				assert.Equal(t, "* Full upstream history\n- Original change\n", string(changelog))
			} else {
				assert.Empty(t, stub.calls)
			}
		})
	}
}

const carriedAutoreleaseChangelog = "* Mon Nov 02 2026 Packager - 1-9\n- Preserve spaces  \n\n"

func testCommittedAutoreleaseHistory(t *testing.T) lastRender {
	t.Helper()

	content := "## RPMAUTOSPEC: processed with autorelease, autochangelog\n%global carried_release 9\n" +
		renderTestSpec("1", "%{carried_release}%{?dist}",
			"## START: rpmautospec generated changelog\n"+carriedAutoreleaseChangelog+
				"## END: rpmautospec generated changelog\n") +
		"%files\n/file\n"
	repo, commits := testRepoWithCommits(t, []testRepoCommit{{files: map[string][]byte{
		"curl.spec": []byte(content), "macros.inc": []byte("committed macro definitions"),
	}}})
	tree, err := resolveTree(repo, commits[0])
	require.NoError(t, err)

	return lastRender{spec: content, tree: tree, previous: "old", current: "old"}
}

func TestStripChangelogBoundaryComments(t *testing.T) {
	for _, testCase := range []struct {
		name, input, want string
	}{
		{name: "empty"},
		{name: "unchanged", input: "* Entry\n- Keep spaces  \n\n", want: "* Entry\n- Keep spaces  \n\n"},
		{name: "leading only", input: "## First\n## Second\n* Entry\n", want: "* Entry\n"},
		{name: "trailing only", input: "* Entry\n## First\n## Second", want: "* Entry\n"},
		{
			name:  "boundary blocks and interior comments",
			input: "\n## Start\n\n* Entry\n## Interior\n- Keep\n\n## End\n\n",
			want:  "\n\n* Entry\n## Interior\n- Keep\n\n\n",
		},
		{name: "comments only", input: "## Start\n## End\n", want: ""},
		{name: "CRLF", input: "## Start\r\n* Entry\r\n## End\r\n", want: "* Entry\r\n"},
		{
			name:  "other comments remain",
			input: "# Start\n## Interior\n* Entry\n ## Indented\n",
			want:  "# Start\n## Interior\n* Entry\n ## Indented\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, stripChangelogBoundaryComments(testCase.input))
		})
	}
}

func TestPrepareLocklessSpec_CommittedAutorelease(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		version         string
		upstreamChanged bool
		wantRelease     string
		expanded        string
	}{
		{name: "same upstream", version: "1", wantRelease: "%autorelease -b 9 -e test", expanded: "9.fc43"},
		{
			name: "new upstream same version", version: "1", upstreamChanged: true,
			wantRelease: "%autorelease -b 9 -e test", expanded: "9.fc43",
		},
		{
			name: "version change", version: "2", upstreamChanged: true,
			wantRelease: "%autorelease -b 9 -e test", expanded: "9.fc43",
		},
		{
			name: "version change without upstream changes", version: "2",
			wantRelease: "%autorelease -b 9 -e test", expanded: "9.fc43",
		},
		{
			name: "default expanded base", version: "2",
			wantRelease: "%autorelease -e test", expanded: "1.fc43",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)

			history := testCommittedAutoreleaseHistory(t)
			if testCase.upstreamChanged {
				history.current = "new"
			}

			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/curl.spec",
				[]byte(renderTestSpec(testCase.version, "%autorelease -b 2 -e test", "%autochangelog\n")),
				fileperms.PublicFile))
			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/changelog", []byte("stale"),
				fileperms.PublicFile))

			var snapshot string

			stub := &renderAutospecStub{expand: func(dir, filename string) (string, error) {
				snapshot = dir
				content, err := fileutils.ReadFile(env.TestFS, filepath.Join(dir, filename))
				require.NoError(t, err)
				assert.Equal(t, history.spec, string(content))
				content, err = fileutils.ReadFile(env.TestFS, filepath.Join(dir, "macros.inc"))
				require.NoError(t, err)
				assert.Equal(t, "committed macro definitions", string(content))

				return " " + testCase.expanded + "\n", nil
			}}

			env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")
			env.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
				return "abc123 Upstream fix\n", nil
			}
			bumped := false
			env.CmdFactory.RunHandler = func(_ *exec.Cmd) error {
				bumped = true
				content, err := fileutils.ReadFile(env.TestFS, "/working/curl.spec")
				require.NoError(t, err)
				assert.Contains(t, string(content), "Release: "+testCase.wantRelease+"\n")
				assert.Contains(t, string(content), "%changelog\n%autochangelog\n")
				content, err = fileutils.ReadFile(env.TestFS, "/working/changelog")
				require.NoError(t, err)
				assert.Equal(t, carriedAutoreleaseChangelog, string(content))

				return nil
			}
			update := &locklessRenderUpdate{subject: "updated curl"}
			require.NoError(t, collectRenderUpstreamChanges(env.Env, "/working", "curl",
				testCase.version, history, update))
			err := prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy, history)
			require.NoError(t, err)
			assert.False(t, bumped)

			_, err = bumpLocklessSpec(env.Env, "/working/curl.spec", update)
			require.NoError(t, err)
			assert.True(t, bumped)

			assert.Equal(t, []string{"expand"}, stub.calls)
			assert.NotEqual(t, "/working", snapshot)
			exists, err := fileutils.DirExists(env.TestFS, snapshot)
			require.NoError(t, err)
			assert.False(t, exists)
		})
	}
}

func TestPrepareLocklessSpec_UnprocessedAutorelease(t *testing.T) {
	const changelog = "## Keep committed comments\r\n* Entry\r\n- Keep spaces  \r\n\r\n"

	for _, testCase := range []struct {
		name, previous, working, want string
		newVersion                    bool
		noWorkingChangelog            bool
		base                          string
	}{
		{
			name: "changed version recalculates base", previous: "%autorelease -b 17",
			working: "%autorelease -b 2 -e test", want: "%autorelease -b 42 -e test",
			newVersion: true,
		},
		{
			name: "changed version omits default base", previous: "%autorelease -b 17",
			working: "%autorelease -b 2 -e test", want: "%autorelease -e test",
			newVersion: true, base: "Calculated release number: 1\n",
		},
		{
			name: "same version retains explicit committed default", previous: "%autorelease -b 1",
			working: "%autorelease -b 2", want: "%autorelease -b 1",
		},
		{
			name: "changed version recalculates braced base", previous: "%{autorelease -b17}",
			working: "%{autorelease -b 2 -e %{suffix}}", want: "%{autorelease -b 42 -e %{suffix}}",
			newVersion: true, noWorkingChangelog: true,
		},
		{
			name: "braced compact base", previous: "%{autorelease -b17}",
			working: "%{autorelease -b 2 -e %{suffix}}", want: "%{autorelease -b 17 -e %{suffix}}",
			noWorkingChangelog: true,
		},
		{
			name: "equals base", previous: "%autorelease -b=17",
			working: "%autorelease", want: "%autorelease -b 17",
		},
		{
			name: "omitted base", previous: "%autorelease",
			working: "%autorelease -b 2 -e test", want: "%autorelease -e test",
		},
		{
			name: "omitted braced base", previous: "%{autorelease}",
			working: "%{autorelease -e %{suffix} -b2}", want: "%{autorelease -e %{suffix}}",
		},
		{
			name: "both bases omitted", previous: "%autorelease",
			working: "%autorelease", want: "%autorelease",
		},
		{
			name: "same version carries release base", previous: "%autorelease -b 17",
			working: "%autorelease -b 2", want: "%autorelease -b 17",
		},
		{
			name: "changed version calculates base despite prior omission", previous: "%autorelease",
			working: "%autorelease -b 2", want: "%autorelease -b 42",
			newVersion: true,
		},
		{
			name: "changed version adds omitted working base", previous: "%autorelease -b 17",
			working: "%autorelease", want: "%autorelease -b 42",
			newVersion: true,
		},
		{
			name: "changed version ignores old invalid base", previous: "%autorelease -b invalid",
			working: "%autorelease -b 2", want: "%autorelease -b 42",
			newVersion: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			committed := renderTestSpec("1", testCase.previous, "%autochangelog\n")
			repo, hashes := testRepoWithCommits(t, []testRepoCommit{{files: map[string][]byte{
				"curl.spec": []byte(committed), "changelog": []byte(changelog),
			}}})
			tree, err := resolveTree(repo, hashes[0])
			require.NoError(t, err)

			env := testutils.NewTestEnv(t)

			version := "1"
			if testCase.newVersion {
				version = "2"
			}

			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/curl.spec",
				[]byte(renderTestSpec(version, testCase.working, "%autochangelog\n")), fileperms.PublicFile))

			if !testCase.noWorkingChangelog {
				require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/changelog",
					[]byte("replace upstream changelog"), fileperms.PublicFile))
			}

			stub := &renderAutospecStub{base: testCase.base}
			err = prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy,
				lastRender{spec: committed, tree: tree})
			require.NoError(t, err)

			if testCase.newVersion {
				assert.Equal(t, []string{"release"}, stub.calls)
			} else {
				assert.Empty(t, stub.calls)
			}

			content, err := fileutils.ReadFile(env.TestFS, "/working/curl.spec")
			require.NoError(t, err)
			assert.Contains(t, string(content), "Release: "+testCase.want+"\n")
			assert.Contains(t, string(content), "%changelog\n%autochangelog\n")
			content, err = fileutils.ReadFile(env.TestFS, "/working/changelog")
			require.NoError(t, err)
			assert.Equal(t, changelog, string(content))
		})
	}
}

func TestPrepareLocklessSpec_UnprocessedAutoreleaseFailure(t *testing.T) {
	for _, testCase := range []struct {
		name, release, want string
		missingFile         bool
		missingTree         bool
		newVersion          bool
		releaseError        error
	}{
		{
			name: "missing changelog", release: "%autorelease -b 17",
			missingFile: true, want: "reading committed 'changelog'",
		},
		{
			name: "missing tree", release: "%autorelease -b 17",
			missingTree: true, want: "missing committed dist-git tree",
		},
		{
			name: "zero base", release: "%autorelease -b 0",
			want: "using committed autorelease base",
		},
		{
			name: "invalid base", release: "%autorelease -b invalid",
			want: "using committed autorelease base",
		},
		{
			name: "changed version query failure", release: "%autorelease -b 17",
			newVersion: true, releaseError: errors.New("query failed"),
			want: "calculating upstream release base",
		},
		{
			name: "changed version missing changelog", release: "%autorelease -b 17",
			newVersion: true, missingFile: true, want: "reading committed 'changelog'",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			committed := renderTestSpec("1", testCase.release, "%autochangelog\n")

			files := map[string][]byte{"curl.spec": []byte(committed)}
			if !testCase.missingFile {
				files["changelog"] = []byte("committed changelog")
			}

			repo, hashes := testRepoWithCommits(t, []testRepoCommit{{files: files}})
			tree, err := resolveTree(repo, hashes[0])
			require.NoError(t, err)

			if testCase.missingTree {
				tree = nil
			}

			env := testutils.NewTestEnv(t)

			version := "1"
			if testCase.newVersion {
				version = "2"
			}

			original := renderTestSpec(version, "%autorelease -b 2", "%autochangelog\n")
			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/curl.spec",
				[]byte(original), fileperms.PublicFile))
			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/changelog",
				[]byte("upstream changelog"), fileperms.PublicFile))

			stub := &renderAutospecStub{err: testCase.releaseError}
			err = prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy,
				lastRender{spec: committed, tree: tree})
			require.ErrorContains(t, err, testCase.want)

			if testCase.newVersion {
				assert.Equal(t, []string{"release"}, stub.calls)
			} else {
				assert.Empty(t, stub.calls)
			}

			content, err := fileutils.ReadFile(env.TestFS, "/working/curl.spec")
			require.NoError(t, err)
			assert.Equal(t, original, string(content))
			content, err = fileutils.ReadFile(env.TestFS, "/working/changelog")
			require.NoError(t, err)
			assert.Equal(t, "upstream changelog", string(content))
		})
	}
}

func TestLocklessRenderRelease_StaticHistoryWithoutAutoreleaseMarker(t *testing.T) {
	for _, testCase := range []struct{ header, release string }{
		{"## RPMAUTOSPEC: autochangelog\n", "%autorelease"},
		{"## RPMAUTOSPEC: autochangelog\nautorelease\n", "%autorelease"},
		{" ## RPMAUTOSPEC: autorelease\n", "%autorelease"},
		{"## RPMAUTOSPEC: autorelease\n", "1%{?dist}"},
	} {
		env := testutils.NewTestEnv(t)
		stub := &renderAutospecStub{}
		history := lastRender{spec: testCase.header + renderTestSpec("1", "9", "old\n")}
		got, err := locklessRenderRelease(env.Env, stub, "/working", "curl.spec", testCase.release, history, false)
		require.NoError(t, err)
		assert.Equal(t, testCase.release, got)
		assert.Empty(t, stub.calls)
	}
}

func TestPrepareLocklessSpec_CommittedAutoreleaseFailure(t *testing.T) {
	for _, testCase := range []struct {
		name, output, want string
		err                error
	}{
		{name: "query failure", err: errors.New("query failed"), want: "expanding committed Release"},
		{name: "unresolved release", output: "%{unknown}", want: "using committed Release"},
		{name: "zero base", output: "0.fc43", want: "using committed Release"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			original := renderTestSpec("2", "%autorelease", "%autochangelog\n")
			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/curl.spec",
				[]byte(original), fileperms.PublicFile))
			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/changelog",
				[]byte("original changelog"), fileperms.PublicFile))

			stub := &renderAutospecStub{expand: func(_, _ string) (string, error) {
				return testCase.output, testCase.err
			}}
			err := prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy,
				testCommittedAutoreleaseHistory(t))
			require.ErrorContains(t, err, testCase.want)
			content, err := fileutils.ReadFile(env.TestFS, "/working/curl.spec")
			require.NoError(t, err)
			assert.Equal(t, original, string(content))
			content, err = fileutils.ReadFile(env.TestFS, "/working/changelog")
			require.NoError(t, err)
			assert.Equal(t, "original changelog", string(content))
		})
	}
}

func TestWithAutoreleaseBase(t *testing.T) {
	for _, testCase := range []struct{ release, want, wantDefault string }{
		{"%autorelease", "%autorelease -b 42", "%autorelease"},
		{"%autorelease -b 7 -e test", "%autorelease -b 42 -e test", "%autorelease -e test"},
		{"%autorelease -b7", "%autorelease -b 42", "%autorelease"},
		{"%autorelease -b=7 -e test", "%autorelease -b 42 -e test", "%autorelease -e test"},
		{"%{autorelease}", "%{autorelease -b 42}", "%{autorelease}"},
		{"%{autorelease -b 7}", "%{autorelease -b 42}", "%{autorelease}"},
		{"%autorelease -e %{suffix}", "%autorelease -e %{suffix} -b 42", "%autorelease -e %{suffix}"},
		{"%{autorelease}%{?dist}", "%{autorelease -b 42}%{?dist}", "%{autorelease}%{?dist}"},
		{"%{autorelease -e %{suffix}}", "%{autorelease -e %{suffix} -b 42}", "%{autorelease -e %{suffix}}"},
	} {
		got, err := withAutoreleaseBase(testCase.release, "Calculated release number: 42\n", true)
		require.NoError(t, err)
		assert.Equal(t, testCase.want, got)

		got, err = withAutoreleaseBase(testCase.release, "Calculated release number: 1\n", true)
		require.NoError(t, err)
		assert.Equal(t, testCase.wantDefault, got)
	}

	for _, output := range []string{"", "release: invalid", "0", "7.fc43"} {
		_, err := withAutoreleaseBase("%autorelease", output, true)
		require.Error(t, err)
	}
}

func TestPrepareComponentSources_LocalAndLockedSkipNewLifecycle(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		source   projectconfig.SpecSourceType
		lockless bool
	}{
		{name: "locked local", source: projectconfig.SpecSourceTypeLocal},
		{name: "lockless local", source: projectconfig.SpecSourceTypeLocal, lockless: true},
		{name: "locked upstream", source: projectconfig.SpecSourceTypeUpstream},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			ctrl := gomock.NewController(t)
			comp := components_testutils.NewMockComponent(ctrl)
			comp.EXPECT().GetName().AnyTimes().Return("curl")
			comp.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				Spec: projectconfig.SpecSource{SourceType: testCase.source},
			})

			manager := sourceproviders_test.NewMockSourceManager(ctrl)
			manager.EXPECT().FetchComponent(gomock.Any(), comp, "/staging/curl", gomock.Any()).Return(nil)

			original := newSourcePreparer
			newSourcePreparer = func(
				_ sourceproviders.SourceManager, fs opctx.FS, events opctx.EventListener, dry opctx.DryRunnable,
				options ...sources.PreparerOption,
			) (sources.SourcePreparer, error) {
				return sources.NewPreparer(manager, fs, events, dry, options...)
			}

			t.Cleanup(func() { newSourcePreparer = original })

			// A successful fetch without a spec deliberately stops at overlays.
			// The new lifecycle would instead require a project Git repository.
			_, err := prepareComponentSources(env.Env, nil, comp, "/staging", "/rendered",
				spec.EditorLegacy, false, testCase.lockless)
			require.ErrorContains(t, err, "failed to apply overlays")
		})
	}
}

func TestPrepareComponentSources_OverlaysBeforeReleaseCarryover(t *testing.T) {
	for _, testCase := range []struct {
		name, comment, wantComment string
		explicit, noGitCommit      bool
	}{
		{name: "default comment", wantComment: "update curl"},
		{
			name: "custom comment", comment: "Fix curl's patches; keep $COMPONENT literal",
			wantComment: "Fix curl's patches; keep $COMPONENT literal", explicit: true,
		},
		{name: "empty explicit comment", explicit: true},
		{
			name: "no Git commit", comment: "Prepare curl for review",
			wantComment: "Prepare curl for review", noGitCommit: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnvWithoutLockfile(t)
			ctrl := gomock.NewController(t)
			config := &projectconfig.ComponentConfig{
				Spec:  projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream},
				Build: projectconfig.ComponentBuildConfig{With: []string{"feature"}},
				Overlays: []projectconfig.ComponentOverlay{
					{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Release", Value: "2"},
					{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Version", Value: "1"},
					{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Summary", Value: "Overlaid summary"},
				},
				SourceFiles: []projectconfig.SourceFileReference{{
					Filename: "extra-source.tar.gz", Hash: "abc123", HashType: fileutils.HashTypeSHA512,
					Origin: projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/extra-source.tar.gz"},
				}},
			}
			comp := components_testutils.NewMockComponent(ctrl)
			comp.EXPECT().GetName().AnyTimes().Return("curl")
			comp.EXPECT().GetConfig().AnyTimes().Return(config)

			const (
				oldChangelog = "* Downstream entry\n"
				rootConfig   = `[project]
default-distro = { name = "test-distro", version = "1.0" }
[distros.test-distro.versions."1.0"]
release-ver = "3"
dist-git-branch = "upstream"
[components.curl.spec]
type = "upstream"
upstream-commit = "aaaaaaa"
`
			)

			repo, hashes := testRepoWithCommits(t, []testRepoCommit{{files: map[string][]byte{
				"azldev.toml":            []byte(rootConfig),
				"SPECS/c/curl/curl.spec": []byte(renderTestSpec("1", "9", oldChangelog)),
			}}})
			originalOpen := openRenderRepo
			openRenderRepo = func(_ *azldev.Env) (*gogit.Repository, string, error) {
				return repo, "/project", nil
			}
			manager := sourceproviders_test.NewMockSourceManager(ctrl)
			manager.EXPECT().FetchComponent(gomock.Any(), comp, "/staging/curl", gomock.Any()).DoAndReturn(
				func(_ context.Context, _ components.Component, dir string, _ ...sourceproviders.FetchComponentOption) error {
					return fileutils.WriteFile(env.TestFS, filepath.Join(dir, "curl.spec"),
						[]byte(renderTestSpec("2", "1", "* Upstream entry\n")), fileperms.PublicFile)
				})

			originalPreparer := newSourcePreparer
			newSourcePreparer = func(
				_ sourceproviders.SourceManager, fs opctx.FS, events opctx.EventListener, dry opctx.DryRunnable,
				options ...sources.PreparerOption,
			) (sources.SourcePreparer, error) {
				return sources.NewPreparer(manager, fs, events, dry, options...)
			}

			t.Cleanup(func() {
				openRenderRepo = originalOpen
				newSourcePreparer = originalPreparer
			})
			env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")

			bumped := false
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.Equal(t, []string{
					"git", "-C", "/project", "--literal-pathspecs", "log", "-1", "--format=%H",
					"--end-of-options", hashes[0], "--", "SPECS/c/curl/",
				}, cmd.Args)

				return hashes[0], nil
			}
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				bumped = true

				assert.Equal(t, "rpmdev-bumpspec", cmd.Args[0])
				assert.Equal(t, "-c", cmd.Args[1])
				assert.Equal(t, testCase.wantComment, cmd.Args[2])
				path := cmd.Args[len(cmd.Args)-1]
				content, err := fileutils.ReadFile(env.TestFS, path)
				require.NoError(t, err)
				assert.Contains(t, string(content), "Summary: Overlaid summary\n")
				assert.Contains(t, string(content), "Release: 9\n")
				assert.Contains(t, string(content), "%changelog\n"+oldChangelog)
				assert.Contains(t, string(content), "%{load:%{_sourcedir}/curl.azl.macros}")
				assert.Contains(t, string(content), "Source9999")

				macros, err := fileutils.ReadFile(env.TestFS, "/staging/curl/curl.azl.macros")
				require.NoError(t, err)
				assert.Contains(t, string(macros), "%_with_feature 1\n")

				manifest, err := fileutils.ReadFile(env.TestFS, "/staging/curl/sources")
				require.NoError(t, err)
				assert.Contains(t, string(manifest), "SHA512 (extra-source.tar.gz) = abc123")

				return fileutils.WriteFile(env.TestFS, path,
					[]byte(strings.Replace(string(content), "Release: 9\n", "Release: 10\n", 1)), fileperms.PublicFile)
			}
			results := make([]*RenderResult, 1)
			prepared := parallelPrepare(env.Env, nil, []components.Component{comp}, "/staging", &RenderOptions{
				OutputDir: "/project/SPECS", ChangelogComment: testCase.comment,
				changelogCommentExplicit: testCase.explicit, NoGitCommit: testCase.noGitCommit,
			}, results)
			require.Len(t, prepared, 1)
			prep := prepared[0]
			prep.update.changes = "abc123 Fix upstream"

			assert.False(t, bumped)

			prep.compOutputDir = "/project/SPECS/c/curl"
			changed, err := finishComponentRender(env.Env, prep,
				map[string]*sources.ComponentMockResult{"curl": {}}, "/staging", true, false, false)
			require.NoError(t, err)
			assert.True(t, changed)
			assert.True(t, bumped)

			content, err := fileutils.ReadFile(env.TestFS, "/project/SPECS/c/curl/curl.spec")
			require.NoError(t, err)
			assert.Contains(t, string(content), "Release: 10\n")

			var gitCommands []string

			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				gitCommands = append(gitCommands, cmd.Args[3])

				assert.False(t, testCase.noGitCommit, "no Git commands should run for --no-git-commit")

				if cmd.Args[3] == renderTestDiffCommand {
					return renderTestSpecDiff, nil
				}

				if cmd.Args[3] == "commit" {
					assert.Equal(t, []string{
						"git", "-C", "/project", "commit", "--only", "-m", testCase.wantComment,
						"-m", "abc123 Fix upstream", "--", "SPECS/c/curl",
					}, cmd.Args)
				}

				return "", nil
			}
			results[0] = &RenderResult{Status: renderStatusOK, lockless: true}
			commitLocklessRenders(env.Env, prepared, results, false)

			if testCase.noGitCommit {
				assert.Empty(t, gitCommands)
			} else {
				assert.Equal(t, []string{"add", renderTestDiffCommand, "commit"}, gitCommands)
			}

			assert.Equal(t, renderStatusOK, results[0].Status)

			content, err = fileutils.ReadFile(env.TestFS, "/project/SPECS/c/curl/curl.spec")
			require.NoError(t, err)
			assert.Contains(t, string(content), "Release: 10\n")
		})
	}
}

func TestPrepareComponentSources_LocklessSkipsSyntheticHistory(t *testing.T) {
	for _, release := range []string{"7%{?dist}", "%autorelease -b 9"} {
		t.Run(release, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			ctrl := gomock.NewController(t)
			comp := components_testutils.NewMockComponent(ctrl)
			comp.EXPECT().GetName().AnyTimes().Return("curl")
			comp.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal},
			})

			manager := sourceproviders_test.NewMockSourceManager(ctrl)

			const changelog = "* Existing entry\n- Keep unchanged\n"

			manager.EXPECT().FetchComponent(gomock.Any(), comp, "/staging/curl", gomock.Any()).DoAndReturn(
				func(_ context.Context, _ components.Component, dir string, opts ...sourceproviders.FetchComponentOption) error {
					var resolved sourceproviders.FetchComponentOptions
					for _, opt := range opts {
						opt(&resolved)
					}

					assert.True(t, resolved.PreserveGitDir)
					require.NoError(t, fileutils.WriteFile(env.TestFS, filepath.Join(dir, ".git/HEAD"),
						[]byte("upstream history"), fileperms.PublicFile))

					return fileutils.WriteFile(env.TestFS, filepath.Join(dir, "curl.spec"),
						[]byte(renderTestSpec("1", release, changelog)), fileperms.PublicFile)
				})

			original := newSourcePreparer
			newSourcePreparer = func(
				_ sourceproviders.SourceManager, fs opctx.FS, events opctx.EventListener, dry opctx.DryRunnable,
				options ...sources.PreparerOption,
			) (sources.SourcePreparer, error) {
				return sources.NewPreparer(manager, fs, events, dry, options...)
			}

			t.Cleanup(func() { newSourcePreparer = original })

			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				t.Fatalf("unexpected synthetic-history command: %v", cmd.Args)

				return nil
			}
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				t.Fatalf("unexpected synthetic-history query: %v", cmd.Args)

				return "", nil
			}

			_, err := prepareComponentSources(env.Env, nil, comp, "/staging", "/rendered",
				spec.EditorLegacy, false, true)
			require.NoError(t, err)
			content, err := fileutils.ReadFile(env.TestFS, "/staging/curl/curl.spec")
			require.NoError(t, err)
			assert.Contains(t, string(content), "Release: "+release+"\n")
			assert.Contains(t, string(content), "%changelog\n"+changelog)
			content, err = fileutils.ReadFile(env.TestFS, "/staging/curl/.git/HEAD")
			require.NoError(t, err)
			assert.Equal(t, "upstream history", string(content))
		})
	}
}

func TestPrepareLocklessSpec_DetectsVersionBeforeCarryover(t *testing.T) {
	env := testutils.NewTestEnv(t)
	original := renderTestSpec("2", "%autorelease", "%autochangelog\n")
	require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/curl.spec",
		[]byte(original), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/changelog",
		[]byte("original changelog"), fileperms.PublicFile))

	history := testCommittedAutoreleaseHistory(t)
	history.spec = strings.Replace(history.spec, "Version: 1\n", "", 1)
	stub := &renderAutospecStub{expand: func(_, _ string) (string, error) {
		return "9", nil
	}}

	err := prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy, history)
	require.ErrorContains(t, err, "reading committed Version")
	assert.Empty(t, stub.calls)

	content, err := fileutils.ReadFile(env.TestFS, "/working/curl.spec")
	require.NoError(t, err)
	assert.Equal(t, original, string(content))
	content, err = fileutils.ReadFile(env.TestFS, "/working/changelog")
	require.NoError(t, err)
	assert.Equal(t, "original changelog", string(content))
}

func TestPrepareLocklessSpec_StopsOnAutospecFailure(t *testing.T) {
	env := testutils.NewTestEnv(t)
	require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/curl.spec",
		[]byte(renderTestSpec("1", "%autorelease", "%autochangelog\n")), fileperms.PublicFile))

	stub := &renderAutospecStub{err: errors.New("autospec failed")}
	err := prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy, lastRender{})
	require.ErrorContains(t, err, "autospec failed")
	assert.Equal(t, []string{"release"}, stub.calls)
}

func TestFinishLocklessSpec_TimestampFailures(t *testing.T) {
	for _, testCase := range []struct {
		name, want string
		available  bool
		dateFails  bool
		readOnly   bool
	}{
		{name: "date missing", want: "requires 'date'"},
		{name: "date fails", available: true, dateFails: true, want: "getting rebuild timestamp"},
		{name: "write fails", available: true, readOnly: true, want: "writing rebuild timestamp"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)

			const path = "/working/curl.spec"

			original := renderTestSpec("1", "9", "")
			require.NoError(t, fileutils.WriteFile(env.TestFS, path, []byte(original), fileperms.PublicFile))

			if testCase.readOnly {
				env.TestFS = afero.NewReadOnlyFs(env.TestFS)
			}

			if testCase.available {
				env.CmdFactory.RegisterCommandInSearchPath("date")
			}

			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				t.Fatalf("must not bump without a timestamp: %v", cmd.Args)

				return nil
			}
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.Equal(t, []string{"date", "-Is"}, cmd.Args)

				if testCase.dateFails {
					return "", errors.New("date failed")
				}

				return "2026-11-04T22:53:27+00:00\n", nil
			}

			changed, err := finishLocklessSpec(env.Env, path, &locklessRenderUpdate{}, false, true)
			require.ErrorContains(t, err, testCase.want)
			assert.False(t, changed)

			content, err := fileutils.ReadFile(env.TestFS, path)
			require.NoError(t, err)
			assert.Equal(t, original, string(content))

			exists, err := fileutils.Exists(env.TestFS, filepath.Join("/working", noChangeRebuildMarker))
			require.NoError(t, err)
			assert.False(t, exists)
		})
	}
}

func TestBumpLocklessSpec_Failures(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(strconv.FormatBool(available), func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/curl.spec",
				[]byte(renderTestSpec("1", "1", "")), fileperms.PublicFile))

			if available {
				env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")
			}

			called := false
			env.CmdFactory.RunHandler = func(_ *exec.Cmd) error {
				called = true

				return errors.New("invalid spec")
			}
			_, err := bumpLocklessSpec(env.Env, "/working/curl.spec", &locklessRenderUpdate{subject: "updated curl"})

			assert.Equal(t, available, called)

			if available {
				require.ErrorContains(t, err, "invalid spec")
			} else {
				require.ErrorContains(t, err, "install 'rpmdevtools'")
			}
		})
	}
}

func TestCommitLocklessRenders_CheckOnlyAndFailure(t *testing.T) {
	for _, checkOnly := range []bool{false, true} {
		t.Run(strconv.FormatBool(checkOnly), func(t *testing.T) {
			env := testutils.NewTestEnv(t)

			const path = "/project/SPECS/c/curl/curl.spec"
			require.NoError(t, fileutils.WriteFile(env.TestFS, path, []byte("rendered output"), fileperms.PublicFile))

			called := false
			env.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
				called = true

				return "", errors.New("git identity is missing")
			}
			prepared := []*preparedComponent{{
				update: &locklessRenderUpdate{repoRoot: "/project", path: "SPECS/c/curl"},
			}}
			results := []*RenderResult{{Status: renderStatusOK}}
			commitLocklessRenders(env.Env, prepared, results, checkOnly)
			assert.Equal(t, !checkOnly, called)

			if checkOnly {
				assert.Equal(t, renderStatusOK, results[0].Status)
			} else {
				assert.Equal(t, renderStatusError, results[0].Status)
				assert.Contains(t, results[0].Error, "git identity is missing")
			}

			content, err := fileutils.ReadFile(env.TestFS, path)
			require.NoError(t, err)
			assert.Equal(t, "rendered output", string(content))
		})
	}
}

func TestRestoreRenderChangelog_ExactBytesAndOtherSections(t *testing.T) {
	for _, editor := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(editor), func(t *testing.T) {
			previous := []byte(renderTestSpec("1", "9", "* Old entry\n- Whitespace  \n\n"))
			upstream := []byte(renderTestSpec("2", "1", "* New entry\n- Discard\n") + "%files\n/file\n")
			got, err := restoreRenderChangelog(upstream, previous, editor)
			require.NoError(t, err)
			assert.Contains(t, string(got), "%changelog\n* Old entry\n- Whitespace  \n\n%files\n/file\n")
			assert.NotContains(t, string(got), "Discard")
		})
	}
}

func TestRestoreRenderChangelog_MissingSection(t *testing.T) {
	const oldChangelog = "%changelog\n* Old entry\n- Keep\n"
	for _, testCase := range []struct{ upstream, previous, want string }{
		{"Name: curl", oldChangelog, "Name: curl\n" + oldChangelog},
		{"Name: curl\n", oldChangelog, "Name: curl\n" + oldChangelog},
		{"Name: curl", "Name: old\n", "Name: curl"},
		{"Name: curl\n%changelog\nDiscard\n", "Name: old\n", "Name: curl\n"},
	} {
		got, err := restoreRenderChangelog([]byte(testCase.upstream), []byte(testCase.previous), spec.EditorLegacy)
		require.NoError(t, err)
		assert.Equal(t, testCase.want, string(got))
	}
}

func TestCommitLocklessRenders_SkipsFailedComponents(t *testing.T) {
	env := testutils.NewTestEnv(t)

	var commands [][]string

	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		commands = append(commands, cmd.Args)
		assert.Equal(t, "SPECS/v/vim", cmd.Args[len(cmd.Args)-1])

		if cmd.Args[3] == "diff" {
			return "SPECS/v/vim/vim.spec\n", nil
		}

		return "", nil
	}
	prepared := []*preparedComponent{
		{index: 0},
		{index: 1, update: &locklessRenderUpdate{repoRoot: "/project", path: "SPECS/c/curl"}},
		{index: 2, update: &locklessRenderUpdate{
			repoRoot: "/project", path: "SPECS/v/vim", subject: "updated vim",
		}},
	}
	results := []*RenderResult{
		{Status: renderStatusOK},
		{Status: renderStatusError, Error: "render failed"},
		{Status: renderStatusOK},
	}
	commitLocklessRenders(env.Env, prepared, results, false)
	assert.Equal(t, "render failed", results[1].Error)
	assert.Equal(t, renderStatusOK, results[2].Status)
	require.Len(t, commands, 3)
	assert.Equal(t, "commit", commands[2][3])
}

func TestCommitLocklessRender_PathOnlyAndNoEmptyCommits(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(strconv.FormatBool(changed), func(t *testing.T) {
			env := testutils.NewTestEnv(t)

			var commands [][]string

			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				commands = append(commands, cmd.Args)
				if cmd.Args[3] == "diff" && changed {
					return renderTestSpecDiff, nil
				}

				return "", nil
			}
			update := &locklessRenderUpdate{
				repoRoot: "/project", path: "SPECS/c/curl", subject: "updated curl", changes: "abc123 Fix",
			}
			require.NoError(t, commitLocklessRender(env.Env, update))
			assert.Equal(t, []string{"git", "-C", "/project", "add", "--all", "--", update.path}, commands[0])

			if changed {
				require.Len(t, commands, 3)
				assert.Equal(t, []string{
					"git", "-C", "/project", "commit", "--only", "-m", "updated curl",
					"-m", "abc123 Fix", "--", "SPECS/c/curl",
				}, commands[2])
			} else {
				assert.Len(t, commands, 2)
			}
		})
	}
}
