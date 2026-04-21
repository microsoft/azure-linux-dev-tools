# Images

The `[images]` section defines system images (VMs, containers, etc.) that azldev can build from your project's packages. Each image is defined under `[images.<name>]`.

## Image Config

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of the image |
| Definition | `definition` | [ImageDefinition](#image-definition) | No | Specifies the image definition format, file path, and optional profile |
| Capabilities | `capabilities` | [ImageCapabilities](#image-capabilities) | No | Describes features and properties of this image |
| Tests | `tests` | [ImageTests](#image-tests) | No | Test configuration for this image |
| Publish | `publish` | [ImagePublish](#image-publish) | No | Publishing settings for this image |

## Image Definition

The `definition` field tells azldev where to find the image definition file and what format it uses.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Type | `type` | string | No | Image definition format (e.g., `"kiwi"`) |
| Path | `path` | string | No | Path to the image definition file, relative to the config file |
| Profile | `profile` | string | No | Build profile to use when building the image (format-specific) |

## Image Capabilities

The `capabilities` subtable describes what the image supports. All fields are optional booleans using tri-state semantics: `true` (explicitly enabled), `false` (explicitly disabled), or omitted (unspecified / inherit from defaults).

| Field | TOML Key | Type | Default | Description |
|-------|----------|------|---------|-------------|
| Machine Bootable | `machine-bootable` | bool | unset | Whether the image can be booted on a machine (bare metal or VM) |
| Container | `container` | bool | unset | Whether the image can be run on an OCI container host |
| Systemd | `systemd` | bool | unset | Whether the image runs systemd as its init system |
| Runtime Package Management | `runtime-package-management` | bool | unset | Whether the image supports installing/removing packages at runtime (e.g., via dnf/tdnf) |

## Image Tests

The `tests` subtable links an image to one or more test suites defined in the top-level [`[test-suites]`](test-suites.md) section.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Test Suites | `test-suites` | array of inline tables | No | List of test suite references. Each entry must have a `name` field matching a key in `[test-suites]`. |

## Image Publish

The `publish` subtable configures where an image is published. Unlike packages (which target a single channel), images may be published to multiple channels simultaneously.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Channels | `channels` | string array | No | List of publish channels for this image |

> **Note:** Each image name must be unique across all config files. Defining the same image name in two files produces an error.

## Examples

### VM image with capabilities

```toml
[images.vm-base]
description = "VM Base Image"
definition = { type = "kiwi", path = "vm-base/vm-base.kiwi" }

[images.vm-base.capabilities]
machine-bootable = true
systemd = true
runtime-package-management = true
```

### Container image with capabilities

```toml
[images.container-base]
description = "Container Base Image"
definition = { type = "kiwi", path = "container-base/container-base.kiwi" }

[images.container-base.capabilities]
container = true
```

### Image with a build profile

```toml
[images.vm-azure]
description = "Azure-optimized VM image"
definition = { type = "kiwi", path = "vm-azure/vm-azure.kiwi", profile = "azure" }
```

### Image with test suite references

```toml
[images.vm-base]
description = "VM Base Image"
definition = { type = "kiwi", path = "vm-base/vm-base.kiwi" }

[images.vm-base.capabilities]
machine-bootable = true
systemd = true

[images.vm-base.tests]
test-suites = [
  { name = "smoke" },
  { name = "integration" },
]
```

### Image with publish channels

```toml
[images.vm-base]
description = "VM Base Image"
definition = { type = "kiwi", path = "vm-base/vm-base.kiwi" }

[images.vm-base.publish]
channels = ["registry-prod", "registry-staging"]
```

## Related Resources

- [Config File Structure](config-file.md) — top-level config file layout
- [Test Suites](test-suites.md) — test suite definitions
- [Tools](tools.md) — Image Customizer tool configuration
