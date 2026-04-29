// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// Well-known errors.
var ErrComponentGroupNotFound = errors.New("component group not found")

// Resolver is a utility for resolving components in an environment.
type Resolver struct {
	env *azldev.Env
	// SuppressLockWarnings disables advisory warnings emitted during lock
	// population (e.g., staleness warning when config pin differs from locked
	// commit). Set this for commands that are about to refresh the lock
	// themselves (e.g., 'component update') to avoid noise telling the user
	// to do what they're already doing.
	SuppressLockWarnings bool
}

// NewResolver constructs a new [Resolver] for the given environment.
func NewResolver(env *azldev.Env) *Resolver {
	return &Resolver{
		env: env,
	}
}

// Given a component filter, finds all components defined in the environment that match the filter.
func (r *Resolver) FindComponents(filter *ComponentFilter) (components *ComponentSet, err error) {
	// The filter's SkipLockValidation field is the primary control. When
	// created via AddComponentFilterOptionsToCommand, its default comes from
	// the AZLDEV_ENABLE_LOCK_VALIDATION env var. For programmatic callers
	// (including tests), also check the env var here so the rollout gate
	// applies uniformly.
	//nolint:godox // tracked by TODO(lockfiles) tag.
	// TODO(lockfiles): remove env var gate; filter.SkipLockValidation becomes sole control.
	skipValidation := filter.SkipLockValidation || os.Getenv("AZLDEV_ENABLE_LOCK_VALIDATION") != "1"

	// For usability's sake, detect if the caller/user forgot to specify *any* criteria.
	if filter.HasNoCriteria() {
		slog.Warn("No component selection options were given, no components will be selected.")

		return NewComponentSet(), nil
	}

	// If we were asked to include all components, then it's not even worth looking at anything else.
	if filter.IncludeAllComponents {
		allComps, findErr := r.FindAllComponents()
		if findErr != nil {
			return allComps, findErr
		}

		r.warnOnLockDrift(allComps)

		return allComps, r.validateLockFiles(allComps, true, skipValidation)
	}

	components = NewComponentSet()

	// Find components for named spec paths
	for _, specPath := range filter.SpecPaths {
		err = r.addComponentsBySpecPathToSet(specPath, components)
		if err != nil {
			return components, err
		}
	}

	// Find all components matching the name glob patterns.
	for _, pattern := range filter.ComponentNamePatterns {
		err = r.addComponentsByNamePatternToSet(pattern, components)
		if err != nil {
			return components, err
		}
	}

	// Find all named component groups.
	for _, componentGroupName := range filter.ComponentGroupNames {
		err = r.addComponentsByGroupNameToSet(componentGroupName, components)
		if err != nil {
			return components, err
		}
	}

	r.warnOnLockDrift(components)

	return components, r.validateLockFiles(components, false, skipValidation)
}

// Finds *all* components defined in the environment.
func (r *Resolver) FindAllComponents() (components *ComponentSet, err error) {
	components = NewComponentSet()

	// Enumerate all components in all component groups.
	for groupName := range r.env.Config().ComponentGroups {
		var componentGroup *ComponentGroup

		// Resolve the component group so we can add its contained components.
		// This doesn't actually get the individual components' configuration,
		// but it does sort out which components are in the group.
		componentGroup, err = r.GetComponentGroupByName(groupName)
		if err != nil {
			return components, err
		}

		// Add all components in the group to the map.
		for _, groupMember := range componentGroup.Components {
			var component Component

			// Resolve the config for this group member.
			component, err = r.getComponentFromNameAndSpecPath(groupMember.ComponentName, groupMember.SpecPath)
			if err != nil {
				return components, fmt.Errorf("failed to enumerate components in group '%s':\n%w", groupName, err)
			}

			components.Add(component)
		}
	}

	// Add loose components that aren't part of any group too.
	for name, componentConfig := range r.env.Config().Components {
		// Skip components that are already in the set.
		if components.Contains(name) {
			continue
		}

		var updatedComponentConfig *projectconfig.ComponentConfig

		// Apply defaults from the loaded distro config...
		updatedComponentConfig, err = applyInheritedDefaultsToComponent(r.env, componentConfig)
		if err != nil {
			return components, err
		}

		// ...and add it to the set.
		comp, createErr := r.createComponentFromConfig(updatedComponentConfig)
		if createErr != nil {
			return components, createErr
		}

		components.Add(comp)
	}

	return components, nil
}

