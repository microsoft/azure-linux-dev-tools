# Tests and Test Groups

The `[tests]` and `[test-groups]` sections declare framework-agnostic test
metadata that components and images can target by name. Each test entry
binds a single test (a pytest run, a LISA case, or a TMT plan)
to a named identifier; each group entry bundles tests (and
nested groups) under one name so callers can reference a curated set
without enumerating every member.

## Test Definition

Each entry under `[tests.<name>]` describes one configuration of one
runner. Framework-specific options live in a typed subtable
(`pytest`, `lisa`, `tmt`) whose contents are passed through
to the runner; their internal schemas are intentionally not validated
by azldev so frameworks can evolve independently.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Type | `type` | string | Yes | Test framework: `pytest`, `lisa`, or `tmt` |
| Description | `description` | string | No | Human-readable description |
| Kind | `kind` | string array | No | Free-form hints (e.g. `functional`, `performance`, `bvt`) |
| Long running | `long-running` | boolean | No | Hints that this test may run for hours |
| Required capabilities | `required-capabilities` | string array | No | Capability tokens the image must declare for this test to be applicable |
| Lisa | `lisa` | table | No | LISA-specific configuration (opaque to azldev) |
| Tmt | `tmt` | table | No | TMT-specific configuration (opaque to azldev) |
| Pytest | `pytest` | table | No | pytest-specific configuration (opaque to azldev) |

## Test Group

Each entry under `[test-groups.<name>]` names an ordered list of test or
nested-group references that callers can target as a single unit.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description |
| Tests | `tests` | array of [TestRef](#test-reference) | No | Ordered members of the group |

## Test Reference

`TestRef` is an inline table with exactly one of `name` or `group`:

| Field | TOML Key | Type | Description |
|-------|----------|------|-------------|
| Name | `name` | string | References a `[tests.<name>]` entry |
| Group | `group` | string | References a `[test-groups.<name>]` entry |

## Referencing from Components and Images

Components and images both expose a `tests` subtable that holds a list
of `TestRef`s:

```toml
[components.kernel.tests]
tests = [{ group = "kernel-bvt" }, { name = "kdump-smoke" }]

[images.vm-base.tests]
tests = [{ group = "bvt" }]
```

## Example

```toml
[tests.bvt-ssh]
type        = "pytest"
description = "Basic SSH boot verification"
kind        = ["functional", "bvt"]
required-capabilities = ["ssh"]
pytest = { working-dir = "tests/bvt", test-paths = ["test_ssh.py"] }

[tests.kdump-smoke]
type        = "lisa"
description = "Smoke test for kdump"
lisa        = { case = "kdump.smoke" }

[test-groups.bvt]
description = "Build verification tests"
tests = [
  { name  = "bvt-ssh" },
  { group = "bvt-extras" },
]

[test-groups.bvt-extras]
tests = [{ name = "kdump-smoke" }]
```

## Related Resources

- [Test Suites](test-suites.md) - legacy test suite definitions
- [Components](components.md#component-tests) — per-component `tests` field
- [Images](images.md#image-tests) — per-image `tests` field
- [Config File Structure](config-file.md) — top-level config layout
