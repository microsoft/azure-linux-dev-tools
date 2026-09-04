// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repo

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/repo/repocompare"
	"github.com/microsoft/azure-linux-dev-tools/internal/repo/repolayout"
	"github.com/spf13/cobra"
)

// CompareOptions are the CLI flags for `azldev repo compare`.
type CompareOptions struct {
	Left   string
	Right  string
	Arches []string
}

func compareOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewCompareCmd())
}

// NewCompareCmd constructs the `azldev repo compare` command.
func NewCompareCmd() *cobra.Command {
	var options CompareOptions

	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Compare package inventories in two RPM repo sets",
		Long: `Compare package identities in two configured RPM repo sets.

The command expands each named [resources.rpm-repo-sets] entry using its selected
template. The report groups differences by package name and shows summary
statuses plus the complete left and right NEVR inventories. Package content is
not compared.`,
	}

	cmd.RunE = azldev.RunFunc(func(env *azldev.Env) (interface{}, error) {
		return RunCompare(env, &options)
	})

	cmd.Flags().StringVar(&options.Left, "left", "", "left [resources.rpm-repo-sets] name")
	cmd.Flags().StringVar(&options.Right, "right", "", "right [resources.rpm-repo-sets] name")
	cmd.Flags().StringSliceVar(&options.Arches, "arch", repolayout.DefaultArches,
		"comma-separated target architectures")

	for _, name := range []string{"left", "right"} {
		_ = cmd.MarkFlagRequired(name)
	}

	return cmd
}

// RunCompare loads both repository inventories and returns their package identity differences.
func RunCompare(env *azldev.Env, options *CompareOptions) ([]repocompare.PackageReport, error) {
	fetcher := &repocompare.HTTPFetcher{Attempts: env.NetworkRetries()}

	return runCompare(env, options, fetcher)
}

func runCompare(
	env *azldev.Env,
	options *CompareOptions,
	fetcher repocompare.Fetcher,
) ([]repocompare.PackageReport, error) {
	if options.Left == options.Right {
		return nil, errors.New("'--left' and '--right' must name different rpm-repo-sets")
	}

	leftRepositories, err := comparisonRepositories(
		&env.Config().Resources,
		"left",
		options.Left,
		options.Arches,
	)
	if err != nil {
		return nil, fmt.Errorf("resolving left repositories:\n%w", err)
	}

	rightRepositories, err := comparisonRepositories(
		&env.Config().Resources,
		"right",
		options.Right,
		options.Arches,
	)
	if err != nil {
		return nil, fmt.Errorf("resolving right repositories:\n%w", err)
	}

	leftPackages, err := repocompare.LoadRepositories(env, fetcher, leftRepositories)
	if err != nil {
		return nil, fmt.Errorf("loading left repositories:\n%w", err)
	}

	rightPackages, err := repocompare.LoadRepositories(env, fetcher, rightRepositories)
	if err != nil {
		return nil, fmt.Errorf("loading right repositories:\n%w", err)
	}

	reports, err := repocompare.Compare(leftPackages, rightPackages)
	if err != nil {
		return nil, fmt.Errorf("comparing repository inventories:\n%w", err)
	}

	return reports, nil
}

func comparisonRepositories(
	resources *projectconfig.ResourcesConfig,
	side string,
	setName string,
	arches []string,
) ([]repocompare.Repository, error) {
	set, ok := resources.RpmRepoSets[setName]
	if !ok {
		return nil, fmt.Errorf("rpm-repo-set %#q is not defined", setName)
	}

	template, err := repolayout.ResolveTemplate(resources.RpmRepoSetTemplates, set.Template)
	if err != nil {
		return nil, fmt.Errorf("rpm-repo-set %#q:\n%w", setName, err)
	}

	if set.DisableSSLVerify {
		slog.Warn("TLS certificate verification is disabled", "rpmRepoSet", setName)
	}

	allowlist := make(map[string]struct{}, len(set.Subrepos))
	for _, name := range set.Subrepos {
		allowlist[name] = struct{}{}
	}

	expanded := repolayout.ExpandTemplate(set.BaseURI, set.Template, template, arches)
	selected := make([]repolayout.InputRepo, 0, len(expanded))

	for _, repo := range expanded {
		if len(allowlist) > 0 {
			if _, ok := allowlist[repo.SubrepoName]; !ok {
				continue
			}
		}

		if repo.Arch != "" && !archAllowed(set.Arches, repo.Arch) {
			continue
		}

		selected = append(selected, repo)
	}

	selected = repolayout.DedupInputRepos(selected)
	repositories := make([]repocompare.Repository, 0, len(selected))

	for _, repo := range selected {
		repoID := side + "-" + repo.SubrepoName
		if repo.Arch != "" {
			repoID += "-" + repo.Arch
		}

		repositories = append(repositories, repocompare.Repository{
			ID:               repoID,
			Kind:             repo.Kind,
			URL:              repo.URL,
			DisableSSLVerify: set.DisableSSLVerify,
		})
	}

	return repositories, nil
}

func archAllowed(allowlist []string, arch string) bool {
	if len(allowlist) == 0 {
		return true
	}

	for _, allowed := range allowlist {
		if allowed == arch {
			return true
		}
	}

	return false
}