// Finds all components in the environment whose names match the given glob pattern.
func (r *Resolver) FindComponentsByNamePattern(pattern string) (components *ComponentSet, err error) {
	var allComponents *ComponentSet

	// We need to find all components first so we can filter.
	allComponents, err = r.FindAllComponents()
	if err != nil {
		return components, err
	}

	components = NewComponentSet()

	// See if there's actually any wild-carding going on.
	if strings.ContainsAny(pattern, "*?[") {
		// Add the components whose names match the pattern.
		for _, name := range allComponents.Names() {
			var matched bool

			matched, err = filepath.Match(pattern, name)
			if err != nil {
				return components, fmt.Errorf("failed to compare component pattern %#q:\n%w", pattern, err)
			}

			if matched {
				component, _ := allComponents.TryGet(name)
				components.Add(component)
			}
		}
	} else {
		// Otherwise, look for the exact match.
		component, ok := allComponents.TryGet(pattern)
		if !ok {
			return components, fmt.Errorf("component not found: %#q", pattern)
		}

		components.Add(component)
	}

	return components, nil
}

// Finds the component with the given name defined in the provided environment. Returns error if it can't be found.
func (r *Resolver) GetComponentByName(name string) (component Component, err error) {
	var allComponents *ComponentSet

	// Find all components first.
	allComponents, err = r.FindAllComponents()
	if err != nil {
		return component, err
	}

	// Lookup the exact name.
	var ok bool
	if component, ok = allComponents.TryGet(name); !ok {
		return component, fmt.Errorf("component not found: %#q", name)
	}

	return component, nil
}

// Looks up the named component group in the provided environment. Returns error if it can't be found.
func (r *Resolver) GetComponentGroupByName(componentGroupName string) (componentGroup *ComponentGroup, err error) {
	var (
		ok                   bool
		componentGroupConfig projectconfig.ComponentGroupConfig
	)

	// Look in our loaded configuration for a group with the given name.
	if componentGroupConfig, ok = r.env.Config().ComponentGroups[componentGroupName]; !ok {
		err = fmt.Errorf("%w: %#q", ErrComponentGroupNotFound, componentGroupName)

		return componentGroup, err
	}

	componentGroup = &ComponentGroup{
		Name: componentGroupName,
	}

	var matches []string

	// The group may contain file glob patterns for components that are backed by specs on
	// disk, and which may *not* otherwise have configuration metadata defining them. Enumerate
	// all such components and add them to the map.
	matches, err = findComponentGroupSpecPaths(r.env, &componentGroupConfig)
	if err != nil {
		return componentGroup, err
	}

	for _, specPath := range matches {
		// N.B. For now, we just extract the name from the path.
		specFilename := path.Base(specPath)
		componentName := strings.TrimSuffix(specFilename, filepath.Ext(specFilename))

		groupEntry := ComponentGroupMember{
			ComponentName: componentName,
			SpecPath:      specPath,
		}

		// N.B. If we ever have a different way of defining group membership but retain
		// the ability to use these spec glob patterns, then we will need to find a way
		// to unify/deduplicate components.
		componentGroup.Components = append(componentGroup.Components, groupEntry)
	}

	return componentGroup, nil
}

// Collects file paths to all .spec files known about in this environment.
func FindAllSpecPaths(env *azldev.Env) ([]string, error) {
	// Go through all component groups, and union together the results of expanding
	// their match patterns.
	var matches []string

	for _, group := range env.Config().ComponentGroups {
		var currentMatches []string

		currentMatches, err := findComponentGroupSpecPaths(env, &group)
		if err != nil {
			return nil, err
		}

		matches = append(matches, currentMatches...)
	}

	return matches, nil
}

