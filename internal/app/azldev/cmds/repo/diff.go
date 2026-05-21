// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	pkgcmd "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/pkg"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/qemu"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

// Bucket names used in both repo-side and project-side per-arch output dirs.
const (
	bucketBase          = "base"
	bucketSDK           = "sdk"
	bucketBaseSRPM      = "base-srpms"
	bucketSDKSRPM       = "sdk-srpms"
	bucketBaseDebuginfo = "base-debuginfo"
	bucketSDKDebuginfo  = "sdk-debuginfo"
)

// allBuckets is the fixed set of bucket names diffed by [DiffRepo].
//
//nolint:gochecknoglobals // Fixed bucket order; kept at package scope for clarity.
var allBuckets = []string{
	bucketBase, bucketSDK,
	bucketBaseSRPM, bucketSDKSRPM,
	bucketBaseDebuginfo, bucketSDKDebuginfo,
}

// RepoDiffOptions controls a single 'azldev repo diff' run.
type RepoDiffOptions struct {
	// Source is the base URL of the published repo (same semantics as
	// [RepoQueryOptions.Source]).
	Source string

	// Arch is the target architecture for both the repo query and the local
	// component query. Defaults to x86_64.
	Arch qemu.Arch

	// OutDir is the directory under which '<repo>/' and '<project>/' sub-trees
	// are written. When empty, defaults to '<os.TempDir>/azldev-repo-diff/<repoID>'.
	OutDir string
}

// repoDiffBucketResult is one row reported per bucket.
type repoDiffBucketResult struct {
	Bucket        string `json:"bucket"        table:"Bucket"`
	InBoth        int    `json:"inBoth"        table:"In Both"`
	OnlyInProject int    `json:"onlyInProject" table:"Only in Project"`
	OnlyInRepo    int    `json:"onlyInRepo"    table:"Only in Repo"`
	DiffFile      string `json:"diffFile"      table:"Diff File"`
}

func diffOnAppInit(_ *azldev.App, parent *cobra.Command) {
	parent.AddCommand(NewRepoDiffCommand())
}

// NewRepoDiffCommand constructs the cobra command for "repo diff".
//
//nolint:dupl // Parallel cobra setup with NewRepoQueryCommand; merging would obscure each subcommand.
func NewRepoDiffCommand() *cobra.Command {
	options := &RepoDiffOptions{
		Arch: qemu.Arch(qemu.ArchX86_64),
	}

	cmd := &cobra.Command{
		Use:   "diff --source <repo-url> [--arch x86_64|aarch64] [--out-dir <dir>]",
		Short: "Diff a published repo against the local project's expected package set",
		Long: `Diff a published Azure Linux repo against what the local project would publish.

For the requested arch this command:

  1. Runs 'azldev repo query' against --source to capture the published
     per-channel package lists (base.txt, sdk.txt, base-srpms.txt,
     sdk-srpms.txt) under '<out-dir>/repo/<arch>/'.
  2. Runs 'azldev component query' for all components and resolves each
     subpackage's publish channel via 'azldev pkg list --rpm-file', producing
     the project-side per-channel lists under '<out-dir>/project/<arch>/'.
  3. Diffs the two sides and emits one unified-style '.diff' file per bucket
     under '<out-dir>/diff/<arch>/'. Lines beginning with '+' are present only
     in the project; lines beginning with '-' are present only in the repo.

Requires a configured project; component specs must already be rendered (run
'azldev component render' first if needed).`,
		Example: `  # Diff the local project against the beta repo for x86_64
  azldev repo diff --source https://packages.microsoft.com/azurelinux/4.0/beta

  # Diff aarch64 into a custom directory
  azldev repo diff \
      --source https://packages.microsoft.com/azurelinux/4.0/beta \
      --arch aarch64 \
      --out-dir /tmp/azl-diff`,
		RunE: azldev.RunFunc(func(env *azldev.Env) (interface{}, error) {
			return DiffRepo(env, options)
		}),
	}

	cmd.Flags().StringVar(&options.Source, "source", "",
		"Base URL of the published repo (per-channel URL is '<source>/<channel>/<arch>')")
	cmd.Flags().Var(&options.Arch, "arch",
		"Target architecture for both the repo query and the local component query (x86_64, aarch64). Defaults to x86_64.")
	cmd.Flags().StringVarP(&options.OutDir, "out-dir", "o", "",
		"Directory for repo/, project/, and diff/ output trees. "+
			"Defaults to '$TMPDIR/azldev-repo-diff/<repoID>' "+
			"(repoID is the final path segment of --source).")

	_ = cmd.MarkFlagRequired("source")
	_ = cmd.RegisterFlagCompletionFunc("arch",
		func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return qemu.SupportedArchitectures(), cobra.ShellCompDirectiveNoFileComp
		})

	return cmd
}

