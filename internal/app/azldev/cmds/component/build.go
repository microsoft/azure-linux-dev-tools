// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"path/filepath"

	rpmlib "github.com/cavaliergopher/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/componentbuilder"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/buildenv"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/localrepo"
	"github.com/spf13/cobra"
)

type ComponentBuildOptions struct {
	ComponentFilter components.ComponentFilter

	ContinueOnError   bool
	NoCheck           bool
	WithGitRepo       bool
	SourcePackageOnly bool
	BuildEnvPolicy    BuildEnvPreservePolicy

	LocalRepoPaths           []string
	LocalRepoWithPublishPath string

	// MockConfigOpts is an optional set of key-value config options that will be passed through
	// to mock as --config-opts key=value arguments.
	MockConfigOpts map[string]string
}

// RPMResult encapsulates a single binary RPM produced by a component build,
// together with the resolved publish channel for that package.
type RPMResult struct {
	// Path is the absolute path to the RPM file in the build output directory.
	Path string `json:"path" table:"Path"`

	// PackageName is the binary package name extracted from the RPM header tag (e.g., "libcurl-devel").
	PackageName string `json:"packageName" table:"Package"`

	// Channel is the resolved publish channel from project config.
	// Empty when no channel is configured for this package.
	Channel string `json:"publishChannel" table:"Publish Channel"`
}

// ComponentBuildResults summarizes the results of building a single component.
type ComponentBuildResults struct {
	// Names of the component that was built.
	ComponentName string `json:"componentName"`

	// Absolute paths to any source RPMs built by the operation.
	SRPMPaths []string `json:"srpmPaths" table:"SRPM Paths"`

	// Absolute paths to any RPMs built by the operation.
	RPMPaths []string `json:"rpmPaths" table:"RPM Paths"`

	// RPMChannels holds the resolved publish channel for each RPM, parallel to [RPMPaths].
	// Empty string means no channel was configured for that package.
	RPMChannels []string `json:"rpmChannels" table:"Publish Channels"`

	// RPMs contains enriched per-RPM information including the resolved publish channel.
	RPMs []RPMResult `json:"rpms" table:"-"`
}

func buildOnAppInit(_ *azldev.App, parent *cobra.Command) {
	parent.AddCommand(NewBuildCmd())
}

func NewBuildCmd() *cobra.Command {
	// Fill out options defaults.
	options := &ComponentBuildOptions{
		BuildEnvPolicy: BuildEnvPreserveOnFailure,
	}

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build packages for components",
		Long: `Build RPM packages for one or more components using mock.

This command fetches upstream sources (applying any configured overlays),
creates an SRPM, and invokes mock to produce binary RPMs. Build outputs
are placed in structured subdirectories under the project output directory:

  out/srpms/           - source RPMs (SRPMs)
  out/rpms/            - binary RPMs with no channel configured (or channel="none")
  out/rpms/<channel>/  - binary RPMs moved to their configured publish channel subdirectory

The publish channel for each package is resolved from the project's package
configuration (package groups and per-component overrides). See 'azldev package'
for details.

Use --local-repo-with-publish to build a chain of dependent components:
each component's RPMs are published to a local repository that subsequent
builds can consume.`,
		Example: `  # Build a single component
  azldev component build -p curl

  # Build all components, continuing past failures
  azldev component build -a -k

  # Build SRPM only (skip binary RPM build)
  azldev component build -p curl --srpm-only

  # Chain-build dependent components with a local repo
  azldev component build --local-repo-with-publish ./base/out -p liba -p libb`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(options.ComponentFilter.ComponentNamePatterns, args...)

			return SelectAndBuildComponents(env, options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)
	cmd.Flags().BoolVarP(&options.ContinueOnError, "continue-on-error", "k", false,
		"Continue building when some components fail")
	cmd.Flags().BoolVar(&options.NoCheck, "no-check", false, "Skip package %check tests")
	cmd.Flags().BoolVar(&options.WithGitRepo, "with-git", false,
		"Create a dist-git repository with synthetic commit history (requires a project git repository)")
	cmd.Flags().BoolVar(&options.SourcePackageOnly, "srpm-only", false, "Build SRPM (source RPM) *only*")
	cmd.Flags().Var(&options.BuildEnvPolicy, "preserve-buildenv",
		fmt.Sprintf("Preserve build environment {%s, %s, %s}",
			BuildEnvPreserveOnFailure,
			BuildEnvPreserveAlways,
			BuildEnvPreserveNever,
		))
	cmd.Flags().StringArrayVar(&options.LocalRepoPaths, "local-repo", []string{},
		"Paths to local repositories to include during build (can be specified multiple times)")
	cmd.Flags().StringVar(&options.LocalRepoWithPublishPath, "local-repo-with-publish", "",
		"Path to local repository to include during build and publish built RPMs to")
	cmd.Flags().StringToStringVar(&options.MockConfigOpts, "mock-config-opt", nil,
		"Pass a configuration option through to mock (key=value, can be specified multiple times)")

	// Mark flags as mutually exclusive.
	cmd.MarkFlagsMutuallyExclusive("srpm-only", "local-repo-with-publish")

	return cmd
}

