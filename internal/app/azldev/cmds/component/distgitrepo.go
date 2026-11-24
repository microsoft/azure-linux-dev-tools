// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

type DistGitRepoOptions struct {
	Component string
	OutputDir string
	Force     bool
}

type DistGitRepoResult struct {
	Component string `json:"component"`
	OutputDir string `json:"outputDir"`
}

func distGitRepoOnAppInit(_ *azldev.App, parent *cobra.Command) {
	parent.AddCommand(NewDistGitRepoCmd())
}

// NewDistGitRepoCmd constructs the "component dist-git-repo" command.
func NewDistGitRepoCmd() *cobra.Command {
	var options DistGitRepoOptions

	cmd := &cobra.Command{
		Use:   "dist-git-repo <component>",
		Short: "Extract a component's committed dist-git history into a repository",
		Long: `Clone the local project repository's current branch with '--single-branch'
and '--no-local', then run 'git filter-repo --subdirectory-filter' to retain
only <rendered-specs-dir>/<lowercase-first-letter>/<component>.
The component's dist-git files become the new repository's root.

Requires exactly one component name, '--output-dir' (-o), and git-filter-repo.
Only committed files and history are exported; this does not render the component
or clone its upstream distro repository.

An existing output directory is rejected unless '--force' is supplied.
With '--force', that directory is deleted and recreated after preflight checks.
For safety, the source repository and home directory (including their ancestors),
Git metadata, and paths containing symbolic links cannot be used as output directories.
Clone or filtering failures leave any partial output available for inspection.`,
		Example: `  azldev comp dist-git-repo curl -o ./dist-git/curl
  azldev comp dist-git-repo rust-asyncband --output-dir ./dist-git/rust-asyncband --force`,
		Args: cobra.ExactArgs(1),
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.Component = args[0]

			return CreateComponentDistGitRepo(env, &options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}
	cmd.Flags().StringVarP(&options.OutputDir, "output-dir", "o", "",
		"destination for the extracted repository (required)")
	_ = cmd.MarkFlagRequired("output-dir")
	_ = cmd.MarkFlagDirname("output-dir")
	cmd.Flags().BoolVar(&options.Force, "force", false, "delete and recreate an existing output directory")

	return cmd
}

// CreateComponentDistGitRepo exports a component's committed rendered subtree and its history.
func CreateComponentDistGitRepo(env *azldev.Env, options *DistGitRepoOptions) (*DistGitRepoResult, error) {
	if options.OutputDir == "" {
		return nil, errors.New("'--output-dir' (-o) is required")
	}

	if err := fileutils.ValidateFilename(options.Component); err != nil {
		return nil, fmt.Errorf("invalid component name:\n%w", err)
	}

	resolver := components.NewResolver(env)

	resolver.SuppressLockWarnings = true
	if _, err := resolver.GetComponentByName(options.Component); err != nil {
		return nil, fmt.Errorf("resolving component:\n%w", err)
	}

	output, err := distGitRepoOutputPath(env, options.OutputDir)
	if err != nil {
		return nil, err
	}

	exists, err := fileutils.Exists(env.FS(), output)
	if err != nil {
		return nil, fmt.Errorf("checking output directory %#q:\n%w", output, err)
	}

	if exists && !options.Force {
		return nil, fmt.Errorf("output directory %#q already exists; use '--force' to replace it", output)
	}

	result := &DistGitRepoResult{Component: options.Component, OutputDir: output}
	if env.DryRun() {
		slog.Info("Dry run; would extract component dist-git repository", "component", options.Component, "outputDir", output)

		return result, nil
	}

	if err := requireGitFilterRepo(env); err != nil {
		return nil, err
	}

	repoRoot, err := git.RunInDir(env, env, env.ProjectDir(), "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("finding project repository root:\n%w", err)
	}

	if err := validateDistGitRepoOutput(env, repoRoot, output); err != nil {
		return nil, err
	}

	subdir, err := distGitRepoSubdirectory(env, repoRoot, options.Component)
	if err != nil {
		return nil, err
	}

	return createDistGitRepo(env, repoRoot, subdir, output, exists, result)
}

func requireGitFilterRepo(env *azldev.Env) error {
	prereq := &prereqs.PackagePrereq{VersionCheck: func(ctx opctx.Ctx) error {
		if _, err := git.RunInDir(ctx, ctx, env.ProjectDir(), "filter-repo", "--version"); err != nil {
			return fmt.Errorf("git-filter-repo must be installed to use the dist-git-repo command:\n%w", err)
		}

		return nil
	}}
	if err := prereqs.RequireExecutable(env, "git", prereq); err != nil {
		return fmt.Errorf("checking dist-git-repo prerequisites:\n%w", err)
	}

	return nil
}

func distGitRepoOutputPath(env *azldev.Env, output string) (string, error) {
	if !filepath.IsAbs(output) {
		cwd, err := env.OSEnv().Getwd()
		if err != nil {
			return "", fmt.Errorf("getting current directory:\n%w", err)
		}

		output = filepath.Join(cwd, output)
	}

	return filepath.Clean(output), nil
}

func validateDistGitRepoOutput(env *azldev.Env, repoRoot, output string) error {
	if _, err := git.RepoRelPath(output, repoRoot); err == nil {
		return fmt.Errorf("output directory %#q must not contain the source repository", output)
	}

	if home := env.OSEnv().Getenv("HOME"); home != "" {
		if _, err := git.RepoRelPath(output, home); err == nil {
			return fmt.Errorf("output directory %#q must not contain the home directory", output)
		}
	}

	if _, err := git.RepoRelPath(filepath.Join(repoRoot, ".git"), output); err == nil {
		return fmt.Errorf("output directory %#q must not be inside source Git metadata", output)
	}

	gitDir, err := git.RunInDir(env, env, env.ProjectDir(), "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("finding source Git metadata:\n%w", err)
	}

	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(env.ProjectDir(), gitDir)
	}

	if _, err := git.RepoRelPath(output, gitDir); err == nil {
		return fmt.Errorf("output directory %#q must not contain source Git metadata", output)
	}

	if _, err := git.RepoRelPath(gitDir, output); err == nil {
		return fmt.Errorf("output directory %#q must not be inside source Git metadata", output)
	}

	return validateDistGitRepoOutputLinks(env, output)
}

