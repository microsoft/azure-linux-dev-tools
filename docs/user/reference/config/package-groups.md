# Package Groups

Package groups let you apply shared configuration to named sets of binary packages. They are defined under `[package-groups.<name>]` in the TOML configuration.

Package groups are evaluated at build time, after the binary RPMs are produced. They are analogous to [component groups](component-groups.md), which apply shared configuration to sets of components.

## Field Reference

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of this group |
| Packages | `packages` | string array | No | Explicit list of binary package names that belong to this group |
| Default package config | `default-package-config` | [PackageConfig](#package-config) | No | Configuration inherited by all packages listed in this group |

## Packages

The `packages` field is an explicit list of binary package names (as they appear in the RPM `Name` tag) that belong to this group. Membership is determined by exact name match — no glob patterns or wildcards are supported.

```toml
[package-groups.devel-packages]
description = "Development subpackages"
packages = ["libcurl-devel", "curl-static", "wget2-devel"]

[package-groups.debug-packages]
description = "Debug info and source packages"
packages = ["curl-debuginfo", "curl-debugsource", "wget2-debuginfo"]
```

> **Note:** A package name may appear in at most one group. Listing the same name in two groups produces a validation error.

## Package Config

The `[package-groups.<name>.default-package-config]` section defines the configuration applied to all packages matching this group.

### PackageConfig Fields

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|

| Publish settings | `publish` | [PublishConfig](#publish-config) | No | Publishing settings for matched packages |

### Publish Config

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| RPM Channel | `rpm-channel` | string | No | Publish channel for binary packages. Use `"none"` to signal to downstream tooling that this package should not be published. |
| Debuginfo Channel | `debuginfo-channel` | string | No | Publish channel for debuginfo and debugsource packages. |

## Resolution Order

When determining the effective config for a binary package, azldev applies config layers in this order — later layers override earlier ones:

1. **Project `default-package-config`** — lowest priority; applies to all packages in the project
2. **Package group** — the group (if any) whose `packages` list contains the package name
3. **Component `packages.<name>`** — highest priority; exact per-package override

> **Note:** Each package name may appear in at most one group. Listing the same name in two groups produces a validation error.

## Example

```toml
# Set a project-wide default channel
[default-package-config.publish]
rpm-channel = "rpm-base"

[package-groups.devel-packages]
description = "Development subpackages"
packages = ["libcurl-devel", "curl-static", "wget2-devel"]

[package-groups.devel-packages.default-package-config.publish]
rpm-channel = "rpm-build-only"

[package-groups.debug-packages]
description = "Debug info and source"
packages = [
    "libcurl-debuginfo",
    "libcurl-minimal-debuginfo",
    "curl-debugsource",
    "wget2-debuginfo",
    "wget2-debugsource",
    "wget2-libs-debuginfo"
]

[package-groups.debug-packages.default-package-config.publish]
debuginfo-channel = "rpm-debug"
```

## Related Resources

- [Project Configuration](project.md) — top-level `default-package-config` and `package-groups` fields
- [Components](components.md) — per-component `packages` overrides and `publish` settings
- [Configuration System](../../explanation/config-system.md) — inheritance and merge behavior
