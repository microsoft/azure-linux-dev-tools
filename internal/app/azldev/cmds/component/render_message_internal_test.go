// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"os"
	"os/exec"
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

	original := renderIsTerminal
	renderIsTerminal = func(_ uintptr) bool { return true }

	t.Cleanup(func() { renderIsTerminal = original })

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

func TestRenderMessageRequiresInteractiveTTY(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		stdinTTY, stdoutTTY bool
	}{
		{name: "stdin is not a tty", stdoutTTY: true},
		{name: "stdout is not a tty", stdinTTY: true},
		{name: "neither is a tty"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setRenderEditor(t, env, "unavailable-editor")

			renderIsTerminal = func(descriptor uintptr) bool {
				switch descriptor {
				case os.Stdin.Fd():
					return testCase.stdinTTY
				case os.Stdout.Fd():
					return testCase.stdoutTTY
				default:
					t.Fatalf("unexpected terminal descriptor: %d", descriptor)

					return false
				}
			}
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				t.Fatalf("must not invoke an editor without both TTYs: %v", cmd.Args)

				return nil
			}
			message, err := editRenderMessage(env.Env, "/working/curl.spec", renderLocalCommitTemplate)
			require.ErrorContains(t, err, "non-interactive renders must use '--changelog-comment' and/or '--no-git-commit'")
			assert.Empty(t, message)
		})
	}
}

func TestNonInteractiveRenderMessageOptions(t *testing.T) {
	for _, testCase := range []struct {
		name, release          string
		local, noGit, provided bool
		wantError              bool
	}{
		{name: "provided upstream static", release: "1", provided: true},
		{name: "provided upstream autorelease", release: "%autorelease", provided: true},
		{name: "provided local", release: "1", local: true, provided: true},
		{name: "local static no commit", release: "1", local: true, noGit: true},
		{name: "local autorelease no commit", release: "%autorelease", local: true, noGit: true},
		{name: "upstream autorelease no commit", release: "%autorelease", noGit: true},
		{name: "upstream static no commit still needs message", release: "1", noGit: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			original := renderIsTerminal
			renderIsTerminal = func(_ uintptr) bool { return false }

			t.Cleanup(func() { renderIsTerminal = original })
			env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")

			const specPath = "/working/curl.spec"
			require.NoError(t, fileutils.WriteFile(env.TestFS, specPath,
				[]byte(renderTestSpec("1", testCase.release, "")), fileperms.PublicFile))

			bumped := false
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				require.Equal(t, "rpmdev-bumpspec", cmd.Args[0])

				bumped = true

				return nil
			}

			update := &locklessRenderUpdate{
				local: testCase.local, noGitCommit: testCase.noGit, subjectProvided: testCase.provided,
			}
			if testCase.provided {
				update.subject = "Provided render message"
			}

			_, err := finishLocklessSpec(env.Env, specPath, update, true, false)
			if testCase.wantError {
				require.ErrorContains(t, err, "non-interactive renders must use")
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, !testCase.local && testCase.release == "1" && !testCase.wantError, bumped)
		})
	}
}

