// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/stretchr/testify/assert"
)

func TestRenderGitRepoPreparerOptions(t *testing.T) {
	t.Run("lock file mode enables synthetic history", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		options := renderGitRepoPreparerOptions(env.Env, sourceproviders.ResolvedDistro{})

		assert.Len(t, options, 2)
	})

	t.Run("lock file free mode preserves only upstream history", func(t *testing.T) {
		env := testutils.NewTestEnvWithoutLockfile(t)

		options := renderGitRepoPreparerOptions(env.Env, sourceproviders.ResolvedDistro{})

		assert.Len(t, options, 1)
	})
}
