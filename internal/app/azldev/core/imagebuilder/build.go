// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package imagebuilder

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/kiwi"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/qemu"
	"github.com/samber/lo"
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
	ImageArchX86_64 ImageArch = projectconfig.ImageArchitectureX86_64

	// ImageArchAarch64 represents the aarch64 (a.k.a. arm64) architecture.
	ImageArchAarch64 ImageArch = projectconfig.ImageArchitectureAarch64
)

func (f *ImageArch) String() string {
	return string(*f)
}

// Parses the architecture from a string; used by command-line parser.
func (f *ImageArch) Set(value string) error {
	switch value {
	case projectconfig.ImageArchitectureX86_64:
		*f = ImageArchX86_64
	case projectconfig.ImageArchitectureAarch64:
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

// BuildImage builds the specified image using kiwi-ng.
func BuildImage(env *azldev.Env, options *ImageBuildOptions) (*ImageBuildResult, error) {
	if err := checkBuildPrerequisites(env); err != nil {
		return nil, err
	}

	imageConfig, err := resolveBuildImage(env, options)
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

func resolveBuildImage(env *azldev.Env, options *ImageBuildOptions) (*projectconfig.ImageConfig, error) {
	imageConfig, err := ResolveImageByName(env, options.ImageName)
	if err != nil {
		return nil, err
	}

	if err := validateBuildArchitecture(
		imageConfig,
		options.TargetArch,
		runtime.GOARCH,
	); err != nil {
		return nil, err
	}

	return imageConfig, nil
}

func validateBuildArchitecture(
	imageConfig *projectconfig.ImageConfig,
	targetArch ImageArch,
	hostGoArch string,
) error {
	arch := string(targetArch)
	if arch == "" {
		arch = qemu.GoArchToQEMUArch(hostGoArch)
		if !slices.Contains(qemu.SupportedArchitectures(), arch) {
			return fmt.Errorf("unsupported host architecture %#q", hostGoArch)
		}
	}

	// SupportsArchitecture rejects both architectures the image doesn't declare
	// support for and architectures azldev doesn't recognize at all (relevant for
	// an unrestricted image with no declared Architectures, and for an explicit
	// --arch value that bypassed ImageArch.Set's validation).
	if !imageConfig.SupportsArchitecture(arch) {
		supportedArchitectures := imageConfig.Architectures
		if len(supportedArchitectures) == 0 {
			// An unrestricted image supports every recognized architecture; report
			// that set instead of the empty declared list, which would otherwise
			// misleadingly suggest the image supports none.
			supportedArchitectures = projectconfig.SupportedImageArchitectures()
		}

		return fmt.Errorf(
			"image %#q does not support architecture %#q; supported architectures: %q",
			imageConfig.Name,
			arch,
			supportedArchitectures,
		)
	}

	return nil
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

	_, distroVerDef, err := env.Distro()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve distro for image build:\n%w", err)
	}

	configOverridePath := distroVerDef.KiwiConfigOverridePath
	if configOverridePath != "" {
		if err := fileutils.ValidateFileExists(env.FS(), configOverridePath); err != nil {
			return nil, fmt.Errorf("invalid KIWI config override %#q:\n%w",
				configOverridePath, err)
		}

		slog.Info("Using KIWI config override", "path", configOverridePath)
		runner.WithConfigOverride(configOverridePath)
	}

	if imageConfig.Definition.Profile != "" {
		runner.WithProfile(imageConfig.Definition.Profile)
	}

	if options.TargetArch != "" {
		runner.WithTargetArch(string(options.TargetArch))
	}

	// Build per-repo options for remote repositories.
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

// ResolveImageByName includes a list of available images in the error message when the image is not found.
func ResolveImageByName(env *azldev.Env, imageName string) (*projectconfig.ImageConfig, error) {
	cfg := env.Config()
	if cfg == nil {
		return nil, errors.New("no project configuration loaded")
	}

	var err error

	// If an image name was provided and exists in the configuration, then return the config.
	if imageName != "" {
		if imageConfig, ok := cfg.Images[imageName]; ok {
			return &imageConfig, nil
		}

		err = fmt.Errorf("image %#q not found in project configuration", imageName)
	} else {
		err = errors.New("image name is required")
	}

	// Something went wrong; list available images so we can offer options in the error message.
	availableImages := lo.Keys(cfg.Images)
	sort.Strings(availableImages)

	if len(availableImages) == 0 {
		return nil, fmt.Errorf("%w; no images defined in project configuration", err)
	}

	return nil, fmt.Errorf(
		"%w; available images: %s",
		err, strings.Join(availableImages, ", "),
	)
}