func findComponentGroupSpecPaths(
	env *azldev.Env, group *projectconfig.ComponentGroupConfig,
) (matches []string, err error) {
	for _, pattern := range group.SpecPathPatterns {
		var currentMatches []string

		// NOTE: We intentionally do *not* use doublestar.WithFailOnIOErrors() here; it's possible
		// we will hit permissions errors on some paths under the project root (e.g., build
		// directories).
		currentMatches, err = fileutils.Glob(env.FS(), pattern, doublestar.WithFilesOnly())
		if err != nil {
			return matches, fmt.Errorf("failed to expand spec pattern %#q:\n%w", pattern, err)
		}

		for _, match := range currentMatches {
			var excludes bool

			excludes, err = componentGroupExcludesSpec(group, match)
			if err != nil {
				return matches, err
			}

			if excludes {
				continue
			}

			matches = append(matches, match)
		}
	}

	return matches, nil
}

func componentGroupExcludesSpec(
	group *projectconfig.ComponentGroupConfig, specPath string,
) (excludes bool, err error) {
	for _, excludePattern := range group.ExcludedPathPatterns {
		matched, err := doublestar.PathMatch(excludePattern, specPath)
		if err != nil {
			return false, fmt.Errorf(
				"failed to compare %#q against exclude pattern %#q:\n%w", specPath, excludePattern, err)
		}

		if matched {
			return true, nil
		}
	}

	return false, nil
}

func (r *Resolver) addComponentsBySpecPathToSet(specPath string, components *ComponentSet) error {
	var component Component

	// Look up the component configuration for the component backed by the given spec.
	component, err := r.getComponentForSpecPath(specPath)
	if err != nil {
		return err
	}

	// Skip components that are already in the set.
	if !components.Contains(component.GetName()) {
		components.Add(component)
	}

	return nil
}

func (r *Resolver) addComponentsByNamePatternToSet(pattern string, components *ComponentSet) (err error) {
	var matchedComponents *ComponentSet

	// Find all matching components, then add them to the map.
	matchedComponents, err = r.FindComponentsByNamePattern(pattern)
	if err != nil {
		return err
	}

	for _, name := range matchedComponents.Names() {
		// Skip components that are already in the set.
		if components.Contains(name) {
			continue
		}

		component, _ := matchedComponents.TryGet(name)
		components.Add(component)
	}

	return nil
}

func (r *Resolver) addComponentsByGroupNameToSet(groupName string, components *ComponentSet) (err error) {
	var componentGroup *ComponentGroup

	// First resolve the group.
	componentGroup, err = r.GetComponentGroupByName(groupName)
	if err != nil {
		return err
	}

	// Now add all components in the group to the map, looking up their configs as we go.
	for _, groupMember := range componentGroup.Components {
		var component Component

		component, err = r.getComponentFromNameAndSpecPath(groupMember.ComponentName, groupMember.SpecPath)
		if err != nil {
			return fmt.Errorf(
				"failed to enumerate components in group '%s':\n%w", groupName, err)
		}

		// Skip components that are already in the set.
		if components.Contains(component.GetName()) {
			continue
		}

		components.Add(component)
	}

	return nil
}

// Given a path to a .spec file, returns the component's configuration.
func (r *Resolver) getComponentForSpecPath(specPath string) (component Component, err error) {
	name := deduceComponentNameFromSpec(specPath)

	// Make sure it exists.
	if _, statErr := r.env.FS().Stat(specPath); statErr != nil {
		return component, fmt.Errorf("failed to verify spec '%s' exists:\n%w", specPath, statErr)
	}

	return r.getComponentFromNameAndSpecPath(name, specPath)
}

