// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev_test

import (
	"log/slog"
	"sync"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/stretchr/testify/require"
)

func TestAppEventListenerConcurrentEvents(t *testing.T) {
	t.Parallel()

	listener, err := azldev.NewEventListener(slog.New(slog.DiscardHandler), true, false)
	require.NoError(t, err)

	const eventCount = 100

	var waitGroup sync.WaitGroup
	waitGroup.Add(eventCount)

	for range eventCount {
		go func() {
			defer waitGroup.Done()

			listener.StartEvent("worker").End()
		}()
	}

	waitGroup.Wait()
}