func SelectAndBuildComponents(env *azldev.Env, options *ComponentBuildOptions,
) ([]ComponentBuildResults, error) {
	// Validate options before doing any work.
	if err := validateBuildOptions(env, options); err != nil {
		return nil, err
	}

	var comps *components.ComponentSet

	resolver := components.NewResolver(env)

	comps, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve components:\n%w", err)
	}

	if comps.Len() == 0 {
		return nil, errors.New(
			"no components were selected by the command-line options you provided; please add " +
				"or adjust options to select a valid set of components that you would like to build",
		)
	}

	return BuildComponents(env, comps, options)
}

func BuildComponents(
	env *azldev.Env, components *components.ComponentSet, options *ComponentBuildOptions,
) ([]ComponentBuildResults, error) {
	if env.WorkDir() == "" {
		return nil, errors.New("can't build packages without valid work dir")
	}

	if env.OutputDir() == "" {
		return nil, errors.New("can't build packages without valid output dir")
	}

	workDirFactory, err := workdir.NewFactory(env.FS(), env.WorkDir(), env.ConstructionTime())
	if err != nil {
		return nil, fmt.Errorf("failed to create work dir factory:\n%w", err)
	}

	results := make([]ComponentBuildResults, 0, components.Len())

	for _, component := range components.Components() {
		componentResults, buildErr := BuildComponent(env, component, workDirFactory, options)
		if buildErr != nil {
			buildErr = fmt.Errorf("failed to build %q:\n%w", component.GetName(), buildErr)
		}

		err = errors.Join(err, buildErr)
		if err != nil && !options.ContinueOnError {
			return results, err
		}

		results = append(results, componentResults)
	}

	return results, err
}

func BuildComponent(
	env *azldev.Env,
	component components.Component,
	workDirFactory *workdir.Factory,
	options *ComponentBuildOptions,
) (results ComponentBuildResults, err error) {
	// Resolve the effective distro for this component before creating the source manager.
	distro, err := sourceproviders.ResolveDistro(env, component)
	if err != nil {
		return ComponentBuildResults{},
			fmt.Errorf("failed to resolve distro for component %q:\n%w", component.GetName(), err)
	}

	sourceManager, err := sourceproviders.NewSourceManager(env, distro)
	if err != nil {
		return ComponentBuildResults{},
			fmt.Errorf("failed to setup source retrieval manager for component %q:\n%w", component.GetName(), err)
	}

	var buildEnv buildenv.RPMAwareBuildEnv

	buildEnv, err = workdir.MkComponentBuildEnvironment(env, workDirFactory, component.GetConfig(), "build",
		options.MockConfigOpts)
	if err != nil {
		return ComponentBuildResults{},
			fmt.Errorf("failed to create build environment for component %q:\n%w", component.GetName(), err)
	}

	// Clean up the build environment before we return (unless we were asked not to do so).
	defer defers.HandleDeferError(func() error {
		if !options.BuildEnvPolicy.ShouldPreserve(err == nil) {
			return buildEnv.Destroy(env)
		}

		return nil
	}, &err)

	var preparerOpts []sources.PreparerOption
	if options.WithGitRepo {
		preparerOpts = append(preparerOpts, sources.WithGitRepo(env.Config().Project.DefaultAuthorEmail))
	}

	sourcePreparer, err := sources.NewPreparer(sourceManager, env.FS(), env, env, preparerOpts...)
	if err != nil {
		return ComponentBuildResults{},
			fmt.Errorf("failed to create source preparer for component %q:\n%w", component.GetName(), err)
	}

	builder := componentbuilder.New(env, env.FS(), env, sourcePreparer, buildEnv, workDirFactory)

	// Combine both local repo paths for use during build (both are used as dependencies).
	allLocalRepoPaths := append([]string{}, options.LocalRepoPaths...)
	if options.LocalRepoWithPublishPath != "" {
		allLocalRepoPaths = append(allLocalRepoPaths, options.LocalRepoWithPublishPath)
	}

	results, err = buildComponentUsingBuilder(
		env, component, builder, allLocalRepoPaths,
		options.SourcePackageOnly, options.NoCheck,
		options.LocalRepoWithPublishPath,
	)
	if err != nil {
		return results, err
	}

	return results, nil
}

