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
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
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
		locklessSourcePreparerOptions(env, distro, options)...)
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
	env *azldev.Env, distro sourceproviders.ResolvedDistro, options *PrepareSourcesOptions,
) []sources.PreparerOption {
	var (
		history lastRender
		prepare bool
	)

	preparerOpts := append(gitRepoPreparerOptionsForMode(env, distro, true),
		sources.WithSpecEditor(options.specEditorMode()),
		sources.WithUpstreamProvenance(sources.FedoraDistTag(distro.Ref.Name, distro.Version.ReleaseVer)),
		sources.WithoutSyntheticHistory(),
		sources.WithSkipLocalSpecConfig(),
		sources.WithAutoreleaseResolver(hostRenderProcessor{env: env}),
		sources.WithFetchedSources(func(_ context.Context, fetched components.Component, dir string) error {
			var err error
			//nolint:contextcheck // The callback context is the captured preparation env.
			history, prepare, err = prepareFetchedLocklessSources(env, fetched, dir, options.specEditorMode())

			return err
		}),
		sources.WithOverlaidSources(func(_ context.Context, overlaid components.Component, dir string) error {
			if !prepare {
				return nil
			}

			//nolint:contextcheck // The callback context is the captured preparation env.
			return prepareSplitLocklessSpec(env, overlaid, dir, options.specEditorMode(), history)
		}),
	)
	if options.SkipSources {
		preparerOpts = append(preparerOpts, sources.WithSkipLookaside())
	}

	if options.AllowNoHashes {
		preparerOpts = append(preparerOpts, sources.WithAllowNoHashes())
	}

	return preparerOpts
}

func prepareFetchedLocklessSources(
	env *azldev.Env, component components.Component, dir string, editor spec.EditorMode,
) (history lastRender, prepare bool, err error) {
	repo, root, err := openRenderRepo(env)
	if err != nil {
		return history, false, err
	}

	path, err := distGitRepoSubdirectory(env, root, component.GetName())
	if err != nil {
		return history, false, fmt.Errorf("locating committed render history:\n%w", err)
	}

	if component.GetConfig().Spec.SourceType == projectconfig.SpecSourceTypeLocal {
		return history, false, nil
	}

	history, err = readLastRender(env, repo, root, path, component.GetName())
	if err != nil {
		return history, false, fmt.Errorf("reading committed render state:\n%w", err)
	}

	_, opened, err := openLocklessSpec(env, dir, component.GetName(), editor)
	if err != nil {
		return history, false, err
	}

	version, err := opened.GetLastTag("", "Version")
	if err != nil {
		return history, false, fmt.Errorf("reading upstream Version:\n%w", err)
	}

	update := &locklessRenderUpdate{repoRoot: root, path: path}
	if err := collectRenderUpstreamChanges(env, dir, component.GetName(), version, history, update); err != nil {
		return history, false, err
	}

	return history, true, nil
}

// prepareSplitLocklessSpec prepares the changelog and Release like render, then
// records the overlay and release/changelog edits to tracked files as a new
// commit on top of the upstream HEAD.
func prepareSplitLocklessSpec(
	env *azldev.Env, comp components.Component, dir string, editor spec.EditorMode, history lastRender,
) error {
	if err := prepareOverlaidLocklessSpec(env, comp, dir, editor, history); err != nil {
		return err
	}

	if env.DryRun() {
		return nil
	}

	return commitPreparedUpstream(env, dir)
}

// commitPreparedUpstream commits changes to tracked files as a new azldev commit.
func commitPreparedUpstream(env *azldev.Env, dir string) error {
	status, err := git.RunInDir(env, env, dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return fmt.Errorf("checking prepared changes:\n%w", err)
	}

	if status == "" {
		return nil
	}

	if _, err := git.RunInDir(env, env, dir,
		"-c", "user.name=azldev", "-c", "user.email=azldev@localhost", "-c", "core.hooksPath=/dev/null",
		"commit", "--all", "--no-verify", "--no-gpg-sign", "-m", "azldev prepare-sources",
	); err != nil {
		return fmt.Errorf("committing prepared changes:\n%w", err)
	}

	return nil
}
