// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package kiwi provides utilities for interacting with the kiwi-ng image builder tool.
package kiwi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/brunoga/deep"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
	"github.com/opencontainers/selinux/go-selinux"
)

const (
	// KiwiBinary is the name of the kiwi executable.
	KiwiBinary = "kiwi"

	// ResultFilename is the name of the JSON result file that kiwi-ng produces.
	ResultFilename = "kiwi.result.json"
)

// RepoSourceType specifies how kiwi-ng interprets the repository source path.
type RepoSourceType string

const (
	// RepoSourceTypeDefault uses the kiwi-ng default (baseurl).
	RepoSourceTypeDefault RepoSourceType = ""

	// RepoSourceTypeBaseURL interprets the source path as a simple URI.
	RepoSourceTypeBaseURL RepoSourceType = "baseurl"

	// RepoSourceTypeMetalink interprets the source path as a metalink URI.
	RepoSourceTypeMetalink RepoSourceType = "metalink"

	// RepoSourceTypeMirrorlist interprets the source path as a mirrorlist file.
	RepoSourceTypeMirrorlist RepoSourceType = "mirrorlist"
)

// RepoOptions configures per-repository settings for kiwi-ng's --add-repo flag.
// Fields use Go zero values to indicate "use the kiwi-ng default" (the field is omitted
// from the --add-repo value). Non-zero values explicitly set the corresponding field.
//
// The fields correspond to the positional parameters in kiwi-ng's --add-repo format:
//
//	<source>,<type>,<alias>,<priority>,<imageinclude>,<package_gpgcheck>,
//	{signing_keys},<components>,<distribution>,<repo_gpgcheck>,<repo_sourcetype>
//
// See: https://osinside.github.io/kiwi/commands/system_build.html
type RepoOptions struct {
	// Alias is a descriptive name for the repository. If empty, a default alias
	// is generated (e.g., "local-1", "remote-1").
	Alias string

	// Priority is the repository priority number. If 0, a default is used
	// (1 for local repos, 50 for remote repos). Lower numbers mean higher priority.
	Priority int

	// ImageInclude sets imageinclude=true, indicating the repository is part of the
	// system image repository setup. If false, the field is omitted (kiwi-ng default).
	ImageInclude bool

	// DisablePackageGPGCheck explicitly sets package_gpgcheck=false, disabling package
	// signature validation. If false, the field is omitted (kiwi-ng default).
	DisablePackageGPGCheck bool

	// SigningKeys is a list of signing key URIs. Each key must be in URI format.
	// If empty, the field is omitted.
	SigningKeys []string

	// DisableRepoGPGCheck explicitly sets repo_gpgcheck=false, disabling repository
	// signature validation. If false, the field is omitted (kiwi-ng default).
	DisableRepoGPGCheck bool

	// SourceType specifies how the source path is interpreted.
	// If empty ([RepoSourceTypeDefault]), the field is omitted (kiwi-ng defaults to baseurl).
	SourceType RepoSourceType
}

// repoEntry pairs a repository source (path or URI) with its configuration options.
type repoEntry struct {
	source  string
	options RepoOptions
}

// ResultEntry represents a single artifact entry in kiwi's result JSON.
// The JSON tags match kiwi-ng's output format which uses snake_case.
type ResultEntry struct {
	Filename     string `json:"filename"`
	Compress     bool   `json:"compress"`
	Shasum       bool   `json:"shasum"`
	UseForBundle bool   `json:"use_for_bundle"` //nolint:tagliatelle // kiwi-ng uses snake_case
}

// Runner encapsulates options for invoking kiwi-ng.
type Runner struct {
	//
	// NOTE: Any updates to the struct must be reflected in the implementation of [Clone].
	//

	// Injected dependencies
	fs         opctx.FS
	cmdFactory opctx.CmdFactory

	// Verbosity
	verbose bool

	// descriptionDir is the directory containing the kiwi description (config.xml).
	descriptionDir string

	// targetDir is the directory where kiwi will write build output.
	targetDir string

	// localRepos are local RPM repositories to include during build.
	localRepos []repoEntry

	// remoteRepos are remote RPM repositories (http:// or https://) to include during build.
	remoteRepos []repoEntry

	// profile is the optional kiwi profile to use when building the image.
	profile string

	// targetArch is the optional target architecture to build for (e.g., "x86_64" or "aarch64"). If left
	// empty, the host architecture will be used.
	targetArch string
}