func buildComponentUsingBuilder(
	env *azldev.Env,
	component components.Component,
	builder *componentbuilder.Builder,
	localRepoPaths []string,
	sourcePackageOnly, noCheck bool,
	localRepoWithPublishPath string,
) (results ComponentBuildResults, err error) {
	// Compose the path to the output dir.
	outputDir := env.OutputDir()

	// All binary RPMs land in out/rpms/ so they are kept separate from SRPMs
	// and other build artifacts in out/.
	rpmsDir := filepath.Join(outputDir, "rpms")

	// SRPMs land in out/srpms/.
	srpmsDir := filepath.Join(outputDir, "srpms")

	// Make sure all output directories exist.
	err = fileutils.MkdirAll(env.FS(), outputDir)
	if err != nil {
		return results, fmt.Errorf("failed to ensure dir %#q exists:\n%w", outputDir, err)
	}

	err = fileutils.MkdirAll(env.FS(), rpmsDir)
	if err != nil {
		return results, fmt.Errorf("failed to ensure dir %#q exists:\n%w", rpmsDir, err)
	}

	err = fileutils.MkdirAll(env.FS(), srpmsDir)
	if err != nil {
		return results, fmt.Errorf("failed to ensure dir %#q exists:\n%w", srpmsDir, err)
	}

	buildEvent := env.StartEvent("Building packages with mock", "component", component.GetName())

	defer buildEvent.End()

	//
	// Build the SRPM.
	//

	outputSourcePackagePath, err := builder.BuildSourcePackage(env, component, localRepoPaths, srpmsDir)
	if err != nil {
		return results, fmt.Errorf("failed to build SRPM for %#q:\n%w", component.GetName(), err)
	}

	// Start filling out results.
	results.ComponentName = component.GetName()
	results.SRPMPaths = []string{outputSourcePackagePath}

	// Short circuit if we were asked only to build the SRPM.
	if sourcePackageOnly {
		return results, nil
	}

	//
	// Build the RPM.
	//

	results.RPMPaths, err = builder.BuildBinaryPackage(
		env, component, outputSourcePackagePath, localRepoPaths, rpmsDir, noCheck,
	)
	if err != nil {
		return results, fmt.Errorf("failed to build RPM for %#q:\n%w", component.GetName(), err)
	}

	// Enrich each RPM with its binary package name and resolved publish channel.
	results.RPMs, err = resolveRPMResults(env.FS(), results.RPMPaths, env.Config(), component.GetConfig())
	if err != nil {
		return results, fmt.Errorf("failed to resolve publish channels for %#q:\n%w", component.GetName(), err)
	}

	// Move RPMs with a channel into out/rpms/<channel>/, leaving unconfigured ones in out/rpms/.
	if err = PlaceRPMsByChannel(env, results.RPMs, rpmsDir); err != nil {
		return results, fmt.Errorf("failed to place RPMs by channel for %#q:\n%w", component.GetName(), err)
	}

	// Sync RPMPaths to the final (possibly moved) locations.
	results.RPMPaths = make([]string, len(results.RPMs))
	for rpmIdx, rpm := range results.RPMs {
		results.RPMPaths[rpmIdx] = rpm.Path
	}

	// Populate the parallel Channels slice for table display.
	results.RPMChannels = make([]string, len(results.RPMs))
	for rpmIdx, rpm := range results.RPMs {
		results.RPMChannels[rpmIdx] = rpm.Channel
	}

	// Publish built RPMs to local repo with publish enabled.
	if localRepoWithPublishPath != "" && len(results.RPMPaths) > 0 {
		publishErr := publishToLocalRepo(env, results.RPMPaths, localRepoWithPublishPath)
		if publishErr != nil {
			return results, fmt.Errorf("failed to publish RPMs for %q:\n%w", component.GetName(), publishErr)
		}
	}

	return results, nil
}

// PlaceRPMsByChannel moves each RPM with a configured channel from its initial location in
// rpmsDir to a channel-specific subdirectory rpmsDir/<channel>/.
// RPMs whose channel is empty or the reserved value "none" remain in rpmsDir.
// [RPMResult.Path] is updated in-place to reflect the final location of each RPM.
func PlaceRPMsByChannel(env *azldev.Env, rpmResults []RPMResult, rpmsDir string) error {
	for rpmIdx, rpm := range rpmResults {
		if rpm.Channel == "" || rpm.Channel == "none" {
			continue
		}

		channelDir := filepath.Join(rpmsDir, rpm.Channel)

		if err := fileutils.MkdirAll(env.FS(), channelDir); err != nil {
			return fmt.Errorf("failed to create channel directory %#q:\n%w", channelDir, err)
		}

		destPath := filepath.Join(channelDir, filepath.Base(rpm.Path))

		if err := env.FS().Rename(rpm.Path, destPath); err != nil {
			return fmt.Errorf("failed to move package %#q to channel %#q:\n%w", rpm.PackageName, rpm.Channel, err)
		}

		rpmResults[rpmIdx].Path = destPath
	}

	return nil
}

