# Images

The `[images]` section defines system images (VMs, containers, etc.) that azldev can build from your project's packages. Each image is defined under `[images.<name>]`.

## Image Config

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Description | `description` | string | No | Human-readable description of the image |
| Definition | `definition` | [ImageDefinition](#image-definition) | No | Specifies the image definition format, file path, and optional profile |
| Architectures | `architectures` | string array | No | Architectures supported by this image |
| Capabilities | `capabilities` | [ImageCapabilities](#image-capabilities) | No | Describes features supported by this image |
| Properties | `properties` | string-to-string table | No | Extensible metadata describing this image |
| Tests | `tests` | [ImageTests](#image-tests) | No | Test configuration for this image |
| Publish | `publish` | [ImagePublish](#image-publish) | No | Publishing settings for this image |

The current supported architectures are `x86_64` and `aarch64`. `architectures` is
optional; omitting it (or leaving it empty) means the image is unrestricted and
supports all recognized architectures, which keeps images.toml files written before
this field existed valid. `azldev image list` reports each image's architecture set,
and `azldev image build --arch` rejects architectures outside a declared set.

## Image Definition

The `definition` field tells azldev where to find the image definition file and what format it uses.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Type | `type` | string | No | Image definition format (e.g., `"kiwi"`) |
| Path | `path` | string | No | Path to the image definition file, relative to the config file |
| Profile | `profile` | string | No | Build profile to use when building the image (format-specific) |

## Image Capabilities

The `capabilities` subtable describes what the image supports. Boolean fields use tri-state semantics: `true` (explicitly enabled), `false` (explicitly disabled), or omitted (unspecified).

| Field | TOML Key | Type | Default | Description |
|-------|----------|------|---------|-------------|
| Machine Bootable | `machine-bootable` | bool | unset | Whether the image can be booted on a machine (bare metal or VM) |
| Container | `container` | bool | unset | Whether the image can be run on an OCI container host |
| Systemd | `systemd` | bool | unset | Whether the image runs systemd as its init system |
| Runtime Package Management | `runtime-package-management` | bool | unset | Whether the image supports installing/removing packages at runtime (e.g., via dnf/tdnf) |
| WSL | `wsl` | bool | unset | Whether the image runs under the Windows Subsystem for Linux runtime |
| Installer Media | `installer-media` | bool | unset | Whether the image is installer media (e.g. an ISO) that installs another OS, rather than a directly runnable end-state image |
| FIPS Enabled | `fips-enabled` | bool | unset | Whether the image is built or configured to run in FIPS mode |
| CVM | `cvm` | bool | unset | Whether the image supports running as a Confidential VM (CVM) |

## Image Properties

The `properties` subtable is an open string-to-string property bag for image metadata
that is useful to tests or external orchestration but is not interpreted by azldev.
Adding a property does not require an azldev schema change.

```toml
[images.vm-base.properties]
openssl-fips-provider = "upstream"
release-channel = "preview"
```

The `{properties}` test-runner placeholder serializes the complete property bag as a
JSON object. The existing `{capabilities}` placeholder remains a comma-separated list
of enabled boolean capability names.

## Image Tests

The `tests` subtable links an image to one or more tests or test groups defined in the top-level [`[tests]` / `[test-groups]`](tests.md) sections.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Tests | `tests` | array of [TestRef](tests.md#test-reference) | No | References to `[tests.<name>]` entries or `[test-groups.<name>]` entries (parse-only; see [Tests and Test Groups](tests.md)). |

Image test references may additionally set [`sku-groups`](tests.md#test-reference) to fan a test or group out across the Azure VM sizes of one or more [`[sku-groups.<name>]`](tests.md#sku-groups) entries:

```toml
[images.marketplace-gen2.tests]
tests = [{ group = "multi-sku-tests", sku-groups = ["multi-sku-amd64", "multi-sku-arm64"] }]
```

> **Note:** `sku-groups` cannot be mapped to arbitrary LISA tests. LISA tests
> carry their own requirements and are skipped when the SKU does not satisfy
> them, so fanning a normal test across a SKU group only yields skips on
> non-matching SKUs. SKU mapping is meaningful only for the special case of
> multi-SKU performance tests. See [SKU Groups](tests.md#sku-groups).

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
architectures = ["x86_64", "aarch64"]

[images.vm-base.capabilities]
machine-bootable = true
systemd = true
runtime-package-management = true

[images.vm-base.properties]
openssl-fips-provider = "upstream"
```

### Container image with capabilities

```toml
[images.container-base]
description = "Container Base Image"
definition = { type = "kiwi", path = "container-base/container-base.kiwi" }
architectures = ["x86_64", "aarch64"]

[images.container-base.capabilities]
container = true
```

### Image with a build profile

```toml
[images.vm-azure]
description = "Azure-optimized VM image"
definition = { type = "kiwi", path = "vm-azure/vm-azure.kiwi", profile = "azure" }
architectures = ["x86_64"]
```

### Image with test references

```toml
[images.vm-base]
description = "VM Base Image"
definition = { type = "kiwi", path = "vm-base/vm-base.kiwi" }
architectures = ["x86_64", "aarch64"]

[images.vm-base.capabilities]
machine-bootable = true
systemd = true

[images.vm-base.tests]
tests = [
  { name = "static-image-checks" },
  { group = "vm-base-functional" },
]
```

### Image with publish channels

```toml
[images.vm-base]
description = "VM Base Image"
definition = { type = "kiwi", path = "vm-base/vm-base.kiwi" }
architectures = ["x86_64", "aarch64"]

[images.vm-base.publish]
channels = ["registry-prod", "registry-staging"]
```

## Related Resources

- [Config File Structure](config-file.md) — top-level config file layout
- [Tests and Test Groups](tests.md) — new-shape test/group definitions referenced by `[images.<name>.tests]`
- [Tools](tools.md) — Image Customizer tool configuration
