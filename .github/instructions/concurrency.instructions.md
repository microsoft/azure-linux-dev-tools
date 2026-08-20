---
applyTo: "**/*.go"
description: "Concurrency patterns for parallel task execution. Follow these patterns when writing code that runs tasks concurrently."
---

# Concurrency Patterns

When running tasks in parallel (e.g., per-component operations), follow these patterns:

## Concurrency Limits

Use `env.IOBoundConcurrency()` or `env.CPUBoundConcurrency()` from `azldev.Env` to determine the worker count:

- **I/O-bound** (network clones, file copies): `env.IOBoundConcurrency()` — 2× CPU count
- **CPU-bound** (hashing, parsing): `env.CPUBoundConcurrency()` — 1× CPU count

**Never launch unbounded goroutines.** With 7k+ components, unbounded parallelism will overwhelm the system (loadavg 500+).

## Preferred: `parmap.Map` for slice-parallel work

For the common case — "run a worker over every item in a slice with bounded concurrency" — use [`internal/utils/parmap`](../../internal/utils/parmap/parmap.go) instead of hand-rolling a worker pool. It wraps `golang.org/x/sync/errgroup`, adds per-item results, surfaces "cancelled before start" via `Result.Cancelled`, and serializes the optional progress callback so callers don't need their own mutex.

```go
workerEnv, cancel := env.WithCancel()
defer cancel()

progressEvent := env.StartEvent("Doing the thing", "count", len(items))
defer progressEvent.End()

total := int64(len(items))

results := parmap.Map(
    workerEnv,                          // workerEnv satisfies context.Context
    env.IOBoundConcurrency(),
    items,
    func(done, _ int) { progressEvent.SetProgress(int64(done), total) },
    func(ctx context.Context, item Item) Result {
        // workerEnv (captured) supplies FS, locks, etc.; ctx is the same
        // cancellation signal, useful for ctx-aware APIs like exec.CommandContext.
        return doWork(workerEnv, item)
    },
)

for idx, r := range results {
    if r.Cancelled {
        // worker never ran (ctx ended before parmap reached this item)
        continue
    }
    // consume r.Value
}
```

When to skip `parmap.Map` and use the manual pattern below:

- Streaming results before all items finish (e.g. `render.go`'s historical `resultsChan` pattern — though current render code uses parmap).
- Push-based / event-driven work that isn't a fixed input slice.
- Cases where the worker really does need an unbuffered, hand-tuned channel topology.

## Manual semaphore pattern

When `parmap.Map` doesn't fit, use a buffered channel as a semaphore to limit concurrency:

```go
semaphore := make(chan struct{}, env.IOBoundConcurrency())

go func() {
    select {
    case semaphore <- struct{}{}:
        defer func() { <-semaphore }()
    case <-ctx.Done():
        // handle cancellation
        return
    }
    // do work
}()
```

## Cancellation (Ctrl+C)

Always support graceful cancellation via `env.WithCancel()`:

1. Create a cancellable child env: `workerEnv, cancel := env.WithCancel(); defer cancel()`
2. Pass `workerEnv` (not `env`) to worker goroutines so they respect cancellation
3. With `parmap.Map`, just pass `workerEnv` as the ctx — cancellation handling is built in
4. With the manual pattern, use context-aware semaphore acquisition (select on semaphore and `workerEnv.Done()`)

Example of the manual pattern:

```go
workerEnv, cancel := env.WithCancel()
defer cancel()

semaphore := make(chan struct{}, env.IOBoundConcurrency())

for idx, item := range items {
    waitGroup.Add(1)
    go func() {
        defer waitGroup.Done()

        select {
        case semaphore <- struct{}{}:
            defer func() { <-semaphore }()
        case <-workerEnv.Done():
            results[idx].Error = "context cancelled"
            return
        }

        // Do work using workerEnv (not env)
        doWork(workerEnv, item)
    }()
}

waitGroup.Wait()
```

## Limits

The `Env` type provides a set of methods to determine appropriate concurrency limits for different types of tasks: `IOBoundConcurrency()`, `CPUBoundConcurrency()`, and `FastConcurrency()`. Use these methods to set the size of worker pools or semaphores based on the nature of the tasks being performed.

## Thread Safety

- Events are currently not thread-safe; control long-running event handling outside of worker goroutines. `parmap.Map`'s `onProgress` callback is invoked serialized under an internal mutex, so calling `progressEvent.SetProgress` from there is safe.