// validateBuildOptions validates the build options before any work is done.
func validateBuildOptions(env *azldev.Env, options *ComponentBuildOptions) error {
	// Check for overlap between --local-repo and --local-repo-with-publish.
	// (Check config errors before tool availability for better UX.)
	if err := checkLocalRepoPathOverlap(options.LocalRepoPaths, options.LocalRepoWithPublishPath); err != nil {
		return err
	}

	// If we have a repo to publish to, create and initialize it.
	// This checks for createrepo_c availability and initializes the repo metadata
	// so it can be used as a dependency source during builds.
	if options.LocalRepoWithPublishPath != "" {
		_, err := localrepo.NewPublisher(env, options.LocalRepoWithPublishPath, true)
		if err != nil {
			return fmt.Errorf("failed to initialize publish repository:\n%w", err)
		}
	}

	return nil
}

// checkLocalRepoPathOverlap checks that the path doesn't appear in both --local-repo and --local-repo-with-publish.
func checkLocalRepoPathOverlap(localRepoPaths []string, localRepoWithPublishPath string) error {
	// If no publish path is specified, there's no overlap.
	if localRepoWithPublishPath == "" {
		return nil
	}

	// Normalize the publish path for comparison.
	absPublishPath, err := filepath.Abs(localRepoWithPublishPath)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path for %#q:\n%w", localRepoWithPublishPath, err)
	}

	// Check if the publish path appears in local repo paths.
	for _, repoPath := range localRepoPaths {
		absPath, err := filepath.Abs(repoPath)
		if err != nil {
			return fmt.Errorf("failed to resolve absolute path for %#q:\n%w", repoPath, err)
		}

		if absPath == absPublishPath {
			return fmt.Errorf(
				"path %#q appears in both --local-repo and --local-repo-with-publish; "+
					"use --local-repo-with-publish only for repos you want to both read from and publish to",
				localRepoWithPublishPath,
			)
		}
	}

	return nil
}

// resolveRPMResults builds an [RPMResult] for each RPM path, extracting the binary package
// name from the RPM headers and resolving its publish channel from the project config (if available).
// When no project config is loaded, the Channel field is left empty.
func resolveRPMResults(
	fs opctx.FS, rpmPaths []string, proj *projectconfig.ProjectConfig, compConfig *projectconfig.ComponentConfig,
) ([]RPMResult, error) {
	rpmResults := make([]RPMResult, 0, len(rpmPaths))

	for _, rpmPath := range rpmPaths {
		pkgName, err := packageNameFromRPM(fs, rpmPath)
		if err != nil {
			return nil, fmt.Errorf("failed to determine package name:\n%w", err)
		}

		rpmResult := RPMResult{
			Path:        rpmPath,
			PackageName: pkgName,
		}

		if proj != nil {
			pkgConfig, err := projectconfig.ResolvePackageConfig(pkgName, compConfig, proj)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve package config for %#q:\n%w", pkgName, err)
			}

			rpmResult.Channel = pkgConfig.Publish.Channel
		}

		rpmResults = append(rpmResults, rpmResult)
	}

	return rpmResults, nil
}

// packageNameFromRPM extracts the binary package name from an RPM file by reading
// its headers. Reading the Name tag directly from the RPM metadata is authoritative and
// handles all valid package names regardless of naming conventions.
func packageNameFromRPM(fs opctx.FS, rpmPath string) (string, error) {
	rpmFile, err := fs.Open(rpmPath)
	if err != nil {
		return "", fmt.Errorf("failed to open RPM %#q:\n%w", rpmPath, err)
	}

	defer rpmFile.Close()

	pkg, err := rpmlib.Read(rpmFile)
	if err != nil {
		return "", fmt.Errorf("failed to read RPM headers from %#q:\n%w", rpmPath, err)
	}

	return pkg.Name(), nil
}

// publishToLocalRepo publishes the given RPMs to the specified local repo.
func publishToLocalRepo(env *azldev.Env, rpmPaths []string, repoPath string) error {
	publisher, err := localrepo.NewPublisher(env, repoPath, false)
	if err != nil {
		return fmt.Errorf("failed to create publisher for %#q:\n%w", repoPath, err)
	}

	// Ensure the repo directory exists.
	if err := publisher.EnsureRepoExists(); err != nil {
		return fmt.Errorf("ensuring local repo exists:\n%w", err)
	}

	// Publish RPMs and update repo metadata.
	if err := publisher.PublishRPMs(env, rpmPaths); err != nil {
		return fmt.Errorf("failed to publish to %#q:\n%w", repoPath, err)
	}

	return nil
}
