// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projecttest

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

// LockOption mutates a component lock used by scenario test projects.
type LockOption func(*lockfile.ComponentLock)

// NewLock creates a component lock for scenario test fixtures.
func NewLock(options ...LockOption) *lockfile.ComponentLock {
	componentLock := lockfile.New()
	for _, option := range options {
		option(componentLock)
	}

	return componentLock
}

// WithLockInputFingerprint sets the lock's stored input fingerprint.
func WithLockInputFingerprint(inputFingerprint string) LockOption {
	return func(componentLock *lockfile.ComponentLock) {
		componentLock.InputFingerprint = inputFingerprint
	}
}

// WithLockUpstreamCommit sets the lock's upstream commit.
func WithLockUpstreamCommit(upstreamCommit string) LockOption {
	return func(componentLock *lockfile.ComponentLock) {
		componentLock.UpstreamCommit = upstreamCommit
	}
}

// WithLockImportCommit sets the lock's import commit.
func WithLockImportCommit(importCommit string) LockOption {
	return func(componentLock *lockfile.ComponentLock) {
		componentLock.ImportCommit = importCommit
	}
}

// WithLockManualBump sets the lock's manual bump counter.
func WithLockManualBump(manualBump int) LockOption {
	return func(componentLock *lockfile.ComponentLock) {
		componentLock.ManualBump = manualBump
	}
}

// WithLockResolutionInputHash sets the lock's resolution input hash.
func WithLockResolutionInputHash(resolutionInputHash string) LockOption {
	return func(componentLock *lockfile.ComponentLock) {
		componentLock.ResolutionInputHash = resolutionInputHash
	}
}

// WriteLock writes a component lock to a serialized scenario test project.
func WriteLock(t *testing.T, projectDir, componentName string, options ...LockOption) {
	t.Helper()

	writeComponentLock(t, projectDir, componentName, NewLock(options...))
}

func writeComponentLock(t *testing.T, projectDir, componentName string, componentLock *lockfile.ComponentLock) {
	t.Helper()

	store := lockfile.NewStore(afero.NewOsFs(), filepath.Join(projectDir, projectconfig.DefaultLockDir))
	require.NoError(t, store.Save(componentName, componentLock))
}

func lockRelativePath(componentName string) string {
	lockPath, err := lockfile.LockPath(projectconfig.DefaultLockDir, componentName)
	if err != nil {
		panic(err)
	}

	return filepath.Clean(lockPath)
}