// NewRunner constructs a new [Runner] that can be used to invoke kiwi-ng.
// The descriptionDir is the directory containing the kiwi description file (e.g., config.xml).
func NewRunner(ctx opctx.Ctx, descriptionDir string) *Runner {
	return &Runner{
		fs:             ctx.FS(),
		cmdFactory:     ctx,
		verbose:        ctx.Verbose(),
		descriptionDir: descriptionDir,
		profile:        "",
		targetArch:     "",
	}
}

// Clone creates a deep copy of the provided [Runner] instance.
func (r *Runner) Clone() *Runner {
	return &Runner{
		fs:             r.fs,
		cmdFactory:     r.cmdFactory,
		verbose:        r.verbose,
		descriptionDir: r.descriptionDir,
		targetDir:      r.targetDir,
		localRepos:     deep.MustCopy(r.localRepos),
		remoteRepos:    deep.MustCopy(r.remoteRepos),
		profile:        r.profile,
		targetArch:     r.targetArch,
	}
}

// WithTargetDir sets the target directory where kiwi will write build output.
func (r *Runner) WithTargetDir(targetDir string) *Runner {
	r.targetDir = targetDir

	return r
}

// WithProfile sets the profile that kiwi will use to build the image.
func (r *Runner) WithProfile(profile string) *Runner {
	r.profile = profile

	return r
}

// WithTargetArch sets the target architecture to build for (e.g., "x86_64" or "aarch64").
// If left empty, the host architecture will be used.
func (r *Runner) WithTargetArch(arch string) *Runner {
	r.targetArch = arch

	return r
}

// TargetDir retrieves the target directory configured for this [Runner].
func (r *Runner) TargetDir() string {
	return r.targetDir
}

// AddLocalRepo adds a path to a local RPM repository to include during build.
// Local repositories are added with highest priority (priority 1 by default).
// Pass nil for options to use all defaults.
func (r *Runner) AddLocalRepo(repoPath string, options *RepoOptions) *Runner {
	opts := resolveRepoOptions(options)
	r.localRepos = append(r.localRepos, repoEntry{source: repoPath, options: opts})

	return r
}

// AddRemoteRepo adds a URI to a remote RPM repository (http:// or https://) to include during build.
// Remote repositories are added with lower priority than local repositories (priority 50 by default).
// Pass nil for options to use all defaults.
// Returns an error if the URI is invalid or uses an unsupported scheme.
func (r *Runner) AddRemoteRepo(repoURI string, options *RepoOptions) error {
	parsedURL, err := url.Parse(repoURI)
	if err != nil {
		return fmt.Errorf("invalid repository URI %#q:\n%w", repoURI, err)
	}

	switch parsedURL.Scheme {
	case "http":
		slog.Warn("Using insecure HTTP for remote repository; consider using HTTPS instead",
			"uri", repoURI)
	case "https":
		// Valid, no warning needed
	default:
		return fmt.Errorf("unsupported scheme %#q for remote repository %#q: only http:// and https:// are supported",
			parsedURL.Scheme, repoURI)
	}

	opts := resolveRepoOptions(options)
	r.remoteRepos = append(r.remoteRepos, repoEntry{source: repoURI, options: opts})

	return nil
}

// resolveRepoOptions returns a copy of the provided options, or a zero-value [RepoOptions]
// if nil is passed.
func resolveRepoOptions(options *RepoOptions) RepoOptions {
	if options == nil {
		return RepoOptions{}
	}

	return deep.MustCopy(*options)
}

const (
	// defaultLocalRepoPriority is the default priority for local repositories (highest).
	defaultLocalRepoPriority = 1

	// defaultRemoteRepoPriority is the default priority for remote repositories.
	defaultRemoteRepoPriority = 50

	// repoArgOptionalFieldCount is the number of optional positional fields (5-11)
	// in the kiwi-ng --add-repo format after source, type, alias, and priority.
	repoArgOptionalFieldCount = 7
)

