// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"log/slog"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// orphanStore is the per-component state store that orphan reconciliation acts on.
// Both the lock store and the generated upstream-commit store satisfy it through
// small adapters, so the reconciliation policy lives in one place regardless of the
// mode's storage.
type orphanStore interface {
	// findOrphans returns the stored entries with no matching resolved component.
	findOrphans(resolved map[string]projectconfig.ComponentConfig) ([]string, error)

	// pruneOrphans deletes those entries and returns how many were removed.
	pruneOrphans(resolved map[string]projectconfig.ComponentConfig) (int, error)
}

// orphanMessages holds the mode-specific wording used while reconciling orphans.
type orphanMessages struct {
	// noComponents describes the state that would be treated as orphaned when the
	// resolved component set is empty. It is completed with "would be" or "will be"
	// depending on whether the run is check-only.
	noComponents string

	// pruned describes what was removed, for the summary log line.
	pruned string
}

// handleOrphans reconciles a store of per-component state with the resolved
// component set. In normal mode it deletes orphan entries; in check-only mode it
// returns the entries that would be deleted without touching disk. Returns
// (nil, nil) when not running with --all-components, since orphan handling is
// scoped to whole-set runs.
func handleOrphans(
	store orphanStore,
	comps []components.Component,
	includeAllComponents bool,
	checkOnly bool,
	messages orphanMessages,
) ([]string, error) {
	if !includeAllComponents {
		return nil, nil
	}

	if len(comps) == 0 {
		tense := "will be"
		if checkOnly {
			tense = "would be"
		}

		slog.Warn(fmt.Sprintf("No components resolved; %s %s treated as orphans",
			messages.noComponents, tense))
	}

	resolvedNames := make(map[string]projectconfig.ComponentConfig, len(comps))
	for _, comp := range comps {
		resolvedNames[comp.GetName()] = *comp.GetConfig()
	}

	if checkOnly {
		return store.findOrphans(resolvedNames)
	}

	pruned, pruneErr := store.pruneOrphans(resolvedNames)
	if pruneErr != nil {
		return nil, pruneErr
	}

	if pruned > 0 {
		slog.Info(messages.pruned, "count", pruned)
	}

	return nil, nil
}
