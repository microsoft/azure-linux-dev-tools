// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveImageTestsToRun_UsesNewTestsRefs(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Tests = map[string]projectconfig.TestDefinition{
		"static-image-checks": {Type: "pytest", Pytest: map[string]any{"working-dir": "/project/tests"}},
		"functional_core":     {Type: "lisa", Lisa: map[string]any{"criteria": map[string]any{"priority": []any{1}}}},
	}
	testEnv.Config.TestGroups = map[string]projectconfig.TestGroup{
		"vm-base-functional": {Tests: []projectconfig.TestRef{{Name: "functional_core"}}},
	}

	imageCfg := &projectconfig.ImageConfig{
		Tests: &projectconfig.ImageTestsConfig{
			Tests: []projectconfig.TestRef{
				{Name: "static-image-checks"},
				{Group: "vm-base-functional"},
			},
		},
	}

	resolved, err := resolveImageTestsToRun(testEnv.Config, imageCfg, nil)
	require.NoError(t, err)
	require.Len(t, resolved, 2)
	assert.Equal(t, "static-image-checks", resolved[0].Name)
	assert.Equal(t, "functional_core", resolved[1].Name)
}

func TestResolveImageTestsToRun_NoWarnWhenOnlyNewTestsPresent(t *testing.T) {
	var buf bytes.Buffer

	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	t.Cleanup(func() { slog.SetDefault(prev) })

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Tests = map[string]projectconfig.TestDefinition{
		"static-image-checks": {Type: "pytest", Pytest: map[string]any{"working-dir": "/project/tests"}},
	}

	imageCfg := &projectconfig.ImageConfig{
		Name: "vm-base",
		Tests: &projectconfig.ImageTestsConfig{
			Tests: []projectconfig.TestRef{{Name: "static-image-checks"}},
		},
	}

	_, err := resolveImageTestsToRun(testEnv.Config, imageCfg, nil)
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "ignored")
}
