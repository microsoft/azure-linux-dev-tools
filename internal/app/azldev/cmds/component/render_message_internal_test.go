// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRenderEditor(t *testing.T, env *testutils.TestEnv, editor string) {
	t.Helper()

	osEnv, ok := env.TestOSEnv.(*testctx.TestOSEnv)
	require.True(t, ok)
	osEnv.SetEnv("EDITOR", editor)
}

func TestRenderEditorSelection(t *testing.T) {
	for _, testCase := range []struct {
		name, editor, available, wantError string
		want                               []string
	}{
		{
			name: "quoted arguments", editor: "edit-tool --wait 'two words'", available: "edit-tool",
			want: []string{"edit-tool", "--wait", "two words"},
		},
		{name: "fallback", available: "vi", want: []string{"vi"}},
		{name: "invalid quoting", editor: "edit-tool '", wantError: "parsing 'EDITOR'"},
		{name: "unavailable configured editor", editor: "edit-tool", wantError: "unavailable"},
		{name: "no editor", wantError: "no local editor"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setRenderEditor(t, env, testCase.editor)

			if testCase.available != "" {
				env.CmdFactory.RegisterCommandInSearchPath(testCase.available)
			}

			args, err := renderEditor(env.Env)
			if testCase.wantError != "" {
				require.ErrorContains(t, err, testCase.wantError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.want, args)
			}
		})
	}
}

func TestBumpLocklessSpecMessageAndAutorelease(t *testing.T) {
	for _, testCase := range []struct {
		name, release, message, wantMessage string
		provided, autorelease               bool
	}{
		{
			name: "edited text", release: "1", message: "Fix feature\n\nKeep details.\n",
			wantMessage: "Fix feature\nKeep details.",
		},
		{name: "provided text", release: "1", message: "Explicit entry", provided: true},
		{name: "provided multiline text", release: "1", message: "# Explicit\n\nKeep details.\n", provided: true},
		{name: "explicit empty value", release: "1", provided: true},
		{name: "autorelease without message", release: "%autorelease -b 9", autorelease: true},
		{name: "autorelease with message", release: "%autorelease", message: "Git only", provided: true, autorelease: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setRenderEditor(t, env, "edit-tool --wait")
			env.CmdFactory.RegisterCommandInSearchPath("edit-tool")

			if !testCase.autorelease {
				env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")
			}

			const specPath = "/working/curl.spec"
			require.NoError(t, fileutils.WriteFile(env.TestFS, specPath,
				[]byte(renderTestSpec("1", testCase.release, "")), fileperms.PublicFile))

			editCalls, bumpCalls := 0, 0

			wantMessage := testCase.message
			if !testCase.provided && !testCase.autorelease {
				wantMessage = testCase.wantMessage
			}

			var messagePath string

			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				switch cmd.Args[0] {
				case "edit-tool":
					editCalls++

					assert.Equal(t, "--wait", cmd.Args[1])
					messagePath = cmd.Args[2]
					assert.Same(t, os.Stdin, cmd.Stdin)

					content, err := fileutils.ReadFile(env.TestFS, messagePath)
					require.NoError(t, err)
					assert.True(t, strings.HasPrefix(string(content), "\n\n#"))
					assert.Contains(t, string(content), "enter a changelog entry message")
					assert.Contains(t, string(content), "Blank lines and lines starting with # will be ignored")

					return fileutils.WriteFile(env.TestFS, messagePath,
						[]byte(testCase.message+string(content)), fileperms.PrivateFile)
				case "rpmdev-bumpspec":
					bumpCalls++

					assert.Equal(t, []string{"rpmdev-bumpspec", "-c", wantMessage, specPath}, cmd.Args)
				default:
					t.Fatalf("unexpected command: %v", cmd.Args)
				}

				return nil
			}

			update := &locklessRenderUpdate{subjectProvided: testCase.provided}
			if testCase.provided {
				update.subject = testCase.message
			}

			_, err := bumpLocklessSpec(env.Env, specPath, update)
			require.NoError(t, err)

			if testCase.autorelease {
				assert.Zero(t, editCalls)
				assert.Zero(t, bumpCalls)
			} else {
				assert.Equal(t, 1, bumpCalls)
				assert.Equal(t, wantMessage, update.subject)

				if testCase.provided {
					assert.Zero(t, editCalls)
				} else {
					assert.Equal(t, 1, editCalls)

					exists, err := fileutils.Exists(env.TestFS, messagePath)
					require.NoError(t, err)
					assert.False(t, exists)
				}
			}
		})
	}
}

func TestCleanRenderChangelog(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		lines []string
		want  string
		crlf  bool
	}{
		{
			name:  "comments and blank lines",
			lines: []string{"", "", "# Instructions", "Fix feature", " \t ", "Keep details.", "# Comment", ""},
			want:  "Fix feature\nKeep details.",
		},
		{
			name:  "preserve message whitespace and hashes",
			lines: []string{"  # Indented message", "Keep # inline hash  "},
			want:  "  # Indented message\nKeep # inline hash  ",
		},
		{name: "only ignored lines", lines: []string{"", "\t", "# Instructions", "#", ""}},
		{
			name: "CRLF", lines: []string{"", "# Instructions", "Fix feature", "", "Keep details.", ""},
			want: "Fix feature\nKeep details.", crlf: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			content := strings.Join(testCase.lines, "\n")
			if testCase.crlf {
				content = strings.ReplaceAll(content, "\n", "\r\n")
			}

			assert.Equal(t, testCase.want, cleanRenderChangelog(content))
		})
	}
}

func TestEditRenderChangelogRejectsEmptyAndFailedEditor(t *testing.T) {
	editorErr := errors.New("editor failed")

	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged template", true: "failure"}[fail], func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setRenderEditor(t, env, "edit-tool")
			env.CmdFactory.RegisterCommandInSearchPath("edit-tool")

			var messagePath string

			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				messagePath = cmd.Args[1]

				if fail {
					return editorErr
				}

				return nil
			}

			_, err := editRenderChangelog(env.Env, "/working/curl.spec")
			if fail {
				require.ErrorIs(t, err, editorErr)
			} else {
				require.ErrorContains(t, err, "no changelog text")
			}

			exists, err := fileutils.Exists(env.TestFS, messagePath)
			require.NoError(t, err)
			assert.False(t, exists)
		})
	}
}

func TestLocklessRenderCommitPromptsWithoutMessage(t *testing.T) {
	env := testutils.NewTestEnv(t)

	var templatePath string

	env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		assert.Equal(t, []string{"git", "-C", "/project", "commit", "--only"}, cmd.Args[:5])
		assert.NotContains(t, cmd.Args, "-m")
		assert.Contains(t, cmd.Args, "SPECS/c/curl")
		assert.Contains(t, cmd.Args, ":(top,literal)inputs.toml")
		assert.Same(t, os.Stdin, cmd.Stdin)
		assert.Same(t, os.Stdout, cmd.Stdout)
		assert.Same(t, os.Stderr, cmd.Stderr)
		templatePath = cmd.Args[slices.Index(cmd.Args, "--template")+1]
		content, err := fileutils.ReadFile(env.TestFS, templatePath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "abc123 Upstream fix")

		return nil
	}
	require.NoError(t, runLocklessRenderCommit(env.Env,
		&locklessRenderUpdate{repoRoot: "/project", changes: "abc123 Upstream fix"},
		[]string{"SPECS/c/curl", ":(top,literal)inputs.toml"}))
	require.NotEmpty(t, templatePath)
	exists, err := fileutils.Exists(env.TestFS, templatePath)
	require.NoError(t, err)
	assert.False(t, exists)
}
