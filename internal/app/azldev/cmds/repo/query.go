// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repo

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/microsoft/azure-linux-dev-tools/defaultconfigs"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/repo/repolayout"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
	"github.com/spf13/cobra"
)

// DnfBinary is the underlying system binary the wrapper invokes.
const DnfBinary = "dnf"

// QueryOptions are the CLI flags for `azldev repo query`.
type QueryOptions struct {
	RepoPrefixes []string
	Template     string
	Arches       []string
	NoDebuginfo  bool
	NoSRPMs      bool
	Version      string
	UseCase      string
}

func queryOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewQueryCmd())
}

// NewQueryCmd constructs the cobra command for `azldev repo query`.
func NewQueryCmd() *cobra.Command {
	var options QueryOptions

	cmd := &cobra.Command{
		Use:   "query [flags] -- <dnf args...>",
		Short: "Run dnf against auto-discovered RPM repos",
		Long: `Thin wrapper around dnf that auto-discovers RPM repos and execs into
dnf with the resolved repos wired up via --repofrompath / --enablerepo.

Two selection modes, mutually exclusive:

  --repo-prefix URL [--repo-prefix URL]...
      URL mode. Each URL is expanded against an rpm-repo-set-template
      (--template, default "` + defaultconfigs.DefaultRpmRepoSetTemplateName + `") into one sub-repo per template
      row, fanned out per --arch where the row's subpath contains $basearch.

  --version VER [--use-case rpm-build|image-build]
      Project-config mode. Resolves the inputs list of the default distro's
      VER version (use-case defaults to "` + projectconfig.UseCaseRPMBuild + `"). Gpg-keys
      and per-repo arch allowlists come from [resources.rpm-repo-sets.*] /
      [resources.rpm-repos.*]. --arch defaults to x86_64+aarch64 (each
      repo is still filtered by its declared arches). --no-debuginfo /
      --no-srpms drop sub-repos by their declared kind. --template is not
      used.

Unreachable sub-repos are tolerated via dnf's per-repo
skip_if_unavailable=1 setopt; dnf itself logs and skips ones that fail to
load.

All positional arguments are passed verbatim to dnf. Use ` + "`--`" + ` to separate
azldev flags from dnf flags.

Examples:
  # URL-mode query against a published tree
  azldev repo query --repo-prefix=https://packages.microsoft.com/azurelinux/4.0/beta -- repoquery --available bash

  # whatever the current project's default distro 4.0-stage2 build consumes
  azldev repo query --version 4.0-stage2 -- list --available kernel

  # image-build inputs instead of rpm-build
  azldev repo query --version 4.0-stage2 --use-case image-build -- repolist`,
	}

	cmd.RunE = azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
		return nil, RunQuery(env, &options, args)
	})

	cmd.Flags().SetInterspersed(false)

	cmd.Flags().StringArrayVar(&options.RepoPrefixes, "repo-prefix", nil,
		"layout prefix (http://, https://, or file:// URL); may be repeated")
	cmd.Flags().StringVar(&options.Template, "template", defaultconfigs.DefaultRpmRepoSetTemplateName,
		"name of the rpm-repo-set-template to expand each --repo-prefix against")
	cmd.Flags().StringSliceVar(&options.Arches, "arch", repolayout.DefaultArches,
		"comma-separated arches to expand $basearch over")
	cmd.Flags().BoolVar(&options.NoDebuginfo, "no-debuginfo", false,
		"drop sub-repos whose kind is debug")
	cmd.Flags().BoolVar(&options.NoSRPMs, "no-srpms", false,
		"drop sub-repos whose kind is source")
	cmd.Flags().StringVar(&options.Version, "version", "",
		"resolve repos from the default distro's [distros.<d>.versions.<VERSION>.inputs] list "+
			"(mutually exclusive with --repo-prefix and --template)")
	cmd.Flags().StringVar(&options.UseCase, "use-case", projectconfig.UseCaseRPMBuild,
		"which inputs list to consult in --version mode: "+
			projectconfig.UseCaseRPMBuild+" or "+projectconfig.UseCaseImageBuild)

	cmd.MarkFlagsMutuallyExclusive("repo-prefix", "version")
	cmd.MarkFlagsMutuallyExclusive("template", "version")
	cmd.MarkFlagsOneRequired("repo-prefix", "version")

	return cmd
}

