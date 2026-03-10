// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../tools/mockgen/go.mod mockgen -source=repoquery.go -destination=rpm_test/repoquery_mocks.go -package=rpm_test --copyright_file=../../.license-preamble

package rpm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os/exec"
	"strconv"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/samber/lo"
)

// RepoQuerier interface defines methods for querying an RPM repository.
type RepoQuerier interface {
	// GetLatestVersion returns the latest version of a component in the repository.
	GetLatestVersion(ctx context.Context, packageName string) (*Version, error)

	// GetRPMLocation returns the URL for the RPM of a component.
	GetRPMLocation(ctx context.Context, packageName string, version *Version) (string, error)
}

// RepoQuerierOption represents an option that can be passed to [NewRQRepoQuerier].
type RepoQuerierOption func(*repoQuerierConfig)

type repoQuerierConfig struct {
	repoURLs   []string
	releaseVer string
}

// WithBaseURLs provides a [RepoQuerier] with the base URLs to query.
func WithBaseURLs(baseURLs ...string) RepoQuerierOption {
	return func(cfg *repoQuerierConfig) {
		cfg.repoURLs = baseURLs
	}
}

// WithReleaseVer allows requesting a [RepoQuerier] to use a custom "releasever",
// e.g., to support URLs that contain `$releasever`.
//
// NOTE: in case WithReleaseVer is passed multiple times, only the last one will take effect.
func WithReleaseVer(releaseVer string) RepoQuerierOption {
	return func(cfg *repoQuerierConfig) {
		cfg.releaseVer = releaseVer
	}
}

// RQRepoQuerier implements [RepoQuerier] as a wrapper around the "repoquery" command.
type RQRepoQuerier struct {
	cmdFactory opctx.CmdFactory

	repoQuerierConfig
}

// NewRQRepoQuerier creates a new [RQRepoQuerier] instance.
// Multiple repository URLs can be provided, the order is irrelevant.
// All URLs must be valid according to [url.ParseRequestURI].
func NewRQRepoQuerier(cmdFactory opctx.CmdFactory, options ...RepoQuerierOption) (*RQRepoQuerier, error) {
	if cmdFactory == nil {
		return nil, errors.New("command factory cannot be nil")
	}

	cfg := &repoQuerierConfig{}

	// Apply options.
	for _, option := range options {
		option(cfg)
	}

	err := validateRepoURLs(cfg.repoURLs)
	if err != nil {
		return nil, fmt.Errorf("repository URLs validation failed:\n%w", err)
	}

	return &RQRepoQuerier{
		cmdFactory:        cmdFactory,
		repoQuerierConfig: *cfg,
	}, nil
}

func validateRepoURLs(repoURLs []string) error {
	if len(repoURLs) == 0 {
		return errors.New("must provide at least one non-empty repository URL")
	}

	parsingErrors := lo.Map(repoURLs, func(repoURL string, _ int) error {
		_, err := url.ParseRequestURI(repoURL)
		if err != nil {
			return fmt.Errorf("invalid URL %#q:\n%w", repoURL, err)
		}

		return nil
	})

	return errors.Join(parsingErrors...)
}

// GetLatestVersion returns the latest version of a component in the repository.
func (q *RQRepoQuerier) GetLatestVersion(ctx context.Context, packageName string) (*Version, error) {
	if strings.TrimSpace(packageName) == "" {
		return nil, errors.New("package name cannot be empty")
	}

	args := []string{
		"--qf=%{evr}",
		"--latest-limit=1",
		packageName,
	}

	output, err := q.query(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest version for package %#q:\n%w", packageName, err)
	}

	if output == "" {
		return nil, fmt.Errorf("package %#q not found in repository", packageName)
	}

	version, err := NewVersion(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse version for package %#q:\n%w", packageName, err)
	}

	return version, nil
}

// GetRPMLocation returns the URL for the RPM of a component.
func (q *RQRepoQuerier) GetRPMLocation(ctx context.Context, packageName string, version *Version) (string, error) {
	packageArg, err := buildPackageWithVersionArg(packageName, version)
	if err != nil {
		return "", fmt.Errorf(
			"failed to build package argument for package %#q, version %s:\n%w", packageName, version.String(), err)
	}

	args := []string{
		"--latest-limit=1",
		"--location",
		packageArg,
	}

	locationURL, err := q.query(ctx, args)
	if err != nil {
		return "", fmt.Errorf("failed to get RPM URL for package %#q:\n%w", packageName, err)
	}

	if locationURL == "" {
		return "", fmt.Errorf("RPM for package %#q not found in repository", packageName)
	}

	return locationURL, nil
}

// addBasicArgs adds arguments used for all "repoquery" commands:
// -yq: Default to answering 'yes' to not block on loading GPG keys on fresh systems
//
//	Also set quiet mode to suppress unnecessary output.
//
// --setopt=skip_if_unavailable=true: Skip repositories that are unavailable. The default
// behavior is to fail if any repository is unavailable. AZL3 sets this to true in the
// dnf config, but for example Fedora leaves it to the default false.
//
// --repofrompath: Set the repository path to the configured RPM repository.
// --repo: Set the repository name to the configured RPM repository. This also ignores the default system repositories.
func (q *RQRepoQuerier) addBasicArgs(inputArgs []string) []string {
	completeArgs := append([]string{"-yq", "--setopt=skip_if_unavailable=true"}, q.buildRepoArgs()...)

	if q.releaseVer != "" {
		completeArgs = append(completeArgs, "--releasever", q.releaseVer)
	}

	return append(completeArgs, inputArgs...)
}

func (q *RQRepoQuerier) buildRepoArgs() []string {
	repoArgs := lo.Map(
		q.repoURLs, func(url string, i int) lo.Tuple2[string, string] {
			repoName := "dummy-repo" + strconv.Itoa(i)

			return lo.T2(
				"--repofrompath="+repoName+","+url,
				"--repo="+repoName,
			)
		})

	return lo.Union(lo.Unzip2(repoArgs))
}

// buildPackageWithVersionArg constructs the package name argument for the "repoquery" command
// based on the provided version and package name.
func buildPackageWithVersionArg(packageName string, version *Version) (string, error) {
	if strings.TrimSpace(packageName) == "" {
		return "", errors.New("package name cannot be empty")
	}

	if version == nil {
		return packageName, nil
	}

	return packageName + "-" + version.String(), nil
}

// query runs the "repoquery" command with the given arguments.
func (q *RQRepoQuerier) query(ctx context.Context, args []string) (string, error) {
	var stderr strings.Builder

	args = q.addBasicArgs(args)

	cmd := exec.CommandContext(ctx, "repoquery", args...)
	cmd.Stderr = &stderr

	wrappedCmd, err := q.cmdFactory.Command(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to wrap the 'repoquery' command:\n%w", err)
	}

	slog.Debug("Executing the 'repoquery' command", "args", args)

	stdout, err := wrappedCmd.RunAndGetOutput(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to execute 'repoquery':\n%w, stderr: %s", err, stderr.String())
	}

	return strings.TrimSpace(stdout), nil
}
