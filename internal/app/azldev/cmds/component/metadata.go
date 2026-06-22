// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/spf13/cobra"
)

// MetadataSource identifies where a listed documentation-metadata entry originates.
type MetadataSource string

const (
	// MetadataSourceOverlay marks an entry describing one of a component's overlays
	// (declared inline in the component config or inherited from an overlay-dir file).
	MetadataSourceOverlay MetadataSource = "overlay"

	// MetadataSourceGroup marks an entry describing a component group that the component is
	// an explicit member of.
	MetadataSourceGroup MetadataSource = "group"
)

// MetadataOptions configures the 'component metadata' subcommand.
type MetadataOptions struct {
	// ComponentFilter selects which components to inspect.
	ComponentFilter components.ComponentFilter

	// Overlays selects overlay metadata for output. When both Overlays and Groups are
	// false, both sources are listed.
	Overlays bool

	// Groups selects component-group metadata for output. When both Overlays and Groups
	// are false, both sources are listed.
	Groups bool

	// Category, when non-empty, filters output to entries whose metadata declares this
	// category. Entries with no metadata are excluded when this is set.
	Category string

	// OnlyAnnotated, when true, excludes entries with no metadata.
	OnlyAnnotated bool

	// Upstreamable, when non-empty, filters output to entries whose metadata declares this
	// upstreamability ('true', 'false', or 'unknown'). Entries with no metadata, or whose
	// metadata omits the field, count as 'unknown'.
	Upstreamable string
}

// wantOverlays reports whether overlay entries should be listed. With no source flag set,
// both sources are listed.
func (o *MetadataOptions) wantOverlays() bool {
	return o.Overlays || !o.Groups
}

// wantGroups reports whether component-group entries should be listed. With no source flag
// set, both sources are listed.
func (o *MetadataOptions) wantGroups() bool {
	return o.Groups || !o.Overlays
}

func metadataOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewComponentMetadataCommand())
}

// NewComponentMetadataCommand constructs the 'component metadata' subcommand.
func NewComponentMetadataCommand() *cobra.Command {
	options := &MetadataOptions{}

	cmd := &cobra.Command{
		Use:   "metadata",
		Short: "List documentation metadata for components' overlays and groups",
		Long: `List documentation metadata (category, commits, upstream PR, bug links,
upstreamability, etc.) for the selected components. Metadata comes from two
sources, each tagged in the output:

  - 'overlay': metadata attached to one of the component's overlays, declared
    inline in the component config or inherited from an overlay-dir
    '.overlay.toml' file's [metadata] block.
  - 'group':   metadata attached to a component group the component is an
    explicit member of (per the group's 'components' list).

By default both sources are listed. Pass --overlays to show only overlay
metadata, or --groups to show only component-group metadata.

This command is read-only and does not parse spec files or fetch upstream
sources, so it is fast and works even when locks are missing or stale.`,
		Example: `  # List all metadata (overlays and groups) for all components
  azldev component metadata -a

  # List metadata for one component
  azldev component metadata -p curl

  # Only overlay metadata
  azldev component metadata -p curl --overlays

  # Only component-group metadata
  azldev component metadata -p curl --groups

  # List only entries carrying documentation metadata
  azldev component metadata -a --only-annotated

  # Filter by category
  azldev component metadata -a --category backport-dist-git

  # List only entries that can be upstreamed
  azldev component metadata -a --upstreamable true

  # JSON output for scripting
  azldev component metadata -a -q -O json`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ComponentFilter.ComponentNamePatterns = append(args, options.ComponentFilter.ComponentNamePatterns...)

			return ListMetadata(env, options)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
	}

	azldev.ExportAsMCPTool(cmd)

	components.AddComponentFilterOptionsToCommand(cmd, &options.ComponentFilter)

	cmd.Flags().BoolVar(&options.Overlays, "overlays", false,
		"list overlay metadata (default lists both overlays and groups)")
	cmd.Flags().BoolVar(&options.Groups, "groups", false,
		"list component-group metadata (default lists both overlays and groups)")
	cmd.Flags().StringVar(&options.Category, "category", "",
		"only include entries whose metadata declares this category")
	cmd.Flags().BoolVar(&options.OnlyAnnotated, "only-annotated", false,
		"exclude entries that have no metadata")
	cmd.Flags().StringVar(&options.Upstreamable, "upstreamable", "",
		"only include entries whose metadata declares this upstreamability ('true', 'false', or 'unknown')")

	// This command is read-only; lock validation is irrelevant.
	_ = cmd.Flags().MarkHidden("skip-lock-validation")

	return cmd
}

