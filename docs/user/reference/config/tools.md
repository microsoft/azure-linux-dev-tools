# Tools Configuration

The `[tools]` section configures external tools used by azldev. azldev ships with built-in defaults for all tool settings — you only need to define this section when overriding those defaults.

## Field Reference

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Image Customizer | `imageCustomizer` | [ImageCustomizerConfig](#image-customizer) | No | Configuration for the Image Customizer tool |

## Image Customizer

The Image Customizer is used for post-build image customization. Its configuration is defined under `[tools.imageCustomizer]`.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Container tag | `containerTag` | string | No | Container image tag for the Image Customizer. azldev provides a default; set this to override. |

### Example

```toml
[tools.imageCustomizer]
containerTag = "mcr.microsoft.com/azurelinux/imagecustomizer:0.18.0"
```

## Related Resources

- [Config File Structure](config-file.md) — top-level config file layout
- [Images](images.md) — image definitions that the tools operate on
