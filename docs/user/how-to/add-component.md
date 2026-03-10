# How To: Add a Component

This guide walks through adding a new component (package) to an Azure Linux project.

## Prerequisites

- A configured Azure Linux project (see [Creating a Project](create-project.md))
- The `azldev` CLI installed and on your `PATH`

## Quick Start

Use the interactive `add` command:

```bash
azldev comp add <package-name>
```

This creates the component definition in your project's component configuration.

## Manual Approach

### Bare Component (Upstream Default)

For a simple upstream import with no customization, add a single line to your `components.toml`:

```toml
[components.curl]
```

This inherits the spec from the default upstream distro configured in your project.

### Dedicated Component File

For components that need overlays, build config, or other customization, create a dedicated file at `comps/<name>/<name>.comp.toml`:

```toml
[components.mypackage]
spec = { type = "upstream" }

[components.mypackage.build]
with = ["feature_x"]

[[components.mypackage.overlays]]
description = "Add missing build dependency for Azure Linux"
type = "spec-add-tag"
tag = "BuildRequires"
value = "extra-devel"
```

Dedicated files are automatically discovered via the `includes = ["**/*.comp.toml"]` pattern.

## Verify the Component

After adding, verify it's recognized:

```bash
azldev comp list -p <name>
```

Then build it:

```bash
azldev comp build -p <name>
```

## Related Resources

- [Components Reference](../reference/config/components.md) — component definition format
- [Overlays Reference](../reference/config/overlays.md) — modifying upstream specs
- [Component Groups Reference](../reference/config/component-groups.md) — grouping components

<!-- TODO: expand with real-world examples and common patterns -->
