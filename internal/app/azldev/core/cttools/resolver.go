// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cttools

import (
	"fmt"
	"sort"
)

// ResolveTemplates resolves all koji target templates for every git-source-repo in every distro
// version. It expands build-root references, mock-options references, and applies
// environment-prefix / repo-prefix / parent-prefix to produce [ResolvedKojiTarget] entries.
func ResolveTemplates(config *DistroConfig) error {
	for _, distroName := range sortedKeys(config.Distros) {
		distro := config.Distros[distroName]

		for _, versionName := range sortedKeys(distro.Versions) {
			version := distro.Versions[versionName]

			for _, repoName := range sortedKeys(version.GitSourceRepos) {
				repos := version.GitSourceRepos[repoName]

				for i := range repos {
					repo := &repos[i]

					if err := resolveRepoTargets(config, &version, repo); err != nil {
						return fmt.Errorf("error resolving targets for distro %#q, version %#q, repo %#q:\n%w",
							distroName, versionName, repoName, err)
					}
				}

				version.GitSourceRepos[repoName] = repos
			}

			distro.Versions[versionName] = version
		}

		config.Distros[distroName] = distro
	}

	return nil
}

// FilterEnvironment removes all environments from the config except the specified one.
func FilterEnvironment(config *DistroConfig, envName string) error {
	env, ok := config.Environments[envName]
	if !ok {
		available := sortedKeys(config.Environments)

		return fmt.Errorf("environment %#q not found; available: %v", envName, available)
	}

	config.Environments = map[string]Environment{
		envName: env,
	}

	return nil
}

// resolveRepoTargets resolves all koji targets for a single git-source-repo.
func resolveRepoTargets(config *DistroConfig, version *Version, repo *GitSourceRepo) error {
	templateSetName := repo.KojiTargets

	templateSet, ok := config.KojiTargetsTemplates[templateSetName]
	if !ok {
		return fmt.Errorf("koji-targets-template %#q not found", templateSetName)
	}

	envPrefix := version.EnvironmentPrefix
	repoPrefix := repo.RepoPrefix
	parentPrefix := repo.ParentPrefix

	var resolved []ResolvedKojiTarget

	for _, targetName := range sortedKeys(templateSet) {
		targets := templateSet[targetName]

		for _, tmpl := range targets {
			resolvedTarget, err := resolveOneTarget(
				config, version, envPrefix, repoPrefix, parentPrefix, targetName, &tmpl,
			)
			if err != nil {
				return fmt.Errorf("error resolving target %#q:\n%w", targetName, err)
			}

			resolved = append(resolved, *resolvedTarget)
		}
	}

	sort.Slice(resolved, func(i, j int) bool {
		return resolved[i].Name < resolved[j].Name
	})

	repo.ResolvedKojiTargets = resolved

	return nil
}

// resolveOneTarget resolves a single koji target template into a [ResolvedKojiTarget].
func resolveOneTarget(
	config *DistroConfig,
	version *Version,
	envPrefix, repoPrefix, parentPrefix, targetName string,
	tmpl *KojiTarget,
) (*ResolvedKojiTarget, error) {
	resolvedName := applyPrefix(envPrefix, repoPrefix, targetName)
	resolvedOutputTag := applyPrefix(envPrefix, repoPrefix, tmpl.OutputTag)

	var resolvedParentTag string
	if tmpl.ParentTag != "" {
		resolvedParentTag = applyPrefix(parentPrefix, "", tmpl.ParentTag)
	}

	// Resolve build roots.
	buildRoots, err := resolveBuildRoots(config, tmpl.BuildRoots)
	if err != nil {
		return nil, err
	}

	// Resolve mock options.
	mockOpts, err := resolveMockOptions(config, tmpl.MockOptionsBase)
	if err != nil {
		return nil, err
	}

	// Resolve mock-dist-tag (dereference field name on the version).
	var mockDistTag string
	if tmpl.MockDistTag != "" {
		mockDistTag, err = resolveDistTag(version, tmpl.MockDistTag)
		if err != nil {
			return nil, err
		}
	}

	resolvedTarget := &ResolvedKojiTarget{
		Name:          resolvedName,
		OutputTag:     resolvedOutputTag,
		ParentTag:     resolvedParentTag,
		BuildRoots:    buildRoots,
		MockOptions:   mockOpts,
		MockDistTag:   mockDistTag,
		ExternalRepos: tmpl.ExternalRepos,
	}

	return resolvedTarget, nil
}

// applyPrefix constructs "{envPrefix}-{repoPrefix}-{suffix}" or "{envPrefix}-{suffix}" when
// repoPrefix is empty.
func applyPrefix(envPrefix, repoPrefix, suffix string) string {
	if repoPrefix != "" {
		return envPrefix + "-" + repoPrefix + "-" + suffix
	}

	return envPrefix + "-" + suffix
}

// resolveBuildRoots expands build-root references to their package lists.
func resolveBuildRoots(config *DistroConfig, refs []BuildRootRef) ([]ResolvedBuildRoot, error) {
	resolved := make([]ResolvedBuildRoot, 0, len(refs))

	for _, ref := range refs {
		tmpl, ok := config.BuildRootTemplates[ref.Value]
		if !ok {
			return nil, fmt.Errorf("build-root-template %#q not found", ref.Value)
		}

		resolved = append(resolved, ResolvedBuildRoot{
			Type:     ref.Type,
			Packages: tmpl.Packages,
		})
	}

	return resolved, nil
}

// resolveMockOptions looks up a mock-options template by name and returns its options.
func resolveMockOptions(config *DistroConfig, templateName string) ([]string, error) {
	tmpl, ok := config.MockOptionsTemplates[templateName]
	if !ok {
		return nil, fmt.Errorf("mock-options-template %#q not found", templateName)
	}

	return tmpl.Options, nil
}

// resolveDistTag maps a mock-dist-tag field name (e.g., "rpm-macro-dist") to the corresponding
// value on the [Version].
func resolveDistTag(version *Version, fieldName string) (string, error) {
	switch fieldName {
	case "rpm-macro-dist":
		return version.RPMMacroDist, nil
	case "rpm-macro-dist-bootstrap":
		return version.RPMMacroDistBootstrap, nil
	default:
		return "", fmt.Errorf("unknown mock-dist-tag field %#q", fieldName)
	}
}

// sortedKeys returns the keys of a string-keyed map in sorted order.
func sortedKeys[V any](inputMap map[string]V) []string {
	keys := make([]string, 0, len(inputMap))
	for key := range inputMap {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}
