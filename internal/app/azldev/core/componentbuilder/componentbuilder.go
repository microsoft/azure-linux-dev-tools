// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../../../../tools/mockgen/go.mod mockgen -source=componentbuilder.go -destination=componentbuilder_test/componentbuilder_mocks.go -package=componentbuilder --copyright_file=../../../../../.license-preamble

package componentbuilder

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"

	"github.com/fatih/color"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/buildenv"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// ComponentBuilder defines the interface for building components.
type ComponentBuilder interface {
	// BuildBinaryPackage builds a binary package for the given component from the provided source package
	// and returns the paths to the built binary packages.
	BuildBinaryPackage(
		ctx context.Context,
		component components.Component, sourcePackagePath string, localRepoPaths []string,
		outputDir string, noCheck bool,
	) (packagePaths []string, err error)

	// BuildSourcePackage builds a source package for the given component and returns the path to the
	// built source package.
	BuildSourcePackage(ctx context.Context, component components.Component, localRepoPaths []string, outputDir string,
	) (packagePath string, err error)
}

type Builder struct {
	dryRunnable    opctx.DryRunnable
	fs             opctx.FS
	eventListener  opctx.EventListener
	sourcePreparer sources.SourcePreparer
	buildEnv       buildenv.RPMAwareBuildEnv
	workDirFactory *workdir.Factory
}

// Ensure that [Builder] implements the [ComponentBuilder] interface.
var _ ComponentBuilder = (*Builder)(nil)

func New(
	dryRunnable opctx.DryRunnable,
	fs opctx.FS,
	eventListener opctx.EventListener,
	sourcePreparer sources.SourcePreparer,
	buildEnv buildenv.RPMAwareBuildEnv,
	workDirFactory *workdir.Factory,
) *Builder {
	return &Builder{
		dryRunnable:    dryRunnable,
		fs:             fs,
		eventListener:  eventListener,
		sourcePreparer: sourcePreparer,
		buildEnv:       buildEnv,
		workDirFactory: workDirFactory,
	}
}

func (b *Builder) BuildSourcePackage(
	ctx context.Context, component components.Component, localRepoPaths []string, outputDir string,
) (packagePath string, err error) { // NOTE: We intentionally name the returns for better self-documentation.
	packagePath, err = b.buildSRPM(ctx, component, b.buildEnv, localRepoPaths, outputDir)
	if err != nil {
		return packagePath, fmt.Errorf("failed to build source package for component %#q:\n%w", component.GetName(), err)
	}

	return packagePath, nil
}

func (b *Builder) BuildBinaryPackage(
	ctx context.Context,
	component components.Component, sourcePackagePath string, localRepoPaths []string,
	outputDir string, noCheck bool,
) (packagePaths []string, err error) { // NOTE: We intentionally name the returns for better self-documentation.
	packagePaths, err = b.buildRPM(ctx, component, sourcePackagePath, b.buildEnv, localRepoPaths, outputDir, noCheck)
	if err != nil {
		return packagePaths, fmt.Errorf("failed to build binary package for component %#q:\n%w", component.GetName(), err)
	}

	return packagePaths, nil
}

func (b *Builder) buildSRPM(
	ctx context.Context, component components.Component, buildEnv buildenv.RPMAwareBuildEnv,
	localRepoPaths []string,
	outputDir string,
) (outputSRPMPath string, err error) { // NOTE: We intentionally name the returns for better self-documentation.
	preparedSourcesDir, err := b.prepSourcesForSRPM(ctx, component)
	if err != nil {
		return "", fmt.Errorf("failed to prepare sources for SRPM:\n%w", err)
	}

	tempSRPMOutputDir, err := b.buildSRPMFromPreparedSources(ctx, component, preparedSourcesDir, buildEnv, localRepoPaths)
	if err != nil {
		return "", fmt.Errorf("failed to build SRPM from prepared sources:\n%w", err)
	}

	return b.copySRPMToOutputDir(tempSRPMOutputDir, outputDir)
}