// Given a path to a .spec file, deduce the component's name.
func deduceComponentNameFromSpec(specPath string) string {
	if specPath == "" {
		return ""
	}

	// N.B. For now, we just return the component with the same name as the spec file. We should
	// probably at *least* validate the spec exists and is well-formed.
	specFilename := path.Base(specPath)

	return strings.TrimSuffix(specFilename, filepath.Ext(specFilename))
}

// Finds the named component in the provided environment; returns its configuration. Returns error if it can't be found.
func (r *Resolver) getComponentFromNameAndSpecPath(name, specPath string) (component Component, err error) {
	config := r.env.Config()
	if config == nil {
		return component, errors.New("no project config loaded")
	}

	// See if we can find the component in our loaded config.
	foundComponentConfig, found := config.Components[name]
	if !found {
		// If we didn't find it *and* if we don't have a spec path, then we need to return error.
		if specPath == "" {
			return component, fmt.Errorf("component config not found: %#q", name)
		}

		// Otherwise, we'll synthesize an empty component config.
		foundComponentConfig = projectconfig.ComponentConfig{Name: name}
	}

	var updatedComponentConfig *projectconfig.ComponentConfig

	// Apply inherited defaults to the component.
	updatedComponentConfig, err = applyInheritedDefaultsToComponent(r.env, foundComponentConfig)
	if err != nil {
		return component, err
	}

	// If we have a spec path, then fill it in the component.
	if specPath != "" {
		// ...but make sure it doesn't conflict with whatever the component already has.
		if updatedComponentConfig.Spec.Path != "" && updatedComponentConfig.Spec.Path != specPath {
			return component, fmt.Errorf(
				"component '%s' spec path mismatch: '%s' != '%s'", name, updatedComponentConfig.Spec.Path, specPath,
			)
		}

		updatedComponentConfig.Spec = projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       specPath,
		}
	}

	return r.createComponentFromConfig(updatedComponentConfig)
}

func (r *Resolver) createComponentFromConfig(componentConfig *projectconfig.ComponentConfig) (Component, error) {
	var err error

	componentConfig.RenderedSpecDir, err = RenderedSpecDir(
		r.env.Config().Project.RenderedSpecsDir, componentConfig.Name,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve rendered spec dir for component %#q:\n%w",
			componentConfig.Name, err)
	}

	if componentConfig.Release.Calculation == "" {
		componentConfig.Release.Calculation = projectconfig.ReleaseCalculationAuto
	}

	// Populate locked state onto the component config. This makes lock data
	// available to all downstream consumers (render, build, prepare-sources,
	// diff-sources) without each needing lock-file awareness.
	r.populateFromLock(componentConfig)

	return &resolvedComponent{
		env:    r.env,
		config: *componentConfig,
	}, nil
}

// populateFromLock reads lock file data and attaches it to the component config
// as [projectconfig.ComponentLockData]. This centralizes lock-file consumption so
// all downstream commands (render, build, prepare-sources, diff-sources) get
// locked state automatically via config.Locked.
//
// Works for both upstream and local components. For local components, the lock
// file will have empty UpstreamCommit/ImportCommit fields but a populated
// InputFingerprint.
//
// IMPORTANT: This must NEVER overwrite user-specified config values. Lock data
// goes into the separate Locked field, preserving the manifest/lock boundary:
// Spec.UpstreamCommit = user intent, Locked.UpstreamCommit = resolved reality.
func (r *Resolver) populateFromLock(config *projectconfig.ComponentConfig) {
	reader := r.env.LockReader()
	if reader == nil {
		return
	}

	// Distinguish "not found" from real errors. A missing lock is normal
	// (new component, or lock validation disabled); a corrupt/unreadable
	// lock should be surfaced so it doesn't silently fall back to live
	// upstream resolution.
	exists, existsErr := reader.Exists(config.Name)
	if existsErr != nil {
		slog.Warn("Cannot check lock file", "component", config.Name, "error", existsErr)

		return
	}

	if !exists {
		return
	}

	lock, err := reader.Get(config.Name)
	if err != nil {
		slog.Warn("Lock file exists but is unreadable (corrupt or unsupported version)",
			"component", config.Name, "error", err)

		return
	}

	config.Locked = &projectconfig.ComponentLockData{
		UpstreamCommit:   lock.UpstreamCommit,
		ImportCommit:     lock.ImportCommit,
		ManualBump:       lock.ManualBump,
		InputFingerprint: lock.InputFingerprint,
	}

	slog.Debug("Populated lock data", "component", config.Name, "commit", lock.UpstreamCommit)
}

