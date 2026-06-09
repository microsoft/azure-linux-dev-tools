# Tests and Test Groups

The `[test-suites]` section defines named, framework-specific test configurations that components and images can target by name. The `[test-groups]` section bundles those tests under a single name so callers can reference a curated set without enumerating every member.

A `TestRef` is the structured reference used by image and component `tests` subtables. Each ref points at exactly one of a `[test-suites.<name>]` entry or a `[test-groups.<name>]` entry.

> Test references on images and components are metadata and do **not** participate in build fingerprints — adding or removing a reference never invalidates a component's build.

## Test Config

Each entry under `[test-suites.<name>]` describes one configuration of one runner. Framework-specific options live in a typed subtable (`pytest`) whose schema is validated by azldev. `lisa` tests are accepted as a type but have no subtable — they are metadata for external orchestration and are not run by `azldev image test`.

Test names must be simple identifiers (no path separators, traversal segments, or whitespace) since they may be used as path components (for example, each pytest test gets its own Python virtual environment under the project work directory).

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Type | `type` | string | **Yes** | Test framework: `pytest` or `lisa` |
| Description | `description` | string | No | Human-readable description |
| Kind | `kind` | string array | No | Closed enum of classification hints: `functional`, `performance`. May be multi-valued. |
| Long running | `long-running` | bool | No | Hints that this test may run for hours. Declarative cost hint, not a configurable timeout. |
| Pytest | `pytest` | [PytestConfig](#pytest-config) | When `type = "pytest"` | pytest-specific configuration |

The framework subtable must match the declared `type`: `type = "pytest"` requires a `pytest` subtable; `type = "lisa"` forbids it.

> **Note:** Each test name must be unique across all config files. Defining the same test name in two files produces an error.

### Pytest Config

When `type = "pytest"`, a `[test-suites.<name>.pytest]` subtable must be provided. `azldev` runs the test by creating (or reusing) a Python virtual environment, optionally installing dependencies, and invoking `python -m pytest` with the configured arguments.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Working directory | `working-dir` | string | No | Directory used as pytest's CWD. Relative paths are resolved against the config file's directory. Required when `install` is `pyproject` or `requirements`. |
| Test paths | `test-paths` | array of strings | No | Test file paths or directories passed to pytest as positional arguments. Each entry is glob-expanded (including recursive `**`) relative to `working-dir`. Patterns that match nothing are passed through so pytest reports the failure. |
| Extra args | `extra-args` | array of strings | No | Additional arguments passed to pytest verbatim, after placeholder substitution. Use `{image-path}` as a placeholder for the image path. |
| Install mode | `install` | string | No | One of `pyproject`, `requirements`, or `none` (default). |

## Test Group

Each entry under `[test-groups.<name>]` names an ordered list of test references that callers can target as a single unit. Group membership is one layer of indirection: groups list **test names only**, never other groups.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description |
| Tests | `tests` | string array | No | Ordered list of test names (keys in `[test-suites]`) that belong to this group |

Every name in `tests` must resolve to a defined `[test-suites.<name>]` entry; nested group references are not supported.

> **Note:** Each test-group name must be unique across all config files. Defining the same group name in two files produces an error.

## Test Reference

`TestRef` is an inline table used by `[images.<name>.tests.test-suites]` and `[components.<name>.tests.test-suites]`. Exactly one of `name` or `group` must be set:

| Field | TOML Key | Type | Description |
|-------|----------|------|-------------|
| Name | `name` | string | References a `[test-suites.<name>]` entry |
| Group | `group` | string | References a `[test-groups.<name>]` entry |

References must resolve at project load time — an unknown test or group is a config error.

## Example

```toml
[test-suites.bvt-ssh]
type = "pytest"
description = "Basic SSH boot verification"
kind = ["functional"]

[test-suites.bvt-ssh.pytest]
working-dir = "tests/bvt"
test-paths = ["test_ssh.py"]
extra-args = ["--image-path", "{image-path}"]
install = "pyproject"

[test-suites.kdump-smoke]
type = "pytest"
description = "Smoke test for kdump"

[test-suites.kdump-smoke.pytest]
working-dir = "tests/kdump"
test-paths = ["test_kdump.py"]

[test-suites.kdump-lisa]
type = "lisa"
description = "Kdump scenarios driven by external LISA orchestration"
long-running = true

[test-groups.bvt]
description = "Build verification tests"
tests = ["bvt-ssh", "kdump-smoke"]

[images.vm-base.tests]
test-suites = [{ group = "bvt" }]

[components.kernel.tests]
test-suites = [
  { name  = "kdump-smoke" },
  { name  = "kdump-lisa" },
]
```

## Running tests

Use [`azldev image test IMAGE_NAME`](../cli/azldev_image_test.md) to run all `pytest` tests associated with an image (directly or via test-groups). Use `--test <name>` to run a single named test. `lisa` tests are recognized by the schema but are not currently executed by `azldev image test`.

## Related Resources

- [Components](components.md#component-tests) — per-component `test-suites` references
- [Images](images.md#image-tests) — per-image `test-suites` references
- [Config File Structure](config-file.md) — top-level config layout
- [`azldev image test`](../cli/azldev_image_test.md) — CLI reference for running tests against an image
