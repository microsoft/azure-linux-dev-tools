# Package Groups

Package groups let you apply shared publish configuration to sets of binary packages matched by name glob patterns. They are defined under `[package-groups.<name>]` in the TOML configuration.

Package groups are evaluated at build time, after the binary RPMs are produced, to determine the publish channel for each package. This is analogous to how [component groups](component-groups.md) apply shared configuration to components at config-load time.

## Field Reference

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of this group |
| Package patterns | `package-patterns` | string array | No | Glob patterns matched against binary package names to determine group membership |
| Default package config | `default-package-config` | [PackageConfig](#package-config) | No | Configuration inherited by all packages whose name matches any pattern in this group |

## Package Patterns

The `package-patterns` field accepts glob patterns following [`path.Match`](https://pkg.go.dev/path#Match) syntax:

- `*` matches any sequence of non-separator characters
- `?` matches any single non-separator character
- `[abc]` matches any character in the set

A binary package is a member of a group if its name matches **any** pattern in `package-patterns`.

```toml
[package-groups.devel-packages]
description = "All -devel subpackages"
package-patterns = ["*-devel"]

[package-groups.python-packages]
description = "Python 3 packages"
package-patterns = ["python3-*", "python3"]
```

## Package Config

The `[package-groups.<name>.default-package-config]` section defines the configuration applied to all packages matching this group.

### PackageConfig Fields

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable note about this package's configuration |
| Publish settings | `publish` | [PublishConfig](#publish-config) | No | Publishing settings for matched packages |

### Publish Config

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Channel | `channel` | string | No | Publish channel for this package. Use `"none"` to skip publishing entirely. |

## Resolution Order

When determining the effective config for a binary package, azldev applies config layers in this order — later layers override earlier ones:

1. **Project `default-package-config`** — lowest priority; applies to all packages in the project
2. **Package groups** — all groups whose `package-patterns` match the package name, applied in **alphabetical order by group name** (later-named groups win)
3. **Component `default-package-config`** — applies to all packages produced by that component
4. **Component `packages.<name>`** — highest priority; exact per-package override

> **Tip:** Group names beginning with letters near the end of the alphabet take precedence over earlier names. Prefix group names with a priority hint (e.g., `10-base`, `20-security`) if you need explicit ordering.

## Example

```toml
# Set a project-wide default channel
[default-package-config.publish]
channel = "base"

# Route all -devel packages to the "devel" channel
[package-groups.devel-packages]
description = "Development subpackages"
package-patterns = ["*-devel", "*-static", "*-headers"]

[package-groups.devel-packages.default-package-config.publish]
channel = "devel"

# Exclude debug packages from publishing entirely
[package-groups.debug-packages]
description = "Debug info packages — not published"
package-patterns = ["*-debuginfo", "*-debugsource"]

[package-groups.debug-packages.default-package-config.publish]
channel = "none"
```

## Related Resources

- [Project Configuration](project.md) — top-level `default-package-config` and `package-groups` fields
- [Components](components.md) — per-component `default-package-config` and `packages` overrides
- [Configuration System](../../explanation/config-system.md) — inheritance and merge behavior
