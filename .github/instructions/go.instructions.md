---
applyTo: "**/*.go"
description: "Instructions for working on the azldev Go codebase. IMPORTANT: Always read these instructions when reading or editing Go code in this repository."
---

# Guidelines for Copilot

- Assume you are an expert Go developer following good coding practices and design patterns
- Reason and iterate through your changes before you settle on a final solution
- Ignore code in auto-generated files, tests, and documentation

## Documentation Requirements

- Document all changes made in your PR description in a clear and concise manner
- Update existing repository documentation files if the changes affect documented behavior
- Include code comments for complex logic when necessary

## Coding Standards

- **CRITICAL**: Unit tests must NOT write to real filesystem or spawn external processes. See `.github/instructions/testing.instructions.md` for test conventions, mock patterns, and test environment setup.
- Follow common coding principles like:
  - DRY - Don't Repeat Yourself
  - KISS - Keep It Simple, Stupid
  - YAGNI - You Aren't Gonna Need It
  - Open/Closed Principle - Code should be open for extension but closed for modification
  - Single Responsibility Principle - Each function should have one reason to change
  - Separation of Concerns - Keep different concerns in separate modules or packages
  - Encapsulation - Hide implementation details and expose only necessary interfaces
  - Interface Segregation - Prefer small, specific interfaces over large, general-purpose ones
  - Programming for interfaces, not implementations - Use interfaces to define behavior and allow for flexible implementations
- Stick strictly to the existing coding conventions in this repository
- For error handling we like to wrap errors: `return fmt.Errorf("context:\n%w", err)`. Define global errors where it makes sense: `var ErrName = errors.New("...")`
- For error messages with context, add a newline after the context message, before the error format specifier. Examples:
  - `fmt.Errorf("This is an error context with wrapped error:\n%w")`
  - `fmt.Errorf("This is a regular error with context string:\n%v")`
- Follow established Go language practices and conventions:
  - Follow Go naming conventions (e.g., CamelCase for exported names)
  - Write concise, readable code with appropriate comments
  - Use Go idioms and standard library functions where appropriate
- In formatted strings enclose string types in quotes. For that purpose use the `%#q` format verb unless the message already encloses the string in quotes. Examples:
  - `return fmt.Errorf("failed to open %#q:\n%w", filename, err)`
  - `return fmt.Errorf("failed to run command 'go %s':\n%w", strings.Join(args, " "), err)`
- Comments referring to types should encapsulate the type name in square brackets. Example: `// [packagename.MyType] is a custom type`
- Config field names and CLI flags in comments and error messages:
  - In code comments, use square brackets for field names: `[module.StructName.FieldName]`
  - In code comments, use single quotes for flag names: `'--flag-name'`
  - In log messages and error strings, use single quotes: `'field-name'`, `'--flag-name'`
- Use structured logging with slog
- Ensure code passes golangci-lint checks
- Use `github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms` instead of re-defining file permission constants
- Use `github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils` for file operations like `Exists`, `ReadFile`, `WriteFile`, not "github.com/spf13/afero" directly.
  - Avoid directly using os package functions for file operations; use the fileutils package instead.

### External Command Execution

- **NEVER** use `exec.LookPath` to check for external tools. Use `ctx.CommandInSearchPath("toolname")` or `env.CommandInSearchPath("toolname")` instead — these delegate to the underlying command factory and integrate with `testctx` for stubbing in tests (for example via `testEnv.CmdFactory.RegisterCommandInSearchPath(...)`).
- Use `exec.CommandContext(ctx, "toolname", args...)` with the binary name (not an absolute path) after the `CommandInSearchPath` check. PATH resolution happens at exec time.
- Always wrap with `ctx.Command(rawCmd)` or `env.Command(rawCmd)` and use `cmd.RunAndGetOutput(ctx)` or `cmd.Run(ctx)` for consistent event tracking and dry-run support.
- Capture stderr separately via `rawCmd.Stderr = &stderr` (set BEFORE `ctx.Command()` / `env.Command()`) for use in error messages.

### Return values

- For functions with named returns, **ALWAYS** explicitly mention the return values.
  - Good example:
    `go
    func GetUserID(username string) (name string, age int, err error) {
        // GOOD: explicit return values are clear.
        return name, age, nil
    }
    `
  - Bad example:
    `go
    func GetUserInfo(userID string) (name string, age int, err error) {
        // BAD: "naked" return below is not clear.
        return
    }
    `
- Use nameless returns except for the examples mentioned below:
  - When the function returns multiple **NON-ERROR** values, use named return values to improve readability. Example:
    ```go
    func GetUserInfo(userID string) (name string, age int, err error) {
        // ...
        return name, age, nil
    }
    ```
  - When the function needs to overwrite the returned values in a deferred call. Example:
    ```go
    func SafeDivide(a, b int) (result int, err error) {
        defer func() {
            if r := recover(); r != nil {
                err = fmt.Errorf("panic occurred: %v", r)
            }
        }()
        result = a / b
        return result, nil
    }
    ```

## Component Fingerprinting — `fingerprint:"-"` Tags

Structs in `internal/projectconfig/` are hashed by `hashstructure.Hash()` to detect component changes. Fields **included by default** (safe: false positive > false negative).

When adding a new field to a fingerprinted struct, ask: **"Does changing this field change the build output?"**
- **Yes** (build flags, spec source, defines, etc.) → do nothing, included automatically.
- **No** (human docs, scheduling hints, CI policy, metadata, back-references) → add `fingerprint:"-"` to the struct tag and register the exclusion in `expectedExclusions` in `internal/projectconfig/fingerprint_test.go`.

If a parent struct field is already excluded (e.g. `Failure ComponentBuildFailureConfig ... fingerprint:"-"`), do **not** also tag the inner struct's fields — `hashstructure` skips the entire subtree.

Run `mage unit` to verify — the guard test will catch unregistered exclusions or missing tags.

### Cmdline Returns

CLI commands should return meaningful structured results. azldev has output formatting helpers to facilitate this (for example, `RunFunc*` wrappers handle formatting, so callers typically should not call `reflectable.FormatValue` directly).

## Quality Standards

- Make minimal, focused changes to achieve the required functionality
- Write or update tests for new or modified code
- Ensure backward compatibility unless explicitly instructed otherwise
- Organize imports according to Go best practices
- Linting: Prefer fixing issues over `//nolint` comments. Use targeted `//nolint:<linter>` if absolutely required
- Testing: See `.github/instructions/testing.instructions.md` for conventions

## Distro Resolution

Components can override the project-default distro via `Spec.UpstreamDistro`. There are three ways to get distro information, each for a different purpose:

| Need | Call | Returns |
|------|------|---------|
| Project-default distro (release ver, mock config) | `env.Distro()` | `(DistroDefinition, DistroVersionDefinition, error)` |
| Per-component distro (for source providers) | `sourceproviders.ResolveDistro(env, comp)` | `ResolvedDistro` (includes ref, definition, version) |
| Per-component release version only (for fingerprints) | Read `distroVer.ReleaseVer` from the resolved distro | `string` |

**When to use which:**
- **`env.Distro()`** — safe when all components share the same distro (e.g., iterating over results in `saveComponentLocks`). Breaks if components override the distro.
- **`sourceproviders.ResolveDistro(env, comp)`** — use when you need the full distro context for a specific component (snapshot time, dist-git branch, lookaside URI). This is what `resolveOneSourceIdentity` uses to create the source manager.
- **Per-component release version** — when computing fingerprints per-component, resolve the distro per-component to get the correct `ReleaseVer`. Using the project-default release version is wrong when component-level distro overrides exist.
