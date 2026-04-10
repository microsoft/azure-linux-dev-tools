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

## Semaphore Pattern

Use a buffered channel as a semaphore to limit concurrency:

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
2. Use context-aware semaphore acquisition (select on semaphore and `workerEnv.Done()`)
3. Pass `workerEnv` (not `env`) to worker goroutines so they respect cancellation

Example from `render.go` and `update.go`:

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

- Events are currently not thread-safe; control long-running event handling outside of worker goroutines.
