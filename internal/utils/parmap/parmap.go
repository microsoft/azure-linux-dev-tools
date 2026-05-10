// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package parmap provides a bounded parallel map over a slice of inputs.
//
// It is a thin wrapper around [golang.org/x/sync/errgroup] that adds two
// things our callers want and errgroup doesn't:
//
//  1. Per-item results: callers get a slice of [Result] in input order. The
//     worker function encodes per-item errors inside its returned value
//     (e.g., as a status field). errgroup's first-error aggregation isn't
//     a good fit when every item must report its own outcome (render
//     status, update result, etc.).
//  2. A "didn't start" signal: items that were never invoked because ctx
//     was already done are flagged via [Result.Cancelled], so callers can
//     distinguish "cancelled before start" from "started but bailed out
//     inside worker".
//
// Concurrency is bounded via [errgroup.Group.SetLimit] — the worker
// goroutine for each item only launches when a slot is free, so peak
// goroutine count stays ≤ limit even with millions of items.
package parmap

import (
	"context"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

// Result wraps the output of a single [Map] invocation for one input item.
//
// Cancelled is true when ctx was done before the worker function was
// invoked, so Value is the zero value of Out. Workers that did run and
// observed cancellation inside their body are responsible for surfacing
// that in their returned Value (so callers can distinguish "didn't start"
// from "started but bailed out").
type Result[T any] struct {
	Value     T
	Cancelled bool
}

// Map runs worker for each item in items with at most limit concurrent
// goroutines, respecting ctx cancellation. Results are returned in input order.
//
// Behaviour:
//   - Returns nil for an empty items slice.
//   - limit < 1 is treated as 1.
//   - onProgress, when non-nil, is invoked after each item completes
//     (success or cancelled). Callers must ensure onProgress is safe to
//     call concurrently; it is given (completed, total).
//   - Items that never start because ctx is done set Result.Cancelled = true
//     and never call worker. Items that did start always call worker with
//     ctx; it is the worker function's job to react to ctx cancellation
//     (e.g., by returning early).
//   - Internally uses [errgroup.Group.SetLimit]: worker goroutines are
//     launched lazily, so peak goroutine count is bounded by limit
//     regardless of items length.
func Map[In, Out any](
	ctx context.Context,
	limit int,
	items []In,
	onProgress func(completed, total int),
	worker func(context.Context, In) Out,
) []Result[Out] {
	if len(items) == 0 {
		return nil
	}

	if limit < 1 {
		limit = 1
	}

	results := make([]Result[Out], len(items))
	total := len(items)

	var completed atomic.Int64

	notifyProgress := func() {
		if onProgress == nil {
			return
		}

		done := int(completed.Add(1))
		onProgress(done, total)
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(limit)

	for idx, item := range items {
		// If ctx is already done, don't bother launching — mark every
		// remaining item as Cancelled. group.Go would block waiting for a
		// slot, but [errgroup.Group.SetLimit] does not consult ctx, so we
		// must short-circuit here ourselves.
		if groupCtx.Err() != nil {
			for j := idx; j < len(items); j++ {
				results[j].Cancelled = true

				notifyProgress()
			}

			break
		}

		group.Go(func() error {
			defer notifyProgress()

			// Slot acquired but ctx ended between SetLimit's release and
			// our entry — surface as Cancelled rather than invoking worker
			// against a dead ctx. Returning nil (not ctx.Err()) is
			// intentional: cancellation status lives in Result.Cancelled,
			// not in errgroup's aggregated error.
			if groupCtx.Err() != nil {
				results[idx].Cancelled = true

				return nil
			}

			results[idx].Value = worker(groupCtx, item)

			return nil
		})
	}

	// Wait returns the first non-nil error from a worker; our workers never
	// return errors (per-item outcomes live in Result.Value), so the return
	// is always nil. Wait also ensures all launched goroutines finish.
	_ = group.Wait()

	return results
}