// RunQuery is the entry point for `azldev repo query`. It resolves the
// candidate sub-repos and runs `dnf` with them wired up via
// --repofrompath / --enablerepo plus skip_if_unavailable=1 — letting dnf
// itself log and tolerate sub-repos that turn out to be missing.
func RunQuery(env *azldev.Env, options *QueryOptions, dnfArgs []string) error {
	if err := prereqs.RequireExecutable(env, DnfBinary, &prereqs.PackagePrereq{
		AzureLinuxPackages: []string{DnfBinary},
		FedoraPackages:     []string{DnfBinary},
	}); err != nil {
		return fmt.Errorf("%s is required to query RPM repos:\n%w", DnfBinary, err)
	}

	groups, prefixes, err := buildCandidates(env, options)
	if err != nil {
		return err
	}

	groups = filterGroupsByKind(groups, options)
	repos := logAndFlatten(groups, prefixes)

	if len(repos) == 0 {
		return errors.New("no sub-repos remain after applying filters")
	}

	return runDNF(env, buildDNFArgv(repos, dnfArgs))
}

// logAndFlatten emits a per-group discovery log and returns the concatenated
// candidate list. Each row's URL is logged so a user reading the build output
// can see exactly what was handed to dnf, while dnf itself decides which
// repos actually load.
func logAndFlatten(groups [][]repolayout.InputRepo, prefixes []string) []repolayout.InputRepo {
	total := 0
	for _, g := range groups {
		total += len(g)
	}

	out := make([]repolayout.InputRepo, 0, total)

	logRepos := func(group []repolayout.InputRepo) {
		for _, repo := range group {
			slog.Info("  wiring sub-repo", "id", repo.RepoID, "url", repo.URL)
		}
	}

	if len(prefixes) == 0 {
		slog.Info("Resolving repos from project config", "count", total)

		for _, group := range groups {
			logRepos(group)
			out = append(out, group...)
		}

		return out
	}

	for pIdx, prefix := range prefixes {
		slog.Info("Discovering repos under prefix", "prefix", prefix)

		if len(groups[pIdx]) == 0 {
			slog.Warn("No sub-repos under prefix", "prefix", prefix)
		}

		logRepos(groups[pIdx])
		out = append(out, groups[pIdx]...)
	}

	return out
}

// runDNF runs the assembled dnf invocation via the env's command factory
// (backed by [externalcmd]) so the call is observable / dry-run-aware.
func runDNF(env *azldev.Env, argv []string) error {
	rawCmd := exec.CommandContext(env, argv[0], argv[1:]...)
	rawCmd.Stdin = os.Stdin
	rawCmd.Stdout = os.Stdout
	rawCmd.Stderr = os.Stderr

	cmd, err := env.Command(rawCmd)
	if err != nil {
		return fmt.Errorf("failed to create %s command:\n%w", DnfBinary, err)
	}

	if err := cmd.Run(env); err != nil {
		// Match the original syscall.Exec semantics by propagating dnf's exit
		// code verbatim (e.g., 100 for `check-update` with updates available)
		// instead of letting cobra collapse it to 1.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}

		return fmt.Errorf("%s run failed:\n%w", DnfBinary, err)
	}

	return nil
}

// buildCandidates picks the input-list builder based on which mode the user
// selected and returns (groups, prefixesForLogging). groups is one
// [repolayout.InputRepo] slice per --repo-prefix (preserving user order) in
// prefix mode, or a single slice in --version mode. prefixesForLogging is
// non-empty only in --repo-prefix mode so logAndFlatten can label its
// per-group output.
func buildCandidates(env *azldev.Env, options *QueryOptions) ([][]repolayout.InputRepo, []string, error) {
	if options.Version != "" {
		repos, err := reposFromVersion(env, options)
		if err != nil {
			return nil, nil, err
		}

		return [][]repolayout.InputRepo{repos}, nil, nil
	}

	templateName := options.Template
	if templateName == "" {
		templateName = defaultconfigs.DefaultRpmRepoSetTemplateName
	}

	tmpl, err := repolayout.ResolveTemplate(
		env.Config().Resources.RpmRepoSetTemplates, templateName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve template:\n%w", err)
	}

	groups, err := buildInputRepos(options, templateName, tmpl, options.Arches)
	if err != nil {
		return nil, nil, err
	}

	return groups, options.RepoPrefixes, nil
}

// reposFromVersion resolves a distro version's inputs into the flat
// [repolayout.InputRepo] list the rest of the pipeline expects.
func reposFromVersion(env *azldev.Env, options *QueryOptions) ([]repolayout.InputRepo, error) {
	useCase := options.UseCase
	if useCase != projectconfig.UseCaseRPMBuild && useCase != projectconfig.UseCaseImageBuild {
		return nil, fmt.Errorf("--use-case %#q is invalid (want %q or %q)",
			useCase, projectconfig.UseCaseRPMBuild, projectconfig.UseCaseImageBuild)
	}

	cfg := env.Config()
	version := options.Version

	distroName := cfg.Project.DefaultDistro.Name
	if distroName == "" {
		return nil, errors.New("--version requires `project.default-distro.name` to be set in project config")
	}

	names, err := resolveVersionRepoNames(cfg, distroName, version, useCase)
	if err != nil {
		return nil, err
	}

	effective, err := cfg.Resources.EffectiveRpmRepos()
	if err != nil {
		return nil, fmt.Errorf("resolving rpm-repos:\n%w", err)
	}

	kinds := rpmRepoKindMap(&cfg.Resources)

	return materializeVersionRepos(names, effective, kinds, options.Arches, options, distroName, version)
}

