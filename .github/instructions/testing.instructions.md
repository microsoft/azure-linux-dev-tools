---
applyTo: "**/*_test.go"
description: "Testing conventions for the azldev Go codebase. IMPORTANT: Always read these instructions when writing or editing test code."
---

# Go Testing Conventions

## Test Tiers

The repo has a multi-layered testing strategy. Tests are classified by
**side-effect surface**, not scope — a multi-stage workflow is still "unit"
if it uses memfs and mocks.

| Tier | Mage Target | Build Tag | Purpose |
|------|-------------|-----------|---------|
| Unit | `mage unit` | (none) | Fast, isolated, in-memory |
| Scenario | `mage scenario` | `//go:build scenario` | End-to-end CLI with snapshot validation |

### Sub-Tiers Within `mage unit`

| Sub-tier | FS backing | When to use |
|----------|------------|-------------|
| **Pure unit** | none | Single function/type validation |
| **In-memory project** | `afero.MemMapFs` via `testutils.NewTestEnv(t)` | Full workflows with mock commands |
| **In-memory go-git** | `go-git/memory.NewStorage()` + `go-git/memfs` | Git tree/commit operations without real repos |
| **Real-FS** | `t.TempDir()` | Only when kernel/OS semantics can't be replicated in-memory (e.g., `openat2`, real `git` CLI) |

Real-FS tests should be rare and justified. Existing examples:
`rootfs_test.go` (kernel `openat2`), `showfile_test.go` (real git CLI).

### Scenario Tests

Live in `scenario/` with `//go:build scenario` tag. Two execution modes:

- `Locally()` — fast, no isolation, no file mounting. For smoke/help tests.
- `InContainer()` — slow, full Docker isolation. For integration tests needing fs state.

Use `mage scenarioUpdate` to update snapshots (review diffs — don't blindly accept).

## Test Environment

Use `testutils.NewTestEnv(t)` for tests that need an `azldev.Env`. It provides:
- In-memory filesystem (`env.TestFS` / `afero.MemMapFs`)
- Mock command factory (`env.CmdFactory`)
- Project config with test distro ("test-distro" v1.0, ReleaseVer "3.0")
- Lock store backed by memfs (at `/project/locks/`)

## Mock Components

Use the **generated** `MockComponent` from `components_testutils`, NOT hand-rolled mock structs:

```go
import (
    "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
    "go.uber.org/mock/gomock"
)

ctrl := gomock.NewController(t)
comp := components_testutils.NewMockComponent(ctrl)
comp.EXPECT().GetName().AnyTimes().Return("curl")
comp.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
    Name: "curl",
    Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream},
})
```

Helper pattern (from `identityprovider_test.go`):
```go
func newMockCompWithConfig(ctrl *gomock.Controller, name string, config *projectconfig.ComponentConfig) *components_testutils.MockComponent {
    comp := components_testutils.NewMockComponent(ctrl)
    comp.EXPECT().GetName().AnyTimes().Return(name)
    comp.EXPECT().GetConfig().AnyTimes().Return(config)
    return comp
}
```

Similarly for specs: use `specs_testutils.NewMockComponentSpec(ctrl)`.

### NoOp Mock Wrappers

For common dependencies (DryRunnable, EventListener, SourceManager), pre-built
NoOp wrappers exist in `*_test/` packages. These are gomock mocks with
`.AnyTimes()` expectations returning safe defaults:

```go
import "github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"

dryRunnable := opctx_test.NewNoOpMockDryRunnable(ctrl)
eventListener := opctx_test.NewNoOpMockEventListener(ctrl)
```

For interfaces with very few methods (1-2), a hand-rolled stub struct is
acceptable when it's simpler than a gomock mock:

```go
type noOpDownloader struct{}
func (d *noOpDownloader) ExtractSourcesFromRepo(...) error { return nil }
```

Prefer generated gomock mocks for interfaces with 3+ methods or when you need
to verify specific call expectations.

## Lock Files in Tests

Use `env.WriteLock(t, name, lock)` to create lock files on the test filesystem:

```go
lock := lockfile.New()
lock.UpstreamCommit = "abc123"
lock.ManualBump = 1
env.WriteLock(t, "curl", lock)
```

## Mocking External Commands

`CmdFactory.RunHandler` and `RunAndGetOutputHandler` intercept ALL external
commands routed through the command factory (not just git). Use them to stub
any tool (git, mock, rpmbuild, etc.) without spawning real processes:

```go
env.CmdFactory.RegisterCommandInSearchPath("git")
env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
    // Intercepts cmd.Run() — handle clone, checkout, etc.
    return nil
}
env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
    // Intercepts cmd.RunAndGetOutput() — return stdout for rev-parse, query tools, etc.
    return "abc123", nil
}
```

Use `cmd.Args` to distinguish which command/subcommand is being called and
return appropriate responses.

## Test File Naming

- `*_test.go` (external package, e.g., `package component_test`) — tests exported APIs only
- `*_internal_test.go` (same package, e.g., `package component`) — tests unexported functions
  - Requires `//nolint:testpackage` directive
  - Use sparingly — prefer testing through exported APIs when possible

## Test Style

- Table-driven tests preferred for testing multiple input/output combinations
- Use `require` for preconditions that must hold; `assert` for test assertions
- Use `t.Run(name, func(t *testing.T) { ... })` for subtests
- Use `t.Helper()` in test helper functions
- When the input under test is naturally a sequence of lines (a config file, a `sources` file, multi-line CLI output, etc.) and the test asserts something on each line, declare the input as `[]string` and join it into the on-disk form at call time:
  ```go
  content := []string{
      "# header comment",
      "",
      "key = value",
  }
  result, err := Parse(strings.Join(content, "\n") + "\n")
  // ...
  for i, expectedLine := range content {
      assert.Equal(t, expectedLine, result.Lines[i].Raw, "result.Lines[%d].Raw mismatch", i)
  }
  ```
  This keeps the input visually aligned with per-line expectations and avoids copy/pasting the same literals into both the input and the assertions. Reserve a single concatenated string literal for cases where the bytes-on-disk form (escapes, trailing whitespace, missing final newline) is itself the thing under test.

## Component Command Testing

New component subcommands (`internal/app/azldev/cmds/component/`) require:
- **Command wiring test** (`*_test.go`, external `package component_test`): verify
  `NewXxxCmd()` returns a valid command with correct `Use`, `RunE`, and expected flags
- **No-match test**: call `cmd.ExecuteContext(testEnv.Env)` with a nonexistent component
  to verify error handling
- **Snapshot update**: if the command changes CLI help text or schema, run
  `mage docs` (regenerates CLI docs and JSON schema) then `mage scenarioUpdate` (updates snapshots)

## Build System

- `mage unit` (NOT `go test`) — runs code generation (mockgen, stringer) then tests
- `mage check all` — lint, formatting, static analysis
- `mage fix all` — auto-fix formatting/lint issues
- `mage scenario` — end-to-end tests (slow, requires containers)
- `mage scenarioUpdate` — update snapshot baselines (review diffs!)
- `mage generate` — standalone codegen (also runs implicitly with unit/build)
- `mage docs` — build binary + regenerate CLI docs and JSON schema

## Common Pitfalls

- **Forgetting `mage generate`** after editing an interface → stale mocks → confusing failures
- **Hand-rolled mocks** when `MockComponent` / `MockComponentSpec` already exist
- **Bypassing `CmdFactory`** with raw `exec.Command` → test spawns real process
- **Updating snapshots blindly** to make a test pass → masks real bugs
- **Non-deterministic output** in snapshots (timestamps, abs paths) → normalize first
