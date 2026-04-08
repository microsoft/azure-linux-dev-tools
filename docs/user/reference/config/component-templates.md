# Component Templates

Component templates define a matrix of axes whose cartesian product expands into multiple [component](components.md) definitions. This is useful when you need to build several variants of the same source project — for example, an out-of-tree kernel module compiled against different kernel versions and toolchains.

Templates are defined under `[component-templates.<name>]` in the TOML configuration. During config loading, each template is expanded into regular components that behave identically to explicitly defined ones.

## Template Config

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-friendly description of this template |
| Default component config | `default-component-config` | [ComponentConfig](components.md#component-config) | No | Base configuration applied to every expanded variant before axis overrides |
| Matrix | `matrix` | array of [MatrixAxis](#matrix-axis) | **Yes** | Ordered list of axes whose cartesian product defines the expanded variants |

## Matrix Axis

Each matrix axis defines one dimension of the expansion. The cartesian product of all axes determines the set of expanded components.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Axis name | `axis` | string | **Yes** | Name of this axis (e.g., `"kernel"`, `"toolchain"`) |
| Values | `values` | map of string → [ComponentConfig](components.md#component-config) | **Yes** | Named values for this axis; each value is a partial component config merged into the expanded component |

### Constraints

- At least one axis is required per template.
- Each axis must have at least one value.
- Axis names must be unique within a template.
- Value names must be non-empty.

## Expansion Rules

### Name Synthesis

Expanded component names are formed by joining the template name with each selected value name, separated by `-`, in the order the axes appear in the `matrix` array:

```
<template-name>-<axis1-value>-<axis2-value>-...
```

For example, a template `my-driver` with axes `kernel` (values `6-6`, `6-12`) and `toolchain` (values `gcc13`, `gcc14`) produces:

- `my-driver-6-6-gcc13`
- `my-driver-6-6-gcc14`
- `my-driver-6-12-gcc13`
- `my-driver-6-12-gcc14`

### Config Layering

For each expanded component, configurations are layered in the following order (later layers override earlier ones):

1. Template's `default-component-config`
2. First axis's selected value config
3. Second axis's selected value config
4. ... (additional axes in definition order)

This uses the same `MergeUpdatesFrom` mechanism as [component group defaults](component-groups.md), so all standard merge rules apply.

### Collision Handling

If an expanded component name collides with an explicitly defined component (or another template's expansion), a validation error is produced at load time. Expanded components are merged **after** regular components, so the error message will identify the template as the source of the collision.

## Example

### Basic Two-Axis Template

```toml
[component-templates.my-driver]
description = "Out-of-tree driver built against multiple kernel versions and toolchains"

[component-templates.my-driver.default-component-config]
spec = { type = "local", path = "my-driver.spec" }

[component-templates.my-driver.default-component-config.build]
defines = { base_config = "true" }

[[component-templates.my-driver.matrix]]
axis = "kernel"
[component-templates.my-driver.matrix.values.6-6.build]
defines = { kernel_version = "6.6.72" }
[component-templates.my-driver.matrix.values.6-12.build]
defines = { kernel_version = "6.12.8" }

[[component-templates.my-driver.matrix]]
axis = "toolchain"
[component-templates.my-driver.matrix.values.gcc13.build]
defines = { gcc_version = "13" }
[component-templates.my-driver.matrix.values.gcc14.build]
defines = { gcc_version = "14" }
```

This produces 4 components. For example, `my-driver-6-6-gcc14` will have:
- `spec` from the default: `{ type = "local", path = "my-driver.spec" }`
- `build.defines` merged from all layers: `{ base_config = "true", kernel_version = "6.6.72", gcc_version = "14" }`

### Template with Overlays

Axis values can include any [ComponentConfig](components.md#component-config) fields, including overlays:

```toml
[component-templates.my-driver]

[component-templates.my-driver.default-component-config]
spec = { type = "local", path = "my-driver.spec" }

[[component-templates.my-driver.matrix]]
axis = "kernel"

[component-templates.my-driver.matrix.values.6-6]

[[component-templates.my-driver.matrix.values.6-6.overlays]]
type = "spec-set-tag"
description = "Set kernel version to 6.6"
tag = "kernel_version"
value = "6.6.72"

[component-templates.my-driver.matrix.values.6-12]

[[component-templates.my-driver.matrix.values.6-12.overlays]]
type = "spec-set-tag"
description = "Set kernel version to 6.12"
tag = "kernel_version"
value = "6.12.8"
```

### Mixing Templates with Regular Components

Templates and regular components coexist in the same config files:

```toml
# Regular component
[components.curl]

# Template-expanded components
[component-templates.my-driver]

[[component-templates.my-driver.matrix]]
axis = "kernel"
[component-templates.my-driver.matrix.values.6-6]
[component-templates.my-driver.matrix.values.6-12]
```

This produces 3 components: `curl`, `my-driver-6-6`, and `my-driver-6-12`.

## Related Resources

- [Components](components.md) — component configuration reference
- [Component Groups](component-groups.md) — grouping components with shared defaults
- [Config File Structure](config-file.md) — top-level config file layout
- [Configuration System](../../explanation/config-system.md) — inheritance and merge behavior
- [JSON Schema](../../../../schemas/azldev.schema.json) — machine-readable schema