func TestFinishLocklessSpecMessages(t *testing.T) {
	for _, testCase := range []struct {
		name, release, message, template string
		provided, local, noGit           bool
	}{
		{name: "upstream static commit", release: "1", template: renderChangelogTemplate},
		{name: "upstream static no commit", release: "1", noGit: true, template: renderNoGitChangelogTemplate},
		{name: "upstream autorelease commit", release: "%autorelease -b 9", template: renderAutoreleaseTemplate},
		{name: "upstream autorelease no commit", release: "%autorelease", noGit: true},
		{name: "local static commit", release: "9", local: true, template: renderLocalCommitTemplate},
		{name: "local static no commit", release: "9", local: true, noGit: true},
		{name: "local autorelease commit", release: "%autorelease", local: true, template: renderAutoreleaseTemplate},
		{name: "local autorelease no commit", release: "%autorelease", local: true, noGit: true},
		{name: "provided text", release: "1", message: "Explicit entry", provided: true},
		{name: "provided multiline text", release: "1", message: "# Explicit\n\nKeep details.\n", provided: true},
		{name: "explicit empty value", release: "1", provided: true},
		{name: "provided autorelease", release: "%autorelease", message: "Git only", provided: true},
		{name: "provided local", release: "9", local: true, message: "Git only", provided: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setRenderEditor(t, env, "edit-tool --wait")
			env.CmdFactory.RegisterCommandInSearchPath("edit-tool")

			wantBump := !testCase.local && !strings.Contains(testCase.release, "%autorelease")
			if wantBump {
				env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")
			}

			const specPath = "/working/curl.spec"

			original := renderTestSpec("1", testCase.release, "* Preserve changelog\n- Existing entry\n")
			require.NoError(t, fileutils.WriteFile(env.TestFS, specPath, []byte(original), fileperms.PublicFile))

			editCalls, bumpCalls := 0, 0

			wantMessage := testCase.message
			if testCase.template != "" {
				wantMessage = "Fix feature\nKeep details."
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
					assert.Equal(t, testCase.template, string(content))

					return fileutils.WriteFile(env.TestFS, messagePath,
						[]byte("Fix feature\n\nKeep details.\n"+string(content)), fileperms.PrivateFile)
				case "rpmdev-bumpspec":
					bumpCalls++

					assert.Equal(t, []string{"rpmdev-bumpspec", "-c", wantMessage, specPath}, cmd.Args)

					if testCase.template != "" {
						assert.Equal(t, 1, editCalls, "collect the message before bumpspec")
					}
				default:
					t.Fatalf("unexpected command: %v", cmd.Args)
				}

				return nil
			}

			update := &locklessRenderUpdate{
				subjectProvided: testCase.provided, local: testCase.local, noGitCommit: testCase.noGit,
			}
			if testCase.provided {
				update.subject = testCase.message
			}

			_, err := finishLocklessSpec(env.Env, specPath, update, true, false)
			require.NoError(t, err)
			assert.Equal(t, wantMessage, update.subject)

			if wantBump {
				assert.Equal(t, 1, bumpCalls)
			} else {
				assert.Zero(t, bumpCalls)
			}

			if testCase.template == "" {
				assert.Zero(t, editCalls)
			} else {
				assert.Equal(t, 1, editCalls)

				exists, err := fileutils.Exists(env.TestFS, messagePath)
				require.NoError(t, err)
				assert.False(t, exists)
			}

			content, err := fileutils.ReadFile(env.TestFS, specPath)
			require.NoError(t, err)
			assert.Equal(t, original, string(content))
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

func TestFinishLocklessSpecRejectsEmptyAndFailedEditor(t *testing.T) {
	editorErr := errors.New("editor failed")

	for _, testCase := range []struct {
		name, response string
		fail           bool
	}{
		{name: "blank"},
		{name: "only blank lines", response: "\n \t\n"},
		{name: "only comments and blank lines", response: "\n# Instructions\n#\n \t\n"},
		{name: "failure", fail: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setRenderEditor(t, env, "edit-tool")
			env.CmdFactory.RegisterCommandInSearchPath("edit-tool")

			const specPath = "/working/curl.spec"

			original := renderTestSpec("1", "9", "")
			require.NoError(t, fileutils.WriteFile(env.TestFS, specPath, []byte(original), fileperms.PublicFile))

			var messagePath string

			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				require.Equal(t, "edit-tool", cmd.Args[0], "must fail before bumpspec or Git")
				messagePath = cmd.Args[1]

				if testCase.fail {
					return editorErr
				}

				return fileutils.WriteFile(env.TestFS, messagePath, []byte(testCase.response), fileperms.PrivateFile)
			}

			_, err := finishLocklessSpec(env.Env, specPath,
				&locklessRenderUpdate{changes: "abc123 Upstream fix"}, true, false)
			if testCase.fail {
				require.ErrorIs(t, err, editorErr)
			} else {
				require.ErrorContains(t, err, "no render message provided")
			}

			exists, err := fileutils.Exists(env.TestFS, messagePath)
			require.NoError(t, err)
			assert.False(t, exists)

			content, err := fileutils.ReadFile(env.TestFS, specPath)
			require.NoError(t, err)
			assert.Equal(t, original, string(content))
		})
	}
}

