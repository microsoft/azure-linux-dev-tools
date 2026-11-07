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
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
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
	processInputs := make([]sources.ComponentInput, 0, len(comps))
	queryInputs := make([]sources.SpecQueryInput, 0, len(comps))

	for _, comp := range comps {
		name := comp.GetName()
		if _, err := CreateComponentDistGitRepo(env, &DistGitRepoOptions{
			Component: name, OutputDir: filepath.Join(stagingDir, name),
		}); err != nil {
			return nil, fmt.Errorf("extracting dist-git for %#q:\n%w", name, err)
		}

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
