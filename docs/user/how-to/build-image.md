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

## Boot an Image

After building, boot the image in a QEMU VM for testing:

```bash
azldev image boot -i <path-to-image> -f <format> --test-password <password>
```

Supported formats: `raw`, `qcow2`, `vhdx`.

## Customize a Pre-Built Image

Use Image Customizer to modify an existing image:

```bash
azldev image customize --image-file <path> --image-config <config> --output-path <output>
```

## Related Resources

- [Images Reference](../reference/config/images.md) — image definition format
- [Tools Reference](../reference/config/tools.md) — external tool configuration (Image Customizer)

<!-- TODO: expand with kiwi image configuration examples and customization workflows -->
