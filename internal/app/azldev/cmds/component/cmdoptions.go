// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/spf13/cobra"
)

// cmdOptions holds the mode-sensitive choices made when a component command is
// constructed. The zero value describes azldev's default (lock file) mode.
type cmdOptions struct {
	// withoutLockfile omits lock-file-specific flags, matching the command surface
	// exposed by the global '--without-lockfile' flag.
	withoutLockfile bool
}

// CmdOption customizes how a component command is constructed.
type CmdOption func(*cmdOptions)

// WithoutLockfileFlags omits the lock-file-specific flags from a component command.
// Commands are registered with this option when the global '--without-lockfile'
// flag selects lock-file-free mode, so the flags that only lock files can honor are
// never offered.
func WithoutLockfileFlags() CmdOption {
	return func(options *cmdOptions) {
		options.withoutLockfile = true
	}
}

// newCmdOptions resolves the supplied command options.
func newCmdOptions(opts ...CmdOption) cmdOptions {
	options := cmdOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	return options
}

// addComponentFilterOptions registers the component selection flags, including the
// lock-file flags that only apply in azldev's default mode.
func addComponentFilterOptions(
	cmd *cobra.Command, filter *components.ComponentFilter, options cmdOptions,
) {
	components.AddComponentFilterOptionsToCommand(cmd, filter)

	if !options.withoutLockfile {
		components.AddLockValidationFlagToCommand(cmd, filter)
	}
}

// cmdOptionsForApp returns the command options matching the app's selected mode.
func cmdOptionsForApp(app *azldev.App) []CmdOption {
	if app.WithoutLockfile() {
		return []CmdOption{WithoutLockfileFlags()}
	}

	return nil
}
