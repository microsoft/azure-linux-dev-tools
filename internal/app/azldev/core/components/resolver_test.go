// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func touchFile(t *testing.T, testEnv *testutils.TestEnv, path string) {
	t.Helper()

	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, filepath.Dir(path)))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, path, []byte{}, fileperms.PrivateFile))
}

// Creates a test spec and returns its expected config.
func setupTestSpec(t *testing.T, testEnv *testutils.TestEnv, path string) projectconfig.ComponentConfig {
	t.Helper()

	touchFile(t, testEnv, path)

	return projectconfig.ComponentConfig{
		Name: strings.TrimSuffix(filepath.Base(path), ".spec"),
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       path,
		},
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		},
	}
}

// Constructs a test component's config, adds it to the test environment, and returns a copy of it.
func addTestComponentToConfig(t *testing.T, env *testutils.TestEnv) projectconfig.ComponentConfig {
	t.Helper()

	component := projectconfig.ComponentConfig{
		Name: "test-component",
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		},
	}

	env.Config.Components[component.Name] = component

	return component
}

func TestFindComponents_EmptyFilter(t *testing.T) {
	env := testutils.NewTestEnv(t)

	components, err := components.NewResolver(env.Env).FindComponents(&components.ComponentFilter{})
	require.NoError(t, err)
	require.Zero(t, components.Len())
}

func TestFindComponents_AllComponents(t *testing.T) {
	// Add a test component and setup the filter to include all components.
	env := testutils.NewTestEnv(t)
	expectedComponent := addTestComponentToConfig(t, env)
	filter := &components.ComponentFilter{IncludeAllComponents: true}

	// Find!
	components, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Len(t, components.Components(), 1)

	actualComponent := components.Components()[0]
	require.Equal(t, &expectedComponent, actualComponent.GetConfig())
}

func TestFindComponents_NonExistentSpecPaths(t *testing.T) {
	env := testutils.NewTestEnv(t)

	filter := &components.ComponentFilter{SpecPaths: []string{"/specs/test-component.spec"}}

	// Find!
	_, err := components.NewResolver(env.Env).FindComponents(filter)
	require.Error(t, err)
}

func TestFindComponents_ExistentSpecPath(t *testing.T) {
	const specPath = "/specs/test-component.spec"

	// Setup a test spec, and set up the filter to match it exactly.
	env := testutils.NewTestEnv(t)
	expectedComponent := setupTestSpec(t, env, specPath)
	filter := &components.ComponentFilter{SpecPaths: []string{specPath}}

	// Find!
	components, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Len(t, components.Components(), 1)

	// Make sure we found the component for the spec.
	actualComponent := components.Components()[0]
	require.Equal(t, &expectedComponent, actualComponent.GetConfig())
}

func TestFindComponents_NonExistentGroup(t *testing.T) {
	// Setup the filter to match a non-existent component group.
	env := testutils.NewTestEnv(t)
	filter := &components.ComponentFilter{ComponentGroupNames: []string{"non-existent-group"}}

	// Find!
	_, err := components.NewResolver(env.Env).FindComponents(filter)
	require.Error(t, err)
}

func TestFindComponents_ExistentEmptyGroup(t *testing.T) {
	const testGroupName = "test-group"

	env := testutils.NewTestEnv(t)
	filter := &components.ComponentFilter{ComponentGroupNames: []string{testGroupName}}

	env.Config.ComponentGroups[testGroupName] = projectconfig.ComponentGroupConfig{}

	// Find!
	components, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Empty(t, components.Components())
}

func TestFindComponents_NonMatchingNamedPattern(t *testing.T) {
	env := testutils.NewTestEnv(t)
	filter := &components.ComponentFilter{
		ComponentNamePatterns: []string{"non-existent-*"},
	}

	components, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Empty(t, components.Components())
}

func TestFindComponents_NonMatchingExactName(t *testing.T) {
	env := testutils.NewTestEnv(t)
	filter := &components.ComponentFilter{
		ComponentNamePatterns: []string{"non-existent"},
	}

	_, err := components.NewResolver(env.Env).FindComponents(filter)
	require.Error(t, err)
}

func TestFindComponents_MatchingNamedPattern(t *testing.T) {
	env := testutils.NewTestEnv(t)
	component := addTestComponentToConfig(t, env)

	filter := &components.ComponentFilter{
		ComponentNamePatterns: []string{component.Name[0:1] + "*"},
	}

	// Find!
	components, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Len(t, components.Components(), 1)

	actualComponent := components.Components()[0]
	assert.Equal(t, &component, actualComponent.GetConfig())
}

