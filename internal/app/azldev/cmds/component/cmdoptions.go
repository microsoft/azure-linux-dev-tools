// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/spf13/cobra"
)

// addComponentFilterOptions registers the component selection flags, including the
// lock-validation flag honored by components using lock files.
func addComponentFilterOptions(
	cmd *cobra.Command, filter *components.ComponentFilter,
) {
	components.AddComponentFilterOptionsToCommand(cmd, filter)
	components.AddLockValidationFlagToCommand(cmd, filter)
}
