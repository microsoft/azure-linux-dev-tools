# Ideal Development Workflow

## Before Making Changes

1. **Understand the codebase**: Review existing code patterns and architecture
2. **Run the full test suite**: Execute `mage all` to ensure clean state
3. **Create an issue**: For non-trivial changes, discuss the approach first

## Making Changes

1. **Create a feature branch**: Use descriptive names (e.g., `user/feature/add-config-validation`)
2. **Write tests first**: Consider adding tests for new functionality before implementing
3. **Follow coding standards**: See standards section below
4. **Make incremental commits**: Use clear, descriptive commit messages following conventional commit format. Conventional commit naming will be enforced by the CI pipeline for the final PR title.

## Validating Changes

Before submitting, ensure your changes pass all checks:

```bash
mage all  # Comprehensive validation (will also invoke scenario tests, which will be slow)
```

For faster iteration during development:

```bash
mage build     # Rebuild the tool
mage unit      # Fast unit tests
mage check all # Code quality checks
mage fix all   # Auto-fix issues
```

For scenario tests, use:

```bash
mage scenario  # Run all scenario tests (may take time)
```

To see all available Mage targets, run:

```bash
mage -l
```
