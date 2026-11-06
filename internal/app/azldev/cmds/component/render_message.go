// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kballard/go-shellquote"
	"github.com/mattn/go-isatty"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
)

//nolint:gochecknoglobals // Replaced by isolated render tests without changing real standard streams.
var renderIsTerminal = isatty.IsTerminal

const renderChangelogTemplate = "\n\n" +
	"# Please enter a changelog entry message. The text will be used\n" +
	"# unmodified for the changelog entry (other than the first line\n" +
	"# automatically getting a prefix of '- '. This will also be used for\n" +
	"# the git commit message.\n"

const renderNoGitChangelogTemplate = "\n\n" +
	"# Please enter a changelog entry message. The text will be used\n" +
	"# unmodified for the changelog entry (other than the first line\n" +
	"# automatically getting a prefix of '- '.\n"

const renderAutoreleaseTemplate = "\n\n" +
	"# Please enter a changelog entry message. The first line will be used\n" +
	"# for the changelog entry and git commit summary, and the following\n" +
	"# lines will be used as the git commit message.\n" +
	"#\n" +
	"# To include more lines in the changelog entry, use this reference:\n" +
	"# https://fedora-infra.github.io/rpmautospec-docs/autochangelog.html" +
	"#changelog-entries-generated-from-commit-messages\n"

const renderLocalCommitTemplate = "\n\n" +
	"# Please provide a git commit message. This is not used for the\n" +
	"# changelog, this is used only for the git commit.\n"

func renderFinishConcurrency(env *azldev.Env, prepared []*preparedComponent, checkOnly bool) int {
	if !checkOnly && !env.DryRun() {
		for _, prep := range prepared {
			if prep.update != nil && (!prep.update.local || !prep.update.noGitCommit) &&
				!prep.update.subjectProvided && prep.update.subject == "" {
				return 1
			}
		}
	}

	return env.IOBoundConcurrency()
}

func renderEditor(env *azldev.Env) ([]string, error) {
	if editor := env.OSEnv().Getenv("EDITOR"); editor != "" {
		args, err := shellquote.Split(editor)
		if err != nil {
			return nil, fmt.Errorf("parsing 'EDITOR':\n%w", err)
		}

		if len(args) == 0 || !env.CommandInSearchPath(args[0]) {
			return nil, fmt.Errorf("editor configured by 'EDITOR' is unavailable: %#q", editor)
		}

		return args, nil
	}

	for _, editor := range []string{"editor", "sensible-editor", "vi", "nano"} {
		if env.CommandInSearchPath(editor) {
			return []string{editor}, nil
		}
	}

	return nil, errors.New("no local editor found; set 'EDITOR' or provide '--changelog-comment'")
}

func prepareRenderMessage(env *azldev.Env, specPath string, update *locklessRenderUpdate) error {
	if update.subjectProvided || update.subject != "" || env.DryRun() || (update.local && update.noGitCommit) {
		return nil
	}

	autorelease, err := locklessSpecUsesAutorelease(env, specPath)
	if err != nil {
		return err
	}

	if autorelease && update.noGitCommit {
		return nil
	}

	template := renderChangelogTemplate

	switch {
	case autorelease:
		template = renderAutoreleaseTemplate
	case update.local:
		template = renderLocalCommitTemplate
	case update.noGitCommit:
		template = renderNoGitChangelogTemplate
	}

	update.subject, err = editRenderMessage(env, specPath, prefillRenderChanges(template, update.changes, autorelease))

	return err
}

func prefillRenderChanges(template, changes string, autorelease bool) string {
	if strings.TrimSpace(changes) == "" {
		return template
	}

	prefix := "\n"
	if autorelease {
		prefix += "...\n"
	}

	changes = strings.TrimSuffix(changes, "\n")

	return prefix + "- " + strings.ReplaceAll(changes, "\n", "\n- ") +
		"\n\n" + strings.TrimPrefix(template, "\n\n")
}

func editRenderMessage(env *azldev.Env, specPath, template string) (string, error) {
	if !renderIsTerminal(os.Stdin.Fd()) || !renderIsTerminal(os.Stdout.Fd()) {
		return "", errors.New("non-interactive renders must use '--changelog-comment' and/or '--no-git-commit'")
	}

	args, err := renderEditor(env)
	if err != nil {
		return "", err
	}

	path, cleanup, err := renderMessageFile(env, template)
	if err != nil {
		return "", err
	}
	defer cleanup()

	slog.Info("Enter render message, save and close the editor; blank lines and lines starting with # are ignored",
		"spec", specPath)

	raw := exec.CommandContext(env, args[0], append(args[1:], path)...)
	if err := runRenderInteractive(env, raw); err != nil {
		return "", fmt.Errorf("editing render message:\n%w", err)
	}

	content, err := fileutils.ReadFile(env.FS(), path)
	if err != nil {
		return "", fmt.Errorf("reading edited render message:\n%w", err)
	}

	message := cleanRenderChangelog(string(content))
	if message == "" {
		return "", errors.New("no render message provided; rendering will not be done")
	}

	return message, nil
}

func cleanRenderChangelog(content string) string {
	var lines []string

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}

	return strings.Join(lines, "\n")
}

func renderMessageFile(env *azldev.Env, content string) (path string, cleanup func(), err error) {
	dir, err := fileutils.MkdirTempInTempDir(env.FS(), "azldev-render-message-")
	if err != nil {
		return "", nil, fmt.Errorf("creating render message directory:\n%w", err)
	}

	cleanup = func() {
		if err := env.FS().RemoveAll(dir); err != nil {
			slog.Debug("Failed to remove render message directory", "path", dir, "error", err)
		}
	}

	path = filepath.Join(dir, "message.txt")
	if err := fileutils.WriteFile(env.FS(), path, []byte(content), fileperms.PrivateFile); err != nil {
		cleanup()

		return "", nil, fmt.Errorf("creating render message file:\n%w", err)
	}

	return path, cleanup, nil
}

func runRenderInteractive(env *azldev.Env, raw *exec.Cmd) error {
	raw.Stdin = os.Stdin
	raw.Stdout = os.Stdout
	raw.Stderr = os.Stderr

	command, err := env.Command(raw)
	if err != nil {
		return fmt.Errorf("creating interactive render command:\n%w", err)
	}

	if err := command.Run(env); err != nil {
		return fmt.Errorf("running interactive render command:\n%w", err)
	}

	return nil
}

func runLocklessRenderCommit(env *azldev.Env, update *locklessRenderUpdate, paths []string) error {
	if !update.subjectProvided && update.subject == "" {
		return errors.New("no render message collected before Git commit")
	}

	message := update.subject
	if summary, body, multiline := strings.Cut(message, "\n"); multiline && !strings.HasPrefix(body, "\n") {
		message = summary + "\n\n" + body
	}

	args := []string{"commit", "--only", "-m", message}
	if update.subjectProvided && update.changes != "" {
		args = append(args, "-m", update.changes)
	}

	args = append(args, "--")
	args = append(args, paths...)

	if _, err := git.RunInDir(env, env, update.repoRoot, args...); err != nil {
		return fmt.Errorf("running render commit:\n%w", err)
	}

	return nil
}
