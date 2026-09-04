# Tests and Test Groups

The `[tests]` and `[test-groups]` sections declare framework-agnostic test
metadata that components and images can target by name. Each test entry
binds a single test (a pytest run, a LISA case, or a TMT plan)
to a named identifier; each group entry bundles tests (and
named references) under one name so callers can reference a curated set
without enumerating every member.

## Test Definition

Each entry under `[tests.<name>]` describes one configuration of one
runner. Framework-specific options live in a typed subtable
(`pytest`, `lisa`, `tmt`). azldev validates the fields it consumes for local
execution; other framework-specific fields are passed through so frameworks
can evolve independently.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Type | `type` | string | Yes | Test framework: `pytest`, `lisa`, or `tmt` |
| Description | `description` | string | No | Human-readable description |
| Kind | `kind` | string | No | Test kind hint: `functional` or `performance` |
| Long running | `long-running` | boolean | No | Hints that this test may run for hours |
| Metrics enabled | `metrics-enabled` | boolean | No | Hints to the test execution environment/validation service whether metrics from this test should be collected and stored |
| Required capabilities | `required-capabilities` | string array | No | Capability tokens the image must declare for this test to be applicable |
| Lisa | `lisa` | table | No | LISA-specific configuration (see [LISA fields](#lisa-fields)) |
| Tmt | `tmt` | table | No | TMT plan configuration (see [TMT fields](#tmt-fields)) |
| Pytest | `pytest` | table | No | pytest-specific configuration (opaque to azldev) |

### LISA Fields

The `[tests.<name>.lisa]` subtable is mostly opaque to azldev, but it
recognizes a few keys used to select LISA test cases and, optionally, to
run the test locally via `azldev image test` (booting the image in a QEMU
VM, same as legacy `[test-suites]` LISA suites).

| Field | TOML Key | Type | Description |
|-------|----------|------|-------------|
| Source | `source` | table (`git-url`, `ref`) | Git source for the LISA framework. Required for local execution; `ref` must be a full 40-character hex commit SHA. Tests without a `source` are metadata-only and must be run through external LISA orchestration. |
| Criteria | `criteria` | table or array of tables | One or more LISA criteria blocks (`name`, `area`, `category`, `priority`, `tags`) used to select test cases. |
| Name | `name` | string | Shorthand for a single criteria with a `name` filter. |
| Testcase name | `testcase-name` | string | Shorthand for a single criteria matching one test case by name. |
| Testcase names | `testcase-names` | string array | Shorthand for a single criteria matching multiple test cases by name (joined as an OR). |
| Pip pre-install | `pip-pre-install` | string array | Pip packages to install before the framework (for overriding version pins); used only for local execution. |
| Pip extras | `pip-extras` | string array | Pip extras to install from the LISA framework package; used only for local execution. |
| Extra args | `extra-args` | string array | Additional arguments passed to LISA. Supports `{image-path}`, `{image-name}`, `{capabilities}` placeholders; used only for local execution. |

At least one of `criteria`, `name`, `testcase-name`, or `testcase-names` is
required.

### TMT Fields

The `[tests.<name>.tmt]` subtable identifies a pinned upstream TMT plan. It is
also used by [`azldev component test`](../cli/azldev_component_test.md) to run
the mapped plan locally in a QEMU VM. Local execution clones the source at the
configured commit, provisions the supplied image with TMT/testcloud, and
installs the RPMs passed through `--rpm` before the plan runs.

| Field | TOML Key | Type | Description |
|-------|----------|------|-------------|
| Source | `source` | table (`git-url`, `ref`) | Git repository containing the plan. Required; `ref` must be a full 40-character hex commit SHA. |
| Plan | `plan` | string | Absolute TMT plan name to run. Required. |

For example, define a plan as follows:

```toml
[tests.example-tmt]
type = "tmt"

[tests.example-tmt.tmt]
source = { git-url = "https://example.test/tests.git", ref = "0123456789012345678901234567890123456789" }
plan = "/plans/example"
```

After associating `example-tmt` with a component, run it locally with
`azldev component test <component> --image-path <image.qcow2> --rpm <package.rpm>`.

## Test Group

Each entry under `[test-groups.<name>]` names an ordered list of test
references that callers can target as a single unit.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description |
| Tests | `tests` | array of [TestRef](#test-reference) | No | Ordered members of the group (name refs only) |

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
kind        = "functional"
required-capabilities = ["ssh"]
pytest = { working-dir = "tests/bvt", test-paths = ["test_ssh.py"] }

[tests.kdump-smoke]
type        = "lisa"
description = "Smoke test for kdump"
lisa        = { testcase-name = "kdump_smoke" }

[tests.kdump-smoke-local]
type        = "lisa"
description = "Smoke test for kdump (runs locally via 'azldev image test')"
lisa.source = { git-url = "https://github.com/microsoft/lisa.git", ref = "0123456789012345678901234567890123456789" }
lisa.testcase-names = ["kdump_smoke"]

[test-groups.bvt]
description = "Build verification tests"
tests = [
  { name  = "bvt-ssh" },
  { name  = "kdump-smoke" },
]
```

## Related Resources

- [Test Suites](test-suites.md) - legacy test suite definitions
- [Components](components.md#component-tests) — per-component `tests` field
- [Images](images.md#image-tests) — per-image `tests` field
- [Config File Structure](config-file.md) — top-level config layout
