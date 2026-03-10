# How To: Create a Project

This guide walks through creating a new Azure Linux project with `azldev`.

## Prerequisites

- The `azldev` CLI installed and on your `PATH`

## Create a New Project

Create a new project in a new directory:

```bash
azldev project new <path>
```

This generates a basic project structure with default configuration files.

## Initialize an Existing Directory

To initialize the current working directory as an Azure Linux project:

```bash
azldev project init
```

## Project Structure

After initialization, your project will contain:

```
<project>/
├── azldev.toml          # Root config file
├── project.toml         # Project metadata
├── distro/              # Distro definitions
│   └── distro.toml
└── comps/               # Component definitions
    └── components.toml
```

## Next Steps

1. Add components: see [Adding a Component](add-component.md)
2. Build components: see [Building a Component](build-component.md)
3. Build images: see [Building an Image](build-image.md)

## Related Resources

- [Config File Structure](../reference/config/config-file.md) — root config file layout
- [Project Reference](../reference/config/project.md) — project metadata and directory configuration
- [Configuration System](../explanation/config-system.md) — how config files are loaded and merged

<!-- TODO: expand with detailed project configuration walkthrough -->
