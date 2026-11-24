// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const detectorTestRoot = "/project"

func detectorTestConfig(define string) []byte {
	return []byte(fmt.Sprintf(`[project]
default-distro = { name = "testdistro", version = "1.0" }
rendered-specs-dir = "SPECS"

[distros.testdistro.versions."1.0"]
release-ver = "1.0"

[components.curl]
spec = { type = "upstream", upstream-distro = { name = "testdistro", version = "1.0" } }
build = { defines = { feature = %q } }
`, define))
}

// detectorTestCommit is a project commit with its subject.
type detectorTestCommit struct {
	subject string
	files   map[string][]byte
}

// detectorTestCommits are the default commits: the last render at index 0, an
// unrelated change, a component change, a rendered-only change, and another
// component change.
func detectorTestCommits() []detectorTestCommit {
	return []detectorTestCommit{
		{"Render curl", map[string][]byte{"azldev.toml": detectorTestConfig("a")}},
		{"Update README", map[string][]byte{"README.md": []byte("readme\n")}},
		{"Enable feature b", map[string][]byte{"azldev.toml": detectorTestConfig("b")}},
		{"Render specs", map[string][]byte{"SPECS/c/curl/curl.spec": []byte("spec\n")}},
		{"Enable feature c", map[string][]byte{"azldev.toml": detectorTestConfig("c")}},
	}
}

// newDetectorTest builds the repo, stubs [openRenderRepo], and answers the git
// commands of [renderMessageDetector] from the repo and the working files.
func newDetectorTest(
	t *testing.T, commits []detectorTestCommit, working map[string][]byte,
) (*renderMessageDetector, []string) {
	t.Helper()

	repoCommits := make([]testRepoCommit, 0, len(commits))
	for _, commit := range commits {
		repoCommits = append(repoCommits, testRepoCommit{files: commit.files})
	}

	goGitRepo, repo, hashes := testNativeRepoWithCommits(t, repoCommits)
	env := testutils.NewTestEnvWithoutLockfile(t)
	env.Config.Project.RenderedSpecsDir = filepath.Join(detectorTestRoot, "SPECS")

	head := hashes[len(hashes)-1]
	headFiles := detectorTreeFiles(t, goGitRepo, head)

	for path, content := range headFiles {
		if _, changed := working[path]; !changed {
			working[path] = content
		}
	}

	for path, content := range working {
		require.NoError(t, fileutils.MkdirAll(env.TestFS, filepath.Dir(filepath.Join(detectorTestRoot, path))))
		require.NoError(t, fileutils.WriteFile(env.TestFS, filepath.Join(detectorTestRoot, path),
			content, fileperms.PublicFile))
	}

	originalOpen := openRenderRepo
	openRenderRepo = func(_ *azldev.Env) (*git.Repo, string, error) {
		return repo, detectorTestRoot, nil
	}

	t.Cleanup(func() { openRenderRepo = originalOpen })

	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		require.Equal(t, []string{"git", "-C", detectorTestRoot}, cmd.Args[:3])

		args := cmd.Args[3:]
		switch args[0] {
		case renderTestRevParse:
			return head, nil
		case "log":
			var lines []string

			for idx := len(commits) - 1; idx > 0; idx-- {
				lines = append(lines, hashes[idx]+"\x00"+commits[idx].subject)
			}

			return strings.Join(lines, "\n"), nil
		case "diff-tree":
			return strings.Join(detectorChangedPaths(
				detectorTreeFiles(t, goGitRepo, args[5]), detectorTreeFiles(t, goGitRepo, args[6])), "\x00"), nil
		case "diff":
			return strings.Join(detectorChangedPaths(headFiles, working), "\x00"), nil
		case "ls-files":
			if slices.Contains(args, "--cached") {
				return strings.Join(detectorPaths(working), "\x00"), nil
			}

			return "", nil
		}

		t.Fatalf("unexpected command: %v", cmd.Args)

		return "", nil
	}

	return newRenderMessageDetector(env.Env), hashes
}

