// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
)

func vendorPreparerOptions(
	env *azldev.Env, comp components.Component, opts map[string]string,
) ([]sources.PreparerOption, error) {
	if !comp.GetConfig().HasGoVendorSources() || env.DryRun() {
		return nil, nil
	}

	_, distro, err := env.Distro()
	if err != nil {
		return nil, fmt.Errorf("resolving vendor generation distro:\n%w", err)
	}

	if distro.MockConfigPath == "" {
		return nil, errors.New("'go-vendor' sources require project distro 'mock-config'")
	}

	return []sources.PreparerOption{
		sources.WithGoVendorRunner(mock.NewRunner(env, distro.MockConfigPath).
			WithConfigOpts(opts).WithBaseDir(filepath.Join(env.WorkDir(), "go-vendor"))),
	}, nil
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
