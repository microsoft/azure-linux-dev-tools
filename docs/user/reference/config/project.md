# Project Configuration

The `[project]` section defines metadata and directory layout for an azldev project. It is typically defined in a project-level config file (e.g., `base/project.toml`) rather than the root `azldev.toml`.

## `[project]` Field Reference

The following fields are nested under the `[project]` TOML section:

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable project description |
| Log directory | `log-dir` | string | No | Path to the directory where build logs are written (relative to this config file) |
| Work directory | `work-dir` | string | No | Path to the temporary working directory for build artifacts (relative to this config file) |
| Output directory | `output-dir` | string | No | Path to the directory where final build outputs (RPMs, SRPMs) are placed (relative to this config file) |
| Rendered specs directory | `rendered-specs-dir` | string | No | Output directory for `component render` (relative to this config file) |
| Default distro | `default-distro` | [DistroReference](distros.md#distro-references) | No | The default distro and version to use when building components |

> **Note:** `[default-component-config]`, `[default-package-config]`, and `[package-groups]` are **top-level** TOML sections — they are not nested under `[project]`. They are documented in the sections below.

## Directory Paths

The `log-dir`, `work-dir`, `output-dir`, and `rendered-specs-dir` paths are resolved relative to the config file that defines them. These directories are created automatically by azldev as needed.

- **`log-dir`** — build logs are written here (e.g., `azldev.log`)
- **`work-dir`** — temporary per-component working directories are created under this path during builds (e.g., source preparation, SRPM construction)
- **`output-dir`** — final build artifacts (RPMs, SRPMs) are placed here
- **`rendered-specs-dir`** — rendered spec and sidecar files are written here by `azldev component render`. Components are organized into letter-prefixed subdirectories (e.g., `SPECS/c/curl`, `SPECS/v/vim`)

> **Note:** Do not edit files under these directories manually — they are managed by azldev and may be overwritten or cleaned at any time.

## Default Distro

The `default-distro` field specifies which distro and version components should be built against by default. This is a [distro reference](distros.md#distro-references) that must point to a distro and version defined elsewhere in the config:

```toml
[project]
default-distro = { name = "azurelinux", version = "4.0" }
```

Components inherit their spec source and build environment from the default distro's configuration unless they override it explicitly. See [Configuration Inheritance](../../explanation/config-system.md#configuration-inheritance) for details.

## Default Component Config

The `[default-component-config]` section is a **top-level** TOML section (not nested under `[project]`). It defines the lowest-priority configuration layer applied to every component in the project before any component-group or component-level config is considered.

The most common use is to set project-wide default publish channels for all components:

```toml
[default-component-config.publish]
rpm-channel = "rpms-base"
srpm-channel = "rpms-base-srpm"
debuginfo-channel = "rpms-base-debuginfo"
```

Individual components or [component groups](component-groups.md) can override these defaults. See [Components — Publish Settings](components.md#publish-settings) for the full field reference and [Package Groups](package-groups.md#resolution-order) for the complete resolution order.

## Default Package Config

The `[default-package-config]` section is a **top-level** TOML section (not nested under `[project]`). It defines the lowest-priority configuration layer applied to every binary package produced by any component in the project. It is overridden by [package groups](package-groups.md) `default-package-config` settings and explicit per-package overrides.

The most common use is to set a project-wide default publish channel:

```toml
[default-package-config.publish]
rpm-channel = "rpm-base"
```

See [Package Groups](package-groups.md#resolution-order) for the full resolution order.

## Package Groups

The `[package-groups.<name>]` section is a **top-level** TOML section (not nested under `[project]`). It defines named groups of binary packages. Each group lists its members explicitly in the `packages` field and provides a `default-package-config` that is applied to all listed packages.

This is currently used to route different types of packages (e.g., `-devel`, `-debuginfo`) to different publish channels, though groups can also carry other future configuration.

See [Package Groups](package-groups.md) for the full field reference.

## Example

```toml
[project]
description = "azurelinux-base"
log-dir = "build/logs"
work-dir = "build/work"
output-dir = "out"
default-distro = { name = "azurelinux", version = "4.0" }

[default-component-config.publish]
rpm-channel = "rpms-base"
srpm-channel = "rpms-base-srpm"
debuginfo-channel = "rpms-base-debuginfo"

[default-package-config.publish]
rpm-channel = "base"

[package-groups.devel-packages]
description = "Development subpackages"
packages = ["curl-devel", "curl-static", "wget2-devel"]

[package-groups.devel-packages.default-package-config.publish]
rpm-channel = "devel"
```

## Related Resources

- [Config File Structure](config-file.md) — top-level config file layout
- [Distros](distros.md) — distro definitions referenced by `default-distro`
- [Component Groups](component-groups.md) — group-level component config overrides
- [Package Groups](package-groups.md) — full reference for `package-groups` and package config resolution
- [Components](components.md) — per-component package config overrides
- [Configuration System](../../explanation/config-system.md) — how project config merges with other files
