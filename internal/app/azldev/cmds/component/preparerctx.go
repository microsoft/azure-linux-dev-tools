// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
)

func goVendorPreparerOptions(
	env *azldev.Env, component components.Component, mockConfigOpts map[string]string,
) ([]sources.PreparerOption, error) {
	if !component.GetConfig().GoVendor.IsEnabled() || env.DryRun() {
		return nil, nil
	}

	_, distro, err := env.Distro()
	if err != nil {
		return nil, fmt.Errorf("resolving Go vendor build distro:\n%w", err)
	}

	if distro.MockConfigPath == "" {
		return nil, errors.New("go vendoring requires a project distro with a mock configuration")
	}

	runner := mock.NewRunner(env, distro.MockConfigPath).WithConfigOpts(mockConfigOpts)

	return []sources.PreparerOption{sources.WithGoVendorRunner(runner)}, nil
}

// gitRepoPreparerOptions returns the [sources.PreparerOption] values that enable
// synthetic dist-git history for the current mode.
//
// By default, history is derived from the component's lock file, and working-tree
// changes are detected by comparing input fingerprints. In lock-file-free mode it
// is derived from the component's generated upstream-commit TOML, which has no
// fingerprint to compare against, so only committed changes are represented.
func gitRepoPreparerOptions(
	env *azldev.Env, distro sourceproviders.ResolvedDistro,
) []sources.PreparerOption {
	if env.WithoutLockfile() {
		return []sources.PreparerOption{
			sources.WithGitRepo(env, nil, ""),
			sources.WithoutLockfileHistory(),
		}
	}

	return []sources.PreparerOption{
		sources.WithGitRepo(env, env.LockReader(), distro.Version.ReleaseVer),
		sources.WithDirtyDetection(),
	}
}
