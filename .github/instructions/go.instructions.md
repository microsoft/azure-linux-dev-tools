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

- **CRITICAL**: Unit tests must NOT write to real filesystem or spawn external processes. Use `internal/global/testctx` or `afero.NewMemMapFs` directly for in-memory filesystem
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

### Cmdline Returns

CLI commands should return meaningful structured results. azldev has output formatting helpers to facilitate this (for example, `RunFunc*` wrappers handle formatting, so callers typically should not call `reflectable.FormatValue` directly).

## Quality Standards

- Make minimal, focused changes to achieve the required functionality
- Write or update tests for new or modified code
- Ensure backward compatibility unless explicitly instructed otherwise
- Organize imports according to Go best practices
- Linting: Prefer fixing issues over `//nolint` comments. Use targeted `//nolint:<linter>` if absolutely required
- Testing: Table-driven tests preferred. Use `scenario/internal/cmdtest` helpers

### Component Command Testing

New component subcommands (`internal/app/azldev/cmds/component/`) require:
- **Command wiring test** (`*_test.go`, external `package component_test`): verify `NewXxxCmd()` returns a valid command with correct `Use`, `RunE`, and expected flags/defaults.
- **No-match test**: call `cmd.ExecuteContext(testEnv.Env)` with a nonexistent component to verify error handling.
- **Helper unit tests** (`*_test.go`, same-package `package component`): test unexported helper functions (e.g., `findSpecFile`, `cleanupStaleRenders`) using `afero.NewMemMapFs`; where needed, follow the existing `//nolint:testpackage` pattern used in this repo.
- **Snapshot update**: if the command changes the schema or CLI docs, run `mage scenarioUpdate` to update snapshots.
