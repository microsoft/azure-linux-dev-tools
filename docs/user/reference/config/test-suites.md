# Test Suites

The `[test-suites]` section defines named test suites that can be referenced by images. Each test suite is defined under `[test-suites.<name>]`.

Test suite names must be simple identifiers (no path separators, traversal segments, or whitespace) since they are used as path components — for example, each pytest suite gets its own Python virtual environment under the project work directory.

## Test Suite Config

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of the test suite |
| Type | `type` | string | Yes | Test framework to use: `"pytest"` or `"lisa"`. |
| Pytest | `pytest` | table | When `type = "pytest"` | Pytest-specific configuration (see below) |
| Lisa | `lisa` | table | Optional (required to run locally) | LISA-specific configuration (see below). May be omitted for metadata-only suites; required to run the suite locally via `azldev image test`. |

Test suites are referenced by images through the [`[images.<name>.tests]`](images.md#image-tests) subtable. Each image can reference one or more test suites by name.

> **Note:** Each test suite name must be unique across all config files. Defining the same test suite name in two files produces an error.

## Pytest Suite Config

When `type = "pytest"`, a `[test-suites.<name>.pytest]` subtable must be provided. `azldev` runs the suite by creating (or reusing) a Python virtual environment, installing dependencies, and invoking `python -m pytest` with the configured arguments.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Working directory | `working-dir` | string | No | Directory used as pytest's CWD. Relative paths are resolved against the config file's directory. Required when `install` is `pyproject` or `requirements`. |
| Test paths | `test-paths` | array of strings | No | Test file paths or directories passed to pytest as positional arguments. Each entry is glob-expanded (including recursive `**`) relative to `working-dir`. Patterns that match nothing are passed through unchanged so pytest reports the failure. |
| Extra args | `extra-args` | array of strings | No | Additional arguments passed to pytest verbatim, after placeholder substitution. See [Placeholders](#placeholders). |
| Install mode | `install` | string | No | How dependencies are installed into the venv. One of `pyproject`, `requirements`, or `none` (default). |

### Install modes

| Mode | Behavior |
|------|----------|
| `pyproject` | Installs the project at `working-dir` in editable mode (`pip install -e <working-dir>`). Errors if `pyproject.toml` is not present. |
| `requirements` | Installs from `<working-dir>/requirements.txt`. Errors if the file is not present. |
| `none` (default) | Skips dependency installation entirely. Use when the venv has been pre-populated or pytest is otherwise on `PATH`. |

`--junit-xml` output requested via the `azldev image test --junit-xml <path>` CLI flag is appended automatically; you do not need to add it to `extra-args`. Relative `--junit-xml` paths are resolved against the user's current working directory (not the test suite's `working-dir`).

### Placeholders

The following placeholders may appear in `extra-args` and are substituted at run time. They are **not** substituted in `test-paths`.

| Placeholder | Substitution |
|-------------|-------------|
| `{image-path}` | Absolute path to the image artifact under test |
| `{image-name}` | Name of the image being tested |
| `{capabilities}` | Comma-separated list of capability names enabled on the image |


## LISA Suite Config

The `[test-suites.<name>.lisa]` subtable is optional. A `type = "lisa"` suite without it is a metadata-only suite (usable by external orchestration but not runnable locally). To run the suite locally via `azldev image test`, the `[test-suites.<name>.lisa]` subtable must be provided.

When run locally, `azldev` executes the suite as follows: it clones the LISA framework at a pinned commit, creates (or reuses) a Python virtual environment and installs the framework into it, generates a runbook from the configured `test-cases`, and boots the image in a QEMU VM to run those cases. VMs are torn down after the run.

The image under test must already be in **qcow2** format; other formats are rejected. Each run generates and later removes an ephemeral admin SSH key pair for VM access.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Framework | `framework` | table | Yes | Git source for the LISA framework (see below). |
| Test cases | `test-cases` | array of strings | Yes | LISA test case names to run. They are joined with `\|` into the criteria of the generated runbook. Must be non-empty. |
| Pip pre-install | `pip-pre-install` | array of strings | No | Pip packages installed into the venv *before* the framework, to override framework version pins that conflict with the local environment (e.g., a system-matching `libvirt-python`). |
| Pip extras | `pip-extras` | array of strings | No | Pip extras installed from the framework package, appended as `pip install -e ".[extra1,extra2]"`. |
| Extra args | `extra-args` | array of strings | No | Additional arguments passed to the `lisa` CLI verbatim, after placeholder substitution. See [Placeholders](#placeholders). The generated runbook is always passed via `-r`. |

### Framework git source

The `[test-suites.<name>.lisa.framework]` subtable pins the LISA framework repository.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Git URL | `git-url` | string | Yes | URL of the LISA framework git repository. |
| Ref | `ref` | string | Yes | Full 40-character hex commit SHA to check out. Branch names and tags are rejected. |

The framework checkout is keyed by ref, so updating `ref` clones the new revision. If a pinned ref cannot be checked out in an existing checkout (e.g., it was updated to a newer commit), `azldev` re-clones the repository automatically.



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

### Suite that installs from `pyproject.toml`

```toml
[test-suites.integration-pyproject]
type = "pytest"

[test-suites.integration-pyproject.pytest]
working-dir = "tests/integration"
install = "pyproject"
test-paths = ["cases/test_*.py"]
```

### Suite with no dependency install (default)

```toml
[test-suites.preinstalled]
type = "pytest"

[test-suites.preinstalled.pytest]
# install defaults to "none" — pytest must already be available.
test-paths = ["/opt/preinstalled-tests/test_*.py"]
```

### LISA suite

LISA suites are executed locally by `azldev`, which generates a runbook from `test-cases` and boots the image in a QEMU VM. The image must be in qcow2 format.

```toml
[test-suites.vm-integration]
description = "VM integration tests using LISA"
type = "lisa"

[test-suites.vm-integration.lisa]
test-cases = ["verify_cpu_count", "verify_grub"]
# Optional: override a framework version pin and pass extra CLI args to LISA.
pip-pre-install = ["libvirt-python==9.0.0"]
extra-args = ["-v"]

[test-suites.vm-integration.lisa.framework]
git-url = "https://github.com/microsoft/lisa.git"
ref = "abcdef0123456789abcdef0123456789abcdef01"  # full 40-char commit SHA
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