// formatRepoArg constructs the comma-delimited --add-repo value for kiwi-ng.
//
// The format is:
//
//	<source>,rpm-md,<alias>,<priority>,<imageinclude>,<package_gpgcheck>,
//	{signing_keys},,<repo_gpgcheck>,<repo_sourcetype>
//
// Trailing empty fields are trimmed to keep the argument concise.
func formatRepoArg(source string, opts RepoOptions, defaultAlias string, defaultPriority int) string {
	alias := opts.Alias
	if alias == "" {
		alias = defaultAlias
	}

	priority := defaultPriority
	if opts.Priority != 0 {
		priority = opts.Priority
	}

	// Build positional fields 5-11:
	// 5: imageinclude, 6: package_gpgcheck, 7: {signing_keys},
	// 8: components (omitted), 9: distribution (omitted),
	// 10: repo_gpgcheck, 11: repo_sourcetype
	fields := make([]string, repoArgOptionalFieldCount)

	if opts.ImageInclude {
		fields[0] = "true"
	}

	if opts.DisablePackageGPGCheck {
		fields[1] = "false"
	}

	if len(opts.SigningKeys) > 0 {
		fields[2] = "{" + strings.Join(opts.SigningKeys, ";") + "}"
	}

	// fields[3] (components) and fields[4] (distribution) are always empty — Debian-only.

	if opts.DisableRepoGPGCheck {
		fields[5] = "false"
	}

	if opts.SourceType != RepoSourceTypeDefault {
		fields[6] = string(opts.SourceType)
	}

	// Trim trailing empty fields.
	lastNonEmpty := -1

	for i := len(fields) - 1; i >= 0; i-- {
		if fields[i] != "" {
			lastNonEmpty = i

			break
		}
	}

	// Build the final argument: source,rpm-md,alias,priority[,field5,...,fieldN]
	base := fmt.Sprintf("%s,rpm-md,%s,%d", source, alias, priority)
	if lastNonEmpty >= 0 {
		base += "," + strings.Join(fields[:lastNonEmpty+1], ",")
	}

	return base
}

// DescriptionDir retrieves the description directory configured for this [Runner].
func (r *Runner) DescriptionDir() string {
	return r.descriptionDir
}

// Profile retrieves the kiwi profile configured for this [Runner].
func (r *Runner) Profile() string {
	return r.profile
}

// TargetArch retrieves the target architecture configured for this [Runner].
func (r *Runner) TargetArch() string {
	return r.targetArch
}

