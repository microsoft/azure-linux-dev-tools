// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/externalcmd"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func runTreeTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Author", "GIT_AUTHOR_EMAIL=author@example.com",
		"GIT_COMMITTER_NAME=Committer", "GIT_COMMITTER_EMAIL=committer@example.com")

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	return strings.TrimSpace(string(output))
}

func TestRepoReadsCommittedTrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	ctrl := gomock.NewController(t)
	cmdFactory, err := externalcmd.NewCmdFactory(
		opctx_test.NewNoOpMockDryRunnable(ctrl), opctx_test.NewNoOpMockEventListener(ctrl),
	)
	require.NoError(t, err)

	root := filepath.Join(t.TempDir(), "repo")
	runTreeTestGit(t, filepath.Dir(root), "init", "-q", root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dir", "sub"), 0o755))

	repo, err := git.OpenRepo(t.Context(), cmdFactory, filepath.Join(root, "dir"))
	require.NoError(t, err)
	assert.Equal(t, runTreeTestGit(t, root, "rev-parse", "--show-toplevel"), repo.Root())

	head, err := repo.Head(t.Context())
	require.NoError(t, err)
	assert.Empty(t, head, "unborn HEAD")

	files := map[string]string{
		"dir/sub/file": "content with trailing space \n\n",
		"dir/top":      "\x00binary\r\n",
		"other":        "",
		"skipped/file": "skipped",
	}
	for name, content := range files {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0o600))
	}

	require.NoError(t, os.Symlink("dir/top", filepath.Join(root, "link")))
	runTreeTestGit(t, root, "add", "-A")
	runTreeTestGit(t, root, "update-index", "--add", "--cacheinfo",
		"160000,"+strings.Repeat("1", 40)+",submodule")
	runTreeTestGit(t, root, "commit", "-q", "-m", "commit")

	head, err = repo.Head(t.Context())
	require.NoError(t, err)
	assert.Equal(t, runTreeTestGit(t, root, "rev-parse", "HEAD"), head)

	resolved, err := repo.ResolveCommit(t.Context(), "HEAD")
	require.NoError(t, err)
	assert.Equal(t, head, resolved)

	_, err = repo.ResolveCommit(t.Context(), "missing-ref")
	require.Error(t, err)

	tree, err := repo.Tree(t.Context(), head)
	require.NoError(t, err)
	assert.Equal(t, runTreeTestGit(t, root, "rev-parse", "HEAD^{tree}"), tree.Hash())

	subtree, err := tree.Subtree(t.Context(), "dir/sub")
	require.NoError(t, err)
	assert.Equal(t, runTreeTestGit(t, root, "rev-parse", "HEAD:dir/sub"), subtree.Hash())

	content, err := subtree.ReadFile(t.Context(), "file")
	require.NoError(t, err)
	assert.Equal(t, files["dir/sub/file"], string(content))

	content, err = tree.ReadFile(t.Context(), "link")
	require.NoError(t, err)
	assert.Equal(t, "dir/top", string(content))

	for _, missing := range []string{"absent", "dir/absent", "dir/top/absent", "submodule"} {
		_, err = tree.ReadFile(t.Context(), missing)
		require.ErrorIs(t, err, git.ErrPathNotFound, missing)
	}

	_, err = tree.ReadFile(t.Context(), "dir")
	require.ErrorIs(t, err, git.ErrPathNotFound, "directory is not a file")

	_, err = tree.Subtree(t.Context(), "dir/top")
	require.ErrorIs(t, err, git.ErrPathNotFound, "file is not a directory")

	_, err = tree.Subtree(t.Context(), "absent")
	require.ErrorIs(t, err, git.ErrPathNotFound)

	visited := make(map[string]string)
	require.NoError(t, tree.ForEachFile(t.Context(),
		func(path string) bool { return strings.HasPrefix(path, "skipped/") },
		func(path string, content []byte) error {
			visited[path] = string(content)

			return nil
		}))

	assert.Equal(t, map[string]string{
		"dir/sub/file": files["dir/sub/file"],
		"dir/top":      files["dir/top"],
		"other":        "",
		"link":         "dir/top",
	}, visited)
}
