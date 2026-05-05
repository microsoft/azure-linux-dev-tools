// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/kiwi"
	"github.com/spf13/cobra"
)

// Options for building images.
type ImageBuildOptions struct {
	// Name of the image to build.
	ImageName string

	// Paths to local repositories to include during build.
	LocalRepoPaths []string

	// URIs to remote repositories (http:// or https://) to include during build.
	RemoteRepoPaths []string

	// NoRemoteRepoGpgCheck disables GPG checking for all remote repositories
	// specified via RemoteRepoPaths.
	NoRemoteRepoGpgCheck bool

	// RemoteRepoIncludeInImage marks all remote repositories specified via RemoteRepoPaths
	// as part of the system image repository setup (imageinclude=true).
	RemoteRepoIncludeInImage bool

	// TargetArch specifies the target architecture to build for (e.g., "x86_64" or "aarch64").
	// If left empty, the host architecture will be used.
	TargetArch ImageArch
}

// ImageBuildResult summarizes the results of building an image.
type ImageBuildResult struct {
	// Name of the image that was built.
	ImageName string `json:"imageName" table:",sortkey"`

	// Path to the output directory containing the built image.
	OutputDir string `json:"outputDir" table:"Output Dir"`

	// Paths to the artifact files that were linked into the output directory.
	ArtifactPaths []string `json:"artifactPaths" table:"Artifact Paths"`
}

// ImageArch represents the architecture of an image, such as "x86_64" or "aarch64".
type ImageArch string

const (
	// ImageArchDefault represents the default architecture (i.e., the host architecture).
	ImageArchDefault ImageArch = ""

	// ImageArchX86_64 represents the x86_64 architecture.
	ImageArchX86_64 ImageArch = "x86_64"

	// ImageArchAarch64 represents the aarch64 (a.k.a. arm64) architecture.
	ImageArchAarch64 ImageArch = "aarch64"
)

func (f *ImageArch) String() string {
	return string(*f)
}

// Parses the architecture from a string; used by command-line parser.
func (f *ImageArch) Set(value string) error {
	switch value {
	case "x86_64":
		*f = ImageArchX86_64
	case "aarch64":
		*f = ImageArchAarch64
	case "":
		*f = ImageArchDefault
	default:
		return fmt.Errorf("unsupported architecture: %s", value)
	}

	return nil
}

// Returns a descriptive string used in command-line help.
func (f *ImageArch) Type() string {
	return "arch"
}

func buildOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewImageBuildCmd())
}

// Constructs a [cobra.Command] for the 'image build' command.
func NewImageBuildCmd() *cobra.Command {
	options := &ImageBuildOptions{}

	cmd := &cobra.Command{
		Use:   "build [image-name]",
		Short: "Build an image using kiwi-ng",
		Long: `Build an image using kiwi-ng.

The image must be defined in the project configuration with a kiwi definition type.
This command invokes kiwi-ng via sudo to build the image. Built image artifacts
are placed in the project output directory.`,
		Example: `  # Build an image by name
  azldev image build my-image

  # Build for a specific architecture
  azldev image build my-image --arch aarch64

  # Build with a local RPM repository
  azldev image build my-image --local-repo ./base/out`,
		Args: cobra.MaximumNArgs(1),
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			if len(args) > 0 {
				options.ImageName = args[0]
			}

			return BuildImage(env, options)
		}),
		ValidArgsFunction: generateImageNameCompletions,
	}

	cmd.Flags().Var(&options.TargetArch, "arch",
		"Target architecture to build for (e.g., x86_64 or aarch64). Defaults to the host architecture if not specified)")
	cmd.Flags().StringArrayVar(&options.LocalRepoPaths, "local-repo", []string{},
		"Paths to local repositories to include during build (can be specified multiple times)")
	cmd.Flags().StringArrayVar(&options.RemoteRepoPaths, "remote-repo", []string{},
		"URIs to remote repositories (http:// or https://) to include during build (can be specified multiple times)")
	cmd.Flags().BoolVar(&options.NoRemoteRepoGpgCheck, "remote-repo-no-gpgcheck", false,
		"Disable GPG checking for all remote repositories specified via --remote-repo (not for production use)")
	cmd.Flags().BoolVar(&options.RemoteRepoIncludeInImage, "remote-repo-include-in-image", false,
		"Include all remote repositories specified via --remote-repo in the built image's repository setup")

	return cmd
}

