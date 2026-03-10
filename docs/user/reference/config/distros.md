# Distro Definitions

The `[distros]` section defines the Linux distributions that azldev can build against or pull upstream sources from. Each distro has general properties and one or more version-specific configurations.

Distro definitions are typically placed in dedicated files (e.g., `fedora.distro.toml`, `azurelinux.distro.toml`) under a `distro/` directory.

## Distro Definition

Defined under `[distros.<name>]`, where `<name>` is the distro identifier used throughout the configuration.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of the distro |
| Default version | `default-version` | string | No | The version to use when no explicit version is specified |
| Dist-git base URI | `dist-git-base-uri` | string (URI) | No | Base URI template for the distro's dist-git repositories |
| Lookaside base URI | `lookaside-base-uri` | string (URI) | No | Base URI template for the distro's lookaside cache |
| Repos | `repos` | array of [PackageRepository](#package-repositories) | No | Package repository definitions for this distro |
| Versions | `versions` | map of [DistroVersion](#distro-versions) | No | Version-specific configuration |

### URI Placeholders

The `dist-git-base-uri` and `lookaside-base-uri` fields support placeholder variables that azldev substitutes at runtime:

| Placeholder | Description | Example expansion |
|-------------|-------------|-------------------|
| `$pkg` | Component/package name | `curl`, `kernel` |
| `$filename` | Source filename | `curl-8.5.0.tar.xz` |
| `$hashtype` | Hash algorithm used | `sha512` |
| `$hash` | Hash value of the file | `a1b2c3...` |

Example:

```toml
[distros.fedora]
dist-git-base-uri = "https://src.fedoraproject.org/rpms/$pkg.git"
lookaside-base-uri = "https://example.com/repo/pkgs/$pkg/$filename/$hashtype/$hash/$filename"
```

### Merging Across Files

Unlike components and images, distro definitions with the same name can appear in multiple files. When this happens, their fields are merged — later files' non-empty fields override earlier ones. However, slice fields like `repos` are **replaced** entirely, not appended. See [Configuration System](../../explanation/config-system.md#merge-rules) for details.

## Distro Versions

Each distro can define multiple versions under `[distros.<name>.versions.<version>]`. Version keys can be arbitrary strings — common patterns include numeric versions (`"4.0"`, `"43"`) and branch names (`"rawhide"`).

> **Tip:** Quote version keys that contain dots or could be misinterpreted by TOML: `[distros.azurelinux.versions.'4.0']`.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of this version |
| Release version | `release-ver` | string | **Yes** | Formal release version string (e.g., `"43"`, `"4.0"`, `"rawhide"`) |
| Dist-git branch | `dist-git-branch` | string | No | Branch name in the dist-git repository for this version |
| Default component config | `default-component-config` | [ComponentConfig](components.md) | No | Default configuration inherited by all components built against this distro version |
| Mock config | `mock-config` | string | No | Path to the mock config file for this version (architecture-independent) |
| Mock config (x86_64) | `mock-config-x86_64` | string | No | Path to the x86_64-specific mock config file |
| Mock config (aarch64) | `mock-config-aarch64` | string | No | Path to the aarch64-specific mock config file |

### Default Component Config

The `default-component-config` field is a powerful inheritance mechanism. Any configuration defined here is inherited by **every component** built against this distro version, unless the component explicitly overrides it. This is how you set a baseline spec source for all components:

```toml
[distros.azurelinux.versions.'4.0'.default-component-config]
spec = { type = "upstream", upstream-distro = { name = "fedora", version = "43", snapshot = "2026-02-24T00:00:00-08:00" } }
```

With this configuration, every component automatically uses specs from Fedora 43 (as they existed at the snapshot date) unless it specifies its own `spec` field.

See [Configuration Inheritance](../../explanation/config-system.md#configuration-inheritance) for the full inheritance chain.

## Distro References

A distro reference is a compact inline type used to point to a specific distro and version. It appears in fields like `default-distro` (in [project config](project.md)) and `upstream-distro` (in [spec source config](components.md#spec-source)).

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Name | `name` | string | **Yes** | Name of the distro (must match a key in `[distros]`) |
| Version | `version` | string | No | Version of the distro (must match a key in the distro's `versions` map). Falls back to the distro's `default-version` if omitted. |
| Snapshot | `snapshot` | string (datetime) | No | If specified, use source code as it existed at this point in time |

The `snapshot` field is particularly useful for upstream distros whose repositories change over time. By setting a snapshot date, you pin source code to a known state, ensuring reproducible builds regardless of when `azldev` runs:

```toml
{ name = "fedora", version = "43", snapshot = "2026-02-24T00:00:00-08:00" }
```

## Package Repositories

Package repositories are defined in the distro's `repos` array. Each entry specifies a base URI for a package repository.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Base URI | `base-uri` | string | **Yes** | Base URI for the repository. Supports the `$releasever` placeholder. |

```toml
repos = [
    { "base-uri" = "https://example.com/releases/$releasever/Everything/source/tree" },
    { "base-uri" = "https://example.com/updates/$releasever/Everything/source/tree" },
]
```

The `$releasever` placeholder is substituted with the version's `release-ver` value at runtime.

## Examples

### Upstream Distro (Fedora)

```toml
[distros.fedora]
description = "Fedora Linux"
default-version = "43"
dist-git-base-uri = "https://src.fedoraproject.org/rpms/$pkg.git"
lookaside-base-uri = "https://example.com/repo/pkgs/$pkg/$filename/$hashtype/$hash/$filename"
repos = [
    { "base-uri" = "https://example.com/releases/$releasever/Everything/source/tree" },
    { "base-uri" = "https://example.com/updates/$releasever/Everything/source/tree" },
]

[distros.fedora.versions.41]
description = "Fedora Linux 41"
release-ver = "41"
dist-git-branch = "f41"

[distros.fedora.versions.43]
description = "Fedora Linux 43"
release-ver = "43"
dist-git-branch = "f43"

[distros.fedora.versions.rawhide]
description = "Fedora Linux Rawhide"
release-ver = "rawhide"
dist-git-branch = "rawhide"
```

### Build Target Distro (Azure Linux)

```toml
[distros.azurelinux]
description = "Azure Linux"
default-version = "4.0"

[distros.azurelinux.versions.'4.0']
description = "Azure Linux 4.0"
release-ver = "4.0"
mock-config-x86_64 = "mock/azurelinux-4.0-x86_64.cfg"
mock-config-aarch64 = "mock/azurelinux-4.0-aarch64.cfg"

# All components default to using Fedora 43 specs
[distros.azurelinux.versions.'4.0'.default-component-config]
spec = { type = "upstream", upstream-distro = { name = "fedora", version = "43", snapshot = "2026-02-24T00:00:00-08:00" } }
```

## Related Resources

- [Config File Structure](config-file.md) — top-level config file layout
- [Project Configuration](project.md) — `default-distro` references distro definitions
- [Components](components.md) — component spec sources reference distros
- [Configuration System](../../explanation/config-system.md) — merge rules and inheritance behavior