// rpmRepoKindMap reproduces the name -> SubrepoKind mapping that
// [projectconfig.ResourcesConfig.EffectiveRpmRepos] discards. Explicit
// rpm-repos are treated as Binary; set-expanded entries take their kind
// from the template's SubrepoSpec.
func rpmRepoKindMap(resources *projectconfig.ResourcesConfig) map[string]projectconfig.SubrepoKind {
	kinds := make(map[string]projectconfig.SubrepoKind)
	if resources == nil {
		return kinds
	}

	for name := range resources.RpmRepos {
		kinds[name] = projectconfig.SubrepoKindBinary
	}

	for _, set := range resources.RpmRepoSets {
		tmpl, ok := resources.RpmRepoSetTemplates[set.Template]
		if !ok {
			continue
		}

		allow := map[string]struct{}{}
		for _, subName := range set.Subrepos {
			allow[subName] = struct{}{}
		}

		for _, sub := range tmpl.Subrepos {
			if len(allow) > 0 {
				if _, ok := allow[sub.Name]; !ok {
					continue
				}
			}

			kinds[set.NamePrefix+sub.Name] = sub.Kind.Default()
		}
	}

	return kinds
}

// resolveVersionRepoNames returns the ordered list of rpm-repo names declared
// for (distroName, version, useCase) after set expansion.
func resolveVersionRepoNames(
	cfg *projectconfig.ProjectConfig,
	distroName, version, useCase string,
) ([]string, error) {
	distro, found := cfg.Distros[distroName]
	if !found {
		return nil, fmt.Errorf("default distro %#q is not defined under [distros]", distroName)
	}

	versionDef, found := distro.Versions[version]
	if !found {
		return nil, fmt.Errorf("distro %#q has no version %#q", distroName, version)
	}

	var (
		names []string
		err   error
	)

	switch useCase {
	case projectconfig.UseCaseRPMBuild:
		names, err = versionDef.EffectiveRpmBuildRepos(&cfg.Resources)
	case projectconfig.UseCaseImageBuild:
		names, err = versionDef.EffectiveImageBuildRepos(&cfg.Resources)
	}

	if err != nil {
		return nil, fmt.Errorf("resolving %s inputs for %s/%s:\n%w", useCase, distroName, version, err)
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("%s/%s declares no %s inputs", distroName, version, useCase)
	}

	return names, nil
}

// materializeVersionRepos turns rpm-repo names into the flat InputRepo list,
// expanding over arches and applying --no-debuginfo / --no-srpms via kinds.
// Per-repo arch allowlists ([RpmRepoResource.Arches]) drop arches a repo
// doesn't publish; metalink-only repos are rejected.
func materializeVersionRepos(
	names []string,
	effective map[string]projectconfig.RpmRepoResource,
	kinds map[string]projectconfig.SubrepoKind,
	arches []string,
	options *QueryOptions,
	distroName, version string,
) ([]repolayout.InputRepo, error) {
	out := make([]repolayout.InputRepo, 0, len(names)*len(arches))

	for _, name := range names {
		repo, found := effective[name]
		if !found {
			return nil, fmt.Errorf("rpm-repo %#q referenced by %s/%s inputs is not defined",
				name, distroName, version)
		}

		if repo.BaseURI == "" {
			return nil, fmt.Errorf(
				"rpm-repo %#q has no base-uri (metalink-only repos are not supported by `repo query`)",
				name)
		}

		kind := kinds[name].Default()
		if options.NoDebuginfo && kind == projectconfig.SubrepoKindDebug {
			continue
		}

		if options.NoSRPMs && kind == projectconfig.SubrepoKindSource {
			continue
		}

		gpgKey := ""
		if !repo.DisableGPGCheck {
			gpgKey = repo.GPGKey
		}

		for _, arch := range arches {
			if !repo.IsAvailableForArch(arch) {
				continue
			}

			out = append(out, repolayout.InputRepo{
				RepoID: versionRepoID(name, arches, arch),
				URL:    repolayout.SubstituteBasearch(repo.BaseURI, arch),
				Arch:   arch,
				GPGKey: gpgKey,
			})
		}
	}

	// DedupInputRepos collapses entries with identical URLs. That happens
	// naturally for source/SRPM subrepos whose subpath has no `$basearch` —
	// the per-arch fan-out above produces N identical URLs, and we want only
	// one row in the final dnf invocation.
	return repolayout.DedupInputRepos(out), nil
}

