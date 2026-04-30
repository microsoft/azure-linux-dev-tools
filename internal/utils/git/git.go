// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../../tools/mockgen/go.mod mockgen -source=git.go -destination=git_test/git_mocks.go -package=git_test --copyright_file=../../../.license-preamble

package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

// ErrFileNotFound is returned by [ShowFileAtCommit] when the requested path
// does not exist in the given commit. This covers both missing files and
// missing parent directories.
var ErrFileNotFound = errors.New("path not found in commit")

type GitProvider interface {
	// Clone clones a git repository to the specified destination
	Clone(ctx context.Context, repoURL string, destDir string, options ...GitOptions) error
	// Checkout checks out a specific commit in the repository at the specified directory.
	Checkout(ctx context.Context, repoDir string, commitHash string) error
	// GetCommitHashBeforeDate returns the commit hash at or before the specified date in the repository.
	GetCommitHashBeforeDate(ctx context.Context, repoDir string, dateTime time.Time) (string, error)
	// GetCurrentCommit returns the current commit hash of the repository at the given directory, regardless of the date.
	GetCurrentCommit(ctx context.Context, repoDir string) (string, error)
}

type GitProviderImpl struct {
	eventListener opctx.EventListener
	cmdFactory    opctx.CmdFactory
}

var _ GitProvider = (*GitProviderImpl)(nil)

// GitOptions is a functional option that configures a clone operation.
// Options may add CLI arguments and/or request post-clone actions.
type GitOptions func(opts *cloneOptions)

// cloneOptions holds the resolved configuration for a clone operation,
// including any post-clone actions.
type cloneOptions struct {
	// args are the CLI arguments to pass to 'git clone'.
	args []string
	// quiet suppresses event emission during the clone. Use this for
	// internal clones (e.g., identity resolution) that run concurrently
	// and would otherwise produce misleading nested output.
	quiet bool
}

func NewGitProviderImpl(eventListener opctx.EventListener, cmdFactory opctx.CmdFactory) (*GitProviderImpl, error) {
	if eventListener == nil {
		return nil, errors.New("event listener cannot be nil")
	}

	if cmdFactory == nil {
		return nil, errors.New("command factory cannot be nil")
	}

	return &GitProviderImpl{
		eventListener: eventListener,
		cmdFactory:    cmdFactory,
	}, nil
}

func (g *GitProviderImpl) Clone(ctx context.Context, repoURL, destDir string, options ...GitOptions) error {
	if repoURL == "" {
		return errors.New("repository URL cannot be empty")
	}

	_, err := url.Parse(repoURL)
	if err != nil {
		return fmt.Errorf("invalid URL %#q:\n%w", repoURL, err)
	}

	if destDir == "" {
		return errors.New("destination directory cannot be empty")
	}

	// Resolve options into args and post-clone actions.
	resolved := resolveCloneOptions(options)

	args := append([]string{"clone"}, resolved.args...)
	args = append(args, repoURL, destDir)

	cmd := exec.CommandContext(ctx, "git", args...)

	wrappedCmd, err := g.cmdFactory.Command(cmd)
	if err != nil {
		return fmt.Errorf("failed to create git command:\n%w", err)
	}

	if !resolved.quiet {
		event := g.eventListener.StartEvent("Cloning git repo", "repoURL", repoURL)
		defer event.End()
	}

	err = wrappedCmd.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to clone repository %#q:\n%w", repoURL, err)
	}

	return nil
}

func (g *GitProviderImpl) Checkout(ctx context.Context, repoDir string, commitHash string) error {
	if repoDir == "" {
		return errors.New("repository directory cannot be empty")
	}

	if commitHash == "" {
		return errors.New("commit hash cannot be empty")
	}

	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", commitHash)

	wrappedCmd, err := g.cmdFactory.Command(cmd)
	if err != nil {
		return fmt.Errorf("failed to create git command:\n%w", err)
	}

	event := g.eventListener.StartEvent("Checking out git commit", "commitHash", commitHash)

	defer event.End()

	err = wrappedCmd.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to checkout commit %#q in repo at %#q:\n%w", commitHash, repoDir, err)
	}

	return nil
}

func (g *GitProviderImpl) GetCommitHashBeforeDate(
	ctx context.Context, repoDir string, dateTime time.Time,
) (string, error) {
	if repoDir == "" {
		return "", errors.New("repository directory cannot be empty")
	}

	var cmd *exec.Cmd
	if dateTime.IsZero() {
		// Return current HEAD
		cmd = exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD")
	} else {
		// Return latest commit at or before the specified time
		cmd = exec.CommandContext(
			ctx, "git", "-C", repoDir, "rev-list", "-1", "--before="+dateTime.Format(time.RFC3339), "HEAD",
		)
	}

	wrappedCmd, err := g.cmdFactory.Command(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to create git command:\n%w", err)
	}

	output, err := wrappedCmd.RunAndGetOutput(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get latest commit hash in repo at %#q:\n%w", repoDir, err)
	}

	output = strings.TrimSpace(output)

	// Fail if not commit found before the specified dateTime
	if output == "" && !dateTime.IsZero() {
		return "", fmt.Errorf("no commits found before %s in repo at %#q",
			dateTime.Format(time.RFC3339), repoDir)
	}

	if output == "" {
		return "", fmt.Errorf("no commits found in repo at %#q", repoDir)
	}

	return output, nil
}