// BuildImage builds the specified image using kiwi-ng.
func BuildImage(env *azldev.Env, options *ImageBuildOptions) (*ImageBuildResult, error) {
	if err := checkBuildPrerequisites(env); err != nil {
		return nil, err
	}

	// Resolve the image from config.
	imageConfig, err := ResolveImageByName(env, options.ImageName)
	if err != nil {
		return nil, err
	}

	// Validate the image definition type.
	if imageConfig.Definition.DefinitionType != projectconfig.ImageDefinitionTypeKiwi {
		return nil, fmt.Errorf(
			"image %#q has definition type %#q, but only %#q is currently supported for building",
			options.ImageName,
			imageConfig.Definition.DefinitionType,
			projectconfig.ImageDefinitionTypeKiwi,
		)
	}

	// Check required directories.
	if env.WorkDir() == "" {
		return nil, errors.New("can't build images without a valid work directory configured")
	}

	if env.OutputDir() == "" {
		return nil, errors.New("can't build images without a valid output directory configured")
	}

	// Validate that the kiwi definition file exists.
	kiwiDefPath := imageConfig.Definition.Path
	if exists, _ := fileutils.Exists(env.FS(), kiwiDefPath); !exists {
		return nil, fmt.Errorf("kiwi definition file not found at %#q", kiwiDefPath)
	}

	// Handle dry-run mode.
	if env.DryRun() {
		slog.Info("Dry run: would build image with kiwi-ng",
			"image", options.ImageName,
			"definition", kiwiDefPath,
		)

		return &ImageBuildResult{
			ImageName: options.ImageName,
		}, nil
	}

	// Create workdir factory for intermediate outputs.
	workDirFactory, err := workdir.NewFactory(env.FS(), env.WorkDir(), env.ConstructionTime())
	if err != nil {
		return nil, fmt.Errorf("failed to create work dir factory:\n%w", err)
	}

	// Create a work directory for kiwi's output. Kiwi requires a fresh directory.
	kiwiWorkDir, err := workDirFactory.Create(options.ImageName, "kiwi")
	if err != nil {
		return nil, fmt.Errorf("failed to create work directory for image build:\n%w", err)
	}

	// Run kiwi to build the image into the work directory.
	kiwiRunner, err := createKiwiRunner(env, imageConfig, kiwiWorkDir, options)
	if err != nil {
		return nil, err
	}

	err = kiwiRunner.Build(env)
	if err != nil {
		return nil, fmt.Errorf("failed to build image %#q:\n%w", options.ImageName, err)
	}

	// Final output directory for linked artifacts.
	imageOutputDir := filepath.Join(env.OutputDir(), "images", options.ImageName)

	// Link the final artifacts to the output directory.
	artifactPaths, err := linkImageArtifacts(env, kiwiWorkDir, imageOutputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to link image artifacts to output directory:\n%w", err)
	}

	return &ImageBuildResult{
		ImageName:     options.ImageName,
		OutputDir:     imageOutputDir,
		ArtifactPaths: artifactPaths,
	}, nil
}

// checkBuildPrerequisites verifies that required tools are available for building images.
func checkBuildPrerequisites(env *azldev.Env) error {
	if err := kiwi.CheckPrerequisites(env); err != nil {
		return fmt.Errorf("kiwi prerequisite check failed:\n%w", err)
	}

	return nil
}

// createKiwiRunner sets up a kiwi runner with the configured repositories.
func createKiwiRunner(
	env *azldev.Env,
	imageConfig *projectconfig.ImageConfig,
	targetDir string,
	options *ImageBuildOptions,
) (*kiwi.Runner, error) {
	runner := kiwi.NewRunner(env, filepath.Dir(imageConfig.Definition.Path)).
		WithTargetDir(targetDir)

	if imageConfig.Definition.Profile != "" {
		runner.WithProfile(imageConfig.Definition.Profile)
	}

	if options.TargetArch != "" {
		runner.WithTargetArch(string(options.TargetArch))
	}

	// Inject TOML-defined image-build repos for the active distro version.
	if err := addConfiguredImageBuildRepos(env, runner, options); err != nil {
		return nil, err
	}

	// Build per-repo options for user-supplied remote repositories (additive).
	remoteRepoOptions := &kiwi.RepoOptions{
		DisableRepoGPGCheck: options.NoRemoteRepoGpgCheck,
		ImageInclude:        options.RemoteRepoIncludeInImage,
	}

	for _, repoURI := range options.RemoteRepoPaths {
		if err := runner.AddRemoteRepo(repoURI, remoteRepoOptions); err != nil {
			return nil, fmt.Errorf("invalid remote repository:\n%w", err)
		}
	}

	for _, repoPath := range options.LocalRepoPaths {
		runner.AddLocalRepo(repoPath, nil)
	}

	return runner, nil
}

