# Config File Structure

Every azldev configuration file is a TOML file that conforms to the azldev schema. The root config file is typically named `azldev.toml` and sits at the project root. Additional config files are pulled in via the [`includes`](#includes) mechanism.

All config files share the same schema — there is no distinction between a "root" file and an "included" file. Any file can define any section, subject to the [merge rules](../../explanation/config-system.md#merge-rules).

## Top-Level Sections

| TOML Key | Type | Description | Reference |
|----------|------|-------------|-----------|
| `includes` | string array | Glob patterns for additional config files to load | [Includes](#includes) |
| `project` | object | Project metadata and directory configuration | [Project](project.md) |
| `distros` | map of objects | Distro definitions (build environments, upstream sources) | [Distros](distros.md) |
| `components` | map of objects | Component (package) definitions | [Components](components.md) |
| `component-groups` | map of objects | Named groups of components with shared defaults | [Component Groups](component-groups.md) |
| `images` | map of objects | Image definitions (VMs, containers) | [Images](images.md) |
| `tools` | object | Configuration for external tools used by azldev | [Tools](tools.md) |

## Includes

The `includes` field lists glob patterns for additional config files to load and merge:

```toml
includes = ["distro/distro.toml", "base/project.toml"]
```

| Field | Type | Description |
|-------|------|-------------|
| `includes` | string array | Glob patterns resolved relative to the directory containing this file. Supports `*`, `?`, `[...]`, and `**` (recursive) wildcards. |

Glob patterns that match no files are silently ignored. Literal filenames (no wildcards) that do not exist produce an error.

Includes are resolved recursively — included files can themselves declare further includes. For a detailed explanation of load order and merge semantics, see [Configuration System](../../explanation/config-system.md).

## Minimal Example

A minimal root config file that includes distro definitions and a project:

```toml
includes = ["distro/distro.toml", "base/project.toml"]
```

A project-level config file with its own includes:

```toml
includes = ["comps/components.toml", "images/images.toml"]

[project]
description = "my-project"
log-dir = "build/logs"
work-dir = "build/work"
output-dir = "out"
default-distro = { name = "azurelinux", version = "4.0" }
```

A simple component list file:

```toml
includes = ["**/*.comp.toml"]

[components.curl]
[components.git]
[components.vim]
```

## Related Resources

- [Configuration System](../../explanation/config-system.md) — how config files are loaded, merged, and how inheritance works
- [JSON Schema](../../../../schemas/azldev.schema.json) — machine-readable schema for editor integration and validation
- Run `azldev config dump -q -O json` to inspect the fully resolved configuration
- Run `azldev config generate-schema` to generate the latest schema