func (b *Builder) prepSourcesForSRPM(
	ctx context.Context, component components.Component,
) (preparedSourcesDir string, err error) {
	// Prep sources.
	prepEvent := b.eventListener.StartEvent("Acquiring and preparing sources", "component", component.GetName())

	defer prepEvent.End()

	// Create a temp dir for the sources.
	preparedSourcesDir, err = b.workDirFactory.Create(component.GetName(), "sources")
	if err != nil {
		return "", fmt.Errorf("failed to create work dir for source preparation:\n%w", err)
	}

	err = b.sourcePreparer.PrepareSources(ctx, component, preparedSourcesDir, true /*applyOverlays?*/)
	if err != nil {
		return "", fmt.Errorf("failed to prepare sources:\n%w", err)
	}

	return preparedSourcesDir, nil
}

func (b *Builder) buildSRPMFromPreparedSources(
	ctx context.Context,
	component components.Component, preparedSourcesDir string,
	buildEnv buildenv.RPMAwareBuildEnv, localRepoPaths []string,
) (srpmOutputDir string, err error) {
	preparedSpecPath := path.Join(preparedSourcesDir, component.GetName()+".spec")

	// Create a work dir for the SRPM itself.
	srpmOutputDir, err = b.workDirFactory.Create(component.GetName(), "srpm-build")
	if err != nil {
		return "", fmt.Errorf("failed to create work dir:\n%w", err)
	}

	// Build the SRPM
	srpmEvent := b.eventListener.StartEvent("Building SRPM in isolated environment",
		"component", component.GetName(), "logs", srpmOutputDir)

	defer srpmEvent.End()

	// Since we've already applied the macros, with, and without values, we don't need the builder
	// to apply them; we leave them blank.
	srpmOptions := buildenv.SRPMBuildOptions{
		CommonBuildOptions: mock.CommonBuildOptions{
			With:           []string{},
			Without:        []string{},
			Defines:        map[string]string{},
			LocalRepoPaths: localRepoPaths,
		},
	}

	err = buildEnv.BuildSRPM(ctx, preparedSpecPath, preparedSourcesDir, srpmOutputDir, srpmOptions)
	if err != nil {
		slog.Error("failed to build SRPM", "logs", srpmOutputDir)

		return srpmOutputDir, fmt.Errorf("failed to build srpm:\n%w", err)
	}

	return srpmOutputDir, nil
}

func (b *Builder) copySRPMToOutputDir(inputSRPMDir, outputSRPMDir string) (string, error) {
	if b.dryRunnable.DryRun() {
		return "package.src.rpm", nil
	}

	// Find the SRPM and copy it to the final output dir. We only ever expect to see
	// exactly one SRPM.
	srpmPaths, _ := fileutils.Glob(b.fs, path.Join(inputSRPMDir, "*.src.rpm"))
	if len(srpmPaths) != 1 {
		return "", fmt.Errorf("failed to find single .src.rpm in %q; found %d", inputSRPMDir, len(srpmPaths))
	}

	srpmPath := srpmPaths[0]

	err := fileutils.CopyFile(b.dryRunnable, b.fs,
		srpmPath,
		path.Join(outputSRPMDir, path.Base(srpmPath)),
		fileutils.CopyFileOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("failed to copy SRPM to its final output dir:\n%w", err)
	}

	return path.Join(outputSRPMDir, path.Base(srpmPath)), nil
}