// addConfiguredImageBuildRepos resolves the active distro version's
// inputs.image-build list to RPM repo resources and registers each with the kiwi
// runner. Fails strictly if no inputs are configured for image-build, or if all
// configured repos are filtered out for the target architecture.
func addConfiguredImageBuildRepos(
	env *azldev.Env, runner *kiwi.Runner, options *ImageBuildOptions,
) error {
	_, distroVerDef, err := env.Distro()
	if err != nil {
		return fmt.Errorf("failed to resolve distro for image build:\n%w", err)
	}

	repoNames := distroVerDef.Inputs.ImageBuild
	if len(repoNames) == 0 {
		return errors.New("no rpm repos configured for image-build on the active distro version; " +
			"define inputs.image-build under the appropriate [distros.X.versions.Y]",
		)
	}

	cfg := env.Config()
	if cfg == nil {
		return errors.New("no project config loaded; cannot resolve image-build inputs")
	}

	repos := cfg.Resources.RpmRepos

	targetArch := string(options.TargetArch)

	addedCount := 0
	skippedForArch := []string{}

	for _, name := range repoNames {
		repo, ok := repos[name]
		if !ok {
			// Should have been caught by load-time validation, but defend defensively.
			return fmt.Errorf("inputs.image-build references undefined rpm-repo %#q", name)
		}

		if targetArch != "" && !repo.IsAvailableForArch(targetArch) {
			slog.Warn("Skipping rpm-repo for image-build (arch mismatch)",
				"repo", name, "targetArch", targetArch, "repoArches", repo.Arches)

			skippedForArch = append(skippedForArch, name)

			continue
		}

		if err := addKiwiRepoFromResource(runner, name, &repo); err != nil {
			return fmt.Errorf("failed to add image-build rpm-repo %#q:\n%w", name, err)
		}

		addedCount++
	}

	if addedCount == 0 {
		return fmt.Errorf(
			"all %d image-build rpm-repos were filtered out for target arch %q (skipped: %v); "+
				"check the `arches` settings on these repos",
			len(repoNames), targetArch, skippedForArch,
		)
	}

	return nil
}

// addKiwiRepoFromResource registers a single [projectconfig.RpmRepoResource] with the
// kiwi runner via [kiwi.Runner.AddRemoteRepo], applying the repo's GPG-check / signing
// key and source-type semantics.
//
// `disable-gpg-check` is mapped to *both* kiwi GPG knobs (package and repo metadata).
// dnf treats `gpgcheck=0` as covering package signature verification, and we want the
// kiwi semantics to mirror that: a single TOML field means "don't enforce signatures
// at all for this repo". If we ever need finer control we can add a second field.
func addKiwiRepoFromResource(runner *kiwi.Runner, name string, repo *projectconfig.RpmRepoResource) error {
	opts := &kiwi.RepoOptions{
		Alias:                  name,
		DisablePackageGPGCheck: repo.DisableGPGCheck,
		DisableRepoGPGCheck:    repo.DisableGPGCheck,
	}

	if repo.GPGKey != "" {
		opts.SigningKeys = []string{repo.GPGKey}
	}

	source := repo.BaseURI
	if source == "" && repo.Metalink != "" {
		// kiwi v10.x supports metalink via repo_sourcetype.
		source = repo.Metalink
		opts.SourceType = kiwi.RepoSourceTypeMetalink
	}

	if source == "" {
		return fmt.Errorf("rpm-repo %#q has neither base-uri nor metalink", name)
	}

	if err := runner.AddRemoteRepo(source, opts); err != nil {
		return fmt.Errorf("invalid repo source for %#q:\n%w", name, err)
	}

	return nil
}

// linkImageArtifacts hard-links the final image artifacts from the work directory to the
// output directory. Uses symlinks to avoid duplicating large image files.
// It parses kiwi's result JSON to determine which files are artifacts.
func linkImageArtifacts(env *azldev.Env, workDir, outputDir string) ([]string, error) {
	// Parse kiwi's result file to get artifact paths.
	artifactSourcePaths, err := kiwi.ParseResult(env.FS(), workDir)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kiwi result:\n%w", err)
	}

	if len(artifactSourcePaths) == 0 {
		slog.Warn("No artifacts found in kiwi result", "workDir", workDir)

		return []string{}, nil
	}

	linkedPaths := make([]string, 0, len(artifactSourcePaths))

	// Ensure output directory exists.
	err = fileutils.MkdirAll(env.FS(), outputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create output directory %#q:\n%w", outputDir, err)
	}

	// First remove any existing files from previous builds.
	for _, sourcePath := range artifactSourcePaths {
		filename := filepath.Base(sourcePath)
		destPath := filepath.Join(outputDir, filename)

		if exists, _ := fileutils.Exists(env.FS(), destPath); exists {
			err = env.FS().Remove(destPath)
			if err != nil {
				return linkedPaths, fmt.Errorf("failed to remove existing file %#q:\n%w", destPath, err)
			}
		}
	}

	// Now try to link each artifact to the output directory.
	for _, sourcePath := range artifactSourcePaths {
		filename := filepath.Base(sourcePath)
		destPath := filepath.Join(outputDir, filename)

		// Try symlink first (most efficient, no extra space), fall back to copy.
		err = fileutils.SymLinkOrCopy(env, env.FS(), sourcePath, destPath, fileutils.CopyFileOptions{
			PreserveFileMode: true,
		})
		if err != nil {
			return linkedPaths, fmt.Errorf("failed to link or copy artifact %#q to output:\n%w", filename, err)
		}

		linkedPaths = append(linkedPaths, destPath)

		slog.Info("Linked image artifact to output", "path", destPath)
	}

	return linkedPaths, nil
}
