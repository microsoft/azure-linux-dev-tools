# Component Groups

Component groups organize related components together and let you apply shared default configuration to all members. They are defined under `[component-groups.<name>]` in the TOML configuration.

## Field Reference

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of this group |
| Components | `components` | string array | No | List of component names that belong to this group |
| Specs | `specs` | string array | No | Glob patterns for discovering local spec files to include as group members |
| Excluded paths | `excluded-paths` | string array | No | Glob patterns for paths to exclude from spec discovery |
| Default component config | `default-component-config` | [ComponentConfig](components.md) | No | Default configuration inherited by all components in this group |

## Component Membership

Components are assigned to groups in two ways:

### Explicit List

The `components` field lists component names directly:

```toml
[component-groups.networking]
description = "Networking-related packages"
components = ["curl", "wget2", "bind", "openssh"]
```

### Spec Discovery

The `specs` field uses glob patterns to discover local spec files. Components are created automatically from discovered specs:

```toml
[component-groups.local-packages]
description = "All local specs under SPECS/"
specs = ["SPECS/**/*.spec"]
excluded-paths = ["build/**"]
```

The `excluded-paths` patterns filter out spec files in directories that should not be included (e.g., build output directories).

## Default Component Config

The `default-component-config` field defines configuration that all group members inherit. This uses the same structure as a [component config](components.md), so you can set spec sources, build options, and overlays that apply to every member:

```toml
[component-groups.azure-packages]
description = "Azure-specific packages"
components = ["azure-vm-utils", "cloud-init"]

[component-groups.azure-packages.default-component-config.build]
defines = { azure = "1" }
```

### Inheritance Order

When a component belongs to one or more groups, the effective configuration is assembled in this order (later layers override earlier ones):

1. Distro version `default-component-config`
2. Project-level `default-component-config`
3. Component group `default-component-config` (in alphabetical order by group name if multiple groups apply)
4. Component's own explicit configuration

See [Configuration Inheritance](../../explanation/config-system.md#configuration-inheritance) for the full details.

> **Note:** Each component group name must be unique across all config files. Defining the same group name in two files produces an error.

## Example

```toml
[component-groups.python-extras]
description = "Python packages with extended test suites that need network access"
components = [
    "python-tornado",
    "python-twisted",
    "python-dns",
]

[component-groups.python-extras.default-component-config.build]
check = { skip = true, skip_reason = "Tests require network access unavailable in mock" }
```

## Related Resources

- [Components](components.md) — individual component configuration
- [Config File Structure](config-file.md) — top-level config file layout
- [Configuration System](../../explanation/config-system.md) — inheritance and merge behavior