// DiffRepo orchestrates the three steps described in the command help and
// returns one [repoDiffBucketResult] per bucket.
func DiffRepo(env *azldev.Env, options *RepoDiffOptions) ([]*repoDiffBucketResult, error) {
	repoID, arch, outDir, err := resolveDiffPaths(options)
	if err != nil {
		return nil, err
	}

	repoDir := filepath.Join(outDir, "repo")
	projectDir := filepath.Join(outDir, "project")
	diffDir := filepath.Join(outDir, "diff", arch)

	if err := env.FS().MkdirAll(diffDir, fileperms.PublicDir); err != nil {
		return nil, fmt.Errorf("creating diff directory %#q:\n%w", diffDir, err)
	}

	slog.Info("Diffing project against published repo",
		"repo", repoID, "arch", arch, "source", options.Source, "outDir", outDir)

	step1 := env.StartEvent("repo diff: step 1/3 — query published repo",
		"repo", repoID, "arch", arch, "outDir", repoDir)

	repoBuckets, err := collectRepoBuckets(env, options.Source, options.Arch, repoDir, arch)

	step1.End()

	if err != nil {
		return nil, fmt.Errorf("step 1/3 (query published repo) failed:\n%w", err)
	}

	slog.Info("Step 1/3 complete: repo-side lists captured", bucketCountArgs(repoBuckets)...)

	step2 := env.StartEvent("repo diff: step 2/3 — build project package lists",
		"arch", arch, "outDir", projectDir)

	projectBuckets, err := collectProjectBuckets(env, options.Arch, projectDir, arch)

	step2.End()

	if err != nil {
		return nil, fmt.Errorf("step 2/3 (build project package lists) failed:\n%w", err)
	}

	slog.Info("Step 2/3 complete: project-side lists built", bucketCountArgs(projectBuckets)...)

	step3 := env.StartEvent("repo diff: step 3/3 — diff repo vs project",
		"arch", arch, "outDir", diffDir)
	defer step3.End()

	results, err := writeBucketDiffs(env.FS(), diffDir, arch, repoBuckets, projectBuckets)
	if err != nil {
		return nil, err
	}

	logDiffSummary(arch, diffDir, results)

	return results, nil
}

// resolveDiffPaths derives the repoID, arch, and outDir from options, applying
// defaults and validating the source URL.
func resolveDiffPaths(options *RepoDiffOptions) (repoID, arch, outDir string, err error) {
	if options.Source == "" {
		return "", "", "", errors.New("--source is required")
	}

	parsedSource, parseErr := url.ParseRequestURI(options.Source)
	if parseErr != nil {
		return "", "", "", fmt.Errorf("invalid --source URL %#q:\n%w", options.Source, parseErr)
	}

	repoID = path.Base(strings.TrimRight(parsedSource.Path, "/"))
	if repoID == "" || repoID == "." || repoID == "/" {
		return "", "", "", fmt.Errorf(
			"cannot derive repo id from --source %#q (URL path has no trailing segment)",
			options.Source)
	}

	arch = options.Arch.String()
	if arch == "" {
		arch = qemu.ArchX86_64
	}

	outDir = options.OutDir
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "azldev-repo-diff", repoID)
	}

	return repoID, arch, outDir, nil
}

// bucketCountArgs returns the slog key/value pairs for the four buckets.
func bucketCountArgs(buckets map[string]map[string]struct{}) []any {
	args := make([]any, 0, len(allBuckets)*2) //nolint:mnd // 2 entries per bucket: key + value.
	for _, bucket := range allBuckets {
		args = append(args, bucket, len(buckets[bucket]))
	}

	return args
}

func logDiffSummary(arch, diffDir string, results []*repoDiffBucketResult) {
	var totalOnlyProject, totalOnlyRepo, totalInBoth int

	for _, r := range results {
		totalOnlyProject += r.OnlyInProject
		totalOnlyRepo += r.OnlyInRepo
		totalInBoth += r.InBoth
	}

	slog.Info("Step 3/3 complete: diff written",
		"arch", arch,
		"inBoth", totalInBoth,
		"onlyInProject", totalOnlyProject,
		"onlyInRepo", totalOnlyRepo,
		"diffDir", diffDir)
}

