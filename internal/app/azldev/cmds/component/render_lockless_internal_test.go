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
default-distro = { name = "test", version = "1" }
rendered-specs-dir = "SPECS"
[distros.test.versions."1"]
release-ver = "1"
`)
	oldSpec := renderTestSpec("1", "9%{?dist}", "* Previous\n- Keep exactly\n")
	repo, _ := testRepoWithCommits(t, []testRepoCommit{
		{files: map[string][]byte{
			"azldev.toml":            root,
			"component.toml":         []byte("[components.curl.spec]\ntype = 'upstream'\n"),
			"pins/curl.toml":         []byte("[components.curl.spec]\nupstream-commit = 'aaaaaaa'\n"),
			"SPECS/c/curl/curl.spec": []byte(oldSpec),
		}},
		{files: map[string][]byte{
			"pins/curl.toml": []byte("[components.curl.spec]\nupstream-commit = 'bbbbbbb'\n"),
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
}

func TestReadLastRender_HistoricalLockFallback(t *testing.T) {
	const (
		oldLock = "version = 1\nupstream-commit = 'aaaaaaa'\n"
		config  = `[project]
default-distro = { name = "test", version = "1" }
lock-dir = "../historical-locks"
[distros.test.versions."1"]
release-ver = "1"
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
		{name: "missing selection", noSelection: true, lock: oldLock},
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

			repo, _ := testRepoWithCommits(t, []testRepoCommit{
				{files: files},
				{files: map[string][]byte{
					"components.toml":            []byte(current),
					"settings/project.toml":      []byte(strings.ReplaceAll(config, "historical-locks", "current-locks")),
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

			history, err := readLastRender(env.Env, repo, "/project", "SPECS/c/curl", "curl")
			if testCase.missingCurrent {
				require.ErrorContains(t, err, "current render upstream commit")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, history.previous)
			assert.Equal(t, "bbbbbbb", history.current)
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

			repo, _ := testRepoWithCommits(t, commits)
			env := testutils.NewTestEnv(t)

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
	expand func(string, string) (string, error)
}

func (s *renderAutospecStub) GenerateChangelog(_ context.Context, _, _ string) (string, error) {
	s.calls = append(s.calls, "changelog")

	return "* Full upstream history\n- Original change\n", s.err
}

func (s *renderAutospecStub) CalculateBaseRelease(_ context.Context, _, _ string) (string, error) {
	s.calls = append(s.calls, "release")

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
			name: "version change resets release", version: "2", release: "1%{?dist}",
			history: lastRender{
				spec: renderTestSpec("1", "9%{?dist}", oldChangelog), previous: "old", current: "new",
			},
			wantRelease: "1%{?dist}", wantChanges: "abc123 Upstream fix\ndef456 Another fix",
		},
		{
			name: "initial static spec", version: "2", release: "3%{?dist}",
			wantRelease: "3%{?dist}", wantChanges: "Update curl to version 2",
		},
		{
			name: "initial autorelease", version: "2", release: "%autorelease -b 7",
			wantRelease: "%autorelease -b 7", wantChanges: "Update curl to version 2", bootstrap: true,
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
				called = true

				entry := "updated curl"
				if testCase.wantChanges != "" {
					entry += "\n" + testCase.wantChanges
				}

				assert.Equal(t, []string{"rpmdev-bumpspec", "-c", entry, path}, cmd.Args)
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
			stub := &renderAutospecStub{}
			update := &locklessRenderUpdate{subject: "updated curl"}
			err := prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy,
				testCase.history, update)
			require.NoError(t, err)
			assert.True(t, called)
			assert.Equal(t, testCase.wantChanges, update.changes)

			if testCase.bootstrap {
				assert.Equal(t, []string{"changelog", "release"}, stub.calls)

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
	}{
		{name: "same upstream", version: "1", wantRelease: "%autorelease -b 9 -e test"},
		{
			name: "new upstream same version", version: "1", upstreamChanged: true,
			wantRelease: "%autorelease -b 9 -e test",
		},
		{
			name: "version reset", version: "2", upstreamChanged: true,
			wantRelease: "%autorelease -b 2 -e test",
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

				return " 9.fc43\n", nil
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
			err := prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy,
				history, &locklessRenderUpdate{subject: "updated curl"})
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

func TestLocklessRenderRelease_RequiresAutoreleaseMarker(t *testing.T) {
	for _, testCase := range []struct{ header, release string }{
		{"## RPMAUTOSPEC: autochangelog\n", "%autorelease"},
		{"## RPMAUTOSPEC: autochangelog\nautorelease\n", "%autorelease"},
		{" ## RPMAUTOSPEC: autorelease\n", "%autorelease"},
		{"## RPMAUTOSPEC: autorelease\n", "1%{?dist}"},
	} {
		env := testutils.NewTestEnv(t)
		stub := &renderAutospecStub{}
		history := lastRender{spec: testCase.header + renderTestSpec("1", "9", "old\n")}
		got, err := locklessRenderRelease(env.Env, stub, "/working", "curl.spec", testCase.release, history)
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
			original := renderTestSpec("1", "%autorelease", "%autochangelog\n")
			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/curl.spec",
				[]byte(original), fileperms.PublicFile))
			require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/changelog",
				[]byte("original changelog"), fileperms.PublicFile))

			stub := &renderAutospecStub{expand: func(_, _ string) (string, error) {
				return testCase.output, testCase.err
			}}
			err := prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy,
				testCommittedAutoreleaseHistory(t), &locklessRenderUpdate{subject: "updated curl"})
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
	for _, testCase := range []struct{ release, want string }{
		{"%autorelease", "%autorelease -b 42"},
		{"%autorelease -b 7 -e test", "%autorelease -b 42 -e test"},
		{"%autorelease -b7", "%autorelease -b 42"},
		{"%{autorelease}", "%{autorelease -b 42}"},
		{"%{autorelease -b 7}", "%{autorelease -b 42}"},
		{"%autorelease -e %{suffix}", "%autorelease -e %{suffix} -b 42"},
		{"%{autorelease}%{?dist}", "%{autorelease -b 42}%{?dist}"},
		{"%{autorelease -e %{suffix}}", "%{autorelease -e %{suffix} -b 42}"},
	} {
		got, err := withAutoreleaseBase(testCase.release, "Calculated release number: 42\n")
		require.NoError(t, err)
		assert.Equal(t, testCase.want, got)
	}

	for _, output := range []string{"", "release: invalid", "0", "7.fc43"} {
		_, err := withAutoreleaseBase("%autorelease", output)
		require.Error(t, err)
	}

	got, err := withAutoreleaseBase("%autorelease", "1")
	require.NoError(t, err)
	assert.Equal(t, "%autorelease -b 1", got)
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

func TestPrepareLocklessSpec_StopsOnAutospecFailure(t *testing.T) {
	env := testutils.NewTestEnv(t)
	require.NoError(t, fileutils.WriteFile(env.TestFS, "/working/curl.spec",
		[]byte(renderTestSpec("1", "%autorelease", "%autochangelog\n")), fileperms.PublicFile))

	stub := &renderAutospecStub{err: errors.New("autospec failed")}
	err := prepareLocklessSpec(env.Env, stub, "/working", "curl", spec.EditorLegacy, lastRender{},
		&locklessRenderUpdate{subject: "updated curl"})
	require.ErrorContains(t, err, "autospec failed")
	assert.Equal(t, []string{"changelog"}, stub.calls)
}

func TestBumpLocklessSpec_Failures(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(strconv.FormatBool(available), func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			if available {
				env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")
			}

			called := false
			env.CmdFactory.RunHandler = func(_ *exec.Cmd) error {
				called = true

				return errors.New("invalid spec")
			}
			err := bumpLocklessSpec(env.Env, "/working/curl.spec", "updated curl")

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
					return "SPECS/c/curl/curl.spec\n", nil
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