func TestFindComponents_FoundGroups(t *testing.T) {
	testGroupNames := []string{"test-group-a", "test-group-b"}
	specPaths := []string{"/specs/t1.spec", "/specs/t2.spec"}

	env := testutils.NewTestEnv(t)
	filter := &components.ComponentFilter{ComponentGroupNames: testGroupNames}

	// Define 2 component groups with intentionally overlapping patterns.
	env.Config.ComponentGroups[testGroupNames[0]] = projectconfig.ComponentGroupConfig{
		SpecPathPatterns: []string{"/specs/*.spec"},
	}
	env.Config.ComponentGroups[testGroupNames[1]] = projectconfig.ComponentGroupConfig{
		SpecPathPatterns: []string{"/specs/*2.spec"},
	}

	// Setup the specs and compose a list of expected components.
	expectedComponentConfigs := []projectconfig.ComponentConfig{}
	for _, specPath := range specPaths {
		expectedComponentConfigs = append(expectedComponentConfigs, setupTestSpec(t, env, specPath))
	}

	// Find!
	components, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)

	actualComponentConfigs := make([]projectconfig.ComponentConfig, 0, components.Len())

	for _, comp := range components.Components() {
		actualComponentConfigs = append(actualComponentConfigs, *comp.GetConfig())
	}

	assert.ElementsMatch(t, expectedComponentConfigs, actualComponentConfigs)
}

func TestFindAllComponents_NoComponents(t *testing.T) {
	env := testutils.NewTestEnv(t)

	components, err := components.NewResolver(env.Env).FindAllComponents()
	require.NoError(t, err)
	require.Zero(t, components.Len())
}

func TestFindAllComponents_SomeComponents(t *testing.T) {
	env := testutils.NewTestEnv(t)

	expectedComponent := addTestComponentToConfig(t, env)

	// Find!
	components, err := components.NewResolver(env.Env).FindAllComponents()
	require.NoError(t, err)
	require.Len(t, components.Components(), 1)

	actualComponent := components.Components()[0]
	assert.Equal(t, &expectedComponent, actualComponent.GetConfig())
}

func TestFindAllComponents_MergesComponentPresentBySpecAndConfig(t *testing.T) {
	const testComponentName = "test-component"

	testSpecPath := filepath.Join("/specs/test", testComponentName+".spec")

	// Add its config.
	env := testutils.NewTestEnv(t)
	env.Config.Components[testComponentName] = projectconfig.ComponentConfig{Name: testComponentName}

	// Add it by group.
	env.Config.ComponentGroups["some-group"] = projectconfig.ComponentGroupConfig{
		SpecPathPatterns: []string{"/specs/**/*.spec"},
	}

	// Make sure the spec is present in the test FS.
	touchFile(t, env, testSpecPath)

	expectedComponent := projectconfig.ComponentConfig{
		Name: testComponentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       testSpecPath,
		},
		Release: projectconfig.ReleaseConfig{
			Calculation: projectconfig.ReleaseCalculationAuto,
		},
	}

	// Find!
	components, err := components.NewResolver(env.Env).FindAllComponents()
	require.NoError(t, err)
	require.Len(t, components.Components(), 1)

	actualComponent := components.Components()[0]
	assert.Equal(t, &expectedComponent, actualComponent.GetConfig())
}

func TestGetComponentByName_NotFound(t *testing.T) {
	env := testutils.NewTestEnv(t)

	_, err := components.NewResolver(env.Env).GetComponentByName("some-component")
	require.Error(t, err)
}

func TestGetComponentByName_Found(t *testing.T) {
	const (
		testComponentName = "test-component"
		testSpecPath      = "test/name.spec"
	)

	env := testutils.NewTestEnv(t)
	env.Config.Components[testComponentName] = projectconfig.ComponentConfig{
		Name: testComponentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       testSpecPath,
		},
	}

	// Simulate the spec file existing.
	err := fileutils.WriteFile(env.FS(), testSpecPath, []byte("test spec content"), fileperms.PublicFile)
	require.NoError(t, err)

	component, err := components.NewResolver(env.Env).GetComponentByName(testComponentName)
	require.NoError(t, err)

	assert.Equal(t, testComponentName, component.GetName())

	foundPath, err := component.GetSpec().GetPath()
	require.NoError(t, err)

	assert.Equal(t, testSpecPath, foundPath)
}

func TestFindComponentsByNamePattern_NotFound(t *testing.T) {
	env := testutils.NewTestEnv(t)

	_, err := components.NewResolver(env.Env).FindComponentsByNamePattern("some-component")
	require.Error(t, err)
}