// writeBucketDiffs runs [diffSets] for each known bucket, writes the per-bucket
// '.diff' file, and returns one [repoDiffBucketResult] per bucket.
func writeBucketDiffs(
	fileSystem afero.Fs,
	diffDir, arch string,
	repoBuckets, projectBuckets map[string]map[string]struct{},
) ([]*repoDiffBucketResult, error) {
	results := make([]*repoDiffBucketResult, 0, len(allBuckets))

	for _, bucket := range allBuckets {
		onlyProj, onlyRepo, both := diffSets(projectBuckets[bucket], repoBuckets[bucket])

		diffFile := filepath.Join(diffDir, bucket+".diff")
		if err := writeBucketDiff(fileSystem, diffFile, bucket, arch, onlyProj, onlyRepo, both); err != nil {
			return nil, fmt.Errorf("writing %#q:\n%w", diffFile, err)
		}

		slog.Info("Bucket diff",
			"bucket", bucket, "arch", arch,
			"inBoth", both,
			"onlyInProject", len(onlyProj),
			"onlyInRepo", len(onlyRepo),
			"diffFile", diffFile)

		results = append(results, &repoDiffBucketResult{
			Bucket:        bucket,
			InBoth:        both,
			OnlyInProject: len(onlyProj),
			OnlyInRepo:    len(onlyRepo),
			DiffFile:      diffFile,
		})
	}

	return results, nil
}

// collectRepoBuckets invokes [QueryRepo] and reads the four written files into sets.
func collectRepoBuckets(
	env *azldev.Env, source string, arch qemu.Arch, repoOutDir, archStr string,
) (map[string]map[string]struct{}, error) {
	if _, err := QueryRepo(env, &RepoQueryOptions{
		Source: source,
		Arch:   arch,
		OutDir: repoOutDir,
	}); err != nil {
		return nil, err
	}

	archDir := filepath.Join(repoOutDir, archStr)
	buckets := make(map[string]map[string]struct{}, len(allBuckets))

	for _, bucket := range allBuckets {
		names, err := readLinesAsSet(env.FS(), filepath.Join(archDir, bucket+".txt"))
		if err != nil {
			return nil, err
		}

		buckets[bucket] = names
	}

	return buckets, nil
}

// rpmSourceEntry mirrors the on-disk schema consumed by [pkgcmd.ListPackages] via '--rpm-file'.
type rpmSourceEntry struct {
	PackageName       string `json:"packageName"`
	SourcePackageName string `json:"sourcePackageName"`
}

// collectProjectBuckets runs 'component query' for all components, builds the rpm
// source map, resolves channels via [pkgcmd.ListPackages], buckets the results,
// and writes the four per-bucket files. Returns the in-memory sets.
func collectProjectBuckets(
	env *azldev.Env, arch qemu.Arch, projectOutDir, archStr string,
) (map[string]map[string]struct{}, error) {
	compResults, err := component.QueryComponents(env, &component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		Arch:            arch,
	})
	if err != nil {
		return nil, fmt.Errorf("querying components:\n%w", err)
	}

	entries := buildRPMSourceEntries(compResults)

	archDir := filepath.Join(projectOutDir, archStr)
	if err := env.FS().MkdirAll(archDir, fileperms.PublicDir); err != nil {
		return nil, fmt.Errorf("creating project arch directory %#q:\n%w", archDir, err)
	}

	mapFile := filepath.Join(projectOutDir, "rpm-source-map.json")

	mapJSON, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshalling rpm source map:\n%w", err)
	}

	if err := afero.WriteFile(env.FS(), mapFile, mapJSON, fileperms.PublicFile); err != nil {
		return nil, fmt.Errorf("writing %#q:\n%w", mapFile, err)
	}

	pkgResults, err := pkgcmd.ListPackages(env, &pkgcmd.ListPackageOptions{RPMFile: mapFile})
	if err != nil {
		return nil, fmt.Errorf("resolving packages from rpm source map:\n%w", err)
	}

	buckets := bucketPackageResults(pkgResults)

	for _, bucket := range allBuckets {
		file := filepath.Join(archDir, bucket+".txt")
		if err := writeSortedLines(env.FS(), file, setToSortedSlice(buckets[bucket])); err != nil {
			return nil, fmt.Errorf("writing %#q:\n%w", file, err)
		}
	}

	return buckets, nil
}

// buildRPMSourceEntries converts component-query results into the 'rpm-file' schema.
// Components whose spec excludes the target arch (ExclusiveArch/ExcludeArch) are
// reported by 'component query' with an empty Subpackages list; they contribute
// neither an RPM nor an SRPM to the per-arch repo and are skipped here.
func buildRPMSourceEntries(compResults []*component.ComponentDetails) []rpmSourceEntry {
	entries := make([]rpmSourceEntry, 0, len(compResults))

	for _, comp := range compResults {
		if comp.Name == "" || len(comp.Subpackages) == 0 {
			continue
		}

		for _, sub := range comp.Subpackages {
			entries = append(entries, rpmSourceEntry{
				PackageName:       sub,
				SourcePackageName: comp.Name,
			})
		}
	}

	return entries
}