// MetadataInfo is the per-entry output for the 'component metadata' subcommand. Each entry
// describes either one of a component's overlays or a component group it belongs to.
type MetadataInfo struct {
	// Component is the name of the component the entry relates to; used as the table sort key.
	Component string `json:"component" table:",sortkey"`

	// Source identifies whether the entry describes an overlay or a component group.
	Source MetadataSource `json:"source"`

	// Index is the 1-based position of the overlay within the component's overlay list. It
	// is 0 for group entries.
	Index int `json:"index,omitempty" table:",omitempty"`

	// Group is the name of the component group for group entries. Empty for overlay entries.
	Group string `json:"group,omitempty" table:",omitempty"`

	// Type is the overlay type (e.g. 'spec-set-tag', 'patch-add'). Empty for group entries.
	Type projectconfig.ComponentOverlayType `json:"type,omitempty" table:",omitempty"`

	// Description is the overlay's or group's top-level human-readable description.
	Description string `json:"description,omitempty" table:",omitempty"`

	// Category surfaces [projectconfig.OverlayMetadata.Category] for tabular output without
	// forcing callers to drill into [MetadataInfo.Metadata]. Empty when the entry has no metadata.
	Category projectconfig.OverlayCategory `json:"category,omitempty" table:",omitempty"`

	// Upstreamable surfaces [projectconfig.OverlayMetadata.Upstreamable] for tabular
	// output. Nil when the entry has no metadata or its metadata omits the field, which
	// renders as a blank cell (upstreamability not assessed).
	Upstreamable *bool `json:"upstreamable,omitempty" table:",omitempty"`

	// Metadata is the full metadata for the entry. Nil when it has none.
	Metadata *projectconfig.OverlayMetadata `json:"metadata,omitempty" table:"-"`
}

// ListMetadata returns the documentation-metadata entries across the selected components.
// Overlay entries and/or component-group entries are included according to the source
// selectors in options (both by default). Lock validation is always skipped - this command
// is read-only.
func ListMetadata(env *azldev.Env, options *MetadataOptions) ([]MetadataInfo, error) {
	if options.Category != "" && !projectconfig.OverlayCategory(options.Category).IsValid() {
		return nil, fmt.Errorf("%w: unknown overlay category %#q", azldev.ErrInvalidUsage, options.Category)
	}

	wantUpstreamable, err := normalizeUpstreamableFilter(options.Upstreamable)
	if err != nil {
		return nil, err
	}

	options.ComponentFilter.SkipLockValidation = true

	resolver := components.NewResolver(env)

	comps, err := resolver.FindComponents(&options.ComponentFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve components:\n%w", err)
	}

	results := make([]MetadataInfo, 0)

	for _, comp := range comps.Components() {
		name := comp.GetName()

		for _, info := range collectMetadataInfos(env, name, comp.GetConfig(), options) {
			if metadataInfoMatchesFilters(info, options, wantUpstreamable) {
				results = append(results, info)
			}
		}
	}

	sortMetadataInfos(results)

	return results, nil
}

// collectMetadataInfos returns the metadata entries for a component, honoring the overlay
// and group source selectors in options.
func collectMetadataInfos(
	env *azldev.Env, componentName string, config *projectconfig.ComponentConfig, options *MetadataOptions,
) []MetadataInfo {
	infos := make([]MetadataInfo, 0, len(config.Overlays))

	if options.wantOverlays() {
		for idx := range config.Overlays {
			overlay := &config.Overlays[idx]
			infos = append(infos, buildOverlayMetadataInfo(componentName, idx+1, overlay))
		}
	}

	if options.wantGroups() {
		groupNames := slices.Clone(env.Config().GroupsByComponent[componentName])
		sort.Strings(groupNames)

		for _, groupName := range groupNames {
			group := env.Config().ComponentGroups[groupName]
			infos = append(infos, buildGroupMetadataInfo(componentName, groupName, group))
		}
	}

	return infos
}

// buildOverlayMetadataInfo constructs a [MetadataInfo] for one of a component's overlays.
func buildOverlayMetadataInfo(
	componentName string, idx int, overlay *projectconfig.ComponentOverlay,
) MetadataInfo {
	info := MetadataInfo{
		Component:   componentName,
		Source:      MetadataSourceOverlay,
		Index:       idx,
		Type:        overlay.Type,
		Description: overlay.Description,
		Metadata:    overlay.Metadata,
	}

	applyMetadataSummary(&info, overlay.Metadata)

	return info
}

