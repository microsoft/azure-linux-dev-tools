// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package retry

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"
)

const (
	// DefaultMaxAttempts is the default number of total attempts for network operations.
	DefaultMaxAttempts = 3

	// DefaultInitialDelay is the initial delay before the first retry.
	DefaultInitialDelay = 2 * time.Second

	// DefaultMaxDelay is the maximum delay between retry attempts.
	DefaultMaxDelay = 30 * time.Second

	// DefaultBackoffMultiplier is the multiplier applied to the delay on each subsequent retry.
	DefaultBackoffMultiplier = 2.0

	// jitterFraction defines the maximum fraction of jitter applied to the delay (±25%).
	jitterFraction = 0.25
)

// Config controls the behavior of [Do]. A zero-value [Config] will be treated as
// [DefaultConfig].
type Config struct {
	// MaxAttempts is the total number of attempts (including the initial one).
	// Values <= 0 are treated as 1 (no retries).
	MaxAttempts int

	// InitialDelay is the base delay before the first retry.
	InitialDelay time.Duration

	// MaxDelay caps the delay between any two consecutive attempts.
	MaxDelay time.Duration

	// BackoffMultiplier scales the delay on each successive retry.
	BackoffMultiplier float64
}

// DefaultConfig returns a [Config] with sensible defaults for network operations.
func DefaultConfig() Config {
	return Config{
		MaxAttempts:       DefaultMaxAttempts,
		InitialDelay:      DefaultInitialDelay,
		MaxDelay:          DefaultMaxDelay,
		BackoffMultiplier: DefaultBackoffMultiplier,
	}
}

// Disabled returns a [Config] that performs exactly one attempt (no retries).
func Disabled() Config {
	return Config{MaxAttempts: 1}
}

// Do executes the given operation, retrying on error according to cfg.
// It uses exponential backoff with random jitter between attempts and respects
// context cancellation during waits.
//
// If cfg is zero-valued, [DefaultConfig] is used.
func Do(ctx context.Context, cfg Config, operation func() error) error {
	cfg = normalizeConfig(cfg)

	var lastErr error

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}

		// Don't sleep after the final attempt.
		if attempt == cfg.MaxAttempts {
			break
		}

		delay := computeDelay(cfg, attempt)

		slog.Warn("Operation failed, retrying...",
			"attempt", attempt,
			"maxAttempts", cfg.MaxAttempts,
			"nextRetryIn", delay,
			"error", lastErr,
		)

		if err := sleep(ctx, delay); err != nil {
			return fmt.Errorf("retry cancelled while waiting:\n%w", err)
		}
	}

	return fmt.Errorf("operation failed after %d attempt(s):\n%w", cfg.MaxAttempts, lastErr)
}

// normalizeConfig fills in zero-valued fields from [DefaultConfig].
func normalizeConfig(cfg Config) Config {
	defaults := DefaultConfig()

	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaults.MaxAttempts
	}

	if cfg.InitialDelay == 0 {
		cfg.InitialDelay = defaults.InitialDelay
	}

	if cfg.MaxDelay == 0 {
		cfg.MaxDelay = defaults.MaxDelay
	}

	if cfg.BackoffMultiplier == 0 {
		cfg.BackoffMultiplier = defaults.BackoffMultiplier
	}

	return cfg
}

// computeDelay calculates the delay for the given attempt number (1-indexed)
// using exponential backoff with random jitter.
func computeDelay(cfg Config, attempt int) time.Duration {
	// Exponential: initialDelay * multiplier^(attempt-1)
	delay := float64(cfg.InitialDelay) * math.Pow(cfg.BackoffMultiplier, float64(attempt-1))

	// Cap at MaxDelay.
	if delay > float64(cfg.MaxDelay) {
		delay = float64(cfg.MaxDelay)
	}

	// Apply jitter: ±jitterFraction of the computed delay.
	//nolint:gosec // Jitter doesn't need crypto-grade randomness.
	jitter := delay * jitterFraction * (2*rand.Float64() - 1)
	delay += jitter

	// Ensure delay is non-negative.
	if delay < 0 {
		delay = 0
	}

	return time.Duration(delay)
}

// sleep blocks for the specified duration, returning early if the context is cancelled.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		//nolint:wrapcheck // Propagate context error directly.
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
