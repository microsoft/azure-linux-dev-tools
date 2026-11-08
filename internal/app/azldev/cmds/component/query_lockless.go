// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
)

func splitQueryComponents(
	env *azldev.Env, comps []components.Component,
) (locked, lockless []components.Component) {
	for _, comp := range comps {
		if env.ComponentWithoutLockfile(comp.GetName()) {
			lockless = append(lockless, comp)
		} else {
			locked = append(locked, comp)
		}
	}

	return locked, lockless
}

func validateQueryRenderedDirectory(env *azldev.Env, dir string, required bool) error {
	if !required {
		return nil
	}

	if dir == "" {
		return errors.New("project.rendered-specs-dir is not configured; " +
			"set it in the project config and run 'azldev component render' first")
	}

	exists, err := fileutils.DirExists(env.FS(), dir)
	if err != nil {
		return fmt.Errorf("checking rendered-specs-dir %#q:\n%w", dir, err)
	}

	if !exists {
		return fmt.Errorf("rendered-specs-dir %#q does not exist; run 'azldev component render' first", dir)
	}

	return nil
}

func queryLocklessComponents(
	env *azldev.Env, processor *sources.MockProcessor, comps []components.Component, scratchDir, arch string,
) ([]sources.SpecQueryResult, error) {
	stagingDir := filepath.Join(scratchDir, "dist-git")
	if err := extractQueryDistGitRepos(env, comps, stagingDir); err != nil {
		return nil, err
	}

	processInputs := make([]sources.ComponentInput, 0, len(comps))
	queryInputs := make([]sources.SpecQueryInput, 0, len(comps))

	for _, comp := range comps {
		name := comp.GetName()
		specFilename := name + ".spec"
		processInputs = append(processInputs, sources.ComponentInput{
			Name: name, SpecFilename: specFilename, SkipSpectool: true,
		})
		cfg := comp.GetConfig()
		queryInputs = append(queryInputs, sources.SpecQueryInput{
			Name: name, SpecRelPath: filepath.ToSlash(filepath.Join(name, specFilename)),
			With: cfg.Build.With, Without: cfg.Build.Without, Defines: cfg.Build.Defines,
		})
	}

	processed, err := processor.BatchProcess(env, env, stagingDir, processInputs, env.FS(), env.CPUBoundConcurrency())
	if err != nil {
		return nil, fmt.Errorf("processing lockless dist-git specs:\n%w", err)
	}

	var (
		failures []sources.SpecQueryResult
		ready    []sources.SpecQueryInput
	)

	for idx, result := range processed {
		if result.Error != nil {
			failures = append(failures, sources.SpecQueryResult{Name: result.Name, Error: result.Error})
		} else {
			ready = append(ready, queryInputs[idx])
		}
	}

	results, err := processor.BatchQuerySpecs(env, env, ready, sources.BatchQuerySpecsOptions{
		SpecsDir: stagingDir, ScratchDir: scratchDir, Arch: arch, FS: env.FS(), MaxWorkers: env.CPUBoundConcurrency(),
	})
	if err != nil {
		return nil, fmt.Errorf("querying lockless dist-git specs:\n%w", err)
	}

	return append(results, failures...), nil
}

func extractQueryDistGitRepos(env *azldev.Env, comps []components.Component, stagingDir string) error {
	if env.DryRun() {
		return nil
	}

	repoRoot, err := git.RunInDir(env, env, env.ProjectDir(), "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("finding query project repository:\n%w", err)
	}

	if err := validateDistGitRepoOutput(env, repoRoot, stagingDir); err != nil {
		return err
	}

	if err := fileutils.MkdirAll(env.FS(), stagingDir); err != nil {
		return fmt.Errorf("creating query dist-git directory:\n%w", err)
	}

	source := filepath.Join(filepath.Dir(stagingDir), "project.git")
	if _, err := git.RunInDir(env, env, repoRoot,
		"clone", "--single-branch", "--no-local", "--bare", "--", repoRoot, source); err != nil {
		return fmt.Errorf("cloning query project snapshot:\n%w", err)
	}

	workerEnv, cancel := env.WithCancel()
	defer cancel()

	results := parmap.Map(workerEnv, env.CPUBoundConcurrency(), comps, nil,
		func(ctx context.Context, comp components.Component) error {
			return extractQueryComponent(ctx, workerEnv, repoRoot, source, stagingDir, comp)
		})

	var failures []error

	for idx, result := range results {
		extractErr := result.Value
		if result.Cancelled {
			extractErr = workerEnv.Err()
		}

		if extractErr != nil {
			failures = append(failures, fmt.Errorf("extracting dist-git for %#q:\n%w",
				comps[idx].GetName(), extractErr))
		}
	}

	return errors.Join(failures...)
}

func extractQueryComponent(
	ctx context.Context, env *azldev.Env, repoRoot, source, stagingDir string, comp components.Component,
) error {
	name := comp.GetName()
	if err := fileutils.ValidateFilename(name); err != nil {
		return fmt.Errorf("invalid query component name:\n%w", err)
	}

	subdir, err := distGitRepoSubdirectory(env, repoRoot, name)
	if err != nil {
		return err
	}

	output := filepath.Join(stagingDir, name)
	if _, err := git.RunInDir(ctx, env, repoRoot,
		"clone", "--shared", "--no-checkout", "--single-branch", "--", source, output); err != nil {
		return fmt.Errorf("cloning shared query history:\n%w", err)
	}

	if _, err := git.RunInDir(ctx, env, output,
		"filter-repo", "--force", "--subdirectory-filter", subdir); err != nil {
		return fmt.Errorf("filtering query component history:\n%w", err)
	}

	// Copy only the filtered component's reachable objects before moving it into Mock.
	if _, err := git.RunInDir(ctx, env, output, "repack", "-a", "-d"); err != nil {
		return fmt.Errorf("packing query component history:\n%w", err)
	}

	alternates := filepath.Join(output, ".git", "objects", "info", "alternates")

	exists, err := fileutils.Exists(env.FS(), alternates)
	if err != nil {
		return fmt.Errorf("checking shared query objects:\n%w", err)
	}

	if exists {
		if err := env.FS().Remove(alternates); err != nil {
			return fmt.Errorf("detaching shared query objects:\n%w", err)
		}
	}

	return nil
}
