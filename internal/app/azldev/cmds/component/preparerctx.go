// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
)

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
