// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package docs

import (
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/agentskill"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepoRelativeDir(t *testing.T) {
	tests := []struct {
		name       string
		projectDir string
		absPath    string
		want       string
	}{
		{name: "immediate subdir", projectDir: "/project", absPath: "/project/locks", want: "locks"},
		{name: "nested subdir", projectDir: "/project", absPath: "/project/build/locks", want: "build/locks"},
		{name: "project root maps to empty", projectDir: "/project", absPath: "/project", want: ""},
		{name: "outside tree maps to empty", projectDir: "/project", absPath: "/elsewhere/locks", want: ""},
		{name: "empty project dir", projectDir: "", absPath: "/project/locks", want: ""},
		{name: "empty path", projectDir: "/project", absPath: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, repoRelativeDir(tc.projectDir, tc.absPath))
		})
	}
}

func TestResolveBindingsDegradesToDefaults(t *testing.T) {
	// A nil env (no loaded configuration) yields azldev's built-in defaults so the
	// emitted documentation stays accurate for a default project.
	bindings := resolveBindings(nil)

	assert.Equal(t, projectconfig.DefaultLockDir, bindings.LockDir)
	assert.Equal(t, projectconfig.DefaultRenderedSpecsDir, bindings.RenderedSpecsDir)
	assert.Equal(t, projectconfig.DefaultWorkDir, bindings.WorkDir)
}

func TestResolveBindingsFromConfig(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	// Override the project directories to prove they are resolved (not just defaulted).
	cfg := *testEnv.Config
	cfg.Project.LockDir = "/project/build/locks"
	cfg.Project.RenderedSpecsDir = "/project/build/specs"
	cfg.Project.WorkDir = "/project/build/work"

	options := azldev.NewEnvOptions()
	options.Interfaces = testEnv.TestInterfaces
	options.DryRunnable = testEnv.DryRunnable
	options.EventListener = testEnv.EventListener
	options.ProjectDir = "/project"
	options.Config = &cfg

	env := azldev.NewEnv(t.Context(), options)

	bindings := resolveBindings(env)

	assert.Equal(t, "build/locks", bindings.LockDir)
	assert.Equal(t, "build/specs", bindings.RenderedSpecsDir)
	assert.Equal(t, "build/work", bindings.WorkDir)
}

func TestResolveShowSkill(t *testing.T) {
	// A known skill resolves to itself, with nothing to list.
	name, list, err := resolveShowSkill("azldev")
	require.NoError(t, err)
	assert.Equal(t, "azldev", name)
	assert.Nil(t, list)

	// No skill named, with several registered, returns the names to display.
	name, list, err = resolveShowSkill("")
	require.NoError(t, err)
	assert.Empty(t, name)
	assert.Equal(t, skillNames(), list)
	assert.Greater(t, len(list), 1, "test assumes more than one skill is registered")

	// An unknown skill errors and names the valid choices.
	_, _, err = resolveShowSkill("not-a-real-skill")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown skill")
	assert.Contains(t, err.Error(), "azldev")
}

func TestAgentShowCmdUsesSkillFlag(t *testing.T) {
	cmd := newAgentShowCmd()

	assert.Equal(t, "show", cmd.Use)
	require.NoError(t, cmd.Args(cmd, nil))
	require.Error(t, cmd.Args(cmd, []string{"azldev"}))

	complete, ok := cmd.GetFlagCompletionFunc("skill")
	require.True(t, ok, "--skill must have shell completion")

	choices, directive := complete(cmd, nil, "")
	assert.Equal(t, skillNames(), choices)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

// overlayTypeEnum extracts the overlay type strings from the authoritative
// jsonschema enum tag on projectconfig.ComponentOverlay.Type. This is the same
// source that drives the generated schema, so it stays in lock-step with the code.
func overlayTypeEnum(t *testing.T) []string {
	t.Helper()

	field, ok := reflect.TypeOf(projectconfig.ComponentOverlay{}).FieldByName("Type")
	require.True(t, ok, "ComponentOverlay must have a Type field")

	var types []string

	for _, part := range strings.Split(field.Tag.Get("jsonschema"), ",") {
		if value, found := strings.CutPrefix(part, "enum="); found {
			types = append(types, value)
		}
	}

	require.NotEmpty(t, types, "expected overlay type enum values in the jsonschema tag")

	return types
}

// TestOverlaysSkillCoversAllOverlayTypes is a drift guard: the azldev-overlays skill must
// document every overlay type defined in code. If a new overlay type is added, this fails
// until the skill is updated, preventing the reference from silently going stale.
func TestOverlaysSkillCoversAllOverlayTypes(t *testing.T) {
	doc, err := agentskill.SkillDocument("azldev-overlays", agentskill.Params{})
	require.NoError(t, err)

	for _, overlayType := range overlayTypeEnum(t) {
		assert.Containsf(t, doc, "`"+overlayType+"`",
			"azldev-overlays skill must document overlay type %q", overlayType)
	}
}
