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
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
)

const renderChangelogTemplate = "\n\n" +
	"# Please enter a changelog entry message (one or more lines).\n" +
	"# Blank lines and lines starting with # will be ignored.\n"

func renderFinishConcurrency(env *azldev.Env, prepared []*preparedComponent, checkOnly bool) int {
	if !checkOnly && !env.DryRun() {
		for _, prep := range prepared {
			if prep.update != nil && !prep.update.local && !prep.update.subjectProvided && prep.update.subject == "" {
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

func editRenderChangelog(env *azldev.Env, specPath string) (string, error) {
	args, err := renderEditor(env)
	if err != nil {
		return "", err
	}

	path, cleanup, err := renderMessageFile(env, renderChangelogTemplate)
	if err != nil {
		return "", err
	}
	defer cleanup()

	slog.Info("Enter changelog text, save and close the editor; blank lines and lines starting with # are ignored",
		"spec", specPath)

	raw := exec.CommandContext(env, args[0], append(args[1:], path)...)
	if err := runRenderInteractive(env, raw); err != nil {
		return "", fmt.Errorf("editing render changelog:\n%w", err)
	}

	content, err := fileutils.ReadFile(env.FS(), path)
	if err != nil {
		return "", fmt.Errorf("reading edited render changelog:\n%w", err)
	}

	message := cleanRenderChangelog(string(content))
	if message == "" {
		return "", errors.New("no changelog text provided; rendering will not be done")
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
	args := []string{"commit", "--only"}
	if update.subjectProvided || update.subject != "" {
		args = append(args, "-m", update.subject)
		if update.changes != "" {
			args = append(args, "-m", update.changes)
		}

		args = append(args, "--")
		args = append(args, paths...)

		_, err := git.RunInDir(env, env, update.repoRoot, args...)
		if err != nil {
			return fmt.Errorf("running render commit:\n%w", err)
		}

		return nil
	}

	path, cleanup, err := renderMessageFile(env, "\n\n"+update.changes+"\n")
	if err != nil {
		return err
	}
	defer cleanup()

	args = append([]string{"-C", update.repoRoot}, args...)
	args = append(args, "--template", path, "--")
	args = append(args, paths...)

	return runRenderInteractive(env, exec.CommandContext(env, "git", args...))
}
