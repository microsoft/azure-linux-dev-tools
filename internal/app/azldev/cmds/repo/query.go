// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repo

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/qemu"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

// queryChannel describes one channel queried per invocation: its logical name
// (used for output filenames and bucket keys) and its URL sub-path under the
// repo source. For most channels these match, but the debuginfo channels live
// at '<base|sdk>/debuginfo' rather than '<base|sdk>-debuginfo'.
type queryChannel struct {
	name    string
	urlPath string
}

// Channels queried for each invocation. The per-channel repo URL is
// constructed as '<source>/<urlPath>/<arch>'.
//
//nolint:gochecknoglobals // Fixed list of channels; kept at package scope for clarity.
var queryChannels = []queryChannel{
	{name: "base", urlPath: "base"},
	{name: "sdk", urlPath: "sdk"},
	{name: "base-debuginfo", urlPath: "base/debuginfo"},
	{name: "sdk-debuginfo", urlPath: "sdk/debuginfo"},
}

// RepoQueryOptions controls a single 'azldev repo query' run.
type RepoQueryOptions struct {
	// Source is the base URL of the published repo (e.g.
	// "https://packages.microsoft.com/azurelinux/4.0/beta").
	// Per-channel URLs are constructed as "<Source>/<channel>/<arch>".
	Source string

	// Arch is the target architecture passed to dnf via --forcearch.
	// Defaults to x86_64.
	Arch qemu.Arch

	// OutDir is the directory under which per-arch list files are written.
	// When empty, defaults to '<os.TempDir>/azldev-repo-query/<repoID>',
	// where repoID is the final path segment of Source.
	OutDir string
}

// repoQueryChannelResult is one row reported per (arch, channel) pair.
type repoQueryChannelResult struct {
	Arch      string `json:"arch"      table:"Arch"`
	Channel   string `json:"channel"   table:"Channel"`
	RPMCount  int    `json:"rpmCount"  table:"RPMs"`
	SRPMCount int    `json:"srpmCount" table:"SRPMs"`
	RPMFile   string `json:"rpmFile"   table:"RPM File"`
	SRPMFile  string `json:"srpmFile"  table:"SRPM File"`
}

func queryOnAppInit(_ *azldev.App, parent *cobra.Command) {
	parent.AddCommand(NewRepoQueryCommand())
}

// NewRepoQueryCommand constructs the cobra command for "repo query".
//
//nolint:dupl // Parallel cobra setup with NewRepoDiffCommand; merging would obscure each subcommand.
func NewRepoQueryCommand() *cobra.Command {
	options := &RepoQueryOptions{
		Arch: qemu.Arch(qemu.ArchX86_64),
	}

	cmd := &cobra.Command{
		Use:   "query --source <repo-url> [--arch x86_64|aarch64] [--out-dir <dir>]",
		Short: "Query a published repo and write per-channel package lists",
		Long: `Query a published Azure Linux repo with 'dnf repoquery' and write the
results into a per-arch, per-channel directory layout.

For each channel (base, sdk), the per-channel repo URL is constructed as
'<source>/<channel>/<arch>' and queried with:

    dnf repoquery --quiet \
        --repofrompath=<id>,<url> --repo=<id> \
        --forcearch <arch> \
        --queryformat '%{name}|%{source_name}\n'

The binary names go into '<out-dir>/<arch>/<channel>.txt' and the
deduplicated source-package names go into '<out-dir>/<arch>/<channel>-srpms.txt',
each sorted and one name per line.

This mirrors the 'from-repoquery' enumeration step in the upstream
'scripts/regen-channel-lists.sh' but produces only the per-channel rpm/srpm
lists; channel reconciliation against the local branch is out of scope.`,
		Example: `  # Query the default azl4-dev repo for x86_64
  azldev repo query --source https://packages.microsoft.com/azurelinux/4.0/beta

  # Query aarch64 into a custom directory
  azldev repo query \
      --source https://packages.microsoft.com/azurelinux/4.0/beta \
      --arch aarch64 \
      --out-dir /tmp/azl-lists`,
		RunE: azldev.RunFuncWithoutRequiredConfig(func(env *azldev.Env) (interface{}, error) {
			return QueryRepo(env, options)
		}),
	}

	cmd.Flags().StringVar(&options.Source, "source", "",
		"Base URL of the published repo (per-channel URL is '<source>/<channel>/<arch>')")
	cmd.Flags().Var(&options.Arch, "arch",
		"Target architecture passed to dnf via --forcearch (x86_64, aarch64). Defaults to x86_64.")
	cmd.Flags().StringVarP(&options.OutDir, "out-dir", "o", "",
		"Directory under which '<arch>/<channel>.txt' and '<arch>/<channel>-srpms.txt' "+
			"are written. Defaults to '$TMPDIR/azldev-repo-query/<repoID>' "+
			"(repoID is the final path segment of --source).")

	_ = cmd.MarkFlagRequired("source")
	_ = cmd.RegisterFlagCompletionFunc("arch",
		func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return qemu.SupportedArchitectures(), cobra.ShellCompDirectiveNoFileComp
		})

	return cmd
}