// GetCurrentCommit returns the current commit hash of the repository at the given directory, regardless of the date.
func (g *GitProviderImpl) GetCurrentCommit(ctx context.Context, repoDir string) (string, error) {
	// Pass zero time to get the current commit
	return g.GetCommitHashBeforeDate(ctx, repoDir, time.Time{})
}

// resolveCloneOptions collects all [GitOptions] into a [cloneOptions] struct.
func resolveCloneOptions(options []GitOptions) cloneOptions {
	var resolved cloneOptions

	for _, opt := range options {
		if opt == nil {
			continue
		}

		opt(&resolved)
	}

	return resolved
}

// WithGitBranch returns a [GitOptions] that specifies the branch to clone.
func WithGitBranch(branch string) GitOptions {
	return func(opts *cloneOptions) {
		opts.args = append(opts.args, "--branch", branch)
	}
}

// WithQuiet returns a [GitOptions] that suppresses event emission during
// the clone. Use this for internal operations (e.g., identity resolution)
// that run concurrently and would produce misleading nested log output.
func WithQuiet() GitOptions {
	return func(opts *cloneOptions) {
		opts.quiet = true
	}
}

// WithMetadataOnly returns a [GitOptions] that performs a blobless partial clone
// (--filter=blob:none --no-checkout). Only git metadata is fetched; no working-tree
// files are checked out.
func WithMetadataOnly() GitOptions {
	return func(opts *cloneOptions) {
		opts.args = append(opts.args, "--filter=blob:none")
		opts.args = append(opts.args, "--no-checkout")
	}
}

// RunInDir executes a git command in the given directory and returns its
// trimmed stdout output. The dir argument is passed via 'git -C dir'.
func RunInDir(
	ctx context.Context, cmdFactory opctx.CmdFactory, dir string, args ...string,
) (string, error) {
	var stderr bytes.Buffer

	fullArgs := make([]string, 0, len(args)+2) //nolint:mnd // 2 accounts for "-C" and dir.
	fullArgs = append(fullArgs, "-C", dir)
	fullArgs = append(fullArgs, args...)

	rawCmd := exec.CommandContext(ctx, "git", fullArgs...)
	rawCmd.Stderr = &stderr

	cmd, err := cmdFactory.Command(rawCmd)
	if err != nil {
		return "", fmt.Errorf("failed to create git command:\n%w", err)
	}

	output, err := cmd.RunAndGetOutput(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to run command 'git %s':\n%s\n%w",
			strings.Join(fullArgs, " "), stderr.String(), err)
	}

	return strings.TrimSpace(output), nil
}

// ShowFileAtCommit returns the contents of a file at a specific git revision.
// Uses 'git show revision:relPath' via the CLI, which is more efficient than
// go-git for large repositories (avoids loading the full tree into memory).
//
// The revision parameter accepts any valid git revision: a commit hash, "HEAD",
// a branch name, a tag, "HEAD~3", etc.
//
// The relPath must reference a blob (file); passing a tree (directory) path
// will return the tree listing as text, which is unlikely to be useful.
//
// Returns [ErrFileNotFound] (wrapped) when the path does not exist in the
// revision — callers can check with [errors.Is].
func ShowFileAtCommit(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	repoDir string,
	revision string,
	relPath string,
) (string, error) {
	var stderr bytes.Buffer

	objectRef := revision + ":" + relPath
	rawCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "show", objectRef)
	rawCmd.Stderr = &stderr

	// Since we need to parse raw git output, ensure the output is in a consistent
	// locale (C) so that error messages are predictable regardless of the developer's
	// environment.
	rawCmd.Env = append(os.Environ(), "LC_ALL=C")

	cmd, err := cmdFactory.Command(rawCmd)
	if err != nil {
		return "", fmt.Errorf("failed to create git command:\n%w", err)
	}

	output, err := cmd.RunAndGetOutput(ctx)
	if err != nil {
		stderrStr := stderr.String()
		if strings.Contains(stderrStr, "does not exist in") ||
			strings.Contains(stderrStr, "exists on disk, but not in") {
			return "", fmt.Errorf("file %#q at revision %#q:\n%w", relPath, revision, ErrFileNotFound)
		}

		return "", fmt.Errorf("failed to run 'git show %s':\n%s\n%w", objectRef, stderrStr, err)
	}

	return output, nil
}
