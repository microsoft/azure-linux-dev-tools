# Test Suites

The `[test-suites]` section defines named test suites that can be referenced by images. Each test suite is defined under `[test-suites.<name>]`.

Test suite names must be simple identifiers (no path separators, traversal segments, or whitespace) since they are used as path components — for example, each pytest suite gets its own Python virtual environment under the project work directory.

## Test Suite Config

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of the test suite |
| Type | `type` | string | Yes | Test framework to use. Currently only `"pytest"` is supported. |
| Pytest | `pytest` | table | When `type = "pytest"` | Pytest-specific configuration (see below) |

Test suites are referenced by images through the [`[images.<name>.tests]`](images.md#image-tests) subtable. Each image can reference one or more test suites by name.

> **Note:** Each test suite name must be unique across all config files. Defining the same test suite name in two files produces an error.

## Pytest Suite Config

When `type = "pytest"`, a `[test-suites.<name>.pytest]` subtable must be provided. `azldev` runs the suite by creating (or reusing) a Python virtual environment, installing dependencies, and invoking `python -m pytest` with the configured arguments.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Working directory | `working-dir` | string | No | Directory used as pytest's CWD. Relative paths are resolved against the config file's directory. Required when `install` is `pyproject` or `requirements`. |
| Test paths | `test-paths` | array of strings | No | Test file paths or directories passed to pytest as positional arguments. Each entry is glob-expanded (including recursive `**`) relative to `working-dir`. Patterns that match nothing are passed through unchanged so pytest reports the failure. |
| Extra args | `extra-args` | array of strings | No | Additional arguments passed to pytest verbatim, after placeholder substitution. See [Placeholders](#placeholders). |
| Install mode | `install` | string | No | How dependencies are installed into the venv. One of `pyproject` (default), `requirements`, or `none`. |

### Install modes

| Mode | Behavior |
|------|----------|
| `pyproject` (default) | Installs the project at `working-dir` in editable mode (`pip install -e <working-dir>`). Errors if `pyproject.toml` is not present. |
| `requirements` | Installs from `<working-dir>/requirements.txt`. Errors if the file is not present. |
| `none` | Skips dependency installation entirely. Use when the venv has been pre-populated or pytest is otherwise on `PATH`. |

`--junit-xml` output requested via the `azldev image test --junit-xml <path>` CLI flag is appended automatically; you do not need to add it to `extra-args`. Relative `--junit-xml` paths are resolved against the user's current working directory (not the test suite's `working-dir`).

### Placeholders

The following placeholders may appear in `extra-args` and are substituted at run time. They are **not** substituted in `test-paths`.

| Placeholder | Substitution |
|-------------|-------------|
| `{image-path}` | Absolute path to the image artifact under test |
| `{image-name}` | Name of the image being tested |
| `{capabilities}` | Comma-separated list of capability names enabled on the image |

## Examples

### Basic pytest suite

```toml
[test-suites.smoke]
description = "Smoke tests for basic image validation"
type = "pytest"

[test-suites.smoke.pytest]
working-dir = "tests/smoke"
test-paths = ["cases/test_*.py"]
extra-args = ["--image-path", "{image-path}", "--capabilities", "{capabilities}"]
```

### Suite with a `requirements.txt`

```toml
[test-suites.integration]
description = "Integration tests"
type = "pytest"

[test-suites.integration.pytest]
working-dir = "tests/integration"
install = "requirements"
test-paths = ["**/test_*.py"]
extra-args = ["--image-name", "{image-name}"]
```

### Suite with no dependency install

```toml
[test-suites.preinstalled]
type = "pytest"

[test-suites.preinstalled.pytest]
install = "none"
test-paths = ["/opt/preinstalled-tests/test_*.py"]
```

### Referencing test suites from an image

```toml
[test-suites.smoke]
type = "pytest"

[test-suites.smoke.pytest]
working-dir = "tests/smoke"
test-paths = ["cases/"]

[images.vm-base]
description = "VM Base Image"

[images.vm-base.tests]
test-suites = [{ name = "smoke" }]
```

## Related Resources

- [Images](images.md) — image configuration including test references
- [Config File Structure](config-file.md) — top-level config file layout
