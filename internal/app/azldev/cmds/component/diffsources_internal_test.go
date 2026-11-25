// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/dirdiff"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errDiffObserved = errors.New("diff observed")

type diffCapturingPreparer struct {
	sources.SourcePreparer
	diff *string
}

func (p *diffCapturingPreparer) DiffSources(
	ctx context.Context, component components.Component, baseDir string,
) (*dirdiff.DiffResult, error) {
	result, err := p.SourcePreparer.DiffSources(ctx, component, baseDir)
	if err != nil {
		return nil, err
	}

	*p.diff = result.String()

	return nil, errDiffObserved
}

func TestDiffSources_BuildMacrosMatchRenderMode(t *testing.T) {
	for _, lockless := range []bool{false, true} {
		t.Run(map[bool]string{false: "locked", true: "lockless"}[lockless], func(t *testing.T) {
			testEnv := testutils.NewTestEnv(t)
			if lockless {
				testEnv = testutils.NewTestEnvWithoutLockfile(t)
			}

			addSpecEditorTestComponent(t, testEnv)

			config := testEnv.Config.Components["spec-editor-fixture"]
			config.Build.Defines = map[string]string{"with_feature": "1"}
			testEnv.Config.Components["spec-editor-fixture"] = config

			var diff string

			original := newSourcePreparer
			newSourcePreparer = func(
				manager sourceproviders.SourceManager, fs opctx.FS, events opctx.EventListener,
				dry opctx.DryRunnable, options ...sources.PreparerOption,
			) (sources.SourcePreparer, error) {
				preparer, err := original(manager, fs, events, dry, options...)

				return &diffCapturingPreparer{SourcePreparer: preparer, diff: &diff}, err
			}

			t.Cleanup(func() { newSourcePreparer = original })

			cmd := newComponentCmd(&azldev.App{})
			cmd.SetArgs([]string{"diff-sources", "spec-editor-fixture"})
			require.ErrorIs(t, cmd.ExecuteContext(testEnv.Env), errDiffObserved)

			if lockless {
				assert.Contains(t, diff, "+%define with_feature 1")
				assert.NotContains(t, diff, "Source9999")
			} else {
				assert.Contains(t, diff, "Source9999")
				assert.NotContains(t, diff, "%define with_feature")
			}
		})
	}
}