// bucketPackageResults assigns each [pkgcmd.PackageListResult] to one of the
// six diff buckets via [channelToBucket]. Subpackages whose name ends in
// '-debuginfo' or '-debugsource' are routed to the base-debuginfo/sdk-debuginfo
// buckets based on their channel; all other subpackages go to the regular
// base/sdk (or base-srpms/sdk-srpms for source packages) buckets.
func bucketPackageResults(pkgResults []pkgcmd.PackageListResult) map[string]map[string]struct{} {
	buckets := map[string]map[string]struct{}{
		bucketBase:          {},
		bucketSDK:           {},
		bucketBaseSRPM:      {},
		bucketSDKSRPM:       {},
		bucketBaseDebuginfo: {},
		bucketSDKDebuginfo:  {},
	}

	var unmapped int

	for _, row := range pkgResults {
		bucket := channelToBucket(row.Channel, row.Type)
		if bucket == "" {
			unmapped++

			slog.Debug("Skipping package with unmapped channel",
				"package", row.PackageName, "type", row.Type, "channel", row.Channel)

			continue
		}

		buckets[bucket][row.PackageName] = struct{}{}
	}

	if unmapped > 0 {
		slog.Warn("Some packages had channels that did not map to base/sdk",
			"count", unmapped)
	}

	return buckets
}

// channelToBucket maps a (channel, type) pair to one of the six diff buckets.
// Returns "" for packages that should be excluded from the diff (empty
// channel, "none", or a channel that contains none of "base-debuginfo",
// "sdk-debuginfo", "sdk", or "base").
//
// The mapping is intentionally permissive so projects whose channel strings
// embed the bucket name (e.g. "rpm-sdk-srpm") map correctly without an
// explicit table. The '-debuginfo' cases are checked first because their
// channel strings also contain the bare "base"/"sdk" substrings.
func channelToBucket(channel, pkgType string) string {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" || channel == "none" {
		return ""
	}

	var bucket string

	switch {
	case strings.Contains(channel, "base-debuginfo"):
		return bucketBaseDebuginfo
	case strings.Contains(channel, "sdk-debuginfo"):
		return bucketSDKDebuginfo
	case strings.Contains(channel, "sdk"):
		bucket = bucketSDK
	case strings.Contains(channel, "base"):
		bucket = bucketBase

	default:
		return ""
	}

	if pkgType == pkgcmd.PackageTypeSRPM {
		return bucket + "-srpms"
	}

	return bucket
}

// diffSets returns (onlyInLeft, onlyInRight, intersectionCount). The two
// slices are sorted for deterministic output.
func diffSets(left, right map[string]struct{}) (onlyLeft, onlyRight []string, both int) {
	onlyLeft = make([]string, 0)
	onlyRight = make([]string, 0)

	for name := range left {
		if _, ok := right[name]; ok {
			both++
		} else {
			onlyLeft = append(onlyLeft, name)
		}
	}

	for name := range right {
		if _, ok := left[name]; !ok {
			onlyRight = append(onlyRight, name)
		}
	}

	sort.Strings(onlyLeft)
	sort.Strings(onlyRight)

	return onlyLeft, onlyRight, both
}

func writeBucketDiff(
	fileSystem afero.Fs,
	filePath, bucket, arch string,
	onlyProject, onlyRepo []string,
	both int,
) error {
	var buf strings.Builder

	fmt.Fprintf(&buf, "# Bucket: %s (%s)\n", bucket, arch)
	fmt.Fprintf(&buf, "# in-both: %d\n", both)
	fmt.Fprintf(&buf, "# only-in-project: %d\n", len(onlyProject))
	fmt.Fprintf(&buf, "# only-in-repo: %d\n", len(onlyRepo))

	for _, name := range onlyProject {
		buf.WriteString("+ ")
		buf.WriteString(name)
		buf.WriteByte('\n')
	}

	for _, name := range onlyRepo {
		buf.WriteString("- ")
		buf.WriteString(name)
		buf.WriteByte('\n')
	}

	if err := afero.WriteFile(fileSystem, filePath, []byte(buf.String()), fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing %#q:\n%w", filePath, err)
	}

	return nil
}

// readLinesAsSet reads a file produced by [writeSortedLines] back into a set.
// A missing file is treated as an empty set, matching the case where the
// upstream repoquery returned no rows for that bucket.
func readLinesAsSet(fileSystem afero.Fs, filePath string) (map[string]struct{}, error) {
	data, err := afero.ReadFile(fileSystem, filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]struct{}{}, nil
		}

		return nil, fmt.Errorf("reading %#q:\n%w", filePath, err)
	}

	set := make(map[string]struct{})

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		set[line] = struct{}{}
	}

	return set, nil
}
