# User Guide

## How-To Guides

- [Create a Project](./how-to/create-project.md) — set up a new Azure Linux project
- [Add a Component](./how-to/add-component.md) — import or create a new package
- [Build a Component](./how-to/build-component.md) — build RPMs from component definitions
- [Build an Image](./how-to/build-image.md) — build and boot Azure Linux images

## Explanation

- [Configuration System](./explanation/config-system.md) — how config files are loaded, merged, and how inheritance works

## Reference

### Configuration

- [Config File Structure](./reference/config/config-file.md) — root config file layout and includes
- [Project](./reference/config/project.md) — project metadata and directory configuration
- [Distros](./reference/config/distros.md) — distro definitions, versions, and repositories
- [Components](./reference/config/components.md) — component definitions, spec sources, build options, and source files
- [Overlays](./reference/config/overlays.md) — spec and file overlays for modifying upstream sources
- [Component Groups](./reference/config/component-groups.md) — grouping components with shared defaults
- [Images](./reference/config/images.md) — image definitions (VMs, containers)
- [Tools](./reference/config/tools.md) — external tool configuration

### CLI Commands

Auto-generated from `azldev --help`. See [reference/cli/](./reference/cli/azldev.md) for per-command documentation.

