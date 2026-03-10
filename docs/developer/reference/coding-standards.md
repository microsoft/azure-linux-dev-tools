# Coding Standards

## Go Code Style

- **Follow Go conventions**: Use `mage fix lint` for formatting (fallback: `gofmt`), and standard Go idioms
- **Naming**: Use clear, descriptive names for variables, functions, and types
- **Error handling**: Always handle errors appropriately with context. Use the following pattern:

  ```go
  // ErrOperationFailed is a global error for operation failures
  var ErrOperationFailed = errors.New("operation failed")

  // someOperation returns an error
  func someOperation() error {
      return fmt.Errorf("failed to connect to service: %w", ErrOperationFailed)
  }

  // Example usage in a function
  func performOperation() error {
      err := someOperation()
      // Wrap the error with context
      if err != nil {
          return fmt.Errorf("performing operation: %w", err)
      }
      return nil
  }
  ```

- **Comments**: Document exported functions, types, and complex logic
- **Package organization**: Follow the existing package structure
- **String formatting**: Prefer `%#q` format specifier for strings

## Code Quality Requirements

- **Linting**: All code must pass `golangci-lint` checks (use `mage check lint` and `mage fix lint` to fix some issues automatically)
  - Avoid using `//nolint` comments unless absolutely necessary. If you must use it, specify the linter (e.g., `//nolint:golint`).
- **Testing**: New functionality requires unit tests
- **Coverage**: Maintain reasonable test coverage for critical paths
- **Documentation**: Update relevant documentation for user-facing changes

## File Organization

- **Business logic**: Place in `internal/` packages
- **Command handlers**: Keep minimal logic in `cmd/` packages
- **Unit Tests**: Co-locate with source files using `_test.go` suffix
- **Scenario Tests**: Use `scenario/` for scenario tests
- **Configuration**: Use TOML format, provide defaults in `defaultconfigs/`

## Command Structure

- See [azldev command guidelines](../reference/command-guidelines.md) for details on command structure and naming conventions.

## Logging Guidelines

- Use structured logging with the `slog` package
- Include relevant context in log messages
- Use appropriate log levels (Debug, Info, Warn, Error)
