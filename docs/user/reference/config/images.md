# Images

The `[images]` section defines system images (VMs, containers, etc.) that azldev can build from your project's packages. Each image is defined under `[images.<name>]`.

## Image Config

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of the image |
| Definition | `definition` | [ImageDefinition](#image-definition) | No | Specifies the image definition format, file path, and optional profile |

## Image Definition

The `definition` field tells azldev where to find the image definition file and what format it uses.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Type | `type` | string | No | Image definition format (e.g., `"kiwi"`) |
| Path | `path` | string | No | Path to the image definition file, relative to the config file |
| Profile | `profile` | string | No | Build profile to use when building the image (format-specific) |

> **Note:** Each image name must be unique across all config files. Defining the same image name in two files produces an error.

## Examples

### VM image using Kiwi

```toml
[images.vm-base]
description = "VM Base Image"
definition = { type = "kiwi", path = "vm-base/vm-base.kiwi" }
```

### Container image

```toml
[images.container-base]
description = "Container Base Image"
definition = { type = "kiwi", path = "container-base/container-base.kiwi" }
```

### Image with a build profile

```toml
[images.vm-azure]
description = "Azure-optimized VM image"
definition = { type = "kiwi", path = "vm-azure/vm-azure.kiwi", profile = "azure" }
```

## Related Resources

- [Config File Structure](config-file.md) — top-level config file layout
- [Tools](tools.md) — Image Customizer tool configuration
