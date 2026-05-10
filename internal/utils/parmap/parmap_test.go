// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package parmap_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/parmap"
)

func TestMap_EmptyReturnsNil(t *testing.T) {
	t.Parallel()

	got := parmap.Map(
		t.Context(),
		4,
		[]int(nil),
		nil,
		func(_ context.Context, n int) int { return n },
	)
	if got != nil {
		t.Fatalf("expected nil result for empty input, got %v", got)
	}
}

func TestMap_PreservesOrder(t *testing.T) {
	t.Parallel()

	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	got := parmap.Map(
		t.Context(),
		3,
		items,
		nil,
		func(_ context.Context, n int) int { return n * 2 },
	)

	if len(got) != len(items) {
		t.Fatalf("expected %d results, got %d", len(items), len(got))
	}

	for idx, item := range items {
		if got[idx].Cancelled {
			t.Errorf("item %d unexpectedly cancelled", idx)
		}

		if got[idx].Value != item*2 {
			t.Errorf("item %d: expected %d, got %d", idx, item*2, got[idx].Value)
		}
	}
}

func TestMap_LimitTreatedAsAtLeastOne(t *testing.T) {
	t.Parallel()

	// limit=0 must still process all items (treated as 1).
	got := parmap.Map(
		t.Context(),
		0,
		[]int{1, 2, 3},
		nil,
		func(_ context.Context, n int) int { return n },
	)

	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}

	for idx, result := range got {
		if result.Cancelled || result.Value != idx+1 {
			t.Errorf("item %d: cancelled=%v value=%d", idx, result.Cancelled, result.Value)
		}
	}
}

func TestMap_RespectsLimit(t *testing.T) {
	t.Parallel()

	const (
		items = 50
		limit = 4
	)

	var (
		inFlight    atomic.Int64
		maxInFlight atomic.Int64
	)

	inputs := make([]int, items)
	for i := range inputs {
		inputs[i] = i
	}

	parmap.Map(
		t.Context(),
		limit,
		inputs,
		nil,
		func(_ context.Context, _ int) int {
			cur := inFlight.Add(1)
			defer inFlight.Add(-1)

			for {
				prev := maxInFlight.Load()
				if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}

			// Brief work to make concurrency observable.
			time.Sleep(time.Millisecond)

			return 0
		},
	)

	peak := maxInFlight.Load()
	if peak > int64(limit) {
		t.Fatalf("observed %d concurrent workers, limit was %d", peak, limit)
	}

	if peak == 0 {
		t.Fatalf("no workers ran")
	}
}

func TestMap_ProgressCallback(t *testing.T) {
	t.Parallel()

	items := []int{10, 20, 30, 40}

	var (
		mutex      sync.Mutex
		progresses []int
		totals     []int
	)

	got := parmap.Map(
		t.Context(),
		2,
		items,
		func(completed, total int) {
			mutex.Lock()
			defer mutex.Unlock()

			progresses = append(progresses, completed)
			totals = append(totals, total)
		},
		func(_ context.Context, n int) int { return n },
	)

	if len(got) != len(items) {
		t.Fatalf("expected %d results, got %d", len(items), len(got))
	}

	mutex.Lock()
	defer mutex.Unlock()

	if len(progresses) != len(items) {
		t.Fatalf("expected %d progress callbacks, got %d", len(items), len(progresses))
	}

	// Final callback must see completed==total.
	seenFinal := false

	for _, p := range progresses {
		if p == len(items) {
			seenFinal = true
		}
	}

	if !seenFinal {
		t.Errorf("no progress callback observed completed==total; got %v", progresses)
	}

	for _, total := range totals {
		if total != len(items) {
			t.Errorf("progress callback got total=%d, want %d", total, len(items))
		}
	}
}

