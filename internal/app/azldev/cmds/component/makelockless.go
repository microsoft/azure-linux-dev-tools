// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
)

func makeLocklessOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewMakeLocklessCmd())
}

// NewMakeLocklessCmd constructs the "component make-lockless" command.
func NewMakeLocklessCmd() *cobra.Command {
	filter := &components.ComponentFilter{}
	cmd := &cobra.Command{
		Use:   "make-lockless",
		Short: "Select the lockless workflow and refresh upstream commits",
		Long: `Add selected component names to lockless-components.txt beside the top-level
azldev.toml, then run the refresh-upstream-commit pipeline for those components.

Names are written in lexical order, one exact component name per line,
without blank lines or duplicates. Existing names are retained; selection
patterns are expanded to exact component names.
Existing lock files are left untouched. Already-listed components are refreshed
again without duplicating their entries.

Upstream components get generated commit TOML under base/upstream-commits/.
Include that directory in azldev.toml before component-specific configuration.
Non-upstream components are listed too, but do not contact upstream providers.
The refresh pipeline removes their generated commit TOML if one exists.

If refreshing fails, the updated selection remains in place and successful
component pins are still saved. Fix the failure and rerun this command.`,
		Example: `  azldev comp make-lockless -p curl
  azldev comp make-lockless curl rust-asyncband
  azldev comp make-lockless -g core
  azldev comp make-lockless -a`,
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			filter.ComponentNamePatterns = append(args, filter.ComponentNamePatterns...)

			return MakeComponentsLockless(env, filter)
		}),
		ValidArgsFunction: components.GenerateComponentNameCompletions,
		Annotations: map[string]string{
			azldev.CommandAnnotationPermissiveConfig: "true",
		},
	}
	components.AddComponentFilterOptionsToCommand(cmd, filter)

	return cmd
}

// MakeComponentsLockless selects the lockless workflow and refreshes selected component pins.
func MakeComponentsLockless(
	env *azldev.Env, filter *components.ComponentFilter,
) ([]RefreshUpstreamCommitResult, error) {
	if env.ProjectDir() == "" {
		return nil, errors.New("no project directory configured; cannot select lockless components")
	}

	filter.SkipLockValidation = true
	resolver := components.NewResolver(env)
	resolver.SuppressLockWarnings = true

	resolved, err := resolver.FindComponents(filter)
	if err != nil {
		return nil, fmt.Errorf("resolving components:\n%w", err)
	}

	if resolved.Len() == 0 {
		if filter.IncludeAllComponents {
			return nil, nil
		}

		return nil, errors.New("no components matched the filter")
	}

	names := slices.Clone(resolved.Names())
	slices.Sort(names)

	if env.DryRun() {
		slog.Info("Dry run; would select lockless components and refresh upstream commits", "components", names)

		return nil, nil
	}

	if err := addLocklessComponents(env, names); err != nil {
		return nil, err
	}

	return RefreshUpstreamCommits(env, &RefreshUpstreamCommitOptions{ComponentFilter: *filter})
}

func addLocklessComponents(env *azldev.Env, names []string) error {
	selected, err := projectconfig.ReadLocklessComponents(env.FS(), env.ProjectDir())
	if err != nil {
		return fmt.Errorf("reading lockless component selection:\n%w", err)
	}

	path := filepath.Join(env.ProjectDir(), "lockless-components.txt")

	content, err := fileutils.ReadFile(env.FS(), path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %#q:\n%w", path, err)
	}

	for _, name := range names {
		selected[name] = true
	}

	updated := strings.Join(slices.Sorted(maps.Keys(selected)), "\n") + "\n"

	if string(content) != updated {
		if err := fileutils.WriteFile(env.FS(), path, []byte(updated), fileperms.PublicFile); err != nil {
			return fmt.Errorf("writing %#q:\n%w", path, err)
		}
	}

	env.Config().LocklessComponents = selected

	return nil
}
