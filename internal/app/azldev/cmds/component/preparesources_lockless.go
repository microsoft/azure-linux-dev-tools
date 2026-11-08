// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// prepareLocklessSources prepares render-style sources directly in the requested output directory.
func prepareLocklessSources(
	env *azldev.Env, component components.Component, options *PrepareSourcesOptions,
) error {
	if err := validateOutputDir(options.OutputDir); err != nil {
		return err
	}

	if err := fileutils.ValidateFilename(component.GetName()); err != nil {
		return fmt.Errorf("invalid component name for source preparation:\n%w", err)
	}

	event := env.StartEvent("Preparing lockless sources", "component", component.GetName(), "outputDir", options.OutputDir)
	defer event.End()

	distro, err := sourceproviders.ResolveDistro(env, component)
	if err != nil {
		return fmt.Errorf("resolving distro for %#q:\n%w", component.GetName(), err)
	}

	sourceManager, err := sourceproviders.NewSourceManager(env, distro)
	if err != nil {
		return fmt.Errorf("creating source manager for %#q:\n%w", component.GetName(), err)
	}

	if env.DryRun() {
		// [CheckOutputDir] only mutates the filesystem when '--force' is set.
		if !options.Force {
			if err := CheckOutputDir(env, options); err != nil {
				return err
			}
		}

		slog.Info("Dry run; would prepare lockless sources",
			"component", component.GetName(), "outputDir", options.OutputDir)

		return nil
	}

	if err := CheckOutputDir(env, options); err != nil {
		return err
	}

	preparer, err := newSourcePreparer(sourceManager, env.FS(), env, env,
		locklessSourcePreparerOptions(env, component, distro, options)...)
	if err != nil {
		return fmt.Errorf("creating source preparer for %#q:\n%w", component.GetName(), err)
	}

	if err := preparer.PrepareSources(env, component, options.OutputDir, !options.SkipOverlays); err != nil {
		return fmt.Errorf("preparing sources for %#q:\n%w", component.GetName(), err)
	}

	if _, err := findSpecFile(env.FS(), options.OutputDir, component.GetName()); err != nil {
		return fmt.Errorf("finding prepared spec:\n%w", err)
	}

	if options.WithoutGitRepo {
		if err := env.FS().RemoveAll(filepath.Join(options.OutputDir, ".git")); err != nil {
			return fmt.Errorf("removing prepared Git metadata:\n%w", err)
		}
	}

	return nil
}

func locklessSourcePreparerOptions(
	env *azldev.Env, component components.Component, distro sourceproviders.ResolvedDistro, options *PrepareSourcesOptions,
) []sources.PreparerOption {
	preparerOpts := append(gitRepoPreparerOptionsForMode(env, distro, true),
		sources.WithSpecEditor(options.specEditorMode()),
		sources.WithUpstreamProvenance(sources.FedoraDistTag(distro.Ref.Name, distro.Version.ReleaseVer)),
		sources.WithoutSyntheticHistory(),
		sources.WithSkipLocalSpecConfig(),
		sources.WithAutoreleaseResolver(hostRenderProcessor{env: env}),
		sources.WithFetchedSources(func(_ context.Context, fetched components.Component, dir string) error {
			//nolint:contextcheck // The callback context is the captured preparation env.
			return prepareFetchedLocklessSources(env, fetched, dir, options.specEditorMode())
		}),
	)
	if options.SkipSources {
		preparerOpts = append(preparerOpts, sources.WithSkipLookaside())
	}

	if options.AllowNoHashes {
		preparerOpts = append(preparerOpts, sources.WithAllowNoHashes())
	}

	if options.RPMDevBumpspec && component.GetConfig().Spec.SourceType != projectconfig.SpecSourceTypeLocal {
		preparerOpts = append(preparerOpts, sources.WithRPMDevBumpspec(env, env.WorkDir(), ""))
	}

	return preparerOpts
}

func prepareFetchedLocklessSources(
	env *azldev.Env, component components.Component, dir string, editor spec.EditorMode,
) error {
	repo, root, err := openRenderRepo(env)
	if err != nil {
		return err
	}

	path, err := distGitRepoSubdirectory(env, root, component.GetName())
	if err != nil {
		return fmt.Errorf("locating committed render history:\n%w", err)
	}

	if component.GetConfig().Spec.SourceType == projectconfig.SpecSourceTypeLocal {
		return nil
	}

	history, err := readLastRender(env, repo, root, path, component.GetName(), true)
	if err != nil {
		return fmt.Errorf("reading committed render state:\n%w", err)
	}

	_, opened, err := openLocklessSpec(env, dir, component.GetName(), editor)
	if err != nil {
		return err
	}

	version, err := opened.GetLastTag("", "Version")
	if err != nil {
		return fmt.Errorf("reading upstream Version:\n%w", err)
	}

	update := &locklessRenderUpdate{repoRoot: root, path: path}
	if err := collectRenderUpstreamChanges(env, dir, component.GetName(), version, history, update); err != nil {
		return err
	}

	if err := prepareLocklessSpec(env, hostRenderProcessor{env: env},
		dir, component.GetName(), editor, history); err != nil {
		return fmt.Errorf("preparing changelog and release for %#q:\n%w", component.GetName(), err)
	}

	return nil
}