func detectorTreeFiles(t *testing.T, repo *gogit.Repository, hash string) map[string][]byte {
	t.Helper()

	commit, err := repo.CommitObject(plumbing.NewHash(hash))
	require.NoError(t, err)

	files, err := commit.Files()
	require.NoError(t, err)

	result := make(map[string][]byte)

	require.NoError(t, files.ForEach(func(file *object.File) error {
		content, err := file.Contents()
		result[file.Name] = []byte(content)

		return err
	}))

	return result
}

func detectorPaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	return paths
}

func detectorChangedPaths(left, right map[string][]byte) []string {
	var changed []string

	for _, path := range detectorPaths(left) {
		if content, found := right[path]; !found || string(content) != string(left[path]) {
			changed = append(changed, path)
		}
	}

	for _, path := range detectorPaths(right) {
		if _, found := left[path]; !found {
			changed = append(changed, path)
		}
	}

	return changed
}

func TestRenderMessageDetector(t *testing.T) {
	revert := detectorTestCommits()[:3]
	revert = append(revert,
		detectorTestCommit{"Restore feature a", map[string][]byte{"azldev.toml": detectorTestConfig("a")}},
		detectorTestCommit{"Enable feature d", map[string][]byte{"azldev.toml": detectorTestConfig("d")}})

	for _, testCase := range []struct {
		name    string
		commits []detectorTestCommit
		working map[string][]byte
		first   bool
		want    string
		wantErr error
	}{
		{
			name:    "changed subjects oldest first",
			commits: detectorTestCommits(),
			working: map[string][]byte{"SPECS/c/curl/curl.spec": []byte("uncommitted render\n")},
			want:    "Enable feature b\n- Enable feature c",
		},
		{
			name:    "stops at commit matching last render",
			commits: revert,
			working: map[string][]byte{"README.md": []byte("uncommitted unrelated\n")},
			want:    "Enable feature d",
		},
		{
			name:    "no component changes",
			commits: detectorTestCommits()[:2],
			working: map[string][]byte{},
			wantErr: errNoComponentCommits,
		},
		{
			name:    "uncommitted component change",
			commits: detectorTestCommits(),
			working: map[string][]byte{"azldev.toml": detectorTestConfig("z")},
			wantErr: errUncommittedComponentChanges,
		},
		{
			name:    "first render",
			commits: detectorTestCommits(),
			working: map[string][]byte{"azldev.toml": detectorTestConfig("z")},
			first:   true,
			want:    renderInitialMessage,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			detector, hashes := newDetectorTest(t, testCase.commits, testCase.working)

			lastCommit := hashes[0]
			if testCase.first {
				lastCommit = ""
			}

			message, err := detector.detect("curl", lastCommit)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, message)
		})
	}
}

func TestResolveLocklessMessage(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		update locklessRenderUpdate
		want   string
	}{
		{
			name:   "unchanged render uses no-change message",
			update: locklessRenderUpdate{unchanged: true, detector: &renderMessageDetector{}},
			want:   renderNoChangeMessage,
		},
		{
			name:   "explicit message wins for unchanged render",
			update: locklessRenderUpdate{unchanged: true, message: "Rebuild for foo"},
			want:   "Rebuild for foo",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.NoError(t, resolveLocklessMessage(&testCase.update))
			assert.Equal(t, testCase.want, testCase.update.message)
		})
	}
}

func TestCheckRenderErrors_ChangelogDetectionAlwaysFails(t *testing.T) {
	for _, sentinel := range []error{errNoComponentCommits, errUncommittedComponentChanges} {
		err := fmt.Errorf("detecting changelog text for %#q:\n%w", "curl", sentinel)

		for _, failOnError := range []bool{false, true} {
			require.ErrorIs(t, checkRenderErrors([]*RenderResult{{
				Component: "curl", Status: renderStatusError, renderErr: err,
			}}, failOnError), sentinel)
		}
	}

	// Other errors still fail only with '--fail-on-error'.
	other := []*RenderResult{{Component: "curl", Status: renderStatusError, renderErr: assert.AnError}}
	require.NoError(t, checkRenderErrors(other, false))
	require.Error(t, checkRenderErrors(other, true))
}