func (b *Builder) buildRPM(
	ctx context.Context,
	component components.Component,
	srpmPath string, buildEnv buildenv.RPMAwareBuildEnv, localRepoPaths []string,
	outputDir string, noCheck bool,
) (outputRPMPaths []string, err error) { // NOTE: We intentionally name the returns for better self-documentation.
	var tempRPMDir string

	// Create a temp dir for the RPM itself.
	tempRPMDir, err = b.workDirFactory.Create(component.GetName(), "rpm-build")
	if err != nil {
		return outputRPMPaths, fmt.Errorf("failed to create temp dir:\n%w", err)
	}

	rpmEvent := b.eventListener.StartEvent("Building RPM in isolated environment",
		"component", component.GetName(), "logs", tempRPMDir)

	defer rpmEvent.End()

	// Compose RPM build options, based on our parameters and the component's default configuration.
	// Since we've already applied the macros, with, and without values, we don't need the builder
	// to apply them; we leave them blank.
	rpmOptions := buildenv.RPMBuildOptions{
		CommonBuildOptions: mock.CommonBuildOptions{
			With:           []string{},
			Without:        []string{},
			Defines:        map[string]string{},
			LocalRepoPaths: localRepoPaths,
		},
		NoCheck:      noCheck,
		ForceRebuild: false, // Could be added to config if needed
	}

	err = buildEnv.BuildRPM(ctx, srpmPath, tempRPMDir, rpmOptions)
	if err != nil {
		slog.Error("failed to build RPM", "logs", tempRPMDir)

		// Make a best-effort attempt to find relevant details from failure logs.
		displayPossiblyRelevantFailureLogs(b.fs, buildEnv, tempRPMDir)

		return outputRPMPaths, fmt.Errorf("failed to build rpm:\n%w", err)
	}

	outputRPMPaths, err = b.copyRPMsToOutputDir(tempRPMDir, outputDir)
	if err != nil {
		return outputRPMPaths, fmt.Errorf("failed to copy RPMs to output dir:\n%w", err)
	}

	return outputRPMPaths, nil
}

func (b *Builder) copyRPMsToOutputDir(inputDir, outputRPMDir string) ([]string, error) {
	outputRPMPaths := []string{}

	// Find the RPMs; copy them to their final output dir.
	rpmPaths, _ := fileutils.Glob(b.fs, path.Join(inputDir, "*.rpm"))
	for _, rpmPath := range rpmPaths {
		// Skip SRPMs.
		if strings.HasSuffix(rpmPath, ".src.rpm") {
			continue
		}

		// Copy the RPM.
		err := fileutils.CopyFile(b.dryRunnable, b.fs,
			rpmPath,
			path.Join(outputRPMDir, path.Base(rpmPath)),
			fileutils.CopyFileOptions{},
		)
		if err != nil {
			return outputRPMPaths, fmt.Errorf("failed to copy RPM to its final output dir:\n%w", err)
		}

		outputRPMPaths = append(outputRPMPaths, path.Join(outputRPMDir, path.Base(rpmPath)))
	}

	return outputRPMPaths, nil
}

func displayPossiblyRelevantFailureLogs(fs opctx.FS, buildEnv buildenv.RPMAwareBuildEnv, mockOutputDirPath string) {
	details := buildEnv.TryGetFailureDetails(fs, mockOutputDirPath)
	if details == nil {
		return
	}

	color.Set(color.FgHiBlack)

	if len(details.LastRPMBuildLogLines) > 0 {
		color.Set(color.Italic)
		fmt.Fprintf(os.Stderr, "Last %d RPM build log lines: ────────────────────────────────────────────\n",
			len(details.LastRPMBuildLogLines))
		color.Set(color.ResetItalic)

		for _, line := range details.LastRPMBuildLogLines {
			fmt.Fprintf(os.Stderr, "  │ %s\n", line)
		}

		fmt.Fprintf(os.Stderr, "  └──────────────────────────────────────────────────────────────────────\n")
	}

	if len(details.RPMBuildErrors) > 0 {
		color.Set(color.Italic)
		fmt.Fprintf(os.Stderr, "RPM build errors: ───────────────────────────────────────────────────────\n")
		color.Set(color.ResetItalic)

		for _, buildError := range details.RPMBuildErrors {
			fmt.Fprintf(os.Stderr, "  │ %s\n", buildError)
		}

		fmt.Fprintf(os.Stderr, "  └──────────────────────────────────────────────────────────────────────\n")
	}

	color.Unset()
}