func validateDistGitRepoOutputLinks(env *azldev.Env, output string) error {
	lstater, ok := env.FS().(afero.Lstater)
	if !ok {
		return errors.New("cannot check output directory for symbolic links")
	}

	for path := output; ; path = filepath.Dir(path) {
		info, _, err := lstater.LstatIfPossible(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("checking output path %#q:\n%w", path, err)
		}

		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("output path %#q must not contain symbolic links", path)
		}

		if path == filepath.Dir(path) {
			break
		}
	}

	return nil
}

func distGitRepoSubdirectory(env *azldev.Env, repoRoot, name string) (string, error) {
	specDir := env.Config().Project.RenderedSpecsDir
	if specDir == "" {
		specDir = projectconfig.DefaultRenderedSpecsDir
	}

	subdir, err := projectSpecRepoRelPath(env, repoRoot, filepath.Join(specDir, strings.ToLower(name[:1]), name))
	if err != nil {
		return "", err
	}

	subdir = filepath.ToSlash(subdir)

	return subdir, nil
}

func createDistGitRepo(
	env *azldev.Env, repoRoot, subdir, output string, exists bool, result *DistGitRepoResult,
) (*DistGitRepoResult, error) {
	if exists {
		if err := env.FS().RemoveAll(output); err != nil {
			return nil, fmt.Errorf("removing output directory %#q:\n%w", output, err)
		}
	}

	if err := fileutils.MkdirAll(env.FS(), filepath.Dir(output)); err != nil {
		return nil, fmt.Errorf("creating output parent directory:\n%w", err)
	}

	if _, err := git.RunInDir(env, env, repoRoot,
		"clone", "--single-branch", "--no-local", "--", repoRoot, output); err != nil {
		return nil, fmt.Errorf("cloning project repository:\n%w", err)
	}

	if _, err := git.RunInDir(env, env, output, "filter-repo", "--subdirectory-filter", subdir); err != nil {
		return nil, fmt.Errorf("filtering component dist-git history:\n%w", err)
	}

	return result, nil
}
