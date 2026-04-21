# Test Suites

The `[test-suites]` section defines named test suites that can be referenced by images. Each test suite is defined under `[test-suites.<name>]`.

## Test Suite Config

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of the test suite |

Test suites are referenced by images through the [`[images.<name>.tests]`](images.md#image-tests) subtable. Each image can reference one or more test suites by name.

> **Note:** Each test suite name must be unique across all config files. Defining the same test suite name in two files produces an error.

## Examples

### Basic test suite definitions

```toml
[test-suites.smoke]
description = "Smoke tests for basic image validation"

[test-suites.integration]
description = "Integration tests for live VM validation"
```

### Referencing test suites from an image

```toml
[test-suites.smoke]
description = "Smoke tests"

[images.vm-base]
description = "VM Base Image"

[images.vm-base.tests]
test-suites = [{ name = "smoke" }]
```

## Related Resources

- [Images](images.md) — image configuration including test references
- [Config File Structure](config-file.md) — top-level config file layout