func TestFindComponentsByNamePattern_NonPatternName(t *testing.T) {
	const (
		testComponentName = "test-component"
		testSpecPath      = "test/name.spec"
	)

	env := testutils.NewTestEnv(t)
	env.Config.Components[testComponentName] = projectconfig.ComponentConfig{
		Name: testComponentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       testSpecPath,
		},
	}

	// Simulate the spec file existing.
	err := fileutils.WriteFile(env.FS(), testSpecPath, []byte("test spec content"), fileperms.PublicFile)
	require.NoError(t, err)

	// Find!
	components, err := components.NewResolver(env.Env).FindComponentsByNamePattern(testComponentName)
	require.NoError(t, err)
	require.Len(t, components.Components(), 1)

	component := components.Components()[0]
	assert.Equal(t, testComponentName, component.GetName())

	foundPath, err := component.GetSpec().GetPath()
	require.NoError(t, err)
	assert.Equal(t, testSpecPath, foundPath)
}

func TestFindComponentsByNamePattern_Wildcard(t *testing.T) {
	env := testutils.NewTestEnv(t)
	env.Config.Components["test-a"] = projectconfig.ComponentConfig{Name: "test-a"}
	env.Config.Components["test-b"] = projectconfig.ComponentConfig{Name: "test-b"}
	env.Config.Components["other"] = projectconfig.ComponentConfig{Name: "other"}

	components, err := components.NewResolver(env.Env).FindComponentsByNamePattern("test-*")
	require.NoError(t, err)

	assert.Len(t, components.Components(), 2)
	assert.ElementsMatch(t, []string{"test-a", "test-b"}, components.Names())
}

func TestGetComponentGroupByName_NotFound(t *testing.T) {
	env := testutils.NewTestEnv(t)

	_, err := components.NewResolver(env.Env).GetComponentGroupByName("non-existent-group")
	require.ErrorIs(t, err, components.ErrComponentGroupNotFound)
}

func TestGetComponentGroupByName_EmptyGroup(t *testing.T) {
	const testGroupName = "test-group"

	env := testutils.NewTestEnv(t)
	env.Config.ComponentGroups[testGroupName] = projectconfig.ComponentGroupConfig{}

	group, err := components.NewResolver(env.Env).GetComponentGroupByName(testGroupName)
	require.NoError(t, err)

	assert.Equal(t, testGroupName, group.Name)
	assert.Empty(t, group.Components)
}

func TestGetComponentGroupByName_GroupWithNoMatchingSpecs(t *testing.T) {
	const testGroupName = "test-group"

	env := testutils.NewTestEnv(t)
	env.Config.ComponentGroups[testGroupName] = projectconfig.ComponentGroupConfig{
		SpecPathPatterns: []string{"/non-existent-path/**/*.spec"},
	}

	group, err := components.NewResolver(env.Env).GetComponentGroupByName(testGroupName)
	require.NoError(t, err)

	assert.Equal(t, testGroupName, group.Name)
	assert.Empty(t, group.Components)
}

func TestGetComponentGroupByName_GroupWithMatchingSpecs(t *testing.T) {
	const testGroupName = "test-group"

	env := testutils.NewTestEnv(t)

	// Set up specs in the test FS.
	touchFile(t, env, "/specs/a/test-a.spec")
	touchFile(t, env, "/specs/b/test-b.spec")
	touchFile(t, env, "/specs/c/sub/test-c.spec")
	touchFile(t, env, "/specs/c/sub/sub/test-d.spec")

	// Set up a spec that we expect to get ignored based on exclusions.
	touchFile(t, env, "/specs/ignored/test.spec")

	// Create a group that will match the specs.
	env.Config.ComponentGroups[testGroupName] = projectconfig.ComponentGroupConfig{
		SpecPathPatterns:     []string{"/specs/**/*.spec"},
		ExcludedPathPatterns: []string{"/specs/ignored/*"},
	}

	// Retrieve the group.
	group, err := components.NewResolver(env.Env).GetComponentGroupByName(testGroupName)
	require.NoError(t, err)

	// Make sure we find what we're expecting to find.
	expectedComponents := []components.ComponentGroupMember{
		{ComponentName: "test-a", SpecPath: "/specs/a/test-a.spec"},
		{ComponentName: "test-b", SpecPath: "/specs/b/test-b.spec"},
		{ComponentName: "test-c", SpecPath: "/specs/c/sub/test-c.spec"},
		{ComponentName: "test-d", SpecPath: "/specs/c/sub/sub/test-d.spec"},
	}

	assert.ElementsMatch(t, expectedComponents, group.Components)
}

