// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../../tools/mockgen/go.mod mockgen -source=git.go -destination=git_test/git_mocks.go -package=git_test --copyright_file=../../../.license-preamble

package git

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

type GitProvider interface {
	// Clone clones a git repository to the specified destination
	Clone(ctx context.Context, repoURL string, destDir string, options ...GitOptions) error
	// Checkout checks out a specific commit in the repository at the specified directory.
	Checkout(ctx context.Context, repoDir string, commitHash string) error
	// GetCommitHashBeforeDate returns the commit hash at or before the specified date in the repository.
	GetCommitHashBeforeDate(ctx context.Context, repoDir string, dateTime time.Time) (string, error)
}

type GitProviderImpl struct {
	eventListener opctx.EventListener
	cmdFactory    opctx.CmdFactory
}

var _ GitProvider = (*GitProviderImpl)(nil)

type GitOptions func() []string

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

	args := []string{"clone"}

	// Add options before URL and destination
	for _, opt := range options {
		args = append(args, opt()...)
	}

	args = append(args, repoURL, destDir)

	cmd := exec.CommandContext(ctx, "git", args...)

	wrappedCmd, err := g.cmdFactory.Command(cmd)
	if err != nil {
		return fmt.Errorf("failed to create git command:\n%w", err)
	}

	event := g.eventListener.StartEvent("Cloning git repo", "repoURL", repoURL)

	defer event.End()

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

// WithGitBranch returns a GitOptions that specifies the branch to clone.
func WithGitBranch(branch string) GitOptions {
	return func() []string {
		return []string{"--branch", branch}
	}
}