func TestMap_ProgressIsMonotonicAndSerialized(t *testing.T) {
	t.Parallel()

	const (
		items = 200
		limit = 16
	)

	inputs := make([]int, items)
	for i := range inputs {
		inputs[i] = i
	}

	var (
		// Track concurrent entries into the callback. Map serializes
		// onProgress under an internal mutex, so this must never exceed 1.
		inCallback    atomic.Int64
		maxInCallback atomic.Int64
		// observed[k] is the k-th completed value passed to the callback;
		// reading them back in append order verifies monotonicity.
		mutex    sync.Mutex
		observed []int
	)

	parmap.Map(
		t.Context(),
		limit,
		inputs,
		func(completed, _ int) {
			cur := inCallback.Add(1)
			defer inCallback.Add(-1)

			for {
				prev := maxInCallback.Load()
				if cur <= prev || maxInCallback.CompareAndSwap(prev, cur) {
					break
				}
			}

			mutex.Lock()

			observed = append(observed, completed)

			mutex.Unlock()
		},
		func(_ context.Context, n int) int { return n },
	)

	if peak := maxInCallback.Load(); peak > 1 {
		t.Fatalf("onProgress saw %d concurrent invocations; expected serialized", peak)
	}

	mutex.Lock()
	defer mutex.Unlock()

	if len(observed) != items {
		t.Fatalf("expected %d progress callbacks, got %d", items, len(observed))
	}

	for i, val := range observed {
		if val != i+1 {
			t.Fatalf(
				"progress callback %d saw completed=%d, want %d (non-monotonic): %v",
				i, val, i+1, observed)
		}
	}
}

func TestMap_NilProgressIsSafe(t *testing.T) {
	t.Parallel()

	got := parmap.Map(
		t.Context(),
		2,
		[]int{1, 2, 3},
		nil,
		func(_ context.Context, n int) int { return n },
	)

	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
}

func TestMap_CancelledBeforeStart(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already done before Map is called

	var ran atomic.Int64

	got := parmap.Map(
		ctx,
		2,
		[]int{1, 2, 3, 4, 5},
		nil,
		func(_ context.Context, n int) int {
			ran.Add(1)

			return n
		},
	)

	if len(got) != 5 {
		t.Fatalf("expected 5 results, got %d", len(got))
	}

	// With an already-cancelled context, the select on semaphore acquisition
	// may pick either branch (semaphore has spare slots, ctx is done). The
	// strict invariants are:
	//   - every result that ran has Cancelled=false and a valid Value.
	//   - every result that didn't run has Cancelled=true and zero Value.
	for idx, result := range got {
		if result.Cancelled && result.Value != 0 {
			t.Errorf("item %d: cancelled but Value=%d (want zero)", idx, result.Value)
		}

		if !result.Cancelled && result.Value != idx+1 {
			t.Errorf("item %d: ran but Value=%d (want %d)", idx, result.Value, idx+1)
		}
	}
}

func TestMap_CancelMidFlight(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	const items = 100

	inputs := make([]int, items)
	for i := range inputs {
		inputs[i] = i
	}

	// Cancel as soon as the first worker enters fn. With a small limit (2),
	// only a handful of workers will ever acquire a slot; the rest must
	// observe ctx.Done() at the semaphore select and surface Cancelled=true.
	var firstOnce sync.Once

	got := parmap.Map(
		ctx,
		2,
		inputs,
		nil,
		func(workerCtx context.Context, num int) int {
			firstOnce.Do(cancel)

			// Workers that did acquire a slot must return promptly once ctx
			// is done; if any worker hangs here Map.Wait() will block.
			<-workerCtx.Done()

			return num
		},
	)

	if len(got) != items {
		t.Fatalf("expected %d results, got %d", items, len(got))
	}

	var cancelledCount int

	for _, r := range got {
		if r.Cancelled {
			cancelledCount++
		}
	}

	if cancelledCount == 0 {
		t.Fatalf("expected some items to be cancelled before starting; got 0")
	}
}

func TestMap_FnSeesCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	var (
		fnObservedCancel atomic.Bool
		fnReached        = make(chan struct{}, 1)
	)

	go func() {
		<-fnReached
		cancel()
	}()

	parmap.Map(
		ctx,
		1,
		[]int{42},
		nil,
		func(workerCtx context.Context, _ int) int {
			fnReached <- struct{}{}

			<-workerCtx.Done()

			fnObservedCancel.Store(true)

			return 0
		},
	)

	if !fnObservedCancel.Load() {
		t.Fatal("fn did not observe ctx cancellation")
	}
}