// versionRepoID keeps the canonical resource id when only one arch was
// requested (matches the name the user typed in TOML) and appends the arch
// otherwise so per-arch dnf repo ids stay unique. The decision is based on
// the *requested* arch list, not how many survive per-repo filtering — that
// keeps the id stable when the user re-runs with the same flags even if
// some repos drop one arch.
func versionRepoID(name string, arches []string, arch string) string {
	if len(arches) > 1 {
		return name + "-" + arch
	}

	return name
}

// buildInputRepos normalizes each --repo-prefix, expands it against tmpl, and
// returns one [repolayout.InputRepo] slice per prefix (in user order). The
// dnf repo id is stamped here so downstream stages don't need to know the
// prefix index: the per-prefix suffix is added only when multiple prefixes
// are in play so ids stay unique across them.
func buildInputRepos(
	options *QueryOptions,
	templateName string,
	tmpl projectconfig.RpmRepoSetTemplate,
	arches []string,
) ([][]repolayout.InputRepo, error) {
	total := len(options.RepoPrefixes)
	groups := make([][]repolayout.InputRepo, 0, total)

	for idx, prefix := range options.RepoPrefixes {
		normalized, err := repolayout.NormalizePrefix(prefix)
		if err != nil {
			return nil, fmt.Errorf("--repo-prefix %#q: %w", prefix, err)
		}

		expanded := repolayout.ExpandTemplate(normalized, templateName, tmpl, arches)
		for i := range expanded {
			expanded[i].RepoID = prefixModeRepoID(expanded[i], idx+1, total)
		}

		groups = append(groups, repolayout.DedupInputRepos(expanded))
	}

	return groups, nil
}

// prefixModeRepoID mints the dnf repo id for a prefix-mode slot. The base
// form is `azl-<subrepo>`; the arch is appended when the row was fanned out
// per arch (so per-arch slots don't collide); and the 1-based prefix index
// is appended only when multiple --repo-prefix values were supplied so ids
// stay unique across prefixes.
func prefixModeRepoID(repo repolayout.InputRepo, prefixIdx, totalPrefixes int) string {
	repoID := "azl-" + repo.SubrepoName
	if repo.Arch != "" {
		repoID = repoID + "-" + repo.Arch
	}

	if totalPrefixes > 1 {
		repoID = fmt.Sprintf("%s-%d", repoID, prefixIdx)
	}

	return repoID
}

// filterGroupsByKind drops debug/source rows per --no-debuginfo / --no-srpms,
// preserving the per-prefix grouping (and the empty-group warning that
// logAndFlatten emits).
func filterGroupsByKind(
	groups [][]repolayout.InputRepo, options *QueryOptions,
) [][]repolayout.InputRepo {
	if !options.NoDebuginfo && !options.NoSRPMs {
		return groups
	}

	out := make([][]repolayout.InputRepo, len(groups))

	for gIdx, group := range groups {
		kept := make([]repolayout.InputRepo, 0, len(group))

		for _, repo := range group {
			switch repo.Kind {
			case projectconfig.SubrepoKindDebug:
				if options.NoDebuginfo {
					continue
				}
			case projectconfig.SubrepoKindSource:
				if options.NoSRPMs {
					continue
				}
			case projectconfig.SubrepoKindBinary:
				// keep
			}

			kept = append(kept, repo)
		}

		out[gIdx] = kept
	}

	return out
}

// buildDNFArgv builds the argv passed to dnf. The first element is the
// program name ("dnf"); the rest disables any host-configured repos, forces
// a metadata refresh, wires up one --repofrompath / --enablerepo pair per
// candidate slot (with skip_if_unavailable=1 so dnf tolerates ones that
// don't actually exist), and finally appends the user's passthrough.
func buildDNFArgv(repos []repolayout.InputRepo, userArgs []string) []string {
	argv := make([]string, 0, 3+len(repos)*7+len(userArgs))
	argv = append(argv, DnfBinary, "--disablerepo=*", "--refresh")

	for _, dnfRepo := range repos {
		repoID := dnfRepo.RepoID
		argv = append(argv,
			"--repofrompath", repoID+","+dnfRepo.URL,
			"--enablerepo", repoID,
			"--setopt="+repoID+".skip_if_unavailable=1",
		)

		if dnfRepo.GPGKey != "" {
			argv = append(argv,
				"--setopt="+repoID+".gpgkey="+dnfRepo.GPGKey,
				"--setopt="+repoID+".gpgcheck=1",
			)
		}
	}

	argv = append(argv, userArgs...)

	return argv
}
