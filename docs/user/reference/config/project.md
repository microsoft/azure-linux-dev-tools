# Project Configuration

The `[project]` section defines metadata and directory layout for an azldev project. It is typically defined in a project-level config file (e.g., `base/project.toml`) rather than the root `azldev.toml`.

## Field Reference

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable project description |
| Log directory | `log-dir` | string | No | Path to the directory where build logs are written (relative to this config file) |
| Work directory | `work-dir` | string | No | Path to the temporary working directory for build artifacts (relative to this config file) |
| Output directory | `output-dir` | string | No | Path to the directory where final build outputs (RPMs, SRPMs) are placed (relative to this config file) |
| Default distro | `default-distro` | [DistroReference](distros.md#distro-references) | No | The default distro and version to use when building components |

## Directory Paths

The `log-dir`, `work-dir`, and `output-dir` paths are resolved relative to the config file that defines them. These directories are created automatically by azldev as needed.

- **`log-dir`** — build logs are written here (e.g., `azldev.log`)
- **`work-dir`** — temporary per-component working directories are created under this path during builds (e.g., source preparation, SRPM construction)
- **`output-dir`** — final build artifacts (RPMs, SRPMs) are placed here

> **Note:** Do not edit files under these directories manually — they are managed by azldev and may be overwritten or cleaned at any time.

## Default Distro

The `default-distro` field specifies which distro and version components should be built against by default. This is a [distro reference](distros.md#distro-references) that must point to a distro and version defined elsewhere in the config:

```toml
[project]
default-distro = { name = "azurelinux", version = "4.0" }
```

Components inherit their spec source and build environment from the default distro's configuration unless they override it explicitly. See [Configuration Inheritance](../../explanation/config-system.md#configuration-inheritance) for details.

## Example

```toml
[project]
description = "azurelinux-base"
log-dir = "build/logs"
work-dir = "build/work"
output-dir = "out"
default-distro = { name = "azurelinux", version = "4.0" }
```

## Related Resources

- [Config File Structure](config-file.md) — top-level config file layout
- [Distros](distros.md) — distro definitions referenced by `default-distro`
- [Configuration System](../../explanation/config-system.md) — how project config merges with other files
