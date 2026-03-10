// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package containertest

import (
	"sync"
	"testing"
)

//nolint:gochecknoglobals // The testing framework doesn't have a clean way to pass this around.
var containerCacheOnce sync.Once

// cacheContainerImage ensures that the container image is build only once per test (technically once per package that
// imports this package). Otherwise every parallel test would try to build the container image.
func cacheContainerImage(t *testing.T) {
	t.Helper()

	containerCacheOnce.Do(func() {
		// This is a best-effort attempt to ensure that the container is created only once. If any error occurs during the
		// creation, it will be logged but no further attempt will be made.
		collateral := NewContainerTestCollateral(t)

		// Run a simple echo command to cache the container image
		_, err := runCmdInContainerImpl(t, collateral, []string{"echo", "ready"}, NoTimeout)
		if err != nil {
			t.Logf("Failed to initialize container: %v", err)
		}
	})
}
