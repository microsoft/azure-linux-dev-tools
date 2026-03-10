// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package retry_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := retry.DefaultConfig()
	assert.Equal(t, retry.DefaultMaxAttempts, cfg.MaxAttempts)
	assert.Equal(t, retry.DefaultInitialDelay, cfg.InitialDelay)
	assert.Equal(t, retry.DefaultMaxDelay, cfg.MaxDelay)
	assert.InEpsilon(t, retry.DefaultBackoffMultiplier, cfg.BackoffMultiplier, 0.001)
}

func TestDisabled(t *testing.T) {
	cfg := retry.Disabled()
	assert.Equal(t, 1, cfg.MaxAttempts)
}

func TestDo(t *testing.T) {
	// Use a fast config for tests.
	fastConfig := retry.Config{
		MaxAttempts:       3,
		InitialDelay:      1 * time.Millisecond,
		MaxDelay:          10 * time.Millisecond,
		BackoffMultiplier: 2.0,
	}

	errTransient := errors.New("transient failure")

	t.Run("succeeds on first attempt", func(t *testing.T) {
		var attempts int

		err := retry.Do(context.Background(), fastConfig, func() error {
			attempts++

			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 1, attempts)
	})

	t.Run("succeeds on second attempt", func(t *testing.T) {
		var attempts int

		err := retry.Do(context.Background(), fastConfig, func() error {
			attempts++
			if attempts < 2 {
				return errTransient
			}

			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 2, attempts)
	})

	t.Run("succeeds on last attempt", func(t *testing.T) {
		var attempts int

		err := retry.Do(context.Background(), fastConfig, func() error {
			attempts++
			if attempts < fastConfig.MaxAttempts {
				return errTransient
			}

			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, fastConfig.MaxAttempts, attempts)
	})

	t.Run("fails after all attempts exhausted", func(t *testing.T) {
		var attempts int

		err := retry.Do(context.Background(), fastConfig, func() error {
			attempts++

			return errTransient
		})

		require.Error(t, err)
		require.ErrorIs(t, err, errTransient)
		assert.Contains(t, err.Error(), "operation failed after 3 attempt(s)")
		assert.Equal(t, fastConfig.MaxAttempts, attempts)
	})

	t.Run("disabled config runs once", func(t *testing.T) {
		var attempts int

		err := retry.Do(context.Background(), retry.Disabled(), func() error {
			attempts++

			return errTransient
		})

		require.Error(t, err)
		require.ErrorIs(t, err, errTransient)
		assert.Equal(t, 1, attempts)
	})

	t.Run("zero config uses defaults", func(t *testing.T) {
		// Use a zero config but with a short delay for testing.
		zeroCfg := retry.Config{
			MaxAttempts:  2,
			InitialDelay: 1 * time.Millisecond,
		}

		var attempts int

		err := retry.Do(context.Background(), zeroCfg, func() error {
			attempts++
			if attempts < 2 {
				return errTransient
			}

			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 2, attempts)
	})

	t.Run("context cancellation during backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		slowConfig := retry.Config{
			MaxAttempts:       5,
			InitialDelay:      5 * time.Second,
			MaxDelay:          10 * time.Second,
			BackoffMultiplier: 2.0,
		}

		var attempts atomic.Int32

		done := make(chan error, 1)

		go func() {
			done <- retry.Do(ctx, slowConfig, func() error {
				attempts.Add(1)

				return errTransient
			})
		}()

		// Wait for the first attempt to fail and the retry to start sleeping.
		require.Eventually(t, func() bool {
			return attempts.Load() >= 1
		}, 2*time.Second, 1*time.Millisecond)

		// Cancel the context during the backoff wait.
		cancel()

		err := <-done
		require.Error(t, err)
		assert.Contains(t, err.Error(), "retry cancelled while waiting")
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("wraps last error", func(t *testing.T) {
		specificErr := errors.New("specific network error")

		err := retry.Do(context.Background(), fastConfig, func() error {
			return specificErr
		})

		require.Error(t, err)
		require.ErrorIs(t, err, specificErr)
	})
}
