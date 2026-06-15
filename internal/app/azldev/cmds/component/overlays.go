// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/spf13/cobra"
)

// OverlaysOptions configures the 'component overlays' subcommand.
type OverlaysOptions struct {
	// ComponentFilter selects which components to inspect.
	ComponentFilter components.ComponentFilter

	// Category, when non-empty, filters output to overlays whose metadata declares this
	// category. Overlays with no metadata are excluded when this is set.
	Category string

	// OnlyAnnotated, when true, excludes overlays with no metadata.
	OnlyAnnotated bool

	// Upstreamability, when non-empty, filters output to overlays whose metadata declares
	// this upstreamability ('yes', 'no', or 'unknown'). Overlays with no metadata count as
	// 'unknown'.
	Upstreamability string
}

func overlaysOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewComponentOverlaysCommand())
}

// NewComponentOverlaysCommand constructs the 'component overlays' subcommand.
func NewComponentOverlaysCommand() *cobra.Command {
	options := &OverlaysOptions{}

	cmd := &cobra.Command{
		Use:   "overlays",
		Short: "List overlays and their metadata for components in this project",
		Long: `List overlays defined on components in the project configuration, including any
documentation metadata (category, commits, upstream PR, bug links, etc.) attached
to each overlay. Overlays loaded from a component's overlay-dir (per-file
.overlay.toml format) inherit their metadata from the file's [metadata] block.

This command is read-only and does not parse spec files or fetch upstream sources,
so it is fast and works even when locks are missing or stale.`,
		Example: `  # List overlays for all components
  azldev component overlays -a

  # List overlays for one component
  azldev component overlays -p curl

  # List only overlays carrying documentation metadata
  azldev component overlays -a --only-annotated

  # Filter by category
  azldev component overlays -a --category backport-fedora

  # List only overlays that can be upstreamed
  azldev component overlays -a --upstreamability yes

  # JSON output for scripting
  azldev component overlays -a -q -O json`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

			return ListOverlays(env, options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	azldev.ExportAsMCPTool(cmd)

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().StringVar(&options.Category, "category", "",
		"only include overlays whose metadata declares this category")
	cmd.Flags().BoolVar(&options.OnlyAnnotated, "only-annotated", false,
		"exclude overlays that have no metadata")
	cmd.Flags().StringVar(&options.Upstreamability, "upstreamability", "",
		"only include overlays whose metadata declares this upstreamability ('yes', 'no', or 'unknown')")

	// This command is read-only; lock validation is irrelevant.
	_ = cmd.Flags().MarkHidden("skip-lock-validation")

	return cmd
}

// OverlayInfo is the per-overlay output for the 'component overlays' subcommand.
type OverlayInfo struct {
	// Component is the name of the component owning the overlay; used as the table sort key.
	Component string `json:"component" table:",sortkey"`

	// Index is the 1-based position of the overlay within the component's overlay list.
	Index int `json:"index"`

	// Type is the overlay type (e.g. 'spec-set-tag', 'patch-add').
	Type projectconfig.ComponentOverlayType `json:"type"`

	// Description is the overlay's top-level human-readable description.
	Description string `json:"description,omitempty" table:",omitempty"`

	// Category surfaces [OverlayMetadata.Category] for tabular output without forcing
	// callers to drill into [OverlayInfo.Metadata]. Empty when the overlay has no metadata.
	Category projectconfig.OverlayCategory `json:"category,omitempty" table:",omitempty"`

	// Upstreamability surfaces [OverlayMetadata.Upstreamability] for tabular output. Always
	// populated: overlays without metadata (or with the field omitted) report 'unknown', so
	// this is never empty and is always rendered.
	Upstreamability projectconfig.OverlayUpstreamability `json:"upstreamability"`

	// Metadata is the full metadata for the overlay. Nil when the overlay has none.
	Metadata *projectconfig.OverlayMetadata `json:"metadata,omitempty" table:"-"`
}

// ListOverlays returns one [OverlayInfo] per matching overlay across the selected
// components, applying the filters in options. Lock validation is always skipped —
// this command is read-only.
func ListOverlays(env *azldev.Env, options *OverlaysOptions) ([]OverlayInfo, error) {
	if options.Category != "" && !projectconfig.OverlayCategory(options.Category).IsValid() {
		return nil, fmt.Errorf("%w: unknown overlay category %#q", azldev.ErrInvalidUsage, options.Category)
	}

	if _, err := normalizeUpstreamabilityFilter(options.Upstreamability); err != nil {
		return nil, err
	}

	options.ComponentFilter.SkipLockValidation = true

	resolver := components.NewResolver(env)

	comps, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve components:\n%w", err)
	}

	results := make([]OverlayInfo, 0)

	for _, comp := range comps.Components() {
		config := comp.GetConfig()

		for idx, overlay := range config.Overlays {
			info := buildOverlayInfo(comp.GetName(), idx+1, &overlay)

			if !overlayInfoMatchesFilters(info, options) {
				continue
			}

			results = append(results, info)
		}
	}

	slices.SortStableFunc(results, func(a, b OverlayInfo) int {
		if c := strings.Compare(a.Component, b.Component); c != 0 {
			return c
		}

		return a.Index - b.Index
	})

	return results, nil
}

// buildOverlayInfo constructs a single [OverlayInfo] for an overlay.
func buildOverlayInfo(
	componentName string, idx int, overlay *projectconfig.ComponentOverlay,
) OverlayInfo {
	info := OverlayInfo{
		Component:       componentName,
		Index:           idx,
		Type:            overlay.Type,
		Description:     overlay.Description,
		Metadata:        overlay.Metadata,
		Upstreamability: projectconfig.OverlayUpstreamabilityUnknown,
	}

	if overlay.Metadata != nil {
		info.Category = overlay.Metadata.Category
		if u := overlay.Metadata.Upstreamability; u != "" {
			info.Upstreamability = u
		}
	}

	return info
}

// normalizeUpstreamabilityFilter validates a user-supplied '--upstreamability' filter
// value. An empty string means no filter was requested. Only 'yes', 'no', and 'unknown'
// are accepted; any other value yields an [azldev.ErrInvalidUsage] error.
func normalizeUpstreamabilityFilter(value string) (projectconfig.OverlayUpstreamability, error) {
	switch value {
	case "":
		return "", nil
	case string(projectconfig.OverlayUpstreamabilityUnknown):
		return projectconfig.OverlayUpstreamabilityUnknown, nil
	case string(projectconfig.OverlayUpstreamabilityYes):
		return projectconfig.OverlayUpstreamabilityYes, nil
	case string(projectconfig.OverlayUpstreamabilityNo):
		return projectconfig.OverlayUpstreamabilityNo, nil
	default:
		return "", fmt.Errorf(
			"%w: unknown upstreamability %#q; want 'yes', 'no', or 'unknown'",
			azldev.ErrInvalidUsage, value,
		)
	}
}

// overlayInfoMatchesFilters reports whether an [OverlayInfo] passes the user-supplied
// category, only-annotated, and upstreamability filters.
func overlayInfoMatchesFilters(info OverlayInfo, options *OverlaysOptions) bool {
	if options.OnlyAnnotated && info.Metadata == nil {
		return false
	}

	if options.Category != "" && string(info.Category) != options.Category {
		return false
	}

	if options.Upstreamability != "" {
		// The filter value is validated upfront in ListOverlays, so the error is unreachable here.
		want, _ := normalizeUpstreamabilityFilter(options.Upstreamability)
		if info.Upstreamability != want {
			return false
		}
	}

	return true
}