func TestLocklessRenderCommitRejectsMissingMessage(t *testing.T) {
	env := testutils.NewTestEnv(t)

	env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		t.Fatalf("must not open Git's editor: %v", cmd.Args)

		return nil
	}
	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		t.Fatalf("must not run Git without a message: %v", cmd.Args)

		return "", nil
	}
	require.ErrorContains(t, runLocklessRenderCommit(env.Env,
		&locklessRenderUpdate{repoRoot: "/project", changes: "abc123 Upstream fix"},
		[]string{"SPECS/c/curl", ":(top,literal)inputs.toml"}), "no render message collected")
}

func TestLocklessRenderCommitReusesMessage(t *testing.T) {
	for _, message := range []string{"Summary\nBody", "Summary\n\nBody"} {
		t.Run(message, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				t.Fatalf("must not open Git's editor: %v", cmd.Args)

				return nil
			}
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.Equal(t, []string{
					"git", "-C", "/project", "commit", "--only", "-m", "Summary\n\nBody",
					"--", "SPECS/c/curl",
				}, cmd.Args)

				return "", nil
			}
			require.NoError(t, runLocklessRenderCommit(env.Env, &locklessRenderUpdate{
				repoRoot: "/project", subject: message, changes: "abc123 Upstream fix",
			}, []string{"SPECS/c/curl"}))
		})
	}
}

func TestRenderMessagePrefillsUpstreamChanges(t *testing.T) {
	const autorelease = "%autorelease"

	for _, testCase := range []struct {
		name, release, changes, prefix, message string
	}{
		{
			name: "static", release: "1", changes: "abc123 First fix\ndef456 Second fix",
			prefix:  "\n- abc123 First fix\n- def456 Second fix\n\n",
			message: "Summary\n- abc123 First fix\n- def456 Second fix",
		},
		{
			name: "autorelease", release: autorelease, changes: "abc123 First fix\ndef456 Second fix\n",
			prefix:  "\n...\n- abc123 First fix\n- def456 Second fix\n\n",
			message: "Summary\n...\n- abc123 First fix\n- def456 Second fix",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setRenderEditor(t, env, "edit-tool")
			env.CmdFactory.RegisterCommandInSearchPath("edit-tool")
			env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")

			const specPath = "/working/curl.spec"
			require.NoError(t, fileutils.WriteFile(env.TestFS, specPath,
				[]byte(renderTestSpec("1", testCase.release, "")), fileperms.PublicFile))

			editorCalls := 0
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				if cmd.Args[0] == "rpmdev-bumpspec" {
					assert.Equal(t, []string{"rpmdev-bumpspec", "-c", testCase.message, specPath}, cmd.Args)

					return nil
				}

				require.Equal(t, "edit-tool", cmd.Args[0])

				editorCalls++
				content, err := fileutils.ReadFile(env.TestFS, cmd.Args[1])
				require.NoError(t, err)

				template := renderChangelogTemplate
				if testCase.release == autorelease {
					template = renderAutoreleaseTemplate
				}

				assert.Equal(t, testCase.prefix+strings.TrimPrefix(template, "\n\n"), string(content))

				return fileutils.WriteFile(env.TestFS, cmd.Args[1], []byte("Summary"+string(content)),
					fileperms.PrivateFile)
			}
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				summary, body, _ := strings.Cut(testCase.message, "\n")
				assert.Equal(t, []string{
					"git", "-C", "/project", "commit", "--only", "-m", summary + "\n\n" + body,
					"--", "SPECS/c/curl",
				}, cmd.Args)

				return "", nil
			}

			update := &locklessRenderUpdate{repoRoot: "/project", changes: testCase.changes}
			_, err := finishLocklessSpec(env.Env, specPath, update, true, false)
			require.NoError(t, err)
			assert.Equal(t, testCase.message, update.subject)
			require.NoError(t, runLocklessRenderCommit(env.Env, update, []string{"SPECS/c/curl"}))
			assert.Equal(t, 1, editorCalls)
		})
	}
}
