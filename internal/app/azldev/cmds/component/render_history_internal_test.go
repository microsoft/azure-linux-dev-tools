// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const renderHistoryLogFormat = "--format=%T %an <%ae> %ad %cn <%ce> %cd %s"

func runTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Author", "GIT_AUTHOR_EMAIL=author@example.com",
		"GIT_COMMITTER_NAME=Committer", "GIT_COMMITTER_EMAIL=committer@example.com")

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	return strings.TrimSpace(string(output))
}

func writeTestGitFile(t *testing.T, repo, path, content string) {
	t.Helper()

	path = filepath.Join(repo, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func commitTestGit(t *testing.T, repo, subject string) {
	t.Helper()

	runTestGit(t, repo, "add", "-A")
	runTestGit(t, repo, "commit", "-q", "--allow-empty", "-m", subject, "-m", "body for "+subject)
}

func TestRenderHistoryRepoMatchesFilteredHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	for _, merge := range []bool{false, true} {
		t.Run(map[bool]string{false: "replay", true: "filter fallback for merges"}[merge], func(t *testing.T) {
			_, filterErr := exec.LookPath("git-filter-repo")
			if merge && filterErr != nil {
				t.Skip("git-filter-repo is not installed")
			}

			root := t.TempDir()
			repo := filepath.Join(root, "project")
			runTestGit(t, root, "init", "-q", "-b", "main", repo)

			writeTestGitFile(t, repo, "specs/c/curl/curl.spec", "v1")
			writeTestGitFile(t, repo, "specs/c/curl-other/curl.spec", "other")
			commitTestGit(t, repo, "add curl")
			writeTestGitFile(t, repo, "base/comps/curl.toml", "config")
			commitTestGit(t, repo, "configure curl")

			if merge {
				runTestGit(t, repo, "checkout", "-q", "-b", "side")
				writeTestGitFile(t, repo, "specs/c/curl/side.patch", "side")
				commitTestGit(t, repo, "side change")
				runTestGit(t, repo, "checkout", "-q", "main")
				writeTestGitFile(t, repo, "README", "readme")
				commitTestGit(t, repo, "main change")
				runTestGit(t, repo, "merge", "-q", "--no-ff", "-m", "merge side", "side")
			}

			writeTestGitFile(t, repo, "specs/c/curl/curl.spec", "v2")
			commitTestGit(t, repo, "update curl")
			runTestGit(t, repo, "rm", "-q", "-r", "specs/c/curl")
			commitTestGit(t, repo, "remove curl")
			writeTestGitFile(t, repo, "specs/c/curl/curl.spec", "v3")
			commitTestGit(t, repo, "restore curl")
			writeTestGitFile(t, repo, "specs/c/curl-other/curl.spec", "other 2")
			commitTestGit(t, repo, "update other")

			testEnv := testutils.NewTestEnv(t)
			dryRun := azldev.NewAppDryRunnable(false)
			cmdFactory, err := azldev.DefaultCmdFactory(dryRun, testEnv.EventListener)
			require.NoError(t, err)

			env := azldev.NewEnv(t.Context(), azldev.EnvOptions{
				ProjectDir: repo, Config: testEnv.Config, DryRunnable: dryRun, EventListener: testEnv.EventListener,
				Interfaces: azldev.SystemInterfaces{
					CmdFactory: cmdFactory, FileSystemFactory: azldev.DefaultFileSystemFactory(),
					OSEnvFactory: azldev.DefaultOSEnvFactory(),
				},
			})

			staging := filepath.Join(root, "staging")
			require.NoError(t, os.Mkdir(staging, 0o755))

			history, err := newRenderHistoryRepo(env, staging, filepath.Join(repo, "specs/c/curl"))
			require.NoError(t, err)

			assert.Equal(t, runTestGit(t, repo, "rev-parse", "HEAD:specs/c/curl"),
				runTestGit(t, history.dir, "rev-parse", "HEAD^{tree}"))

			replayed := runTestGit(t, history.dir, "log", renderHistoryLogFormat)
			if !merge {
				assert.Equal(t, []string{"restore curl", "remove curl", "update curl", "add curl"},
					strings.Split(runTestGit(t, history.dir, "log", "--format=%s"), "\n"))
				assert.Equal(t, "body for add curl",
					runTestGit(t, history.dir, "log", "-1", "--format=%b", "HEAD~3"))
			}

			if filterErr == nil {
				filtered := filepath.Join(root, "filtered")
				runTestGit(t, root, "clone", "-q", "--single-branch", "--no-local", "--", repo, filtered)
				runTestGit(t, filtered, "filter-repo", "--force", "--subdirectory-filter", "specs/c/curl")
				assert.Equal(t, runTestGit(t, filtered, "log", renderHistoryLogFormat), replayed)
			}

			// The project repository is only read; the replay writes its own objects.
			assert.Empty(t, runTestGit(t, repo, "status", "--porcelain"))
			runTestGit(t, repo, "fsck", "--no-progress")

			require.NoError(t, history.Remove())

			entries, err := os.ReadDir(staging)
			require.NoError(t, err)
			assert.Empty(t, entries)
		})
	}
}