// buildGroupMetadataInfo constructs a [MetadataInfo] for a component group the component
// belongs to.
func buildGroupMetadataInfo(
	componentName, groupName string, group projectconfig.ComponentGroupConfig,
) MetadataInfo {
	info := MetadataInfo{
		Component:   componentName,
		Source:      MetadataSourceGroup,
		Group:       groupName,
		Description: group.Description,
		Metadata:    group.Metadata,
	}

	applyMetadataSummary(&info, group.Metadata)

	return info
}

// applyMetadataSummary copies the category and upstreamable summary fields from the
// (possibly nil) metadata onto the info. Entries without metadata leave the fields unset
// (category empty, upstreamability not assessed).
func applyMetadataSummary(info *MetadataInfo, metadata *projectconfig.OverlayMetadata) {
	if metadata == nil {
		return
	}

	info.Category = metadata.Category
	info.Upstreamable = metadata.Upstreamable
}

// normalizeUpstreamableFilter validates a user-supplied '--upstreamable' filter value. An
// empty string means no filter was requested. Only 'true', 'false', and 'unknown' are
// accepted; any other value yields an [azldev.ErrInvalidUsage] error. The returned string
// Recognized values for the '--upstreamable' filter flag. Kept as named constants so
// [normalizeUpstreamableFilter] (which validates user input) and [upstreamableMatches]
// (which interprets it) stay in sync.
const (
	upstreamableFilterTrue    = "true"
	upstreamableFilterFalse   = "false"
	upstreamableFilterUnknown = "unknown"
)

// is the normalized filter ("", "true", "false", or "unknown").
func normalizeUpstreamableFilter(value string) (string, error) {
	switch value {
	case "", upstreamableFilterTrue, upstreamableFilterFalse, upstreamableFilterUnknown:
		return value, nil
	default:
		return "", fmt.Errorf(
			"%w: unknown upstreamable value %#q; want 'true', 'false', or 'unknown'",
			azldev.ErrInvalidUsage, value,
		)
	}
}

// metadataInfoMatchesFilters reports whether a [MetadataInfo] passes the user-supplied
// category, only-annotated, and upstreamable filters. wantUpstreamable is the
// pre-normalized '--upstreamable' value computed once by [ListMetadata].
func metadataInfoMatchesFilters(
	info MetadataInfo, options *MetadataOptions, wantUpstreamable string,
) bool {
	if options.OnlyAnnotated && info.Metadata == nil {
		return false
	}

	if options.Category != "" && string(info.Category) != options.Category {
		return false
	}

	if wantUpstreamable != "" && !upstreamableMatches(info.Upstreamable, wantUpstreamable) {
		return false
	}

	return true
}

// upstreamableMatches reports whether an entry's upstreamable value (nil meaning "not
// assessed") matches the normalized filter ('true', 'false', or 'unknown'). The 'unknown'
// arm matches both unannotated entries (no metadata at all) and annotated entries that
// omit the field, since both are unassessed; combine with '--only-annotated' to restrict
// to entries whose author left the field blank.
func upstreamableMatches(upstreamable *bool, want string) bool {
	switch want {
	case upstreamableFilterUnknown:
		return upstreamable == nil
	case upstreamableFilterTrue:
		return upstreamable != nil && *upstreamable
	case upstreamableFilterFalse:
		return upstreamable != nil && !*upstreamable
	default:
		return false
	}
}

// sortMetadataInfos orders entries by component, then overlays before groups. Overlay
// entries keep their natural index order; group entries are ordered by group name.
func sortMetadataInfos(infos []MetadataInfo) {
	slices.SortStableFunc(infos, func(left, right MetadataInfo) int {
		if c := strings.Compare(left.Component, right.Component); c != 0 {
			return c
		}

		if c := metadataSourceRank(left.Source) - metadataSourceRank(right.Source); c != 0 {
			return c
		}

		if left.Source == MetadataSourceGroup {
			return strings.Compare(left.Group, right.Group)
		}

		return left.Index - right.Index
	})
}

// metadataSourceRank assigns a stable ordering rank so overlay entries sort before group
// entries regardless of the source label's lexical order.
func metadataSourceRank(source MetadataSource) int {
	if source == MetadataSourceOverlay {
		return 0
	}

	return 1
}
