// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package testutils

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
)

func PrepareCommand(cmd *cobra.Command, env *azldev.Env) *cobra.Command {
	cmd.SetContext(env)

	return cmd
}
