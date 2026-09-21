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
| Lisa | `lisa` | table | When `type = "lisa"` | LISA-specific configuration (see [LISA fields](#lisa-fields)) |
| Tmt | `tmt` | table | When `type = "tmt"` | TMT plan configuration (see [TMT fields](#tmt-fields)) |
| Pytest | `pytest` | table | When `type = "pytest"` | pytest-specific configuration (see [Pytest fields](#pytest-fields)) |

Exactly one framework subtable must be present, and it must match `type`; the
other two must be absent (the loader rejects a missing or mismatched subtable).

### LISA Fields

The `[tests.<name>.lisa]` subtable is mostly opaque to azldev, but it
recognizes a few keys used to select LISA test cases and, optionally, to
run the test locally via `azldev image test` (booting the image in a QEMU
VM).

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

### Pytest Fields

The `[tests.<name>.pytest]` subtable configures a pytest run. azldev validates
and consumes these fields: it creates (or reuses) a Python virtual environment,
optionally installs dependencies, and invokes `python -m pytest` with the
configured arguments.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Working directory | `working-dir` | string | Yes | Directory used as pytest's CWD. Must be a non-empty string. Relative paths are resolved against the config file's directory. |
| Test paths | `test-paths` | string array | Yes | Test file paths or directories passed to pytest as positional arguments. Must be a non-empty list of non-empty strings. Each entry is glob-expanded (including recursive `**`) relative to `working-dir`. Patterns that match nothing are passed through unchanged so pytest reports the failure. |
| Extra args | `extra-args` | string array | No | Additional arguments passed to pytest verbatim, after placeholder substitution (see [Placeholders](#placeholders)). |
| Install mode | `install` | string | No | How dependencies are installed into the venv. One of `pyproject`, `requirements`, or `none` (default). |

#### Install modes

| Mode | Behavior |
|------|----------|
| `pyproject` | Installs the project at `working-dir` in editable mode (`pip install -e <working-dir>`). Errors if `pyproject.toml` is not present. |
| `requirements` | Installs from `<working-dir>/requirements.txt`. Errors if the file is not present. |
| `none` (default) | Skips dependency installation entirely. The venv must already contain pytest and its dependencies (pytest is invoked as the venv interpreter's `-m pytest`, not from `PATH`). |

`--junit-xml` output requested via `azldev image test --junit-xml <path>` is
appended automatically; do not add it to `extra-args`. Relative `--junit-xml`
paths are resolved against the user's current working directory (not
`working-dir`).

#### Placeholders

The following placeholders may appear in pytest and LISA `extra-args` and are
substituted at run time. They are **not** substituted in `test-paths`.

| Placeholder | Substitution |
|-------------|--------------|
| `{image-path}` | Absolute path to the image artifact under test |
| `{image-name}` | Name of the image being tested |
| `{capabilities}` | Comma-separated list of capability names enabled on the image |

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

## Migrating from `[test-suites]`

The legacy `[test-suites]` section and the `tests.test-suites` image references
have been removed. Existing configuration must be migrated to the `[tests]` /
`[test-groups]` shape described above. Section renames are mechanical, but the
new `[tests.NAME.pytest]` validation is stricter than legacy `[test-suites]` —
see [Pytest field requirements](#pytest-field-requirements) below.

### Section and reference renames

| Legacy | Replacement |
|--------|-------------|
| `[test-suites.NAME]` | `[tests.NAME]` |
| `[test-suites.NAME.pytest]` | `[tests.NAME.pytest]` |
| `[test-suites.NAME.lisa]` | `[tests.NAME.lisa]` |
| `[images.NAME.tests]` `test-suites = [{ name = "x" }]` | `[images.NAME.tests]` `tests = [{ name = "x" }]` |

### Pytest field requirements

The pytest keys keep the same names, but `[tests.NAME.pytest]` validation is
stricter than legacy `[test-suites]`: `working-dir` and `test-paths` are now
**required** and must be non-empty. Legacy suites that omitted `test-paths`, or
that relied on `install = "none"` without a `working-dir`, must add these fields
when migrating — renaming the section alone is not enough.

| Field | Legacy `[test-suites]` | New `[tests]` |
|-------|------------------------|---------------|
| `working-dir` | Optional (required only for `install = "pyproject"`/`"requirements"`) | **Required** (non-empty string) |
| `test-paths` | Optional | **Required** (non-empty list of non-empty strings) |
| `extra-args`, `install` | Optional | Optional (unchanged) |

### LISA field changes

The LISA subtable changed how the framework source and test cases are
expressed:

| Legacy (`[test-suites.NAME.lisa]`) | Replacement (`[tests.NAME.lisa]`) |
|------------------------------------|-----------------------------------|
| `[...lisa.framework]` table (`git-url`, `ref`) | `source = { git-url, ref }` (or a `[...lisa.source]` subtable) |
| `test-cases = ["a", "b"]` | `testcase-names = ["a", "b"]` (or `testcase-name`, `name`, or a `criteria` block — see [LISA fields](#lisa-fields)) |
| `pip-pre-install`, `pip-extras`, `extra-args` | unchanged |

### Before and after

```toml
# Before (removed):
[test-suites.smoke]
type = "pytest"
[test-suites.smoke.pytest]
working-dir = "tests/smoke"
test-paths = ["cases/test_*.py"]

[test-suites.vm-integration]
type = "lisa"
[test-suites.vm-integration.lisa]
test-cases = ["verify_grub"]
[test-suites.vm-integration.lisa.framework]
git-url = "https://github.com/microsoft/lisa.git"
ref = "0123456789012345678901234567890123456789"

[images.vm-base.tests]
test-suites = [{ name = "smoke" }, { name = "vm-integration" }]
```

```toml
# After:
[tests.smoke]
type = "pytest"
[tests.smoke.pytest]
working-dir = "tests/smoke"
test-paths = ["cases/test_*.py"]

[tests.vm-integration]
type = "lisa"
[tests.vm-integration.lisa]
testcase-names = ["verify_grub"]
[tests.vm-integration.lisa.source]
git-url = "https://github.com/microsoft/lisa.git"
ref = "0123456789012345678901234567890123456789"

[images.vm-base.tests]
tests = [{ name = "smoke" }, { name = "vm-integration" }]
```

## Related Resources

- [Components](components.md#component-tests) — per-component `tests` field
- [Images](images.md#image-tests) — per-image `tests` field
- [Config File Structure](config-file.md) — top-level config layout
