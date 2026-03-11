// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/dirdiff"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
)

// DiffSourcesOptions holds the options for the diff-sources command.
type DiffSourcesOptions struct {
	ComponentFilter components.ComponentFilter

	OutputFile string
}

func diffSourcesOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewDiffSourcesCmd())
}

// NewDiffSourcesCmd constructs a [cobra.Command] for the "component diff-sources" CLI subcommand.
func NewDiffSourcesCmd() *cobra.Command {
	var options DiffSourcesOptions

	cmd := &cobra.Command{
		Use:   "diff-sources",
		Short: "Show the diff that overlays apply to a component's sources",
		Long: `Computes a unified diff showing the changes that overlays apply to a
component's sources. Fetches the sources once, copies them, then applies
overlays to the copy and displays the resulting diff between the two trees.`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

			return DiffComponentSources(env, &options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().StringVar(&options.OutputFile, "output-file", "",
		"write the diff output to a file instead of stdout")

	azldev.ExportAsMCPTool(cmd)

	return cmd
}

// DiffComponentSources computes the diff between original and overlaid sources for a single component.
// When color is enabled and the output format is not JSON, the returned value is a pre-colorized
// string. Otherwise it is [*dirdiff.DiffResult] for structured output.
func DiffComponentSources(env *azldev.Env, options *DiffSourcesOptions) (interface{}, error) {
	resolver := components.NewResolver(env)

	comps, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve components:\n%w", err)
	}

	if comps.Len() == 0 {
		return nil, errors.New("no components were selected; " +
			"please use command-line options to indicate which components you would like to diff",
		)
	}

	if comps.Len() != 1 {
		return nil, fmt.Errorf("expected exactly one component, got %d", comps.Len())
	}

	component := comps.Components()[0]

	event := env.StartEvent("Diffing sources", "component", component.GetName())
	defer event.End()

	// Resolve the effective distro for this component before creating the source manager.
	distro, err := sourceproviders.ResolveDistro(env, component)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve distro for component %q:\n%w", component.GetName(), err)
	}

	sourceManager, err := sourceproviders.NewSourceManager(env, distro)
	if err != nil {
		return nil, fmt.Errorf("failed to create source manager:\n%w", err)
	}

	preparer, err := sources.NewPreparer(sourceManager, env.FS(), env, env)
	if err != nil {
		return nil, fmt.Errorf("failed to create source preparer:\n%w", err)
	}

	// Create a per-component work directory for the diff operation's temp files.
	workDirFactory, err := workdir.NewFactory(env.FS(), env.WorkDir(), env.ConstructionTime())
	if err != nil {
		return nil, fmt.Errorf("failed to create work dir factory:\n%w", err)
	}

	baseDir, err := workDirFactory.Create(component.GetName(), "diff-sources")
	if err != nil {
		return nil, fmt.Errorf("failed to create work dir for component %#q:\n%w", component.GetName(), err)
	}

	result, err := preparer.DiffSources(env, component, baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to diff sources for component %#q:\n%w", component.GetName(), err)
	}

	// If an output file was specified, write the diff there (JSON or plain text depending on format).
	if options.OutputFile != "" {
		if err := writeDiffOutput(env, options.OutputFile, result); err != nil {
			return nil, err
		}

		env.Event("Diff written to file", "file", options.OutputFile)

		return "", nil
	}

	// For JSON output, return the structured form. [FileDiff.MarshalJSON] parses
	// the raw unified diff into per-line change records automatically.
	if env.DefaultReportFormat() == azldev.ReportFormatJSON {
		return result, nil
	}
	// For non-JSON text formats, colorize the diff output when color is enabled.
	if shouldColorize(env) {
		return result.ColorString(), nil
	}

	return result.String(), nil
}

// writeDiffOutput writes the diff result to the specified output file in the appropriate format.
func writeDiffOutput(env *azldev.Env, outputFile string, result *dirdiff.DiffResult) error {
	var fileContent []byte

	if env.DefaultReportFormat() == azldev.ReportFormatJSON {
		jsonBytes, jsonErr := json.MarshalIndent(result, "", "  ")
		if jsonErr != nil {
			return fmt.Errorf("failed to marshal diff to JSON:\n%w", jsonErr)
		}

		jsonBytes = append(jsonBytes, '\n')
		fileContent = jsonBytes
	} else {
		fileContent = []byte(result.String())
	}

	if writeErr := fileutils.WriteFile(env.FS(), outputFile, fileContent, fileperms.PublicFile); writeErr != nil {
		return fmt.Errorf("failed to write diff to %#q:\n%w", outputFile, writeErr)
	}

	return nil
}

// shouldColorize determines whether the current session should produce colorized output,
// based on the environment's [azldev.ColorMode] and whether stdout is a terminal.
func shouldColorize(env *azldev.Env) bool {
	switch env.ColorMode() {
	case azldev.ColorModeAlways:
		return true
	case azldev.ColorModeNever:
		return false
	case azldev.ColorModeAuto:
		return isatty.IsTerminal(os.Stdout.Fd())
	}

	return false
}
