// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/repo/repolayout"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
	"github.com/spf13/cobra"
)

// DnfBinary is the underlying system binary the wrapper invokes.
const DnfBinary = "dnf"

// probeTimeout caps each per-slot HEAD/stat probe.
const probeTimeout = 30 * time.Second

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
      (--template, default "` + repolayout.DefaultTemplateName + `") into one sub-repo per template
      row, fanned out per --arch where the row's subpath contains $basearch.

  --version VER [--use-case rpm-build|image-build]
      Project-config mode. Resolves the inputs list of the default distro's
      VER version (use-case defaults to "` + projectconfig.UseCaseRPMBuild + `"). Gpg-keys
      and per-repo arch allowlists come from [resources.rpm-repo-sets.*] /
      [resources.rpm-repos.*]. --arch defaults to x86_64+aarch64 (each
      repo is still filtered by its declared arches). --no-debuginfo /
      --no-srpms drop sub-repos by their declared kind. --template is not
      used.

Unreachable sub-repos (404 on repodata/repomd.xml, or ENOENT for file://)
are silently dropped; any other probe failure aborts the run.

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
	cmd.Flags().StringVar(&options.Template, "template", repolayout.DefaultTemplateName,
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
// template, probes the per-slot repodata URLs, then execs `dnf` with the
// surviving slots wired up via --repofrompath / --enablerepo. It does not
// return on success — control is transferred to dnf via [syscall.Exec].
func RunQuery(env *azldev.Env, options *QueryOptions, dnfArgs []string) error {
	if !env.CommandInSearchPath(DnfBinary) {
		return fmt.Errorf("required tool %#q is not in PATH; install dnf to provide it", DnfBinary)
	}

	repos, prefixes, err := buildCandidates(env, options)
	if err != nil {
		return err
	}

	if len(repos) == 0 {
		return errors.New("no sub-repos remain after applying filters")
	}

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	results := probeAll(workerEnv, env.IOBoundConcurrency(), repos)

	kept, failures := summarizeResults(repos, results, prefixes)

	if len(failures) > 0 {
		return fmt.Errorf(
			"transport failures while probing the following sub-repos — "+
				"refusing to proceed with a partial repo set:\n  %s",
			strings.Join(failures, "\n  "))
	}

	if len(kept) == 0 {
		return errors.New("no reachable sub-repos")
	}

	argv := buildDNFArgv(kept, dnfArgs)

	dnfPath, err := exec.LookPath(DnfBinary)
	if err != nil {
		return fmt.Errorf("failed to locate %s in PATH: %w", DnfBinary, err)
	}

	// Hand control to dnf — does not return on success.
	if err := syscall.Exec(dnfPath, argv, os.Environ()); err != nil {
		return fmt.Errorf("failed to exec %s: %w", dnfPath, err)
	}

	return nil
}

// buildCandidates picks the input-list builder based on which mode the user
// selected and returns (repos, prefixesForLogging). prefixesForLogging is
// non-empty only in --repo-prefix mode so summarizeResults can group output
// by prefix.
func buildCandidates(env *azldev.Env, options *QueryOptions) ([]repolayout.InputRepo, []string, error) {
	if options.Version != "" {
		repos, err := reposFromVersion(env, options)

		return repos, nil, err
	}

	templateName := options.Template
	if templateName == "" {
		templateName = repolayout.DefaultTemplateName
	}

	tmpl, err := repolayout.ResolveTemplate(
		env.Config().Resources.RpmRepoSetTemplates, templateName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve template:\n%w", err)
	}

	repos, err := buildInputRepos(options, templateName, tmpl, options.Arches)
	if err != nil {
		return nil, nil, err
	}

	return filterByKind(repos, options), options.RepoPrefixes, nil
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
// stamps a 1-based prefix index/total on every produced row so the repo-id
// minter can disambiguate multi-prefix runs.
func buildInputRepos(
	options *QueryOptions,
	templateName string,
	tmpl projectconfig.RpmRepoSetTemplate,
	arches []string,
) ([]repolayout.InputRepo, error) {
	var all []repolayout.InputRepo

	total := len(options.RepoPrefixes)

	for idx, prefix := range options.RepoPrefixes {
		normalized, err := repolayout.NormalizePrefix(prefix)
		if err != nil {
			return nil, fmt.Errorf("--repo-prefix %#q: %w", prefix, err)
		}

		expanded := repolayout.ExpandTemplate(normalized, templateName, tmpl, arches)
		for i := range expanded {
			expanded[i].PrefixIndex = idx + 1
			expanded[i].PrefixCount = total
		}

		all = append(all, expanded...)
	}

	return repolayout.DedupInputRepos(all), nil
}

// filterByKind drops debug/source rows per --no-debuginfo / --no-srpms.
func filterByKind(repos []repolayout.InputRepo, options *QueryOptions) []repolayout.InputRepo {
	if !options.NoDebuginfo && !options.NoSRPMs {
		return repos
	}

	out := make([]repolayout.InputRepo, 0, len(repos))

	for _, repo := range repos {
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

		out = append(out, repo)
	}

	return out
}

// probeAll runs every probe in parallel via [parmap.Map] and returns one
// result per repo (parallel to the input slice). It never returns an error
// directly; fatal probe failures are surfaced via [probeResult.Err] and
// aggregated by [summarizeResults] so the user sees every failure at once.
// Concurrency is bounded by limit (typically [azldev.Env.IOBoundConcurrency]).
func probeAll(ctx context.Context, limit int, repos []repolayout.InputRepo) []probeResult {
	mapped := parmap.Map(
		ctx,
		limit,
		repos,
		nil,
		func(wctx context.Context, repo repolayout.InputRepo) probeResult {
			status, perr := probeOne(wctx, repo)

			return probeResult{Status: status, Err: perr}
		},
	)

	results := make([]probeResult, len(repos))

	for idx, item := range mapped {
		if item.Cancelled {
			results[idx] = probeResult{Status: probeFail, Err: ctx.Err()}

			continue
		}

		results[idx] = item.Value
	}

	return results
}

// summarizeResults walks per-prefix in the order the user supplied them,
// logs each slot's outcome, warns on prefixes that yielded zero kept slots
// (without aborting), and returns (kept, failures). When failures is
// non-empty the caller aborts before exec'ing dnf.
func summarizeResults(
	repos []repolayout.InputRepo,
	results []probeResult,
	prefixes []string,
) (kept []repolayout.InputRepo, failures []string) {
	kept = make([]repolayout.InputRepo, 0, len(repos))

	if len(prefixes) == 0 {
		slog.Info("Resolving repos from project config", "count", len(repos))

		for idx := range repos {
			kept, failures = recordResult(repos[idx], results[idx], kept, failures)
		}

		return kept, failures
	}

	// Group indices by prefix (1-based PrefixIndex) so we can log and
	// tally per prefix in declaration order.
	byPrefix := make(map[int][]int, len(prefixes))
	for idx, repo := range repos {
		byPrefix[repo.PrefixIndex] = append(byPrefix[repo.PrefixIndex], idx)
	}

	for pIdx, prefix := range prefixes {
		slog.Info("Discovering repos under prefix", "prefix", prefix)

		keptHere := 0
		failedHere := 0

		for _, idx := range byPrefix[pIdx+1] {
			before := len(kept)
			failedBefore := len(failures)
			kept, failures = recordResult(repos[idx], results[idx], kept, failures)

			if len(kept) > before {
				keptHere++
			}

			if len(failures) > failedBefore {
				failedHere++
			}
		}

		if keptHere == 0 && failedHere == 0 {
			slog.Warn("No sub-repos discovered under prefix", "prefix", prefix)
		}
	}

	return kept, failures
}

// recordResult logs one slot's outcome and appends to kept or failures as
// appropriate.
func recordResult(
	repo repolayout.InputRepo,
	result probeResult,
	kept []repolayout.InputRepo,
	failures []string,
) ([]repolayout.InputRepo, []string) {
	repoID := slotRepoID(repo)

	switch result.Status {
	case probeOK:
		slog.Info("  kept sub-repo", "id", repoID, "url", repo.URL)
		kept = append(kept, repo)
	case probeMissing:
		slog.Info("  skipped (no repodata)", "id", repoID, "url", repo.URL)
	case probeFail:
		slog.Warn("  probe failed", "id", repoID, "url", repo.URL, "err", result.Err)
		failures = append(failures,
			fmt.Sprintf("%s <- %s: %v", repoID, repo.URL, result.Err))
	}

	return kept, failures
}

// probeResult is one probe outcome stored by [probeAll]. Err is non-nil
// only when Status == probeFail.
type probeResult struct {
	Status probeStatus
	Err    error
}

type probeStatus int

const (
	probeOK probeStatus = iota
	probeMissing
	probeFail
)

// probeOne checks one repo's `repodata/repomd.xml`. For http(s) it issues a
// HEAD; for file:// it stats the path.
func probeOne(ctx context.Context, r repolayout.InputRepo) (probeStatus, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	probeURL := strings.TrimRight(r.URL, "/") + "/repodata/repomd.xml"

	if strings.HasPrefix(probeURL, "file://") {
		return probeFile(probeURL)
	}

	req, err := http.NewRequestWithContext(probeCtx, http.MethodHead, probeURL, nil)
	if err != nil {
		return probeFail, fmt.Errorf("build HEAD %#q: %w", probeURL, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return probeFail, fmt.Errorf("HEAD %#q: %w", probeURL, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return probeOK, nil
	case http.StatusNotFound:
		return probeMissing, nil
	default:
		return probeFail, fmt.Errorf("HEAD %#q returned unexpected status %s", probeURL, resp.Status)
	}
}

// probeFile maps an os.Stat outcome to a probeStatus.
func probeFile(probeURL string) (probeStatus, error) {
	u, err := url.Parse(probeURL)
	if err != nil {
		return probeFail, fmt.Errorf("parse %#q: %w", probeURL, err)
	}

	path := filepath.FromSlash(u.Path)

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return probeMissing, nil
		}

		return probeFail, fmt.Errorf("stat %#q: %w", path, err)
	}

	if info.IsDir() {
		return probeFail, fmt.Errorf("expected file at %#q, got directory", path)
	}

	return probeOK, nil
}

// buildDNFArgv builds the argv passed to dnf via [syscall.Exec]. The first
// element is the program name ("dnf"); the rest disables any host-configured
// repos, forces a metadata refresh, wires up one --repofrompath /
// --enablerepo pair per discovered slot, and finally appends the user's
// passthrough.
func buildDNFArgv(repos []repolayout.InputRepo, userArgs []string) []string {
	argv := make([]string, 0, 3+len(repos)*6+len(userArgs))
	argv = append(argv, DnfBinary, "--disablerepo=*", "--refresh")

	for _, dnfRepo := range repos {
		repoID := slotRepoID(dnfRepo)
		argv = append(argv,
			"--repofrompath", repoID+","+dnfRepo.URL,
			"--enablerepo", repoID,
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

// slotRepoID mints a dnf repo id for the slot. The base form is
// `azl-<subrepo>`; the arch is appended when the row was fanned out per
// arch (so per-arch slots don't collide); and the 1-based prefix index is
// appended when multiple --repo-prefix values were supplied so ids stay
// unique across prefixes.
func slotRepoID(repo repolayout.InputRepo) string {
	if repo.RepoID != "" {
		return repo.RepoID
	}

	repoID := "azl-" + repo.SubrepoName
	if repo.Arch != "" {
		repoID = repoID + "-" + repo.Arch
	}

	if repo.PrefixCount > 1 && repo.PrefixIndex > 0 {
		repoID = fmt.Sprintf("%s-%d", repoID, repo.PrefixIndex)
	}

	return repoID
}
