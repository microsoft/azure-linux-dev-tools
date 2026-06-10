// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projecttest_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamicTestProjectAddLockSerializesLock(t *testing.T) {
	t.Parallel()

	project := projecttest.NewDynamicTestProject(
		projecttest.AddLock("curl",
			projecttest.WithLockInputFingerprint("sha256:input"),
			projecttest.WithLockUpstreamCommit("upstream-commit"),
			projecttest.WithLockImportCommit("import-commit"),
			projecttest.WithLockManualBump(2),
			projecttest.WithLockResolutionInputHash("sha256:resolution"),
		),
	)

	projectDir := t.TempDir()
	project.Serialize(t, projectDir)

	componentLock := loadProjectLock(t, projectDir, "curl")
	assert.Equal(t, "sha256:input", componentLock.InputFingerprint)
	assert.Equal(t, "upstream-commit", componentLock.UpstreamCommit)
	assert.Equal(t, "import-commit", componentLock.ImportCommit)
	assert.Equal(t, 2, componentLock.ManualBump)
	assert.Equal(t, "sha256:resolution", componentLock.ResolutionInputHash)
}

func TestWriteLockUpdatesSerializedProject(t *testing.T) {
	t.Parallel()

	project := projecttest.NewDynamicTestProject()
	projectDir := t.TempDir()
	project.Serialize(t, projectDir)

	projecttest.WriteLock(t, projectDir, "curl", projecttest.WithLockInputFingerprint("sha256:v1"))
	componentLock := loadProjectLock(t, projectDir, "curl")
	assert.Equal(t, "sha256:v1", componentLock.InputFingerprint)

	projecttest.WriteLock(t, projectDir, "curl", projecttest.WithLockInputFingerprint("sha256:v2"))
	componentLock = loadProjectLock(t, projectDir, "curl")
	assert.Equal(t, "sha256:v2", componentLock.InputFingerprint)
}

func TestDynamicTestProjectAddLockConflictsWithAddFile(t *testing.T) {
	t.Parallel()

	lockPath := filepath.Join(projectconfig.DefaultLockDir, "curl.lock")

	require.Panics(t, func() {
		projecttest.NewDynamicTestProject(
			projecttest.AddFile(lockPath, "version = 1\n"),
			projecttest.AddLock("curl"),
		)
	})

	require.Panics(t, func() {
		projecttest.NewDynamicTestProject(
			projecttest.AddLock("curl"),
			projecttest.AddFile(lockPath, "version = 1\n"),
		)
	})
}

func TestDynamicTestProjectAddLockRejectsInvalidComponentName(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		projecttest.NewDynamicTestProject(projecttest.AddLock("bad/name"))
	})
}

func loadProjectLock(t *testing.T, projectDir, componentName string) *lockfile.ComponentLock {
	t.Helper()

	lockPath, err := lockfile.LockPath(filepath.Join(projectDir, projectconfig.DefaultLockDir), componentName)
	require.NoError(t, err)

	componentLock, err := lockfile.Load(afero.NewOsFs(), lockPath)
	require.NoError(t, err)

	return componentLock
}