// warnOnLockDrift emits a staleness warning for each component in the resolved
// set whose explicit config pin (Spec.UpstreamCommit) disagrees with its locked
// commit. This runs against the filtered set so users only see warnings about
// components they asked about, not about all components in the project.
//
// No-op when [Resolver.SuppressLockWarnings] is set (e.g., during component
// update, which is about to refresh the lock).
func (r *Resolver) warnOnLockDrift(resolved *ComponentSet) {
	if r.SuppressLockWarnings {
		return
	}

	for _, comp := range resolved.Components() {
		cfg := comp.GetConfig()
		if cfg.Locked == nil {
			continue
		}

		if cfg.Spec.UpstreamCommit != "" &&
			cfg.Locked.UpstreamCommit != "" &&
			cfg.Spec.UpstreamCommit != cfg.Locked.UpstreamCommit {
			slog.Warn("Lock differs from config pin - run 'component update' to refresh",
				"component", cfg.Name,
				"configPin", cfg.Spec.UpstreamCommit,
				"lockedCommit", cfg.Locked.UpstreamCommit)
		}
	}
}

// Given an explicit component config, apply all inherited defaults.
func applyInheritedDefaultsToComponent(
	env *azldev.Env, component projectconfig.ComponentConfig,
) (result *projectconfig.ComponentConfig, err error) {
	_, distroVer, err := env.Distro()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve current distro:\n%w", err)
	}

	groupNames := env.Config().GroupsByComponent[component.Name]

	resolved, err := projectconfig.ResolveComponentConfig(
		component,
		env.Config().DefaultComponentConfig,
		distroVer.DefaultComponentConfig,
		env.Config().ComponentGroups,
		groupNames,
	)
	if err != nil {
		return nil, fmt.Errorf("resolving config for component '%s':\n%w", component.Name, err)
	}

	return &resolved, nil
}

// validateLockFiles checks lock file consistency against the resolved component
// set. Skipped when skipValidation is true (set per-filter or via the global
// '--skip-lock-validation' flag).
//
// When checkOrphans is true (i.e., all components are being validated), orphan
// lock files are also detected. On filtered commands, only missing/stale checks
// run — orphan detection is a project-wide invariant that would misfire against
// a subset.
func (r *Resolver) validateLockFiles(resolved *ComponentSet, checkOrphans bool, skipValidation bool) error {
	if skipValidation {
		return nil
	}

	reader := r.env.LockReader()
	if reader == nil {
		return nil
	}

	// Build resolved config map from the component set.
	resolvedConfigs := make(map[string]projectconfig.ComponentConfig, resolved.Len())
	for _, comp := range resolved.Components() {
		resolvedConfigs[comp.GetName()] = *comp.GetConfig()
	}

	stale, orphans, err := reader.ValidateConsistency(resolvedConfigs, checkOrphans)
	if err == nil {
		return nil
	}

	// Format fix suggestions at the call site (not in the lockfile package)
	// so CLI-specific strings don't leak into the data layer.
	const maxIssuesForDetailedSuggestion = 10

	if len(orphans) > 0 || len(stale) > maxIssuesForDetailedSuggestion {
		r.env.AddFixSuggestion("run 'azldev component update -a' to fix all lock file issues")
	} else if len(stale) > 0 {
		r.env.AddFixSuggestion(fmt.Sprintf(
			"run 'azldev component update %s'",
			strings.Join(stale, " ")))
	}

	return fmt.Errorf("lock file validation failed:\n%w", err)
}