func TestApplyInheritedDefaults_GroupDefaults(t *testing.T) {
	// A component belongs to a group that defines build defaults.
	// The group defaults should be layered between distro defaults and the component's own config.
	env := testutils.NewTestEnv(t)

	// Set up a component with its own build config.
	component := projectconfig.ComponentConfig{
		Name: "my-comp",
		Build: projectconfig.ComponentBuildConfig{
			With: []string{"feature-x"},
		},
	}
	env.Config.Components[component.Name] = component

	// Set up a group with default build config.
	env.Config.ComponentGroups["my-group"] = projectconfig.ComponentGroupConfig{
		Components: []string{"my-comp"},
		DefaultComponentConfig: projectconfig.ComponentConfig{
			Build: projectconfig.ComponentBuildConfig{
				Without: []string{"docs"},
			},
		},
	}
	env.Config.GroupsByComponent["my-comp"] = []string{"my-group"}

	filter := &components.ComponentFilter{IncludeAllComponents: true}

	// Find!
	result, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Len(t, result.Components(), 1)

	resolved := result.Components()[0].GetConfig()

	// Should have the component's own With setting.
	assert.Contains(t, resolved.Build.With, "feature-x")
	// Should also have the group's Without setting.
	assert.Contains(t, resolved.Build.Without, "docs")
}

func TestApplyInheritedDefaults_MultipleGroupsDeterministicOrder(t *testing.T) {
	// A component belongs to two groups. Their defaults should be applied in
	// sorted group-name order for deterministic behavior.
	env := testutils.NewTestEnv(t)

	component := projectconfig.ComponentConfig{Name: "my-comp"}
	env.Config.Components[component.Name] = component

	// Group "aaa" adds with=["from-aaa"].
	env.Config.ComponentGroups["aaa"] = projectconfig.ComponentGroupConfig{
		Components: []string{"my-comp"},
		DefaultComponentConfig: projectconfig.ComponentConfig{
			Build: projectconfig.ComponentBuildConfig{
				With: []string{"from-aaa"},
			},
		},
	}

	// Group "zzz" adds with=["from-zzz"].
	env.Config.ComponentGroups["zzz"] = projectconfig.ComponentGroupConfig{
		Components: []string{"my-comp"},
		DefaultComponentConfig: projectconfig.ComponentConfig{
			Build: projectconfig.ComponentBuildConfig{
				With: []string{"from-zzz"},
			},
		},
	}

	env.Config.GroupsByComponent["my-comp"] = []string{"zzz", "aaa"}

	filter := &components.ComponentFilter{IncludeAllComponents: true}

	result, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Len(t, result.Components(), 1)

	resolved := result.Components()[0].GetConfig()

	// Both group defaults should be applied.
	assert.Contains(t, resolved.Build.With, "from-aaa")
	assert.Contains(t, resolved.Build.With, "from-zzz")
}

func TestApplyInheritedDefaults_ComponentOverridesGroupDefaults(t *testing.T) {
	// When a component explicitly sets a field that is also set by its group's
	// defaults, the component's value should take precedence via merging.
	env := testutils.NewTestEnv(t)

	component := projectconfig.ComponentConfig{
		Name: "my-comp",
		Build: projectconfig.ComponentBuildConfig{
			Defines: map[string]string{"key": "comp-value"},
		},
	}
	env.Config.Components[component.Name] = component

	env.Config.ComponentGroups["my-group"] = projectconfig.ComponentGroupConfig{
		Components: []string{"my-comp"},
		DefaultComponentConfig: projectconfig.ComponentConfig{
			Build: projectconfig.ComponentBuildConfig{
				Defines: map[string]string{"key": "group-value", "other": "group-only"},
			},
		},
	}
	env.Config.GroupsByComponent["my-comp"] = []string{"my-group"}

	filter := &components.ComponentFilter{IncludeAllComponents: true}

	result, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Len(t, result.Components(), 1)

	resolved := result.Components()[0].GetConfig()

	// The component's own value should override the group's value.
	assert.Equal(t, "comp-value", resolved.Build.Defines["key"])
	// The group's other defaults should still be present.
	assert.Equal(t, "group-only", resolved.Build.Defines["other"])
}

