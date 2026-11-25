// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"log/slog"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
)

func componentsForLockMode(
	env *azldev.Env, comps []components.Component, lockless bool,
) []components.Component {
	selected := make([]components.Component, 0, len(comps))
	for _, comp := range comps {
		if env.ComponentWithoutLockfile(comp.GetName()) == lockless {
			selected = append(selected, comp)
		}
	}

	if skipped := len(comps) - len(selected); skipped > 0 {
		slog.Info("Skipping components using the other lock mode", "count", skipped)
	}

	return selected
}
