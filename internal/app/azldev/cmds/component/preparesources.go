// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
)

type PrepareSourcesOptions struct {
	ComponentFilter components.ComponentFilter

	OutputDir    string
	SkipOverlays bool
	Force        bool
}

func prepareOnAppInit(_ *azldev.App, sourceCmd *cobra.Command) {
	sourceCmd.AddCommand(NewPrepareSourcesCmd())
}

func NewPrepareSourcesCmd() *cobra.Command {
	var options PrepareSourcesOptions

	cmd := &cobra.Command{
		Use:     "prepare-sources",
		Aliases: []string{"prep-sources"},
		Short:   "Prepare buildable sources for components",
		Long: `Prepare buildable sources for a component by fetching the upstream spec and
source files, then applying any configured overlays.

The result is a directory containing the spec file and all sources, ready
for inspection or manual building. This is useful for verifying that
overlays apply cleanly before running a full build.

Only one component may be selected at a time.`,
		Example: `  # Prepare sources for a component
  azldev component prep-sources -p curl -o ./build/work/scratch/curl --force

  # Prepare without applying overlays (raw upstream sources)
  azldev component prep-sources -p curl -o ./build/work/scratch/curl --skip-overlays --force`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

			return nil, PrepareComponentSources(env, &options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
		Annotations: map[string]string{
			azldev.CommandAnnotationRootOK: "true",
		},
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().StringVarP(&options.OutputDir, "output-dir", "o", "", "output directory")
	_ = cmd.MarkFlagRequired("output-dir")
	_ = cmd.MarkFlagDirname("output-dir")

	cmd.Flags().BoolVar(&options.SkipOverlays, "skip-overlays", false, "skip applying overlays to prepared sources")
	cmd.Flags().BoolVar(&options.Force, "force", false, "delete and recreate the output directory if it already exists")

	return cmd
}

func PrepareComponentSources(env *azldev.Env, options *PrepareSourcesOptions) error {
	var comps *components.ComponentSet

	resolver := components.NewResolver(env)

	comps, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return fmt.Errorf("failed to resolve components:\n%w", err)
	}

	if comps.Len() == 0 {
		return errors.New("no components were selected; " +
			"please use command-line options to indicate which components you would like to build",
		)
	}

	if comps.Len() != 1 {
		return fmt.Errorf("expected exactly one component, got %d", comps.Len())
	}

	component := comps.Components()[0]

	event := env.StartEvent("Preparing sources", "component", component.GetName(), "outputDir", options.OutputDir)
	defer event.End()

	var sourceManager sourceproviders.SourceManager

	// Create source manager to handle all source fetching, both local and upstream.
	sourceManager, err = sourceproviders.NewSourceManager(env)
	if err != nil {
		return fmt.Errorf("failed to create source manager:\n%w", err)
	}

	// Pre-flight check: detect non-empty output directory before any work.
	if err := CheckOutputDir(env, options); err != nil {
		return err
	}

	preparer, err := sources.NewPreparer(sourceManager, env.FS(), env, env)
	if err != nil {
		return fmt.Errorf("failed to create source preparer:\n%w", err)
	}

	err = preparer.PrepareSources(env, component, options.OutputDir, !options.SkipOverlays)
	if err != nil {
		return fmt.Errorf("failed to prepare sources for component %q:\n%w", component.GetName(), err)
	}

	return nil
}

// CheckOutputDir verifies the output directory state before source preparation.
// If the directory exists and is non-empty, it either removes it (when Force is set)
// or returns an actionable error suggesting --force.
func CheckOutputDir(env *azldev.Env, options *PrepareSourcesOptions) error {
	dirExists, err := fileutils.DirExists(env.FS(), options.OutputDir)
	if err != nil {
		return fmt.Errorf("failed to check output directory %#q:\n%w", options.OutputDir, err)
	}

	if !dirExists {
		return nil
	}

	empty, err := fileutils.IsDirEmpty(env.FS(), options.OutputDir)
	if err != nil {
		return fmt.Errorf("failed to check if output directory %#q is empty:\n%w", options.OutputDir, err)
	}

	if empty {
		return nil
	}

	if options.Force {
		if err := env.FS().RemoveAll(options.OutputDir); err != nil {
			return fmt.Errorf("failed to clean output directory %#q:\n%w", options.OutputDir, err)
		}

		return nil
	}

	return fmt.Errorf(
		"output directory %#q already exists and is not empty;\n"+
			"use --force to delete and recreate it",
		options.OutputDir)
}