func TestApplyInheritedDefaults_NoGroupMembership(t *testing.T) {
	// A component that doesn't belong to any group should still resolve correctly
	// (only distro defaults + component config).
	env := testutils.NewTestEnv(t)

	component := projectconfig.ComponentConfig{
		Name: "standalone",
		Build: projectconfig.ComponentBuildConfig{
			With: []string{"my-feature"},
		},
	}
	env.Config.Components[component.Name] = component

	filter := &components.ComponentFilter{IncludeAllComponents: true}

	result, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Len(t, result.Components(), 1)

	resolved := result.Components()[0].GetConfig()
	assert.Contains(t, resolved.Build.With, "my-feature")
}

func TestApplyInheritedDefaults_ProjectDefault(t *testing.T) {
	// The project-level DefaultComponentConfig should be applied as the
	// lowest-priority layer, before distro and group defaults.
	env := testutils.NewTestEnv(t)

	// Set project-level defaults.
	env.Config.DefaultComponentConfig = projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{
			Without: []string{"docs"},
		},
		Publish: projectconfig.ComponentPublishConfig{
			RPMChannel:  "rpms-sdk",
			SRPMChannel: "rpms-sdk-srpm",
		},
	}

	component := projectconfig.ComponentConfig{
		Name: "my-comp",
		Build: projectconfig.ComponentBuildConfig{
			With: []string{"feature-x"},
		},
	}
	env.Config.Components[component.Name] = component

	filter := &components.ComponentFilter{IncludeAllComponents: true}

	result, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Len(t, result.Components(), 1)

	resolved := result.Components()[0].GetConfig()

	// Component's own With should be present.
	assert.Contains(t, resolved.Build.With, "feature-x")
	// Project default Without should be inherited.
	assert.Contains(t, resolved.Build.Without, "docs")
	// Project default publish config should be inherited.
	assert.Equal(t, "rpms-sdk", resolved.Publish.RPMChannel)
	assert.Equal(t, "rpms-sdk-srpm", resolved.Publish.SRPMChannel)
}

func TestApplyInheritedDefaults_ProjectDefaultOverriddenByGroup(t *testing.T) {
	// Group defaults should override project defaults, and the component's own
	// config should override everything.
	env := testutils.NewTestEnv(t)

	env.Config.DefaultComponentConfig = projectconfig.ComponentConfig{
		Publish: projectconfig.ComponentPublishConfig{
			RPMChannel:       "rpms-sdk",
			SRPMChannel:      "rpms-sdk-srpm",
			DebugInfoChannel: "rpms-sdk-debuginfo",
		},
	}

	env.Config.ComponentGroups["base-group"] = projectconfig.ComponentGroupConfig{
		Components: []string{"my-comp"},
		DefaultComponentConfig: projectconfig.ComponentConfig{
			Publish: projectconfig.ComponentPublishConfig{
				RPMChannel:  "rpms-base",
				SRPMChannel: "rpms-base-srpm",
			},
		},
	}
	env.Config.GroupsByComponent["my-comp"] = []string{"base-group"}

	component := projectconfig.ComponentConfig{Name: "my-comp"}
	env.Config.Components[component.Name] = component

	filter := &components.ComponentFilter{IncludeAllComponents: true}

	result, err := components.NewResolver(env.Env).FindComponents(filter)
	require.NoError(t, err)
	require.Len(t, result.Components(), 1)

	resolved := result.Components()[0].GetConfig()

	// Group overrides project default for RPM and SRPM channels.
	assert.Equal(t, "rpms-base", resolved.Publish.RPMChannel)
	assert.Equal(t, "rpms-base-srpm", resolved.Publish.SRPMChannel)
	// Project default is inherited for DebugInfoChannel (not overridden by group).
	assert.Equal(t, "rpms-sdk-debuginfo", resolved.Publish.DebugInfoChannel)
}

func TestFindAllSpecPaths_Nothing(t *testing.T) {
	env := testutils.NewTestEnv(t)

	specPaths, err := components.FindAllSpecPaths(env.Env)
	require.NoError(t, err)
	require.Empty(t, specPaths)
}

func TestFindAllSpecPaths_MultipleSpecs(t *testing.T) {
	env := testutils.NewTestEnv(t)

	// Set up 2 specs in the test FS.
	touchFile(t, env, "/specs/a/test-a.spec")
	touchFile(t, env, "/specs/b/test-b.spec")

	// Create a group that will match the specs.
	env.Config.ComponentGroups["test-group"] = projectconfig.ComponentGroupConfig{
		SpecPathPatterns: []string{"/specs/**/*.spec"},
	}

	specPaths, err := components.FindAllSpecPaths(env.Env)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"/specs/a/test-a.spec", "/specs/b/test-b.spec"}, specPaths)
}
