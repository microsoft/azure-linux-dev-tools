// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/downloadsources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/componentbuilder"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

func buildLocklessComponent(
	env *azldev.Env, component components.Component, factory *workdir.Factory, options *ComponentBuildOptions,
) (results ComponentBuildResults, err error) {
	name := component.GetName()
	if options.WithoutGitRepo || options.RPMDevBumpspec {
		return results, fmt.Errorf("lockless component %#q does not support '--without-git' or '--rpmdev-bumpspec'", name)
	}

	dir, err := factory.Create(name, "dist-git")
	if err != nil {
		return results, fmt.Errorf("creating dist-git work directory:\n%w", err)
	}
	defer fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), dir, &err)

	if _, err := CreateComponentDistGitRepo(env, &DistGitRepoOptions{
		Component: name, OutputDir: dir, Force: true,
	}); err != nil {
		return results, err
	}

	if err := downloadDistGitSources(env, component, dir); err != nil {
		return results, err
	}

	buildEnv, err := workdir.MkComponentBuildEnvironment(env, factory, component.GetConfig(), "build",
		options.MockConfigOpts)
	if err != nil {
		return results, fmt.Errorf("creating lockless build environment:\n%w", err)
	}
	defer defers.HandleDeferError(func() error {
		if !options.BuildEnvPolicy.ShouldPreserve(err == nil) {
			return buildEnv.Destroy(env)
		}

		return nil
	}, &err)

	builder := componentbuilder.New(env, env.FS(), env, nil, buildEnv, factory)
	cfg := component.GetConfig()
	builder.UseDistGitSources(dir, mock.CommonBuildOptions{
		With: cfg.Build.With, Without: cfg.Build.Without, Defines: cfg.Build.Defines,
	})

	repos := append([]string{}, options.LocalRepoPaths...)
	if options.LocalRepoWithPublishPath != "" {
		repos = append(repos, options.LocalRepoWithPublishPath)
	}

	return buildComponentUsingBuilder(env, component, builder, repos, options.SourcePackageOnly,
		options.NoCheck, options.LocalRepoWithPublishPath)
}

func downloadDistGitSources(env *azldev.Env, component components.Component, dir string) error {
	if len(component.GetConfig().SourceFiles) > 0 {
		distro, err := sourceproviders.ResolveDistro(env, component)
		if err != nil {
			return fmt.Errorf("resolving source distro:\n%w", err)
		}

		manager, err := sourceproviders.NewSourceManager(env, distro)
		if err != nil {
			return fmt.Errorf("creating source manager:\n%w", err)
		}

		if err := manager.FetchFiles(env, component, dir); err != nil {
			return fmt.Errorf("fetching configured dist-git sources:\n%w", err)
		}
	}

	path := filepath.Join(dir, "sources")

	exists, err := fileutils.Exists(env.FS(), path)
	if err != nil {
		return fmt.Errorf("checking dist-git 'sources' manifest:\n%w", err)
	}

	if !exists {
		return nil
	}

	content, err := fileutils.ReadFile(env.FS(), path)
	if err != nil {
		return fmt.Errorf("reading dist-git 'sources' manifest:\n%w", err)
	}

	lines, err := fedorasource.ReadSourcesFile(string(content))
	if err != nil {
		return fmt.Errorf("parsing dist-git 'sources' manifest:\n%w", err)
	}

	for _, line := range lines {
		if line.Entry == nil {
			continue
		}

		exists, err := fileutils.Exists(env.FS(), filepath.Join(dir, line.Entry.Filename))
		if err != nil {
			return fmt.Errorf("checking dist-git source %#q:\n%w", line.Entry.Filename, err)
		}

		if !exists {
			if err := downloadsources.DownloadSources(env, &downloadsources.DownloadSourcesOptions{
				Directory: dir, ComponentName: component.GetName(),
			}); err != nil {
				return fmt.Errorf("downloading dist-git sources:\n%w", err)
			}

			break
		}
	}

	return nil
}