// QueryRepo runs 'dnf repoquery' for each hardcoded channel against the
// requested arch and writes the bucketed RPM and SRPM name lists under
// options.OutDir. Returns one result entry per channel.
func QueryRepo(env *azldev.Env, options *RepoQueryOptions) ([]*repoQueryChannelResult, error) {
	if options.Source == "" {
		return nil, errors.New("--source is required")
	}

	parsedSource, err := url.ParseRequestURI(options.Source)
	if err != nil {
		return nil, fmt.Errorf("invalid --source URL %#q:\n%w", options.Source, err)
	}

	repoID := path.Base(strings.TrimRight(parsedSource.Path, "/"))
	if repoID == "" || repoID == "." || repoID == "/" {
		return nil, fmt.Errorf(
			"cannot derive repo id from --source %#q (URL path has no trailing segment)",
			options.Source)
	}

	arch := options.Arch.String()
	if arch == "" {
		arch = qemu.ArchX86_64
	}

	outDir := options.OutDir
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "azldev-repo-query", repoID)
	}

	archDir := filepath.Join(outDir, arch)
	if err := env.FS().MkdirAll(archDir, fileperms.PublicDir); err != nil {
		return nil, fmt.Errorf("creating output directory %#q:\n%w", archDir, err)
	}

	source := strings.TrimRight(options.Source, "/")
	results := make([]*repoQueryChannelResult, 0, len(queryChannels))

	for _, channel := range queryChannels {
		repoURL := fmt.Sprintf("%s/%s/%s", source, channel.urlPath, arch)

		slog.Info("Running dnf repoquery", "repo", repoID, "channel", channel.name, "arch", arch, "url", repoURL)

		rpms, srpms, err := runRepoquery(env, repoID, repoURL, arch)
		if err != nil {
			return nil, fmt.Errorf("repoquery for %#q failed:\n%w", repoID, err)
		}

		rpmFile := filepath.Join(archDir, channel.name+".txt")
		srpmFile := filepath.Join(archDir, channel.name+"-srpms.txt")

		if err := writeSortedLines(env.FS(), rpmFile, rpms); err != nil {
			return nil, fmt.Errorf("writing %#q:\n%w", rpmFile, err)
		}

		if err := writeSortedLines(env.FS(), srpmFile, srpms); err != nil {
			return nil, fmt.Errorf("writing %#q:\n%w", srpmFile, err)
		}

		results = append(results, &repoQueryChannelResult{
			Arch:      arch,
			Channel:   channel.name,
			RPMCount:  len(rpms),
			SRPMCount: len(srpms),
			RPMFile:   rpmFile,
			SRPMFile:  srpmFile,
		})
	}

	return results, nil
}

// runRepoquery invokes 'dnf repoquery' for a single (repo, arch) pair and
// returns the deduplicated, unsorted name lists.
func runRepoquery(env *azldev.Env, repoID, repoURL, arch string) (rpms, srpms []string, err error) {
	args := []string{
		"repoquery", "--quiet",
		"--setopt=skip_if_unavailable=false",
		fmt.Sprintf("--repofrompath=%s,%s", repoID, repoURL),
		"--repo=" + repoID,
		"--forcearch", arch,
		"--queryformat", `%{name}|%{source_name}\n`,
	}

	var stderr strings.Builder

	cmd := exec.CommandContext(env, "dnf", args...)
	cmd.Stderr = &stderr

	wrapped, wrapErr := env.Command(cmd)
	if wrapErr != nil {
		return nil, nil, fmt.Errorf("preparing dnf command:\n%w", wrapErr)
	}

	wrapped.SetDescription(fmt.Sprintf("dnf repoquery (%s)", repoID))

	stdout, runErr := wrapped.RunAndGetOutput(env)
	if runErr != nil {
		return nil, nil, fmt.Errorf(
			"executing dnf:\n%w\nstderr:\n%s", runErr, stderr.String())
	}

	rpmSet := make(map[string]struct{})
	srpmSet := make(map[string]struct{})

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		name, source, ok := strings.Cut(line, "|")
		if !ok {
			slog.Warn("Skipping malformed repoquery line", "line", line)

			continue
		}

		if name != "" {
			rpmSet[name] = struct{}{}
		}

		if source != "" {
			srpmSet[source] = struct{}{}
		}
	}

	return setToSortedSlice(rpmSet), setToSortedSlice(srpmSet), nil
}

func setToSortedSlice(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

func writeSortedLines(fileSystem afero.Fs, path string, lines []string) error {
	var buf strings.Builder
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}

	if err := afero.WriteFile(fileSystem, path, []byte(buf.String()), fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing %#q:\n%w", path, err)
	}

	return nil
}
