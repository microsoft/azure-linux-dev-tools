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
}

func queryOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewQueryCmd())
}

// NewQueryCmd constructs the cobra command for `azldev repo query`.
func NewQueryCmd() *cobra.Command {
	var options QueryOptions

	cmd := &cobra.Command{
		Use:   "query [flags] -- <dnf args...>",
		Short: "Run dnf against auto-discovered Azure Linux RPM repos",
		Long: `Thin wrapper around dnf that auto-discovers Azure Linux RPM repos under one
or more URL prefixes and then execs into dnf with the resolved repos wired up
via --repofrompath / --enablerepo.

Each --repo-prefix is expanded against an rpm-repo-set-template
(--template, default "` + repolayout.DefaultTemplateName + `") into one sub-repo per template row, fanned
out per --arch where the row's subpath contains $basearch. Unreachable
sub-repos (404 on repodata/repomd.xml) are silently dropped; any other probe
failure aborts the run.

All positional arguments are passed verbatim to dnf. Use ` + "`--`" + ` to separate
azldev flags from dnf flags.

Examples:
  # repoquery the standard layout under one prefix
  azldev repo query --repo-prefix=https://packages.microsoft.com/azurelinux/4.0/beta -- repoquery --available bash

  # search across two prefixes, skipping debug and source repos
  azldev repo query --repo-prefix=URL1 --repo-prefix=URL2 --no-debuginfo --no-srpms -- search 'kernel*'

  # query a local file:// repo
  azldev repo query --repo-prefix=file:///srv/azl/dist -- list --available`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			return nil, RunQuery(env, &options, args)
		}),
	}

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

	if err := cmd.MarkFlagRequired("repo-prefix"); err != nil {
		panic(fmt.Errorf("failed to mark --repo-prefix required: %w", err))
	}

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

	templateName := options.Template
	if templateName == "" {
		templateName = repolayout.DefaultTemplateName
	}

	arches := options.Arches
	if len(arches) == 0 {
		arches = repolayout.DefaultArches
	}

	tmpl, err := repolayout.ResolveTemplate(
		env.Config().Resources.RpmRepoSetTemplates, templateName)
	if err != nil {
		return fmt.Errorf("failed to resolve template:\n%w", err)
	}

	repos, err := buildInputRepos(options, templateName, tmpl, arches)
	if err != nil {
		return err
	}

	repos = filterByKind(repos, options)
	if len(repos) == 0 {
		return errors.New("no sub-repos remain after applying --no-debuginfo/--no-srpms filters")
	}

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	results := probeAll(workerEnv, env.IOBoundConcurrency(), repos)

	kept, failures := summarizeResults(repos, results, options.RepoPrefixes)

	if len(failures) > 0 {
		return fmt.Errorf(
			"transport failures while probing the following sub-repos — "+
				"refusing to proceed with a partial repo set:\n  %s",
			strings.Join(failures, "\n  "))
	}

	if len(kept) == 0 {
		return errors.New("no reachable sub-repos under any --repo-prefix")
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
			repo := repos[idx]
			result := results[idx]
			repoID := slotRepoID(repo)

			switch result.Status {
			case probeOK:
				slog.Info("  kept sub-repo", "id", repoID, "url", repo.URL)
				kept = append(kept, repo)
				keptHere++
			case probeMissing:
				slog.Info("  skipped (no repodata)", "id", repoID, "url", repo.URL)
			case probeFail:
				slog.Warn("  probe failed", "id", repoID, "url", repo.URL, "err", result.Err)
				failures = append(failures,
					fmt.Sprintf("%s <- %s: %v", repoID, repo.URL, result.Err))
				failedHere++
			}
		}

		if keptHere == 0 && failedHere == 0 {
			slog.Warn("No sub-repos discovered under prefix", "prefix", prefix)
		}
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
	argv := make([]string, 0, 3+len(repos)*4+len(userArgs))
	argv = append(argv, DnfBinary, "--disablerepo=*", "--refresh")

	for _, r := range repos {
		id := slotRepoID(r)
		argv = append(argv,
			"--repofrompath", id+","+r.URL,
			"--enablerepo", id,
		)
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
	repoID := "azl-" + repo.SubrepoName
	if repo.Arch != "" {
		repoID = repoID + "-" + repo.Arch
	}

	if repo.PrefixCount > 1 && repo.PrefixIndex > 0 {
		repoID = fmt.Sprintf("%s-%d", repoID, repo.PrefixIndex)
	}

	return repoID
}
