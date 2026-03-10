// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
)

// Options for listing images within the environment.
type ListImageOptions struct {
	// Name patterns to filter images. Supports glob patterns (*, ?, []).
	ImageNamePatterns []string
}

// ImageListResult represents an image in the list output.
type ImageListResult struct {
	// Name of the image.
	Name string `json:"name" table:",sortkey"`

	// Description of the image.
	Description string `json:"description"`

	// Definition contains the image definition details (hidden from table output).
	Definition ImageDefinitionResult `json:"definition" table:"-"`
}

// ImageDefinitionResult represents the definition details for an image.
type ImageDefinitionResult struct {
	// Type indicates the type of image definition (e.g., "kiwi").
	Type string `json:"type"`

	// Path points to the image definition file.
	Path string `json:"path"`
}

func listOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewImageListCommand())
}

// Constructs a [cobra.Command] for "image list" CLI subcommand.
func NewImageListCommand() *cobra.Command {
	options := &ListImageOptions{}

	cmd := &cobra.Command{
		Use:   "list [image-name-pattern...]",
		Short: "List images in this project",
		Long: `List images defined in this project's configuration.

Image name patterns support glob syntax (*, ?, []).
If no patterns are provided, all images are listed.`,
		Example: `  # List all images
  azldev image list

  # List images matching a pattern
  azldev image list "base-*"

  # Output as JSON
  azldev image list -q -O json`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ImageNamePatterns = append(args, options.ImageNamePatterns...)

			return ListImages(env, options)
		}),
		ValidArgsFunction: generateImageNameCompletions,
	}

	azldev.ExportAsMCPTool(cmd)

	return cmd
}

// ListImages lists images in the env, in accordance with options. Returns the found images.
func ListImages(env *azldev.Env, options *ListImageOptions) ([]ImageListResult, error) {
	cfg := env.Config()
	if cfg == nil {
		return nil, nil
	}

	// Collect all image names, sorted.
	imageNames := lo.Keys(cfg.Images)
	sort.Strings(imageNames)

	// If no patterns provided, match all images.
	patterns := options.ImageNamePatterns
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}

	// Filter images by patterns and build results.
	results := make([]ImageListResult, 0, len(imageNames))

	for _, name := range imageNames {
		matched, err := matchesAnyPattern(name, patterns)
		if err != nil {
			return nil, err
		}

		if !matched {
			continue
		}

		imageConfig := cfg.Images[name]
		results = append(results, ImageListResult{
			Name:        name,
			Description: imageConfig.Description,
			Definition: ImageDefinitionResult{
				Type: string(imageConfig.Definition.DefinitionType),
				Path: imageConfig.Definition.Path,
			},
		})
	}

	return results, nil
}

// matchesAnyPattern returns true if name matches any of the given glob patterns.
func matchesAnyPattern(name string, patterns []string) (bool, error) {
	for _, pattern := range patterns {
		matched, err := filepath.Match(pattern, name)
		if err != nil {
			return false, fmt.Errorf("matching pattern %#q against image name %#q:\n%w", pattern, name, err)
		}

		if matched {
			return true, nil
		}
	}

	return false, nil
}

// generateImageNameCompletions generates shell completions for image names.
func generateImageNameCompletions(
	cmd *cobra.Command, _ []string, toComplete string,
) ([]string, cobra.ShellCompDirective) {
	env, err := azldev.GetEnvFromCommand(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	cfg := env.Config()
	if cfg == nil {
		return nil, cobra.ShellCompDirectiveError
	}

	// Collect image names that match the prefix.
	imageNames := lo.Keys(cfg.Images)
	completions := lo.Filter(imageNames, func(name string, _ int) bool {
		return strings.HasPrefix(name, toComplete)
	})

	sort.Strings(completions)

	return completions, cobra.ShellCompDirectiveNoFileComp
}
