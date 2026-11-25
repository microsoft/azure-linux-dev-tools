// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/downloadsources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/componentbuilder"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/samber/lo"
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
	builder.UseDistGitSources(dir)

	repos := append([]string{}, options.LocalRepoPaths...)
	if options.LocalRepoWithPublishPath != "" {
		repos = append(repos, options.LocalRepoWithPublishPath)
	}

	return buildComponentUsingBuilder(env, component, builder, repos, options.SourcePackageOnly,
		options.NoCheck, options.LocalRepoWithPublishPath)
}

func downloadDistGitSources(env *azldev.Env, component components.Component, dir string) error {
	var manager sourceproviders.SourceManager

	if len(component.GetConfig().SourceFiles) > 0 {
		distro, err := sourceproviders.ResolveDistro(env, component)
		if err != nil {
			return fmt.Errorf("resolving source distro:\n%w", err)
		}

		manager, err = sourceproviders.NewSourceManager(env, distro)
		if err != nil {
			return fmt.Errorf("creating source manager:\n%w", err)
		}

		if err := manager.FetchFiles(env, component, dir); err != nil {
			return fmt.Errorf("fetching configured dist-git sources:\n%w", err)
		}
	}

	lines, err := readDistGitSourcesManifest(env, dir)
	if err != nil || lines == nil {
		return err
	}

	if manager != nil {
		if err := restoreOverlaidArchives(env, manager, component, dir, lines); err != nil {
			return err
		}
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

	return validateDistGitSources(env, component.GetName(), dir, lines)
}

// readDistGitSourcesManifest parses the committed 'sources' manifest, returning
// nil lines when the dist-git checkout has none.
func readDistGitSourcesManifest(env *azldev.Env, dir string) ([]fedorasource.SourcesFileLine, error) {
	path := filepath.Join(dir, "sources")

	exists, err := fileutils.Exists(env.FS(), path)
	if err != nil {
		return nil, fmt.Errorf("checking dist-git 'sources' manifest:\n%w", err)
	}

	if !exists {
		return nil, nil
	}

	content, err := fileutils.ReadFile(env.FS(), path)
	if err != nil {
		return nil, fmt.Errorf("reading dist-git 'sources' manifest:\n%w", err)
	}

	lines, err := fedorasource.ReadSourcesFile(string(content))
	if err != nil {
		return nil, fmt.Errorf("parsing dist-git 'sources' manifest:\n%w", err)
	}

	return lines, nil
}

// restoreOverlaidArchives rebuilds missing archives that archive overlays
// modify. Their committed 'sources' entries carry the post-overlay hash, which
// the lookaside cache does not serve, so the pinned upstream component is
// fetched into a temporary directory and the archive overlays are applied there.
func restoreOverlaidArchives(
	ctx opctx.Ctx, manager sourceproviders.SourceManager, component components.Component,
	dir string, lines []fedorasource.SourcesFileLine,
) (err error) {
	config := component.GetConfig()
	targets := make(map[string]bool)

	for _, ref := range config.SourceFiles {
		if ref.Origin.Type != projectconfig.OriginTypeOverlay || !slices.ContainsFunc(lines,
			func(line fedorasource.SourcesFileLine) bool {
				return line.Entry != nil && line.Entry.Filename == ref.Filename
			}) {
			continue
		}

		exists, err := fileutils.Exists(ctx.FS(), filepath.Join(dir, ref.Filename))
		if err != nil {
			return fmt.Errorf("checking dist-git source %#q:\n%w", ref.Filename, err)
		}

		if !exists {
			targets[ref.Filename] = true
		}
	}

	if len(targets) == 0 {
		return nil
	}

	if ctx.DryRun() {
		slog.Info("Dry run; would rebuild overlaid archives", "component", component.GetName(),
			"archives", len(targets))

		return nil
	}

	overlays := lo.Filter(config.Overlays, func(overlay projectconfig.ComponentOverlay, _ int) bool {
		return overlay.ModifiesArchive() && targets[overlay.Archive]
	})

	tempDir, err := fileutils.MkdirTemp(ctx.FS(), filepath.Dir(dir), "azldev-archive-overlays-")
	if err != nil {
		return fmt.Errorf("creating archive overlay directory:\n%w", err)
	}
	defer fileutils.RemoveAllAndUpdateErrorIfNil(ctx.FS(), tempDir, &err)

	if err := manager.FetchComponent(ctx, component, tempDir, sourceproviders.WithSingleBranch()); err != nil {
		return fmt.Errorf("fetching upstream sources for overlaid archives:\n%w", err)
	}

	if _, err := sources.ApplyArchiveOverlays(ctx, ctx, tempDir, overlays); err != nil {
		return fmt.Errorf("applying archive overlays for component %#q:\n%w", component.GetName(), err)
	}

	for name := range targets {
		if err := fileutils.CopyFile(ctx, ctx.FS(), filepath.Join(tempDir, name), filepath.Join(dir, name),
			fileutils.CopyFileOptions{}); err != nil {
			return fmt.Errorf("copying overlaid archive %#q:\n%w", name, err)
		}
	}

	return nil
}

// validateDistGitSources checks every source payload against the committed
// 'sources' manifest. Payloads fetched from the working configuration may differ
// from the committed dist-git when the component was not rendered and committed
// after a source change.
func validateDistGitSources(
	env *azldev.Env, name, dir string, lines []fedorasource.SourcesFileLine,
) error {
	for _, line := range lines {
		if line.Entry == nil {
			continue
		}

		if err := fileutils.ValidateFileHash(env, env.FS(), line.Entry.HashType,
			filepath.Join(dir, line.Entry.Filename), line.Entry.Hash); err != nil {
			return fmt.Errorf("dist-git source %#q does not match the committed 'sources' manifest; "+
				"render and commit component %#q before building:\n%w", line.Entry.Filename, name, err)
		}
	}

	return nil
}
