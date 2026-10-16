# How To: Build an Image

This guide walks through building an Azure Linux image with `azldev`.

## Prerequisites

- A configured Azure Linux project with at least one image definition (see [Creating a Project](create-project.md))
- Built RPMs available in a local repository or remote package source
- The `azldev` CLI installed and on your `PATH`

## List Available Images

```bash
azldev image list
```

## Build an Image

```bash
azldev image build
```

Follow the interactive prompts to select an image definition and target architecture.

## Common Options

| Flag | Description |
|------|-------------|
| `--arch` | Target architecture (`x86_64` or `aarch64`) |
| `--local-repo <path>` | Add a local RPM repository as a package source |
| `--remote-repo <url>` | Add a remote RPM repository as a package source |
| `--remote-repo-no-gpgcheck` | Disable GPG checking for remote repositories |
## Override KIWI Configuration

Set `kiwi-config-override` on the build distro version to override KIWI settings.
The path is resolved relative to the TOML file that declares the distro version:

```toml
[distros.azurelinux.versions.'4.0-stage1']
kiwi-config-override = "kiwi/azl4/stage1/azurelinux-4.0-kiwi-override.yml"
```

KIWI merges `--config` after its standard configuration files, giving these overrides
the highest precedence. Merging is top-level: defining a section replaces that entire
section from earlier files.

## Boot an Image

After building, boot the image in a QEMU VM for testing:

```bash
azldev image boot -i <path-to-image> -f <format> --test-password <password>
```

`--image-path` (`-i`) selects the disk file; `--format` (`-f`) specifies how QEMU
interprets it, overriding the filename extension. If `--format` is omitted, the
format is inferred from the extension. Supply `--format` for files with no extension
or an unrecognized extension.

```bash
azldev image boot --image-path /image.raw --format raw --memory 2G --cpus 2
```

Supported formats: `raw`, `qcow2`, `vhdx`, `vhd`. When booting by image name instead,
`--format` selects which built image format to look up.

## Customize a Pre-Built Image

Use Image Customizer to modify an existing image:

```bash
azldev image customize --image-file <path> --image-config <config> --output-path <output>
```

## Related Resources

- [Images Reference](../reference/config/images.md) — image definition format
- [Tools Reference](../reference/config/tools.md) — external tool configuration (Image Customizer)

<!-- TODO: expand with kiwi image configuration examples and customization workflows -->