// Build invokes kiwi-ng via sudo to build an image.
func (r *Runner) Build(ctx context.Context) error {
	if r.descriptionDir == "" {
		return errors.New("description directory is required")
	}

	if r.targetDir == "" {
		return errors.New("target directory is required (use WithTargetDir)")
	}

	// Set kiwi log level based on verbosity.
	// Level 10 = DEBUG (most verbose), Level 30 = WARNING (default when not verbose).
	logLevel := "30"
	if r.verbose {
		logLevel = "10"
	}

	// Build the kiwi command arguments.
	// Format: sudo kiwi --loglevel <level> system build --description <dir> --target-dir <output>
	kiwiArgs := []string{
		KiwiBinary,
		"--loglevel", logLevel,
	}

	if r.targetArch != "" {
		kiwiArgs = append(kiwiArgs, "--target-arch", r.targetArch)
	}

	if r.profile != "" {
		kiwiArgs = append(kiwiArgs, "--profile", r.profile)
	}

	kiwiArgs = append(kiwiArgs,
		"system", "build",
		"--description", r.descriptionDir,
		"--target-dir", r.targetDir,
	)

	// Add remote repositories using kiwi's --add-repo flag.
	// Remote repos default to priority 50 (lower than local repos at priority 1).
	for repoIndex, entry := range r.remoteRepos {
		defaultAlias := fmt.Sprintf("remote-%d", repoIndex+1)
		repoArg := formatRepoArg(entry.source, entry.options, defaultAlias, defaultRemoteRepoPriority)
		kiwiArgs = append(kiwiArgs, "--add-repo", repoArg)
	}

	// Add local repositories using kiwi's --add-repo flag.
	// Local repos default to priority 1 (highest) to override remote repos and kiwi defaults.
	for repoIndex, entry := range r.localRepos {
		absRepoPath, err := filepath.Abs(entry.source)
		if err != nil {
			return fmt.Errorf("failed to get absolute path for local repo %#q:\n%w", entry.source, err)
		}

		defaultAlias := fmt.Sprintf("local-%d", repoIndex+1)
		source := "dir://" + absRepoPath
		repoArg := formatRepoArg(source, entry.options, defaultAlias, defaultLocalRepoPriority)
		kiwiArgs = append(kiwiArgs, "--add-repo", repoArg)
	}

	slog.Info("Building image with kiwi-ng",
		"description", r.descriptionDir,
		"profile", r.profile,
		"output", r.targetDir,
	)

	// Run kiwi via sudo.
	cmd, err := r.cmdFactory.Command(exec.CommandContext(ctx, "sudo", kiwiArgs...))
	if err != nil {
		return fmt.Errorf("failed to create kiwi command:\n%w", err)
	}

	// Pipe kiwi output to the console so the user can see progress.
	// Note: We don't use SetLongRunning() here because sudo may prompt for a password,
	// which requires interactive console access.
	cmd.SetStdout(os.Stdout)
	cmd.SetStderr(os.Stderr)

	err = cmd.Run(ctx)
	if err != nil {
		return enhanceKiwiError(err)
	}

	return nil
}

// enhanceKiwiError checks if SELinux is enforcing and provides helpful guidance if so.
func enhanceKiwiError(err error) error {
	// If SELinux is enforcing, provide helpful guidance
	if selinux.EnforceMode() == selinux.Enforcing {
		return fmt.Errorf("kiwi build failed:\n%w\n\n"+
			"SELinux is in enforcing mode, which may cause permission issues during image builds.\n"+
			"To work around this, temporarily disable SELinux:\n"+
			"  sudo setenforce 0\n"+
			"  azldev image build <image-name>\n"+
			"  sudo setenforce 1", err)
	}

	// For other cases, return the original error
	return fmt.Errorf("kiwi build failed:\n%w", err)
}

// CheckPrerequisites verifies that required tools are available for running kiwi-ng.
// This checks for sudo, kiwi, and sgdisk (used by kiwi for disk partitioning).
func CheckPrerequisites(ctx opctx.Ctx) error {
	if err := prereqs.RequireExecutable(ctx, "sudo", nil); err != nil {
		return fmt.Errorf("sudo prerequisite check failed:\n%w", err)
	}

	if err := prereqs.RequireExecutable(ctx, KiwiBinary, &prereqs.PackagePrereq{
		FedoraPackages: []string{"kiwi-cli"},
	}); err != nil {
		return fmt.Errorf("kiwi prerequisite check failed:\n%w", err)
	}

	if err := prereqs.RequireExecutable(ctx, "sgdisk", &prereqs.PackagePrereq{
		FedoraPackages: []string{"gdisk"},
	}); err != nil {
		return fmt.Errorf("sgdisk prerequisite check failed:\n%w", err)
	}

	return nil
}

// ParseResult reads and parses the kiwi result JSON file from the given target directory
// to get artifact paths. Returns a slice of absolute paths to the built artifacts.
func ParseResult(fs opctx.FS, targetDir string) ([]string, error) {
	resultPath := filepath.Join(targetDir, ResultFilename)

	data, err := fileutils.ReadFile(fs, resultPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read kiwi result file %#q:\n%w", resultPath, err)
	}

	// The result file is a map of artifact type names to entry objects.
	var result map[string]ResultEntry
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse kiwi result file %#q:\n%w", resultPath, err)
	}

	artifactPaths := make([]string, 0, len(result))
	for _, entry := range result {
		if entry.Filename != "" {
			artifactPaths = append(artifactPaths, entry.Filename)
		}
	}

	return artifactPaths, nil
}
